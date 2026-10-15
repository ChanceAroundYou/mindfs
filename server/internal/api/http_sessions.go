package api

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/commandexec"
	"mindfs/server/internal/session"

	"github.com/go-chi/chi/v5"
)

func (h *HTTPHandler) handleSessions(w http.ResponseWriter, r *http.Request) {
	rootID := r.URL.Query().Get("root")
	if truthyQuery(r, "multi_root") {
		h.handleMultiRootSessions(w, r)
		return
	}
	beforeTime, err := parseOptionalTimeQuery(r, "before_time")
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	afterTime, err := parseOptionalTimeQuery(r, "after_time")
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	if !beforeTime.IsZero() && !afterTime.IsZero() {
		respondError(w, http.StatusBadRequest, errInvalidRequest("before_time and after_time are mutually exclusive"))
		return
	}
	limit, err := parsePositiveIntQuery(r, "limit")
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("limit must be a positive integer"))
		return
	}
	if limit <= 0 {
		limit = sessionListPageSize
	}
	uc := h.service()
	out, err := uc.ListSessions(r.Context(), usecase.ListSessionsInput{
		RootID:          rootID,
		BeforeTime:      beforeTime,
		AfterTime:       afterTime,
		Limit:           limit,
		TopLevelOnly:    truthyQuery(r, "top_level"),
		IncludeChildren: truthyQuery(r, "include_children"),
		// 默认排除已归档（主面板）；archived=only 时只返回归档区的那批。
		ArchivedOnly: strings.TrimSpace(r.URL.Query().Get("archived")) == "only",
	})
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, err)
		return
	}
	payload := make([]map[string]any, 0, len(out.Sessions))
	for _, s := range out.Sessions {
		payload = append(payload, h.sessionListResponse(s))
	}
	// 置顶的权威只有主节点的 pins 表：会话库里的 pinned_at 已退役（存量实测为 0 行，
	// 不做回填）。刻意**不**把两者并起来 —— 两份真相并集等于两份真相迟早分叉，
	// 而 nodes.json 当年就是这么漂的。
	pinnedItems, pinnedKeys := h.pinnedSessionsForRoot(r.Context(), rootID, true)
	pinnedPayload := make([]map[string]any, 0, len(pinnedItems))
	for _, s := range pinnedItems {
		pinnedPayload = append(pinnedPayload, h.sessionListResponse(s))
	}
	respondJSONList(w, r, map[string]any{
		"items":        payload,
		"pinned_items": pinnedPayload,
		"pinned_keys":  pinnedKeys,
		"total_count":  out.TotalCount,
	})
}

func (h *HTTPHandler) handleMultiRootSessions(w http.ResponseWriter, r *http.Request) {
	limit, err := parsePositiveIntQuery(r, "limit_per_root")
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("limit_per_root must be a positive integer"))
		return
	}
	if limit <= 0 {
		limit = multiRootSessionLimit
	}
	out, err := h.service().ListMultiRootSessions(r.Context(), usecase.ListMultiRootSessionsInput{
		LimitPerRoot: limit,
	})
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, err)
		return
	}
	groups := make([]map[string]any, 0, len(out.Groups))
	for _, group := range out.Groups {
		items := make([]map[string]any, 0, len(group.Sessions))
		for _, s := range group.Sessions {
			item := h.sessionListResponse(s)
			item["root_id"] = group.RootID
			items = append(items, item)
		}
		// 同 handleSessionList：置顶只认 pins 表，会话库的 pinned_at 不再参与。
		pinnedSessions, pinnedKeys := h.pinnedSessionsForRoot(r.Context(), group.RootID, true)
		pinnedItems := make([]map[string]any, 0, len(pinnedSessions))
		for _, s := range pinnedSessions {
			item := h.sessionListResponse(s)
			item["root_id"] = group.RootID
			pinnedItems = append(pinnedItems, item)
		}
		groups = append(groups, map[string]any{
			"root_id":             group.RootID,
			"root_name":           group.RootName,
			"latest_session_time": group.LatestSessionTime,
			"items":               items,
			"pinned_items":        pinnedItems,
			"pinned_keys":         pinnedKeys,
			"total_count":         group.TotalCount,
		})
	}
	respondJSONList(w, r, map[string]any{"groups": groups})
}

func (h *HTTPHandler) handleSessionChildren(w http.ResponseWriter, r *http.Request) {
	rootID := r.URL.Query().Get("root")
	parentSessionKey := strings.TrimSpace(r.URL.Query().Get("parent_session_key"))
	if parentSessionKey == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("parent_session_key required"))
		return
	}
	beforeTime, err := parseOptionalTimeQuery(r, "before_time")
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	limit, err := parsePositiveIntQuery(r, "limit")
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("limit must be a positive integer"))
		return
	}
	if limit <= 0 {
		limit = childSessionPageSize
	} else if limit > childSessionMaxLimit {
		limit = childSessionMaxLimit
	}
	out, err := h.service().ListChildSessions(r.Context(), usecase.ListChildSessionsInput{
		RootID:           rootID,
		ParentSessionKey: parentSessionKey,
		BeforeTime:       beforeTime,
		Limit:            limit,
	})
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, err)
		return
	}
	payload := make([]map[string]any, 0, len(out.Sessions))
	for _, s := range out.Sessions {
		payload = append(payload, h.sessionListResponse(s))
	}
	respondJSON(w, http.StatusOK, payload)
}

