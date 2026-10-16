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
