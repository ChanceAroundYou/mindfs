package api

import (
	"errors"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/session"

	"github.com/go-chi/chi/v5"
)

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
		"work_dir":            s.WorkDir,
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