func (h *HTTPHandler) handleReplyingSessions(w http.ResponseWriter, r *http.Request) {
	if h.AppContext == nil || h.AppContext.GetSessionStreamHub() == nil {
		respondJSONConditional(w, r, map[string]any{"sessions": []map[string]any{}})
		return
	}
	items := h.AppContext.GetSessionStreamHub().ListReplyingSessions()
	payload := make([]map[string]any, 0, len(items))
	for _, item := range items {
		rootTitle := item.RootID
		sessionTitle := strings.TrimSpace(item.SessionTitle)
		if root, err := h.AppContext.GetRoot(item.RootID); err == nil {
			if strings.TrimSpace(root.Name) != "" {
				rootTitle = root.Name
			}
			if sessionTitle == "" {
				if manager, err := h.AppContext.GetSessionManager(item.RootID); err == nil {
					// 只需 name，走 SQLite meta-only，避免全量加载 JSONL（agent 活跃时该端点高频轮询）。
					if sess, err := manager.GetMeta(r.Context(), item.SessionKey); err == nil && sess != nil {
						sessionTitle = sess.Name
					}
				}
			}
		}
		payload = append(payload, map[string]any{
			"rootId":       item.RootID,
			"rootTitle":    rootTitle,
			"sessionKey":   item.SessionKey,
			"sessionTitle": sessionTitle,
			"status":       item.Status,
			"summary":      item.Summary,
			"updatedAt":    item.UpdatedAt,
		})
	}
	respondJSONConditional(w, r, map[string]any{"sessions": payload})
}

func (h *HTTPHandler) handleSessionSearch(w http.ResponseWriter, r *http.Request) {
	rootID := strings.TrimSpace(r.URL.Query().Get("root"))
	query := r.URL.Query().Get("q")
	limit, err := parsePositiveIntQuery(r, "limit")
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("limit must be a positive integer"))
		return
	}
	out, err := h.service().SearchSessions(r.Context(), usecase.SearchSessionsInput{
		RootID:    rootID,
		Query:     query,
		Limit:     limit,
		MultiRoot: truthyQuery(r, "multi_root"),
	})
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"items": out.Items,
	})
}

func (h *HTTPHandler) handleCandidates(w http.ResponseWriter, r *http.Request) {
	rootID := strings.TrimSpace(r.URL.Query().Get("root"))
	candidateType := usecase.CandidateType(strings.TrimSpace(r.URL.Query().Get("type")))
	agent := strings.TrimSpace(r.URL.Query().Get("agent"))
	uc := h.service()
	out, err := uc.SearchCandidates(r.Context(), usecase.SearchCandidatesInput{
		RootID: rootID,
		Type:   candidateType,
		Query:  r.URL.Query().Get("q"),
		Agent:  agent,
	})
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "root not found") {
			status = http.StatusNotFound
		}
		respondError(w, status, err)
		return
	}
	respondJSON(w, http.StatusOK, out.Items)
}

func (h *HTTPHandler) handlePromptSave(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&input); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid prompt payload"))
		return
	}
	out, err := h.service().SavePrompt(r.Context(), usecase.SavePromptInput{
		Text: input.Text,
	})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, out)
}

func (h *HTTPHandler) handlePromptDelete(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&input); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid prompt payload"))
		return
	}
	out, err := h.service().DeletePrompt(r.Context(), usecase.DeletePromptInput{
		Text: input.Text,
	})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, out)
}

func (h *HTTPHandler) handleExternalSessionsList(w http.ResponseWriter, r *http.Request) {
	rootID := strings.TrimSpace(r.URL.Query().Get("root"))
	agentName := strings.TrimSpace(r.URL.Query().Get("agent"))
	if rootID == "" || agentName == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root and agent are required"))
		return
	}
	beforeTime, err := parseOptionalTimeQuery(r, "before_time")
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	afterTime, err := parseOptionalTimeQuery(r, "after_time")
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	if !beforeTime.IsZero() && !afterTime.IsZero() {
		respondError(w, http.StatusBadRequest, errInvalidRequest("before_time and after_time are mutually exclusive"))
		return
	}
	limit, err := parsePositiveIntQuery(r, "limit")
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("limit must be a positive integer"))
		return
	}
	filterBound := false
	switch strings.TrimSpace(r.URL.Query().Get("filter_bound")) {
	case "1", "true", "TRUE", "True":
		filterBound = true
	}
	uc := h.service()
	out, err := uc.ListExternalSessions(r.Context(), usecase.ListExternalSessionsInput{
		RootID:      rootID,
		Agent:       agentName,
		BeforeTime:  beforeTime,
		AfterTime:   afterTime,
		Limit:       limit,
		FilterBound: filterBound,
	})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	payload := make([]map[string]any, 0, len(out.Items))
	for _, item := range out.Items {
		payload = append(payload, externalSessionListResponse(item))
	}
	respondJSON(w, http.StatusOK, payload)
}

