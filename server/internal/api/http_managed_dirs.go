package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/fs"
	"mindfs/server/internal/gitview"

	"github.com/go-chi/chi/v5"
)

func (h *HTTPHandler) handleDirs(w http.ResponseWriter, _ *http.Request) {
	uc := h.service()
	out, err := uc.ListManagedDirs(nil)
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, err)
		return
	}
	resp := make([]map[string]any, 0, len(out.Dirs))
	for _, dir := range out.Dirs {
		resp = append(resp, managedDirResponse(dir))
	}
	respondJSON(w, http.StatusOK, resp)
}

func (h *HTTPHandler) handleAddDir(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path   string `json:"path"`
		Create bool   `json:"create"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json"))
		return
	}
	uc := h.service()
	out, err := uc.AddManagedDir(r.Context(), usecase.AddManagedDirInput{Path: req.Path, Create: req.Create})
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, fs.ErrRootNameConflict) {
			status = http.StatusConflict
		}
		respondError(w, status, err)
		return
	}
	if h.AppContext != nil {
		h.broadcastRootChanged("added", out.Dir.ID)
	}
	respondJSON(w, http.StatusOK, managedDirResponse(out.Dir))
}

func (h *HTTPHandler) handleRenameDir(w http.ResponseWriter, r *http.Request) {
	rootID := strings.TrimSpace(chi.URLParam(r, "id"))
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json"))
		return
	}
	uc := h.service()
	out, err := uc.RenameManagedDir(r.Context(), usecase.RenameManagedDirInput{
		RootID: rootID,
		Name:   req.Name,
	})
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, fs.ErrRootNameConflict) || strings.Contains(err.Error(), "already exists") {
			status = http.StatusConflict
		} else if strings.Contains(err.Error(), "root not found") {
			status = http.StatusNotFound
		}
		respondError(w, status, err)
		return
	}
	if h.AppContext != nil {
		rootPayload := managedDirResponse(out.Dir)
		h.broadcastRootChanged("renamed", out.Dir.ID, map[string]any{
			"old_root_id": out.OldRootID,
			"root":        rootPayload,
		})
	}
	respondJSON(w, http.StatusOK, managedDirResponse(out.Dir))
}

func (h *HTTPHandler) handleUpdateDirDisplayName(w http.ResponseWriter, r *http.Request) {
	rootID := strings.TrimSpace(chi.URLParam(r, "id"))
	var req struct {
		DisplayName string `json:"display_name"`
		Name        string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json"))
		return
	}
	displayName := strings.TrimSpace(req.DisplayName)
	if displayName == "" {
		displayName = strings.TrimSpace(req.Name)
	}
	uc := h.service()
	out, err := uc.UpdateRootDisplayName(r.Context(), usecase.UpdateRootDisplayNameInput{
		RootID:      rootID,
		DisplayName: displayName,
	})
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "root not found") {
			status = http.StatusNotFound
		}
		respondError(w, status, err)
		return
	}
	if h.AppContext != nil {
		h.broadcastRootChanged("display_name_changed", out.Dir.ID, map[string]any{
			"root": managedDirResponse(out.Dir),
		})
	}
	respondJSON(w, http.StatusOK, managedDirResponse(out.Dir))
}

func (h *HTTPHandler) handleRemoveDir(w http.ResponseWriter, r *http.Request) {
	path := readManagedDirPath(r)
	uc := h.service()
	out, err := uc.RemoveManagedDir(r.Context(), usecase.RemoveManagedDirInput{Path: path})
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "root not found") {
			status = http.StatusNotFound
		}
		respondError(w, status, err)
		return
	}
	if h.AppContext != nil {
		h.broadcastRootChanged("removed", out.Dir.ID)
	}
	respondJSON(w, http.StatusOK, managedDirResponse(out.Dir))
}

func readManagedDirPath(r *http.Request) string {
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path != "" {
		return path
	}

	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return ""
	}
	return strings.TrimSpace(req.Path)
}

func managedDirResponse(dir fs.RootInfo) map[string]any {
	effectiveName := dir.EffectiveName()
	resp := map[string]any{
		"id":               dir.ID,
		"display_name":     effectiveName,
		"display_name_raw": dir.DisplayName,
		"root_path":        dir.RootPath,
		"meta_location":    dir.EffectiveMetaLocation(),
		"created_at":       dir.CreatedAt,
		"updated_at":       dir.UpdatedAt,
	}
	if info, err := dir.StatRoot(); err == nil {
		resp["size"] = info.Size()
		resp["mtime"] = info.ModTime().UTC().Format(time.RFC3339Nano)
	}
	if ok, err := gitview.HasRepo(context.Background(), dir.RootPath); err == nil {
		resp["is_git_repo"] = ok
	}
	if ok, err := gitview.IsWorktree(dir.RootPath); err == nil {
		resp["is_git_worktree"] = ok
	}
	return resp
}
