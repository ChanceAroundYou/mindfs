package api

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"

	"mindfs/server/internal/api/usecase"
)

// handleRawAsset 按**路径**返回项目里的原始文件（HTML 网页及其相对资源、视频等）。
//
// 与 /api/file?raw=1 的区别：
//   - 路径式：网页里的相对 URL（style.css、img/x.png）能自然解析到同目录的兄弟文件；
//     查询参数式不行，浏览器解析相对 URL 时会把查询串丢掉。
//   - 支持 Range（http.ServeContent）：视频能边下边播、进度条可拖。
//
// 账户不在这里解析：账户段由 WithRawAccountPath 中间件从路径里搬进 user=，
// 派发到本账户的 handler 时已经是「本账户」视角了。
func (h *HTTPHandler) handleRawAsset(w http.ResponseWriter, r *http.Request) {
	rootID := strings.TrimSpace(chi.URLParam(r, "root"))
	relPath := strings.TrimSpace(chi.URLParam(r, "*"))
	if rootID == "" || relPath == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root and path required"))
		return
	}

	uc := h.service()
	rawOut, err := uc.OpenFileRaw(r.Context(), usecase.OpenFileRawInput{
		RootID: rootID,
		Path:   relPath,
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

	ext := filepath.Ext(rawOut.RelPath)
	contentType := mime.TypeByExtension(ext)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	// 明确告诉浏览器别猜类型：猜错会把 HTML 当成别的类型渲染。
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if strings.HasPrefix(strings.ToLower(contentType), "text/html") {
		// 页面文件来自项目目录，属不可信内容。sandbox 让文档落在 opaque origin：
		// 即使用户把这个 URL 直接在新标签页打开，它也读不到应用的 localStorage。
		// 前端的 iframe 还额外带 sandbox="allow-scripts"（不给 allow-same-origin）。
		w.Header().Set("Content-Security-Policy", "sandbox allow-scripts")
	}
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(rawOut.RelPath)))
	}

	// ServeContent 处理 Range（206）与 If-Modified-Since；视频的进度条靠它。
	http.ServeContent(w, r, rawOut.RelPath, rawOut.Info.ModTime(), rawOut.File)
}