func (h *HTTPHandler) handleExternalSessionImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RootID         string `json:"root_id"`
		Agent          string `json:"agent"`
		AgentSessionID string `json:"agent_session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	req.RootID = strings.TrimSpace(req.RootID)
	req.Agent = strings.TrimSpace(req.Agent)
	req.AgentSessionID = strings.TrimSpace(req.AgentSessionID)
	if req.RootID == "" || req.Agent == "" || req.AgentSessionID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root_id, agent, agent_session_id are required"))
		return
	}
	uc := h.service()
	out, err := uc.ImportExternalSession(r.Context(), usecase.ImportExternalSessionInput{
		RootID:         req.RootID,
		Agent:          req.Agent,
		AgentSessionID: req.AgentSessionID,
	})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	if h.AppContext != nil {
		h.AppContext.GetSessionStreamHub().BroadcastAll(WSResponse{
			Type: "session.imported",
			Payload: map[string]any{
				"root_id":          req.RootID,
				"session_key":      out.SessionKey,
				"agent":            out.Agent,
				"agent_session_id": out.AgentSessionID,
				"imported_count":   out.ImportedCount,
			},
		})
	}
	respondJSON(w, http.StatusOK, out)
}

func (h *HTTPHandler) handleExternalSessionImportBatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RootID          string   `json:"root_id"`
		Agent           string   `json:"agent"`
		AgentSessionIDs []string `json:"agent_session_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	req.RootID = strings.TrimSpace(req.RootID)
	req.Agent = strings.TrimSpace(req.Agent)
	if req.RootID == "" || req.Agent == "" || len(req.AgentSessionIDs) == 0 {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root_id, agent, agent_session_ids are required"))
		return
	}
	uc := h.service()
	out, err := uc.ImportExternalSessionsBatch(r.Context(), usecase.ImportExternalSessionsBatchInput{
		RootID:          req.RootID,
		Agent:           req.Agent,
		AgentSessionIDs: req.AgentSessionIDs,
	})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	if h.AppContext != nil {
		for _, item := range out.Items {
			if !item.Success || strings.TrimSpace(item.SessionKey) == "" {
				continue
			}
			h.AppContext.GetSessionStreamHub().BroadcastAll(WSResponse{
				Type: "session.imported",
				Payload: map[string]any{
					"root_id":          req.RootID,
					"session_key":      item.SessionKey,
					"agent":            req.Agent,
					"agent_session_id": item.AgentSessionID,
					"imported_count":   item.ImportedCount,
				},
			})
		}
	}
	respondJSON(w, http.StatusOK, out)
}

func (h *HTTPHandler) handleSessionGet(w http.ResponseWriter, r *http.Request) {
	rootID := r.URL.Query().Get("root")
	key := chi.URLParam(r, "key")
	if strings.TrimSpace(key) == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("session key required"))
		return
	}
	afterSeq, err := parsePositiveIntQuery(r, "seq")
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("seq must be a positive integer"))
		return
	}
	beforeSeq, err := parsePositiveIntQuery(r, "before_seq")
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("before_seq must be a positive integer"))
		return
	}
	latest, err := parsePositiveIntQuery(r, "latest")
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("latest must be a positive integer"))
		return
	}
	// seq 与 before_seq/latest 互斥：增量语义与窗口语义不可同时请求
	if afterSeq > 0 && (beforeSeq > 0 || latest > 0) {
		respondError(w, http.StatusBadRequest, errInvalidRequest("seq 与 before_seq/latest 互斥"))
		return
	}
	limit, err := parsePositiveIntQuery(r, "limit")
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("limit must be a positive integer"))
		return
	}
	if limit <= 0 {
		limit = 50
	} else if limit > 200 {
		limit = 200
	}
	uc := h.service()
	if _, err := uc.SyncExternalSessionDelta(r.Context(), usecase.SyncExternalSessionDeltaInput{
		RootID: rootID,
		Key:    key,
	}); err != nil {
		log.Printf("[session/sync] external delta best-effort failed root=%s session=%s err=%v", strings.TrimSpace(rootID), strings.TrimSpace(key), err)
	}
	out, windowMeta, err := uc.GetSession(r.Context(), usecase.GetSessionInput{
		RootID:    rootID,
		Key:       key,
		Seq:       afterSeq,
		BeforeSeq: beforeSeq,
		Limit:     limit,
		Latest:    latest,
	})
	if err != nil {
		respondError(w, http.StatusNotFound, err)
		return
	}
	contextWindow, _ := uc.GetSessionContextWindow(r.Context(), usecase.GetSessionContextWindowInput{
		RootID: rootID,
		Key:    key,
	})
	// 窗口请求只需窗口内 seq 的 aux：原先传 Seq=0 会让 manager 全量读整个 aux 文件
	// （实测某会话 11.8MB/约 300ms 且持 m.mu），再在下面按 windowSeqs 过滤掉绝大部分。
	var exchangeAux map[int][]session.ExchangeAux
	if windowMeta != nil {
		windowSeqs := make(map[int]bool, len(out.Exchanges))
		for _, ex := range out.Exchanges {
			if ex.Seq > 0 {
				windowSeqs[ex.Seq] = true
			}
		}
		exchangeAux, _ = uc.GetSessionExchangeAuxWindow(r.Context(), usecase.GetSessionExchangeAuxWindowInput{
			RootID: rootID,
			Key:    key,
			Seqs:   windowSeqs,
		})
	} else {
		exchangeAux, _ = uc.GetSessionExchangeAux(r.Context(), usecase.GetSessionExchangeAuxInput{
			RootID: rootID,
			Key:    key,
			Seq:    afterSeq,
		})
	}
	// 正文块：seq 集合与上面 aux 的窗口口径一致 —— 用**回包里的** exchange seq，
	// 而不是请求参数推算的窗口边界，这样窗口/增量/全量三条路共用同一段逻辑，
	// 也不会出现「块挂到了不在回包里的 seq 上」。
	blocks, _ := uc.GetSessionBlocksWindow(r.Context(), usecase.GetSessionBlocksWindowInput{
		RootID: rootID,
		Key:    key,
		Seqs:   windowSeqsForExchanges(out.Exchanges),
		// 回填输入（S2d-1）：这两份数据响应里本来就要带着，回填不该为此再读一遍盘。
		Exchanges: out.Exchanges,
		Aux:       exchangeAux,
	})
	// 条件请求：这是全站**最重**的端点 —— 窗口化尾部拉取（`?latest=20`）
	// 实测单次 1.14 MB，客户端按 ~1.7 秒一次轮询，两台机器各自量到 60 分钟里
	// 4,746 次、合计约 1.1 GB/h（占全部请求的 70%）。会话没在跑时这些回包
	// **逐字节相同**（连续四次 sha1 一致），所以 304 能把它们压到零字节。
	//
	// 只协商、不瘦身：载荷本身就是详情（exchanges / exchange_aux / window_meta
	// 少一个键就是少一段对话），而且 `seq` 增量语义要求客户端**恰好**收到新行，
	// 删空字段会破坏 `appendSessionDelta` 的判据。
	respondJSONConditional(w, r, h.sessionResponse(out, contextWindow, exchangeAux, blocks, windowMeta))
}

