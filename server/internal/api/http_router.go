package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/kanban"
	"mindfs/server/internal/session"

	"github.com/go-chi/chi/v5"
)

// Task Router 的只读「调度记录」与诊断端点（Plan 2026-11-14 §20.1）。
// 全部是数据面端点：决策/会话是每台机器自己的运行数据，不加控制面前缀。

const (
	routerDecisionDefaultLimit = 50
	routerDecisionMaxLimit     = 200
	routerTranscriptBodyLimit  = 4 * 1024
)

// routerDecisionSummary 是列表投影：不带 brief/actions 全文，避免一次拉几百 KB。
type routerDecisionSummary struct {
	ID                    string                      `json:"id"`
	TaskID                string                      `json:"task_id"`
	TriggerKind           kanban.RouterTriggerKind    `json:"trigger_kind"`
	TriggerRef            string                      `json:"trigger_ref,omitempty"`
	UserActivityID        string                      `json:"user_activity_id,omitempty"`
	PlaceholderActivityID string                      `json:"placeholder_activity_id,omitempty"`
	Status                kanban.RouterDecisionStatus `json:"status"`
	Agreement             string                      `json:"agreement,omitempty"`
	ActionKinds           []kanban.RouterActionKind   `json:"action_kinds,omitempty"`
	LegacyKind            string                      `json:"legacy_kind,omitempty"`
	Error                 string                      `json:"error,omitempty"`
	LatencyMS             int                         `json:"latency_ms"`
	PromptTokens          int                         `json:"prompt_tokens,omitempty"`
	CompletionTokens      int                         `json:"completion_tokens,omitempty"`
	CreatedAt             time.Time                   `json:"created_at"`
}

type routerDecisionDetail struct {
	routerDecisionSummary
	RouterSessionKey string                     `json:"router_session_key,omitempty"`
	TurnID           string                     `json:"turn_id"`
	BriefHash        string                     `json:"brief_hash,omitempty"`
	Brief            json.RawMessage            `json:"brief,omitempty"`
	Actions          []kanban.RouterAction      `json:"actions,omitempty"`
	LegacyAction     *kanban.RouterLegacyAction `json:"legacy_action,omitempty"`
	AppliedSeq       int                        `json:"applied_seq"`
	UpdatedAt        time.Time                  `json:"updated_at"`
}

// handleTaskRouterDecisions handles GET /api/tasks/{id}/router-decisions.
func (h *HTTPHandler) handleTaskRouterDecisions(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	taskID := strings.TrimSpace(chi.URLParam(r, "id"))
	rootID := taskActivityRootID(r)
	if taskID == "" || rootID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("task id and root_id required"))
		return
	}
	limit := queryInt(r, "limit", routerDecisionDefaultLimit)
	if limit <= 0 || limit > routerDecisionMaxLimit {
		limit = routerDecisionDefaultLimit
	}
	var before time.Time
	if raw := strings.TrimSpace(r.URL.Query().Get("before")); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			respondError(w, http.StatusBadRequest, errInvalidRequest("invalid before timestamp"))
			return
		}
		before = parsed
	}
	decisions, err := svc.ListRouterDecisions(r.Context(), rootID, taskID, limit, before)
	if err != nil {
		respondRouterError(w, err)
		return
	}
	summaries := make([]routerDecisionSummary, 0, len(decisions))
	for _, decision := range decisions {
		summaries = append(summaries, summarizeRouterDecision(decision))
	}
	respondJSON(w, http.StatusOK, map[string]any{"decisions": summaries})
}

// handleTaskRouterDecisionGet handles GET /api/tasks/{id}/router-decisions/{decisionId}.
func (h *HTTPHandler) handleTaskRouterDecisionGet(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	taskID := strings.TrimSpace(chi.URLParam(r, "id"))
	decisionID := strings.TrimSpace(chi.URLParam(r, "decisionId"))
	rootID := taskActivityRootID(r)
	if taskID == "" || decisionID == "" || rootID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("task id, decision id and root_id required"))
		return
	}
	decision, err := svc.GetRouterDecision(r.Context(), rootID, taskID, decisionID)
	if err != nil {
		respondRouterError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, detailRouterDecision(decision))
}

