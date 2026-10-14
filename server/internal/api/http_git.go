package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"mindfs/server/internal/api/usecase"
)

func (h *HTTPHandler) handleGitStatus(w http.ResponseWriter, r *http.Request) {
	rootID := strings.TrimSpace(r.URL.Query().Get("root"))
	rootPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if rootID == "" && rootPath == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root or path required"))
		return
	}
	uc := h.service()
	out, err := uc.GetGitStatus(r.Context(), usecase.GitStatusInput{RootID: rootID, RootPath: rootPath})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSONConditional(w, r, out.Status)
}

func (h *HTTPHandler) handleGitDiff(w http.ResponseWriter, r *http.Request) {
	rootID := strings.TrimSpace(r.URL.Query().Get("root"))
	repoPath := strings.TrimSpace(r.URL.Query().Get("repo_path"))
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if rootID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root required"))
		return
	}
	if path == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("path required"))
		return
	}
	uc := h.service()
	out, err := uc.GetGitDiff(r.Context(), usecase.GitDiffInput{
		RootID:   rootID,
		RepoPath: repoPath,
		Path:     path,
	})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, out.Diff)
}

func (h *HTTPHandler) handleGitHistory(w http.ResponseWriter, r *http.Request) {
	rootID := strings.TrimSpace(r.URL.Query().Get("root"))
	if rootID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root required"))
		return
	}
	limit := 10
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil {
			respondError(w, http.StatusBadRequest, errInvalidRequest("invalid limit"))
			return
		}
		limit = parsed
	}
	uc := h.service()
	out, err := uc.GetGitHistory(r.Context(), usecase.GitHistoryInput{
		RootID:       rootID,
		Limit:        limit,
		BeforeCommit: strings.TrimSpace(r.URL.Query().Get("before_commit")),
		AfterCommit:  strings.TrimSpace(r.URL.Query().Get("after_commit")),
	})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, out.History)
}

func (h *HTTPHandler) handleGitCommitFiles(w http.ResponseWriter, r *http.Request) {
	rootID := strings.TrimSpace(r.URL.Query().Get("root"))
	commit := strings.TrimSpace(r.URL.Query().Get("commit"))
	if rootID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root required"))
		return
	}
	if commit == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("commit required"))
		return
	}
	uc := h.service()
	out, err := uc.GetGitCommitFiles(r.Context(), usecase.GitCommitFilesInput{
		RootID: rootID,
		Commit: commit,
	})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, out.Files)
}

func (h *HTTPHandler) handleGitCommitDiff(w http.ResponseWriter, r *http.Request) {
	rootID := strings.TrimSpace(r.URL.Query().Get("root"))
	commit := strings.TrimSpace(r.URL.Query().Get("commit"))
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if rootID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root required"))
		return
	}
	if commit == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("commit required"))
		return
	}
	if path == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("path required"))
		return
	}
	uc := h.service()
	out, err := uc.GetGitCommitDiff(r.Context(), usecase.GitCommitDiffInput{
		RootID: rootID,
		Commit: commit,
		Path:   path,
	})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, out.Diff)
}

func (h *HTTPHandler) handleGitRelatedFileDiff(w http.ResponseWriter, r *http.Request) {
	rootID := strings.TrimSpace(r.URL.Query().Get("root"))
	repoPath := strings.TrimSpace(r.URL.Query().Get("repo_path"))
	repoKind := strings.TrimSpace(r.URL.Query().Get("repo_kind"))
	head := strings.TrimSpace(r.URL.Query().Get("head"))
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if rootID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root required"))
		return
	}
	if path == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("path required"))
		return
	}
	uc := h.service()
	out, err := uc.GetGitRelatedFileDiff(r.Context(), usecase.GitRelatedFileDiffInput{
		RootID:   rootID,
		RepoPath: repoPath,
		RepoKind: repoKind,
		Head:     head,
		Path:     path,
	})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, out.Diff)
}

// maxGitRelatedFileStatTargets 是一次批量统计请求的文件数上限。
// 上限的意义是防止单个请求把后端的 git 子进程数放大到不可控 ——
// 实测一个任务的关联文件在 100 以内，500 留了足够余量。
const maxGitRelatedFileStatTargets = 500

// handleGitRelatedFileStats 批量返回关联文件的增删统计，不返回 diff 正文。
// 会话/任务视图的 +N −M 徽标只需要 stat，逐文件去取完整 diff 是纯浪费：
// 实测 80 个文件的徽标会打出 80 次请求、每次 6473B 里 5073B 是没人读的正文。
func (h *HTTPHandler) handleGitRelatedFileStats(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RootID  string `json:"root"`
		Targets []struct {
			ID       string `json:"id"`
			Path     string `json:"path"`
			Head     string `json:"head"`
			RepoPath string `json:"repo_path"`
			RepoKind string `json:"repo_kind"`
		} `json:"targets"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	req.RootID = strings.TrimSpace(req.RootID)
	if req.RootID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root required"))
		return
	}
	if len(req.Targets) > maxGitRelatedFileStatTargets {
		respondError(w, http.StatusBadRequest, errInvalidRequest("too many targets"))
		return
	}
	targets := make([]usecase.GitRelatedFileStatTargetInput, 0, len(req.Targets))
	for _, target := range req.Targets {
		targets = append(targets, usecase.GitRelatedFileStatTargetInput{
			ID:       target.ID,
			Path:     target.Path,
			Head:     target.Head,
			RepoPath: target.RepoPath,
			RepoKind: target.RepoKind,
		})
	}
	uc := h.service()
	out, err := uc.GetGitRelatedFileStats(r.Context(), usecase.GitRelatedFileStatsInput{
		RootID:  req.RootID,
		Targets: targets,
	})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, out)
}

func (h *HTTPHandler) handleGitBranches(w http.ResponseWriter, r *http.Request) {
	rootID := strings.TrimSpace(r.URL.Query().Get("root"))
	if rootID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root required"))
		return
	}
	uc := h.service()
	out, err := uc.ListGitBranches(r.Context(), usecase.ListGitBranchesInput{RootID: rootID})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, out)
}

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
