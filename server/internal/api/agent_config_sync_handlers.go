package api

import (
	"net/http"

	"mindfs/internal/codexsync"
)

func agentConfigSyncResult(status codexsync.Result) codexsync.Result {
	status.Harnesses = codexsync.HarnessStatuses()
	return status
}

func (h *HTTPHandler) handleAgentConfigSyncStatus(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, agentConfigSyncResult(codexsync.CurrentStatus()))
}

func (h *HTTPHandler) handleAgentConfigSyncRequest(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusAccepted, agentConfigSyncResult(codexsync.Request(r.Context())))
}
