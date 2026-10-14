package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/fs"
	"mindfs/server/internal/gitview"

	"github.com/go-chi/chi/v5"
)

func (h *HTTPHandler) handleTree(w http.ResponseWriter, r *http.Request) {
	rootID := r.URL.Query().Get("root")
	uc := h.service()
	out, err := uc.ListTree(r.Context(), usecase.ListTreeInput{
		RootID: rootID,
		Dir:    r.URL.Query().Get("dir"),
	})
	if err != nil {
		// 目录不存在时返回空树，而非 400：前端会轮询 .mindfs/plugins 等可选目录
		if errors.Is(err, os.ErrNotExist) {
			respondJSONConditional(w, r, map[string]any{"entries": []any{}})
			return
		}
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSONConditional(w, r, map[string]any{
		"entries": out.Entries,
	})
}

func (h *HTTPHandler) handleFile(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("edit") == "1" {
		h.handleEditableFile(w, r)
		return
	}
	rootID := r.URL.Query().Get("root")
	uc := h.service()
	path := r.URL.Query().Get("path")
	if path == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("path required"))
		return
	}
	cursor, err := parseNonNegativeInt64Query(r, "cursor")
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("cursor must be a non-negative integer"))
		return
	}
	readMode := strings.TrimSpace(r.URL.Query().Get("read"))
	if readMode == "" {
		readMode = "incremental"
	}
	if readMode != "incremental" && readMode != "full" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("read must be incremental or full"))
		return
	}
	cachedMTime, err := parseOptionalTimeQuery(r, "mtime")
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("mtime must be RFC3339"))
		return
	}
	raw := r.URL.Query().Get("raw")
	if raw == "1" {
		rawOut, err := uc.OpenFileRaw(r.Context(), usecase.OpenFileRawInput{
			RootID: rootID,
			Path:   path,
		})
		if err != nil {
			// 文件不存在 → 404（资源不存在语义），而非 400
			status := http.StatusBadRequest
			if errors.Is(err, os.ErrNotExist) {
				status = http.StatusNotFound
			}
			respondError(w, status, err)
			return
		}
		defer rawOut.File.Close()
		w.Header().Set("Content-Length", strconv.FormatInt(rawOut.Info.Size(), 10))
		ext := filepath.Ext(rawOut.RelPath)
		if mimeType := mime.TypeByExtension(ext); mimeType != "" {
			w.Header().Set("Content-Type", mimeType)
		} else {
			w.Header().Set("Content-Type", "application/octet-stream")
		}
		if r.URL.Query().Get("download") == "1" {
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(rawOut.RelPath)))
		}
		w.WriteHeader(http.StatusOK)
		io.Copy(w, rawOut.File)
		return
	}
	if !cachedMTime.IsZero() {
		info, err := uc.GetFileInfo(r.Context(), usecase.GetFileInfoInput{
			RootID: rootID,
			Path:   path,
		})
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, os.ErrNotExist) {
				status = http.StatusNotFound
			}
			respondError(w, status, err)
			return
		}
		if info.MTime.Equal(cachedMTime) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	out, err := uc.ReadFile(r.Context(), usecase.ReadFileInput{
		RootID:   rootID,
		Path:     path,
		MaxBytes: 128 * 1024,
		Cursor:   cursor,
		ReadMode: readMode,
	})
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		}
		respondError(w, status, err)
		return
	}
	payload := map[string]any{
		"file": out.File,
	}
	respondJSON(w, http.StatusOK, payload)
}

func (h *HTTPHandler) handleUpload(w http.ResponseWriter, r *http.Request) {
	rootID := strings.TrimSpace(r.URL.Query().Get("root"))
	if rootID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root required"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadRequestBytes)
	if err := r.ParseMultipartForm(maxUploadRequestBytes); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid multipart form"))
		return
	}
	if r.MultipartForm == nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("files required"))
		return
	}
	fileHeaders := r.MultipartForm.File["files"]
	if len(fileHeaders) == 0 {
		respondError(w, http.StatusBadRequest, errInvalidRequest("files required"))
		return
	}
	if len(fileHeaders) > maxUploadFileCount {
		respondError(w, http.StatusBadRequest, errInvalidRequest("too many files"))
		return
	}
	files, err := buildUploadFiles(fileHeaders)
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	defer closeMultipartFiles(files)

	uc := h.service()
	out, err := uc.SaveUploadedFiles(r.Context(), usecase.SaveUploadedFilesInput{
		RootID: rootID,
		Dir:    strings.TrimSpace(r.FormValue("dir")),
		Files:  files,
	})
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "root not found") {
			status = http.StatusNotFound
		}
		respondError(w, status, err)
		return
	}
	payload := map[string]any{
		"files": out.Files,
	}
	respondJSON(w, http.StatusOK, payload)
}

