package api

import (
	"encoding/json"
	"net/http"
	"strings"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/session"
)

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
