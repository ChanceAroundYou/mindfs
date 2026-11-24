package api

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// handleSessionRuntimeIssues 列出「会话还钉在已删除目录上」的会话（skill verify 的读口）。
func (h *HTTPHandler) handleSessionRuntimeIssues(w http.ResponseWriter, r *http.Request) {
	rootID := chi.URLParam(r, "rootID")
	if rootID == "" {
		rootID = r.URL.Query().Get("root")
	}
	if rootID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root is required"))
		return
	}
	out, err := h.service().DiagnoseRuntimeIssues(r.Context(), rootID)
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, out)
}

// handleSessionRuntimeIssuesSweep 巡检并修复。body 为空等同于只诊断，避免误触批量修复。
func (h *HTTPHandler) handleSessionRuntimeIssuesSweep(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RootID string `json:"root_id"`
		Apply  bool   `json:"apply"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	if req.RootID == "" {
		req.RootID = chi.URLParam(r, "rootID")
	}
	if req.RootID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root_id is required"))
		return
	}
	if !req.Apply {
		report, err := h.service().DiagnoseRuntimeIssues(r.Context(), req.RootID)
		if err != nil {
			respondError(w, http.StatusBadRequest, err)
			return
		}
		respondJSON(w, http.StatusOK, report)
		return
	}
	out, err := h.service().SweepRuntimeIssues(r.Context(), req.RootID)
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, out)
}