func buildUploadFiles(headers []*multipart.FileHeader) ([]usecase.UploadFile, error) {
	files := make([]usecase.UploadFile, 0, len(headers))
	for _, header := range headers {
		file, err := header.Open()
		if err != nil {
			closeMultipartFiles(files)
			return nil, errInvalidRequest("failed to open uploaded file")
		}
		files = append(files, usecase.UploadFile{
			Name:        header.Filename,
			ContentType: header.Header.Get("Content-Type"),
			Reader:      file,
		})
	}
	return files, nil
}

func closeMultipartFiles(files []usecase.UploadFile) {
	for _, file := range files {
		closer, ok := file.Reader.(io.Closer)
		if !ok {
			continue
		}
		_ = closer.Close()
	}
}

func parseNonNegativeInt64Query(r *http.Request, key string) (int64, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0, errInvalidRequest(key + " must be non-negative")
	}
	return value, nil
}

func parseOptionalTimeQuery(r *http.Request, key string) (time.Time, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return time.Time{}, nil
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		value, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return time.Time{}, errInvalidRequest(key + " must be RFC3339")
		}
	}
	return value.UTC(), nil
}

func parsePositiveIntQuery(r *http.Request, key string) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, errInvalidRequest(key + " must be positive")
	}
	return value, nil
}

func truthyQuery(r *http.Request, key string) bool {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

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

const indexHTML = `<!doctype html>
<html lang="zh-CN">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>MindFS</title>
    <style>
      :root {
        color-scheme: light;
      }
      * { box-sizing: border-box; }
      body {
        margin: 0;
        font-family: "Avenir Next", "Helvetica Neue", Arial, sans-serif;
        background: #f7f7f5;
        color: #222;
      }
      .notice {
        display: none;
        margin: 12px;
        padding: 10px 12px;
        border: 1px solid #d8b4b4;
        border-radius: 6px;
        background: #fff5f5;
        color: #8a1f1f;
        font-size: 13px;
        line-height: 1.45;
      }
      .notice.is-visible {
        display: block;
      }
      .shell {
        display: grid;
        grid-template-columns: 260px 1fr;
        grid-template-rows: 1fr 64px;
        height: 100vh;
        background: #fff;
      }
      aside {
        grid-row: 1 / span 2;
        border-right: 1px solid #e5e5e5;
        padding: 12px;
        overflow: auto;
        background: linear-gradient(180deg, #faf9f6 0%, #ffffff 100%);
      }
      main {
        padding: 16px;
        overflow: auto;
      }
      footer {
        border-top: 1px solid #e5e5e5;
        padding: 12px 16px;
        display: flex;
        align-items: center;
        gap: 12px;
      }
      .file-button {
        display: block;
        width: 100%;
        text-align: left;
        border: none;
        background: transparent;
        padding: 4px 0;
        cursor: pointer;
        color: #333;
      }
      .card {
        border: 1px solid #e5e5e5;
        border-radius: 8px;
        padding: 10px 12px;
        display: flex;
        justify-content: space-between;
        margin-bottom: 8px;
        background: #fff;
      }
      .status {
        font-size: 12px;
        color: #666;
      }
      input[type="text"] {
        flex: 1;
        padding: 8px 10px;
        border-radius: 6px;
        border: 1px solid #ccc;
      }
      button {
        padding: 8px 12px;
        border-radius: 6px;
        border: 1px solid #ccc;
        background: #f5f5f5;
        cursor: pointer;
      }
    </style>
  </head>
  <body>
    __FALLBACK_NOTICE__
    <div class="shell">
      <aside>
        <h3>Files</h3>
        <div id="tree">加载中...</div>
      </aside>
      <main>
        <h2 style="margin-top: 0;">Workspace</h2>
        <div id="list"></div>
      </main>
      <footer>
        <input type="text" placeholder="Ask or type a command..." />
        <button type="button">Run</button>
        <span class="status" id="status">Connected</span>
      </footer>
    </div>
    <script>
      var root = new URLSearchParams(window.location.search).get("root") || "";
      fetch("/api/tree?" + new URLSearchParams({ root: root, dir: "." }).toString())
        .then(function (res) { return res.json(); })
        .then(function (payload) {
          var tree = Array.isArray(payload) ? payload : (Array.isArray(payload.entries) ? payload.entries : []);
          var treeEl = document.getElementById("tree");
          var listEl = document.getElementById("list");
          treeEl.innerHTML = "";
          listEl.innerHTML = "";
          tree.forEach(function (entry) {
            var btn = document.createElement("button");
            btn.className = "file-button";
            btn.textContent = (entry.is_dir ? "📁 " : "📄 ") + entry.name;
            treeEl.appendChild(btn);

            var card = document.createElement("div");
            card.className = "card";
            card.innerHTML = "<span>" + entry.name + "</span><span>" + (entry.is_dir ? "Folder" : "File") + "</span>";
            listEl.appendChild(card);
          });
        })
        .catch(function () {
          var treeEl = document.getElementById("tree");
          treeEl.textContent = "无法加载目录树";
        });
    </script>
  </body>
</html>`