// handleTaskRouterSession handles GET /api/tasks/{id}/router-session.
func (h *HTTPHandler) handleTaskRouterSession(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	taskID := strings.TrimSpace(chi.URLParam(r, "id"))
	rootID := taskActivityRootID(r)
	if taskID == "" || rootID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("task id and root_id required"))
		return
	}
	record, err := svc.RouterSessionForTask(r.Context(), rootID, taskID)
	if err != nil {
		respondRouterError(w, err)
		return
	}
	if record == nil {
		respondJSON(w, http.StatusOK, map[string]any{"session": nil})
		return
	}
	// 只返回诊断所需的元数据；config_dir/cwd 是本机绝对路径，绝不下发。
	respondJSON(w, http.StatusOK, map[string]any{
		"session": map[string]any{
			"key":          record.SessionKey,
			"agent":        record.Agent,
			"model":        record.Model,
			"effort":       record.Effort,
			"status":       record.Status,
			"prompt_hash":  record.PromptHash,
			"last_error":   record.LastError,
			"last_turn_at": record.LastTurnAt,
			"created_at":   record.CreatedAt,
			"updated_at":   record.UpdatedAt,
		},
	})
}

// handleTaskRouterSessionTranscript handles
// GET /api/tasks/{id}/router-session/transcript?root_id=&before_seq=&limit=.
func (h *HTTPHandler) handleTaskRouterSessionTranscript(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	taskID := strings.TrimSpace(chi.URLParam(r, "id"))
	rootID := taskActivityRootID(r)
	if taskID == "" || rootID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("task id and root_id required"))
		return
	}
	record, err := svc.RouterSessionForTask(r.Context(), rootID, taskID)
	if err != nil {
		respondRouterError(w, err)
		return
	}
	if record == nil || strings.TrimSpace(record.SessionKey) == "" {
		respondError(w, http.StatusNotFound, kanban.ErrRouterDecisionNotFound)
		return
	}
	limit := queryInt(r, "limit", routerDecisionDefaultLimit)
	if limit <= 0 || limit > routerDecisionMaxLimit {
		limit = routerDecisionDefaultLimit
	}
	beforeSeq := queryInt(r, "before_seq", 0)
	uc := &usecase.Service{Registry: h.AppContext}
	window, meta, err := uc.GetSession(r.Context(), usecase.GetSessionInput{
		RootID:    rootID,
		Key:       record.SessionKey,
		BeforeSeq: beforeSeq,
		Limit:     limit,
	})
	if err != nil {
		respondRouterError(w, err)
		return
	}
	if window == nil || window.Type != session.TypeRouter {
		// 任务路由只允许读 Router 会话；绝不能借这个入口读普通会话。
		respondError(w, http.StatusNotFound, kanban.ErrRouterDecisionNotFound)
		return
	}
	turns := routerTranscriptTurns(r, uc, rootID, record.SessionKey, window.Exchanges)
	respondJSON(w, http.StatusOK, map[string]any{
		"session_key": record.SessionKey,
		"model":       record.Model,
		"status":      record.Status,
		"turns":       turns,
		"window":      meta,
	})
}

// handleRouterStatus handles GET /api/router/status?root_id=.
func (h *HTTPHandler) handleRouterStatus(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	rootID := taskActivityRootID(r)
	respondJSON(w, http.StatusOK, map[string]any{
		"root_id": rootID,
		"router":  svc.RouterStatusForRoot(rootID),
	})
}

