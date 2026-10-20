package api

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"mindfs/server/internal/api/usecase"
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
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadFileRequestBytes)
	if err := r.ParseMultipartForm(maxUploadMemoryBytes); err != nil {
		// 超限是用户的错，给个说得清的 413；别再回一句 "invalid multipart form"。
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			respondError(w, http.StatusRequestEntityTooLarge, errInvalidRequest("upload exceeds size limit"))
			return
		}
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
