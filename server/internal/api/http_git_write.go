package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"mindfs/server/internal/api/usecase"
)

func (h *HTTPHandler) handleGitCheckout(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RootID string `json:"root"`
		Branch string `json:"branch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	req.RootID = strings.TrimSpace(req.RootID)
	req.Branch = strings.TrimSpace(req.Branch)
	if req.RootID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root required"))
		return
	}
	if req.Branch == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("branch required"))
		return
	}
	uc := h.service()
	out, err := uc.CheckoutGitBranch(r.Context(), usecase.CheckoutGitBranchInput{
		RootID: req.RootID,
		Branch: req.Branch,
	})
	if err != nil {
		respondJSON(w, http.StatusConflict, map[string]any{
			"error":   "git_checkout_failed",
			"message": err.Error(),
		})
		return
	}
	respondJSON(w, http.StatusOK, out)
}

func (h *HTTPHandler) handleGitPull(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeGitActionRequest(w, r, false, false)
	if !ok {
		return
	}
	uc := h.service()
	out, err := uc.GitPull(r.Context(), usecase.GitActionInput{RootID: req.RootID})
	respondGitAction(w, out, err, "git_pull_failed")
}

func (h *HTTPHandler) handleGitPush(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeGitActionRequest(w, r, false, false)
	if !ok {
		return
	}
	uc := h.service()
	out, err := uc.GitPush(r.Context(), usecase.GitActionInput{RootID: req.RootID})
	respondGitAction(w, out, err, "git_push_failed")
}

func (h *HTTPHandler) handleGitCommit(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeGitActionRequest(w, r, false, true)
	if !ok {
		return
	}
	uc := h.service()
	out, err := uc.GitCommit(r.Context(), usecase.GitActionInput{
		RootID:  req.RootID,
		Message: req.Message,
	})
	respondGitAction(w, out, err, "git_commit_failed")
}

func (h *HTTPHandler) handleGitStage(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeGitActionRequest(w, r, true, false)
	if !ok {
		return
	}
	uc := h.service()
	out, err := uc.GitStagePath(r.Context(), usecase.GitActionInput{
		RootID: req.RootID,
		Path:   req.Path,
	})
	respondGitAction(w, out, err, "git_stage_failed")
}

func (h *HTTPHandler) handleGitUnstage(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeGitActionRequest(w, r, true, false)
	if !ok {
		return
	}
	uc := h.service()
	out, err := uc.GitUnstagePath(r.Context(), usecase.GitActionInput{
		RootID: req.RootID,
		Path:   req.Path,
	})
	respondGitAction(w, out, err, "git_unstage_failed")
}

func (h *HTTPHandler) handleGitDiscard(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeGitActionRequest(w, r, true, false)
	if !ok {
		return
	}
	uc := h.service()
	out, err := uc.GitDiscardPath(r.Context(), usecase.GitActionInput{
		RootID: req.RootID,
		Path:   req.Path,
		Status: req.Status,
	})
	respondGitAction(w, out, err, "git_discard_failed")
}

type gitActionRequest struct {
	RootID  string `json:"root"`
	Path    string `json:"path"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

func decodeGitActionRequest(w http.ResponseWriter, r *http.Request, requirePath bool, requireMessage bool) (gitActionRequest, bool) {
	var req gitActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return req, false
	}
	req.RootID = strings.TrimSpace(req.RootID)
	req.Path = strings.TrimSpace(req.Path)
	req.Status = strings.TrimSpace(req.Status)
	req.Message = strings.TrimSpace(req.Message)
	if req.RootID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root required"))
		return req, false
	}
	if requirePath && req.Path == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("path required"))
		return req, false
	}
	if requireMessage && req.Message == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("message required"))
		return req, false
	}
	return req, true
}

func respondGitAction(w http.ResponseWriter, out usecase.GitActionOutput, err error, code string) {
	if err != nil {
		respondJSON(w, http.StatusConflict, map[string]any{
			"error":   code,
			"message": err.Error(),
		})
		return
	}
	respondJSON(w, http.StatusOK, out)
}
