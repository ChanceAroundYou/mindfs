package api

import (
	"strconv"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/session"

)


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