func (h *HTTPHandler) handleSessionSync(w http.ResponseWriter, r *http.Request) {
	rootID := r.URL.Query().Get("root")
	key := chi.URLParam(r, "key")
	if strings.TrimSpace(key) == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("session key required"))
		return
	}
	afterSeq, err := parsePositiveIntQuery(r, "seq")
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("seq must be a positive integer"))
		return
	}
	// 窗口参数与 handleSessionGet 同义，透传至 usecase；与 seq 互斥
	beforeSeq, err := parsePositiveIntQuery(r, "before_seq")
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("before_seq must be a positive integer"))
		return
	}
	latest, err := parsePositiveIntQuery(r, "latest")
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("latest must be a positive integer"))
		return
	}
	if afterSeq > 0 && (beforeSeq > 0 || latest > 0) {
		respondError(w, http.StatusBadRequest, errInvalidRequest("seq 与 before_seq/latest 互斥"))
		return
	}
	limit, err := parsePositiveIntQuery(r, "limit")
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("limit must be a positive integer"))
		return
	}
	if limit <= 0 {
		limit = 50
	} else if limit > 200 {
		limit = 200
	}
	uc := h.service()
	if _, err := uc.SyncExternalSessionDelta(r.Context(), usecase.SyncExternalSessionDeltaInput{
		RootID: rootID,
		Key:    key,
		Full:   true,
	}); err != nil {
		// 外部转录同步只是「尽力而为」的补充，请求的目的是刷新会话视图，不该被它拖垮 ——
		// GET 路径（handleSessionGet）一直就是这个语义，这里却把错误直接变成 400。
		// 实测 2026-09-22 会话 1790064991（root=mindfs）：agent 跑在 <root>/.worktree/task-10，
		// 任务结束后工作树被移除，而转录仍在 ~/.claude/projects/-home-...-mindfs--worktree-task-10/
		// 下且还在写；worktreeCandidateRoots 只枚举**现存**的 .worktree/* 目录，于是永远找不到
		// → external session not found → 「同步」按钮永久 400。刷新视图不该因此失败。
		// 注意这里**不**去修「按残留 slug 目录找转录」：live-owned 会话的字节游标是冻结的
		// （external_source_* 全空），一旦找到就会从 offset 0 整份重读，正是 2026-09-16 那次
		// 「ask 下面又渲染了一轮出现过的文字」的成因。找不到反而是安全的。
		// 补充（repoint 落地后）：对**已 repoint** 的会话，本条路径的触发条件已经消失——
		// related_worktree 被清空、转录落在主 slug 目录下，worktreeCandidateRoots 的第一项
		// 就是主 checkout，所以一定找得到；cursor 也在 repoint 时被推进到了新转录末尾，
		// 找到之后也不会从 0 重读。没 repoint 的会话仍旧走「找不到」这条安全路径。
		log.Printf("[session/sync] external sync failed root=%s session=%s err=%v", strings.TrimSpace(rootID), strings.TrimSpace(key), err)
	}
	out, windowMeta, err := uc.GetSession(r.Context(), usecase.GetSessionInput{
		RootID:    rootID,
		Key:       key,
		Seq:       afterSeq,
		BeforeSeq: beforeSeq,
		Limit:     limit,
		Latest:    latest,
	})
	if err != nil {
		respondError(w, http.StatusNotFound, err)
		return
	}
	contextWindow, _ := uc.GetSessionContextWindow(r.Context(), usecase.GetSessionContextWindowInput{
		RootID: rootID,
		Key:    key,
	})
	// 窗口请求只需窗口内 seq 的 aux：原先无条件传 Seq=0，会让 manager 全量读整个 aux
	// 文件（实测某会话 11.8MB/约 300ms 且持 m.mu），再按 windowSeqs 过滤掉绝大部分。
	// 与 handleSessionGet 保持同一套选择逻辑。
	var exchangeAux map[int][]session.ExchangeAux
	if windowMeta != nil {
		windowSeqs := make(map[int]bool, len(out.Exchanges))
		for _, ex := range out.Exchanges {
			if ex.Seq > 0 {
				windowSeqs[ex.Seq] = true
			}
		}
		exchangeAux, _ = uc.GetSessionExchangeAuxWindow(r.Context(), usecase.GetSessionExchangeAuxWindowInput{
			RootID: rootID,
			Key:    key,
			Seqs:   windowSeqs,
		})
	} else {
		exchangeAux, _ = uc.GetSessionExchangeAux(r.Context(), usecase.GetSessionExchangeAuxInput{
			RootID: rootID,
			Key:    key,
			Seq:    afterSeq,
		})
	}
	// 正文块与 aux 同一口径（用回包里的 exchange seq），与 handleSessionGet 共用
	// windowSeqsForExchanges。
	blocks, _ := uc.GetSessionBlocksWindow(r.Context(), usecase.GetSessionBlocksWindowInput{
		RootID: rootID,
		Key:    key,
		Seqs:   windowSeqsForExchanges(out.Exchanges),
		// 回填输入（S2d-1）：这两份数据响应里本来就要带着，回填不该为此再读一遍盘。
		Exchanges: out.Exchanges,
		Aux:       exchangeAux,
	})
	// 注：sync 先做全量外部增量拉取（Full:true），再按窗口切片，保证窗口数据最新。
	respondJSON(w, http.StatusOK, h.sessionResponse(out, contextWindow, exchangeAux, blocks, windowMeta))
}

