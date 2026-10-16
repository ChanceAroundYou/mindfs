package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

func (h *HTTPHandler) handleAgentAPIProvidersList(w http.ResponseWriter, r *http.Request) {
	agentName := strings.TrimSpace(r.URL.Query().Get("agent"))
	providers, err := readAgentAPIProviders()
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, err)
		return
	}
	out := make([]agentAPIProviderPublic, 0, len(providers))
	for _, provider := range providers {
		if agentName != "" && !apiProviderCompatibleWithAgent(provider, agentName) {
			continue
		}
		out = append(out, publicAgentAPIProvider(provider))
	}
	respondJSON(w, http.StatusOK, out)
}

func (h *HTTPHandler) handleAgentAPIProviderCreate(w http.ResponseWriter, r *http.Request) {
	var req agentAPIProviderCreateRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxUploadRequestBytes)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid request body"))
		return
	}
	provider, err := createAgentAPIProvider(r.Context(), req)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errAgentConfigConflict) {
			status = http.StatusConflict
		}
		respondError(w, status, err)
		return
	}
	respondJSON(w, http.StatusOK, publicAgentAPIProvider(provider))
}

func (h *HTTPHandler) handleAgentAPIProvidersSync(w http.ResponseWriter, r *http.Request) {
	var req agentAPIProviderSyncRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxUploadRequestBytes)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid request body"))
		return
	}
	providers, err := syncAgentAPIProviders(r.Context(), req.Providers)
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	out := make([]agentAPIProviderPublic, 0, len(providers))
	for _, provider := range providers {
		out = append(out, publicAgentAPIProvider(provider))
	}
	respondJSON(w, http.StatusOK, map[string]any{"providers": out})
}

func (h *HTTPHandler) handleAgentAPIProvidersSyncAll(w http.ResponseWriter, r *http.Request) {
	providers, results, err := syncAllAgentAPIProviders(r.Context(), h.AppContext.GetPreferences())
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, err)
		return
	}
	out := make([]agentAPIProviderPublic, 0, len(providers))
	for _, provider := range providers {
		out = append(out, publicAgentAPIProvider(provider))
	}
	respondJSON(w, http.StatusOK, map[string]any{"providers": out, "results": results})
}

func (h *HTTPHandler) handleAgentAPIProviderTest(w http.ResponseWriter, r *http.Request) {
	var req agentAPIProviderTestRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxUploadRequestBytes)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid request body"))
		return
	}
	result, err := testAgentAPIProviderModel(r.Context(), req)
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, result)
}

func (h *HTTPHandler) handleAgentAPIProviderDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("provider id required"))
		return
	}
	providers, err := deleteAgentAPIProvider(id)
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	out := make([]agentAPIProviderPublic, 0, len(providers))
	for _, provider := range providers {
		out = append(out, publicAgentAPIProvider(provider))
	}
	respondJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id, "providers": out})
}

func (h *HTTPHandler) handleAgentAPIProviderSwitch(w http.ResponseWriter, r *http.Request) {
	var req agentAPIProviderSwitchRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxUploadRequestBytes)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid request body"))
		return
	}
	provider, err := switchAgentAPIProvider(req, h.AppContext)
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"provider": publicAgentAPIProvider(provider),
	})
}
