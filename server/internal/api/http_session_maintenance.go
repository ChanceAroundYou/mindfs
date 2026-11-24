package api

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// handleSessionMaintenanceDiagnose 只读列出本机会话的转录体积、图片与陈旧锁。
func (h *HTTPHandler) handleSessionMaintenanceDiagnose(w http.ResponseWriter, r *http.Request) {
	rootID := chi.URLParam(r, "rootID")
	if rootID == "" {
		rootID = r.URL.Query().Get("root")
	}
	report, err := h.service().SessionMaintenanceDiagnose(r.Context(), rootID)
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, report)
}

func maintenanceRootFromRequest(r *http.Request) string {
	if rootID := chi.URLParam(r, "rootID"); rootID != "" {
		return rootID
	}
	if rootID := r.URL.Query().Get("root"); rootID != "" {
		return rootID
	}
	return ""
}

// handleSessionMaintenanceCleanImages 只替换原生转录里的图片块，不动对话正文。
func (h *HTTPHandler) handleSessionMaintenanceCleanImages(w http.ResponseWriter, r *http.Request) {
	out, err := h.service().SessionMaintenanceCleanImages(r.Context(), maintenanceRootFromRequest(r), chi.URLParam(r, "key"))
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, out)
}

// handleSessionMaintenanceCleanLocks 只删 PID 已死的会话锁。
func (h *HTTPHandler) handleSessionMaintenanceCleanLocks(w http.ResponseWriter, r *http.Request) {
	out, err := h.service().SessionMaintenanceCleanLocks(r.Context())
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, out)
}

// handleSessionMaintenancePurgeEmpty 删空会话必须显式确认。
func (h *HTTPHandler) handleSessionMaintenancePurgeEmpty(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RootID  string `json:"root_id"`
		Confirm bool   `json:"confirm"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	rootID := req.RootID
	if rootID == "" {
		rootID = maintenanceRootFromRequest(r)
	}
	out, err := h.service().SessionMaintenancePurgeEmpty(r.Context(), rootID, chi.URLParam(r, "key"), req.Confirm)
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, out)
}
