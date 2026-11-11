package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

const (
	maxAgentCustomModels      = 100
	maxAgentCustomModelLength = 200
)

type agentCustomModelsRequest struct {
	Agent  string   `json:"agent"`
	Models []string `json:"models"`
}

func (h *HTTPHandler) handleAgentCustomModelsGet(w http.ResponseWriter, r *http.Request) {
	agentName := strings.TrimSpace(r.URL.Query().Get("agent"))
	if agentName == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("agent required"))
		return
	}
	if h.AppContext == nil || h.AppContext.GetPreferences() == nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("preferences not configured"))
		return
	}
	respondJSON(w, http.StatusOK, agentCustomModelsResponse{
		Agent:  agentName,
		Models: h.AppContext.GetPreferences().AgentCustomModels(agentName),
	})
}

func (h *HTTPHandler) handleAgentCustomModelsPut(w http.ResponseWriter, r *http.Request) {
	var req agentCustomModelsRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid request body"))
		return
	}
	req.Agent = strings.TrimSpace(req.Agent)
	if req.Agent == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("agent required"))
		return
	}
	if len(req.Models) > maxAgentCustomModels {
		respondError(w, http.StatusBadRequest, errInvalidRequest("too many custom models"))
		return
	}
	for _, model := range req.Models {
		if len(strings.TrimSpace(model)) > maxAgentCustomModelLength {
			respondError(w, http.StatusBadRequest, errInvalidRequest("custom model id is too long"))
			return
		}
	}
	if h.AppContext == nil || h.AppContext.GetPreferences() == nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("preferences not configured"))
		return
	}
	if err := h.AppContext.GetPreferences().UpdateAgentCustomModels(req.Agent, req.Models); err != nil {
		respondError(w, http.StatusInternalServerError, errInvalidRequest(err.Error()))
		return
	}
	// 让已连接的会话端立刻拿到新的模型目录（agent.status.changed）。
	h.AppContext.BroadcastAgentStatusChanged(req.Agent)
	respondJSON(w, http.StatusOK, agentCustomModelsResponse{
		Agent:  req.Agent,
		Models: h.AppContext.GetPreferences().AgentCustomModels(req.Agent),
	})
}

type agentCustomModelsResponse struct {
	Agent  string   `json:"agent"`
	Models []string `json:"models"`
}
