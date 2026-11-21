package api

import (
	"net/http"
	"strconv"
	"strings"

	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/notify"
	"mindfs/server/internal/session"
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
	since, hasSince := parseCompletedSince(r.URL.Query().Get("completed_since"))
	resp := h.replyingPayload(r, items, since, hasSince)
	respondJSONConditional(w, r, resp)
}

// replyingPayload 是普通轮询与长轮询**共用**的响应体，形状不许分叉。
//
// hasSince=false 是首次播种：只回 completedSeq/completedEpoch，**不回历史完成行** ——
// 新装的客户端不该被账本里已有的旧完成刷屏。客户端拿到游标后，下一次请求带上
// completed_since，之后的响应才会携带它漏掉的那些完成。
func (h *HTTPHandler) replyingPayload(r *http.Request, items []ReplyingSessionState, since uint64, hasSince bool) map[string]any {
	resp := map[string]any{"sessions": h.replyingSessionsPayload(r, items)}
	if h.AppContext == nil || h.AppContext.GetSessionStreamHub() == nil {
		return resp
	}
	hub := h.AppContext.GetSessionStreamHub()
	if !hasSince {
		seq, epoch := hub.CompletionHead()
		resp["completedSeq"] = seq
		resp["completedEpoch"] = epoch
		return resp
	}
	completed, seq, epoch := hub.CompletionsSince(since)
	resp["completed"] = h.replyingCompletionsPayload(r, completed)
	resp["completedSeq"] = seq
	resp["completedEpoch"] = epoch
	return resp
}

// parseCompletedSince 解析客户端的消费游标。缺省/非法一律按「没有游标」处理 ——
// 宁可少补（只播种），也不能把一次脏参数变成一屏旧提醒。
func parseCompletedSince(raw string) (uint64, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, false
	}
	cursor, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, false
	}
	return cursor, true
}

// replyingCompletionsPayload 把完成账本行构造成对外载荷。只在客户端带了游标时调用：
// 补发是低频路径，这里解析 root/session 标题的代价可以接受。
func (h *HTTPHandler) replyingCompletionsPayload(r *http.Request, items []SessionCompletion) []map[string]any {
	payload := make([]map[string]any, 0, len(items))
	for _, item := range items {
		rootTitle := item.RootID
		sessionTitle := strings.TrimSpace(item.SessionTitle)
		if root, err := h.AppContext.GetRoot(item.RootID); err == nil {
			if strings.TrimSpace(root.Name) != "" {
				rootTitle = root.Name
			}
			if sessionTitle == "" && strings.TrimSpace(item.SessionKey) != "" {
				if manager, err := h.AppContext.GetSessionManager(item.RootID); err == nil {
					if sess, err := manager.GetMeta(r.Context(), item.SessionKey); err == nil && sess != nil {
						sessionTitle = sess.Name
					}
				}
			}
		}
		payloadItem := map[string]any{
			"seq":          item.Seq,
			"rootId":       item.RootID,
			"rootTitle":    rootTitle,
			"sessionKey":   item.SessionKey,
			"sessionTitle": sessionTitle,
			"summary":      item.Summary,
			"completedAt":  item.CompletedAt,
		}
		if item.SubSession {
			payloadItem["subSession"] = true
		}
		if item.Silent {
			// 任务内部步骤的回合（工作流节点）：客户端只推进状态，不弹完成提醒。
			payloadItem["silent"] = true
		}
		payload = append(payload, payloadItem)
	}
	return payload
}

// replyingSessionsPayload 把 hub 内存态构造成对外的载荷。**这是昂贵的那一步**：
// 每条都要走 GetRoot 拿项目名，标题缺失时还要走 GetSessionManager(...).GetMeta。
// 所以长轮询等待循环（http_sessions_replying_wait.go）只在**返回时**调它一次，循环里
// 只读纯内存的 ListReplyingSessions。普通轮询端点与等待端点共用这一份，形状不许分叉。
func (h *HTTPHandler) replyingSessionsPayload(r *http.Request, items []ReplyingSessionState) []map[string]any {
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
		payloadItem := map[string]any{
			"rootId":       item.RootID,
			"rootTitle":    rootTitle,
			"sessionKey":   item.SessionKey,
			"sessionTitle": sessionTitle,
			"status":       item.Status,
			// 客户端（安卓壳 / Web 轮询）直接拿这个字段当提醒正文显示，
			// 与 Web Push、完成补发账本同规则：去 Markdown、过长留尾部。
			"summary":   notify.PlainText(item.Summary, notify.BodyMaxRunes),
			"updatedAt": item.UpdatedAt,
		}
		if item.AwaitingAnswer {
			payloadItem["awaiting_answer"] = true
		}
		if item.SubSession {
			// 子会话标记：客户端用它跳过完成提醒（父会话那一轮还在跑）。
			payloadItem["subSession"] = true
		}
		if item.Silent {
			// 同上，但语义是「任务内部的一步」（工作流节点回合）。
			payloadItem["silent"] = true
		}
		payload = append(payload, payloadItem)
	}
	return payload
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

// sessionListResponse 是会话的「列表行」形状（/api/sessions 每条、以及 session.created
// 广播的 payload）。抽成包级函数是为了让 AppContext 的广播路径能用上同一份构造 ——
// 它只有 AppContext、没有 HTTPHandler。resolvedShell 是 command 类会话的 shell 解析结果
// （HTTPHandler 那侧来自 commandShellForSession）；非 command 会话忽略它。
func sessionListResponse(s *session.Session, resolvedShell string) map[string]any {
	if s == nil {
		return map[string]any{}
	}
	item := map[string]any{
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
		"worktree_active":     relatedWorktreeActive(s.RelatedWorktree),
		"work_dir":            s.WorkDir,
		"aux_flags":           s.Aux.AuxFlagsPayload(),
		"pinned_at":           s.PinnedAt,
		"archived_at":         s.ArchivedAt,
		"created_at":          s.CreatedAt,
		"updated_at":          s.UpdatedAt,
		"closed_at":           s.ClosedAt,
	}
	// 同一任务下的顶层会话数（只有列表查询会算，单条读取恒为 0）。侧栏按任务分组
	// 靠它而不是靠当前这一页恰好装了几条 —— 多项目侧栏每根只取 6 条，同一任务的
	// 会话常常只落在窗口外一条。为 0 时**不下发这个键**：广播/详情路径拿不到计数，
	// 下发 0 只会让前端把「不知道」当成「确认只有一条」。
	if s.TaskSessionCount > 0 {
		item["task_session_count"] = s.TaskSessionCount
	}
	return item
}
