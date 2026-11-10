package api

import (
	"net/http"

	"mindfs/internal/codexsync"
)

func (h *HTTPHandler) handleAgentConfigSyncStatus(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, codexsync.CurrentStatus())
}

func (h *HTTPHandler) handleAgentConfigSyncRequest(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusAccepted, codexsync.Request(r.Context()))
}
