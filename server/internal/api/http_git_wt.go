package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"mindfs/server/internal/api/usecase"
)

func (h *HTTPHandler) handleGitWorktreeList(w http.ResponseWriter, r *http.Request) {
	rootID := strings.TrimSpace(r.URL.Query().Get("root"))
	if rootID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root required"))
		return
	}
	uc := h.service()
	out, err := uc.ListGitWorktrees(r.Context(), usecase.ListGitWorktreesInput{RootID: rootID})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, out)
}

func (h *HTTPHandler) handleGitWorktreeCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RootID     string `json:"root"`
		ParentPath string `json:"parent_path"`
		Name       string `json:"name"`
		BranchMode string `json:"branch_mode"`
		Branch     string `json:"branch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json"))
		return
	}
	uc := h.service()
	out, err := uc.CreateGitWorktree(r.Context(), usecase.CreateGitWorktreeInput{
		RootID:     req.RootID,
		ParentPath: req.ParentPath,
		Name:       req.Name,
		BranchMode: req.BranchMode,
		Branch:     req.Branch,
		Register:   true,
	})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	if h.AppContext != nil {
		h.broadcastRootChanged("added", out.Dir.ID)
	}
	respondJSON(w, http.StatusOK, managedDirResponse(out.Dir))
}

func (h *HTTPHandler) handleGitWorktreeRemove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RootID string `json:"root"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json"))
		return
	}
	uc := h.service()
	out, err := uc.RemoveGitWorktree(r.Context(), usecase.RemoveGitWorktreeInput{RootID: req.RootID})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	if h.AppContext != nil {
		h.broadcastRootChanged("removed", out.Dir.ID)
	}
	respondJSON(w, http.StatusOK, managedDirResponse(out.Dir))
}