func summarizeRouterDecision(decision *kanban.RouterDecision) routerDecisionSummary {
	summary := routerDecisionSummary{}
	if decision == nil {
		return summary
	}
	summary.ID = decision.ID
	summary.TaskID = decision.TaskID
	summary.TriggerKind = decision.TriggerKind
	summary.TriggerRef = decision.TriggerRef
	summary.UserActivityID = decision.UserActivityID
	summary.PlaceholderActivityID = decision.PlaceholderActivityID
	summary.Status = decision.Status
	summary.Agreement = decision.Agreement
	summary.Error = decision.Error
	summary.LatencyMS = decision.LatencyMS
	summary.PromptTokens = decision.PromptTokens
	summary.CompletionTokens = decision.CompletionTokens
	summary.CreatedAt = decision.CreatedAt
	if decision.LegacyAction != nil {
		summary.LegacyKind = decision.LegacyAction.Kind
	}
	kinds := make([]kanban.RouterActionKind, 0, len(decision.Actions))
	seen := make(map[kanban.RouterActionKind]bool, len(decision.Actions))
	for _, action := range decision.Actions {
		if seen[action.Kind] {
			continue
		}
		seen[action.Kind] = true
		kinds = append(kinds, action.Kind)
	}
	summary.ActionKinds = kinds
	return summary
}

func detailRouterDecision(decision *kanban.RouterDecision) routerDecisionDetail {
	detail := routerDecisionDetail{
		routerDecisionSummary: summarizeRouterDecision(decision),
	}
	if decision == nil {
		return detail
	}
	detail.RouterSessionKey = decision.RouterSessionKey
	detail.TurnID = decision.TurnID
	detail.BriefHash = decision.BriefHash
	detail.Actions = decision.Actions
	detail.LegacyAction = decision.LegacyAction
	detail.AppliedSeq = decision.AppliedSeq
	detail.UpdatedAt = decision.UpdatedAt
	if raw := strings.TrimSpace(decision.BriefJSON); raw != "" && json.Valid([]byte(raw)) {
		detail.Brief = json.RawMessage(raw)
	}
	return detail
}

func routerTranscriptTurns(r *http.Request, uc *usecase.Service, rootID, key string, exchanges []session.Exchange) []map[string]any {
	turns := make([]map[string]any, 0, len(exchanges))
	if len(exchanges) == 0 {
		return turns
	}
	seqs := make(map[int]bool, len(exchanges))
	for _, exchange := range exchanges {
		seqs[exchange.Seq] = true
	}
	blocks, _ := uc.GetSessionBlocksWindow(r.Context(), usecase.GetSessionBlocksWindowInput{
		RootID:    rootID,
		Key:       key,
		Seqs:      seqs,
		Exchanges: exchanges,
	})
	textBySeq := make(map[int]string, len(blocks))
	for seq, group := range blocks {
		var body strings.Builder
		for _, block := range group {
			if strings.TrimSpace(block.Text) == "" {
				continue
			}
			if body.Len() > 0 {
				body.WriteString("\n")
			}
			body.WriteString(block.Text)
		}
		textBySeq[seq] = truncateRouterRunes(body.String(), routerTranscriptBodyLimit)
	}
	for _, exchange := range exchanges {
		body := textBySeq[exchange.Seq]
		if body == "" {
			// 旧会话或块流尚未回填：回退到主日志里的唯一残留正文。
			body = truncateRouterRunes(exchange.LegacyContent, routerTranscriptBodyLimit)
		}
		turns = append(turns, map[string]any{
			"seq":       exchange.Seq,
			"role":      exchange.Role,
			"agent":     exchange.Agent,
			"model":     exchange.Model,
			"timestamp": exchange.Timestamp,
			"text":      body,
		})
	}
	return turns
}

func truncateRouterRunes(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

func queryInt(r *http.Request, name string, fallback int) int {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

func respondRouterError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, kanban.ErrRouterDecisionNotFound):
		respondError(w, http.StatusNotFound, err)
	case strings.Contains(strings.ToLower(err.Error()), "not found"),
		strings.Contains(strings.ToLower(err.Error()), "no rows"):
		respondError(w, http.StatusNotFound, err)
	default:
		respondError(w, http.StatusInternalServerError, err)
	}
}