func (h *HTTPHandler) handleSessionToolCallGet(w http.ResponseWriter, r *http.Request) {
	rootID := r.URL.Query().Get("root")
	key := chi.URLParam(r, "key")
	callID := chi.URLParam(r, "callID")
	if strings.TrimSpace(key) == "" || strings.TrimSpace(callID) == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("session key and tool call id required"))
		return
	}
	toolCall, err := h.service().GetSessionToolCall(r.Context(), usecase.GetSessionToolCallInput{
		RootID: rootID,
		Key:    key,
		CallID: callID,
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			respondError(w, http.StatusNotFound, err)
			return
		}
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"toolcall": toolCall})
}

func (h *HTTPHandler) handleSessionRelatedFilesGet(w http.ResponseWriter, r *http.Request) {
	rootID := r.URL.Query().Get("root")
	key := chi.URLParam(r, "key")
	if strings.TrimSpace(key) == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("session key required"))
		return
	}
	uc := h.service()
	out, err := uc.GetSessionRelatedFiles(r.Context(), usecase.GetSessionRelatedFilesInput{
		RootID: rootID,
		Key:    key,
	})
	if err != nil {
		respondError(w, http.StatusNotFound, err)
		return
	}
	respondJSON(w, http.StatusOK, out)
}

func (h *HTTPHandler) handleSessionRelatedFilesDelete(w http.ResponseWriter, r *http.Request) {
	rootID := r.URL.Query().Get("root")
	key := chi.URLParam(r, "key")
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	head := strings.TrimSpace(r.URL.Query().Get("head"))
	repoPath := strings.TrimSpace(r.URL.Query().Get("repo_path"))
	repoKind := strings.TrimSpace(r.URL.Query().Get("repo_kind"))
	if strings.TrimSpace(key) == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("session key required"))
		return
	}
	if path == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("path required"))
		return
	}
	uc := h.service()
	if err := uc.RemoveSessionRelatedFile(r.Context(), usecase.RemoveSessionRelatedFileInput{
		RootID:   rootID,
		Key:      key,
		Path:     path,
		Head:     head,
		RepoPath: repoPath,
		RepoKind: repoKind,
	}); err != nil {
		respondError(w, http.StatusNotFound, err)
		return
	}
	if h.AppContext != nil {
		h.AppContext.GetSessionStreamHub().BroadcastAll(WSResponse{
			Type: "session.related_files.updated",
			Payload: map[string]any{
				"root_id":     rootID,
				"session_key": key,
			},
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTPHandler) handleSessionFork(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RootID     string `json:"root_id"`
		SessionKey string `json:"session_key"`
		Seq        int    `json:"seq"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	if strings.TrimSpace(req.RootID) == "" || strings.TrimSpace(req.SessionKey) == "" || req.Seq <= 0 {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root_id, session_key and seq are required"))
		return
	}
	out, err := h.service().ForkSession(r.Context(), usecase.ForkSessionInput{
		RootID: req.RootID,
		Key:    req.SessionKey,
		Seq:    req.Seq,
	})
	if err != nil {
		log.Printf("[session/fork] failed root=%s parent=%s seq=%d err=%v", req.RootID, req.SessionKey, req.Seq, err)
		respondError(w, http.StatusBadRequest, err)
		return
	}
	if h.AppContext != nil && out.Session != nil {
		h.AppContext.GetSessionStreamHub().BroadcastAll(WSResponse{
			Type: "session.created",
			Payload: map[string]any{
				"root_id": req.RootID,
				"session": h.sessionListResponse(out.Session),
			},
		})
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"session_key": out.Session.Key,
		"session":     h.sessionResponse(out.Session, agenttypes.ContextWindow{}, nil, nil, nil),
	})
}

func (h *HTTPHandler) handleSessionRename(w http.ResponseWriter, r *http.Request) {
	rootID := r.URL.Query().Get("root")
	key := chi.URLParam(r, "key")
	if strings.TrimSpace(key) == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("session key required"))
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	uc := h.service()
	renamed, err := uc.RenameSession(r.Context(), usecase.RenameSessionInput{
		RootID: rootID,
		Key:    key,
		Name:   req.Name,
	})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	if h.AppContext != nil {
		h.AppContext.GetSessionStreamHub().BroadcastAll(WSResponse{
			Type: "session.meta.updated",
			Payload: map[string]any{
				"root_id": rootID,
				"session": map[string]any{
					"key":          renamed.Key,
					"name":         renamed.Name,
					"model":        renamed.Model,
					"mode":         session.InferModeFromSession(renamed),
					"effort":       session.InferEffortFromSession(renamed),
					"fast_service": session.InferFastServiceFromSession(renamed),
					"plan_mode":    renamed.PlanMode,
					"pinned_at":    renamed.PinnedAt,
					"updated_at":   renamed.UpdatedAt,
				},
			},
		})
	}
	// 任务名与会话名双向绑定：任务主会话改名时同步任务名。
	if strings.TrimSpace(req.Name) != "" {
		h.syncTaskNameFromSession(r.Context(), rootID, renamed.Key, strings.TrimSpace(req.Name))
	}
	respondJSON(w, http.StatusOK, h.sessionListResponse(renamed))
}

// handleSessionPin 是 POST /api/sessions/{key}/pin —— **旧路径的别名**，转调 /api/pins/session。
//
// 保留它是为了前端迁移期间不用同时改两端：同一个动作，两条 URL 写同一个置顶表。
// 前端改用 /api/pins/session 后即可删掉本函数与那条路由。
//
// 这里**不再**写会话库的 pinned_at（已退役）：那次写会让置顶回到按机器分的
// 存储里，而权威在主节点 —— 于是「在 A 机器置顶、B 机器看不到」的老问题又回来了。
func (h *HTTPHandler) handleSessionPin(w http.ResponseWriter, r *http.Request) {
	var req sessionPinRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	req.RootID = r.URL.Query().Get("root")
	req.Key = chi.URLParam(r, "key")
	h.pinsSessionPut(w, r, req)
}

func (h *HTTPHandler) handleSessionDelete(w http.ResponseWriter, r *http.Request) {
	rootID := r.URL.Query().Get("root")
	key := chi.URLParam(r, "key")
	if strings.TrimSpace(key) == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("session key required"))
		return
	}
	uc := h.service()
	deletedKeys, err := uc.DeleteSession(r.Context(), usecase.DeleteSessionInput{
		RootID: rootID,
		Key:    key,
	})
	if err != nil {
		respondError(w, http.StatusNotFound, err)
		return
	}
	// 会话没了，任务里指向它的链接必须一并清掉，否则任务点进去是空白。
	// 要传整棵子树的 key：子会话也可能绑着别的任务。
	h.detachTaskFromSession(r.Context(), rootID, deletedKeys)
	w.WriteHeader(http.StatusNoContent)
}

// handleSessionArchive 归档/取消归档。
//
// 归档会把子会话真删掉（见 usecase.ArchiveSession），所以这里**要**碰任务：
// 和删除一样，被删掉的子会话可能绑着别的任务，不解绑任务点进去就是空白。
// 被点的那个会话自己不碰任务 —— 它只是归档，还能打开，链接必须继续有效。
func (h *HTTPHandler) handleSessionArchive(w http.ResponseWriter, r *http.Request) {
	rootID := r.URL.Query().Get("root")
	key := chi.URLParam(r, "key")
	if strings.TrimSpace(key) == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("session key required"))
		return
	}
	var req struct {
		Archived bool `json:"archived"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	out, err := h.service().ArchiveSession(r.Context(), usecase.ArchiveSessionInput{
		RootID:   rootID,
		Key:      key,
		Archived: req.Archived,
	})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	// 被删的是子会话，任务里指向它们的链接一并清掉。
	if len(out.DeletedKeys) > 0 {
		h.detachTaskFromSession(r.Context(), rootID, out.DeletedKeys)
	}
	if h.AppContext != nil {
		h.AppContext.GetSessionStreamHub().BroadcastAll(WSResponse{
			Type: "session.meta.updated",
			Payload: map[string]any{
				"root_id": rootID,
				"session": h.sessionListResponse(out.Session),
			},
		})
	}
	respondJSON(w, http.StatusOK, h.sessionListResponse(out.Session))
}

// windowSeqsForExchanges 是「取块 / 取 aux 时用哪些 seq」的唯一定义：
// **回包里真实存在的** exchange seq（seq>0）。不用请求参数推算窗口边界，
// 于是窗口 / 增量 / 全量三条路共用一段逻辑，也不会出现「块挂到了不在回包里的 seq 上」。
func windowSeqsForExchanges(exchanges []session.Exchange) map[int]bool {
	seqs := make(map[int]bool, len(exchanges))
	for _, ex := range exchanges {
		if ex.Seq > 0 {
			seqs[ex.Seq] = true
		}
	}
	return seqs
}

func (h *HTTPHandler) sessionResponse(
	s *session.Session,
	contextWindow agenttypes.ContextWindow,
	exchangeAux map[int][]session.ExchangeAux,
	blocks map[int][]session.Block,
	windowMeta *session.SessionWindowMeta,
) map[string]any {
	if s == nil {
		return map[string]any{}
	}
	exchanges := append([]session.Exchange{}, s.Exchanges...)
	auxPayload := make(map[string][]session.ExchangeAux, len(exchangeAux))
	if windowMeta != nil {
		// 窗口模式：仅保留窗口内 exchange 的 aux，避免 aux.line 错位
		windowSeqs := make(map[int]struct{}, len(exchanges))
		for _, ex := range exchanges {
			if ex.Seq > 0 {
				windowSeqs[ex.Seq] = struct{}{}
			}
		}
		for seq, items := range exchangeAux {
			if seq <= 0 || len(items) == 0 {
				continue
			}
			if _, ok := windowSeqs[seq]; !ok {
				continue
			}
			auxPayload[strconv.Itoa(seq)] = append([]session.ExchangeAux(nil), items...)
		}
	} else {
		for seq, items := range exchangeAux {
			if seq <= 0 || len(items) == 0 {
				continue
			}
			auxPayload[strconv.Itoa(seq)] = append([]session.ExchangeAux(nil), items...)
		}
	}
	// 正文块（CUSTOM(G-BH)）：与 exchange_aux 同形，按 seq 分组。
	// 只下发窗口内的 seq，且**只压工具卡详情、绝不压正文** —— 块流是正文的唯一载体。
	// 历史会话没有块流时这里是空 map，前端回退旧格式。
	blockPayload := make(map[string][]session.Block, len(blocks))
	windowSeqs := make(map[int]struct{}, len(exchanges))
	for _, ex := range exchanges {
		if ex.Seq > 0 {
			windowSeqs[ex.Seq] = struct{}{}
		}
	}
	for seq, items := range blocks {
		if seq <= 0 || len(items) == 0 {
			continue
		}
		if _, ok := windowSeqs[seq]; !ok {
			continue
		}
		blockPayload[strconv.Itoa(seq)] = session.CompactBlocksLight(items)
	}
	resp := map[string]any{
		"key":                 s.Key,
		"type":                s.Type,
		"parent_session_key":  s.ParentSessionKey,
		"parent_tool_call_id": s.ParentToolCallID,
		"source":              s.Source,
		"task_id":             s.TaskID,
		"agent":               session.InferAgentFromSession(s),
		"model":               s.Model,
		"mode":                session.InferModeFromSession(s),
		"effort":              session.InferEffortFromSession(s),
		"fast_service":        session.InferFastServiceFromSession(s),
		"plan_mode":           s.PlanMode,
		"shell":               h.commandShellForResponse(s, exchangeAux),
		"name":                s.Name,
		"exchanges":           exchanges,
		"exchange_aux":        auxPayload,
		"blocks":              blockPayload,
		"related_files":       s.RelatedFiles,
		"related_worktree":    s.RelatedWorktree,
		"worktree_missing":    s.RelatedWorktree.WorktreeMissing(),
		"context_window":      contextWindow,
		"pinned_at":           s.PinnedAt,
		"archived_at":         s.ArchivedAt,
		"created_at":          s.CreatedAt,
		"updated_at":          s.UpdatedAt,
		"closed_at":           s.ClosedAt,
	}
	if windowMeta != nil {
		resp["window_meta"] = windowMeta
	}
	return resp
}

// sessionListResponse 是会话的「列表行」形状（/api/sessions 每条、以及 session.created
// 广播的 payload）。抽成包级函数是为了让 AppContext 的广播路径能用上同一份构造 ——
// 它只有 AppContext、没有 HTTPHandler。resolvedShell 是 command 类会话的 shell 解析结果
// （HTTPHandler 那侧来自 commandShellForSession）；非 command 会话忽略它。
func sessionListResponse(s *session.Session, resolvedShell string) map[string]any {
	if s == nil {
		return map[string]any{}
	}
	return map[string]any{
		"key":                 s.Key,
		"type":                s.Type,
		"parent_session_key":  s.ParentSessionKey,
		"parent_tool_call_id": s.ParentToolCallID,
		"source":              s.Source,
		"task_id":             s.TaskID,
		"agent":               session.InferAgentFromSession(s),
		"model":               s.Model,
		"mode":                session.InferModeFromSession(s),
		"effort":              session.InferEffortFromSession(s),
		"fast_service":        session.InferFastServiceFromSession(s),
		"plan_mode":           s.PlanMode,
		"shell":               commandShellField(s, resolvedShell),
		"name":                s.Name,
		"related_worktree":    s.RelatedWorktree,
		"worktree_missing":    s.RelatedWorktree.WorktreeMissing(),
		"pinned_at":           s.PinnedAt,
		"archived_at":         s.ArchivedAt,
		"created_at":          s.CreatedAt,
		"updated_at":          s.UpdatedAt,
		"closed_at":           s.ClosedAt,
	}
}

func commandShellField(s *session.Session, resolvedShell string) string {
	if s == nil || s.Type != session.TypeCommand {
		return ""
	}
	if shell := strings.TrimSpace(s.Shell); shell != "" {
		return shell
	}
	return strings.TrimSpace(resolvedShell)
}

func (h *HTTPHandler) sessionListResponse(s *session.Session) map[string]any {
	item := sessionListResponse(s, h.resolveCommandShell())
	// 「有个 ask_user 提问还没回答」随列表项下发（只在为真时出现）。
	//
	// 刻意**不**走 /api/replying-sessions：那条通道喂的是 pending 真相表，进去就等于
	// 点亮实心灯，而这里要表达的恰好是"不在跑、在等你"。列表项是"这条会话现在什么样"
	// 的载体，跟着它走，前端不必再为这一个布尔值开第二条状态通道。
	if s != nil && h.AppContext != nil && h.AppContext.GetSessionStreamHub().AwaitingAnswer(s.Key) {
		item["awaiting_answer"] = true
	}
	return item
}

// resolveCommandShell 算出配置里解析出的默认 shell（command 类会话没显式指定时用）。
// AppContext 侧的广播要能填 shell 字段，但那边没有 HTTPHandler，所以这个解析独立成方法。
func (s *AppContext) ResolveCommandShell() string {
	if s == nil {
		return ""
	}
	pool := s.GetAgentPool()
	if pool == nil {
		return ""
	}
	cfg := pool.Config()
	shells := make([]commandexec.ShellSpec, 0, len(cfg.Shells))
	for _, shell := range cfg.Shells {
		shells = append(shells, commandexec.ShellSpec{
			Command:       shell.Command,
			Args:          append([]string(nil), shell.Args...),
			LongShellArgs: append([]string(nil), shell.LongShellArgs...),
			CommandPrefix: shell.CommandPrefix,
		})
	}
	return commandexec.ResolveShell(shells)
}

func (h *HTTPHandler) resolveCommandShell() string {
	return h.AppContext.ResolveCommandShell()
}

func (h *HTTPHandler) configuredShells() []commandexec.ShellSpec {
	if h == nil || h.AppContext == nil || h.AppContext.GetAgentPool() == nil {
		return nil
	}
	cfg := h.AppContext.GetAgentPool().Config()
	shells := make([]commandexec.ShellSpec, 0, len(cfg.Shells))
	for _, shell := range cfg.Shells {
		shells = append(shells, commandexec.ShellSpec{
			Command:       shell.Command,
			Args:          append([]string(nil), shell.Args...),
			LongShellArgs: append([]string(nil), shell.LongShellArgs...),
			CommandPrefix: shell.CommandPrefix,
		})
	}
	return shells
}

func (h *HTTPHandler) commandShellForSession(s *session.Session) string {
	if s == nil || s.Type != session.TypeCommand {
		return ""
	}
	if strings.TrimSpace(s.Shell) != "" {
		return strings.TrimSpace(s.Shell)
	}
	return commandexec.ResolveShell(h.configuredShells())
}

func (h *HTTPHandler) commandShellForResponse(s *session.Session, aux map[int][]session.ExchangeAux) string {
	if s == nil || s.Type != session.TypeCommand {
		return ""
	}
	if shell := session.InferCommandShellFromAux(aux); strings.TrimSpace(shell) != "" {
		return strings.TrimSpace(shell)
	}
	return h.commandShellForSession(s)
}

func externalSessionListResponse(s agenttypes.ExternalSessionSummary) map[string]any {
	// Title already overlaid with manual alias or stripped 20-char prefix in ListExternalSessions;
	// never expose raw REPLY_TIPS to clients — fall back to stripped firstUserText only.
	name := strings.TrimSpace(s.Title)
	if name == "" {
		name = shortExternalSessionTitleForAPI(s.FirstUserText)
	}
	if name == "" {
		cleaned := strings.TrimSpace(stripReplyTipsPrefixForAPI(stripExternalSessionPrefixForAPI(s.FirstUserText)))
		if cleaned != "" {
			if idx := strings.Index(cleaned, "\n"); idx >= 0 {
				cleaned = strings.TrimSpace(cleaned[:idx])
			}
			name = cleaned
		}
	}
	if name == "" {
		name = s.AgentSessionID
	}
	return map[string]any{
		"key":              s.AgentSessionID,
		"type":             session.TypeChat,
		"agent":            s.Agent,
		"model":            "",
		"name":             name,
		"title":            strings.TrimSpace(s.Title),
		"created_at":       s.UpdatedAt,
		"updated_at":       s.UpdatedAt,
		"closed_at":        nil,
		"agent_session_id": s.AgentSessionID,
	}
}

func stripReplyTipsPrefixForAPI(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "[REPLY_TIPS]") {
		if idx := strings.Index(text, "[USER_PROMPT]"); idx >= 0 {
			return strings.TrimSpace(text[idx+len("[USER_PROMPT]"):])
		}
		if idx := strings.Index(text, "\n\n"); idx >= 0 {
			return strings.TrimSpace(text[idx:])
		}
	}
	return text
}

func shortExternalSessionTitleForAPI(text string) string {
	text = strings.TrimSpace(stripReplyTipsPrefixForAPI(stripExternalSessionPrefixForAPI(text)))
	if text == "" {
		return ""
	}
	if idx := strings.Index(text, "\n"); idx >= 0 {
		text = strings.TrimSpace(text[:idx])
	}
	runes := []rune(text)
	if len(runes) > 20 {
		text = string(runes[:20])
	}
	return strings.TrimSpace(text)
}

func stripExternalSessionPrefixForAPI(text string) string {
	text = strings.TrimSpace(text)
	const prefix = "This session was migrated from elsewhere. Your context may lag behind this session;"
	const tail = "Only if reading fails, output a brief error and stop."
	normalized := strings.ReplaceAll(text, "\\n", "\n")
	if !strings.HasPrefix(normalized, prefix) {
		return text
	}
	idx := strings.Index(normalized, tail)
	if idx < 0 {
		return text
	}
	return strings.TrimSpace(normalized[idx+len(tail):])
}
