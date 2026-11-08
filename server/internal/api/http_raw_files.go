package api

import (
	"bytes"
	"errors"
	"fmt"
	"io"
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
	contentType := rawAssetContentType(ext)
	isHTML := strings.HasPrefix(strings.ToLower(contentType), "text/html")
	w.Header().Set("Content-Type", contentType)
	// 明确告诉浏览器别猜类型：猜错会把 HTML 当成别的类型渲染。
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if isHTML {
		// 页面文件来自项目目录，属不可信内容。这里**放开同源**（用户 2026-11-09 拍板）：
		// 页面要像普通标签页一样跑，localStorage / IndexedDB / serviceWorker 都可用。
		// 代价是与 mindfs 同源、能碰 parent.document；预览自己的项目文件是本工具的
		// 既定用法，这个边界按「信任本地项目内容」处理。
		// 前端 iframe 的 sandbox 必须与本头的 flag 保持一致。
		w.Header().Set("Content-Security-Policy", "sandbox allow-scripts allow-same-origin")
	}
	download := r.URL.Query().Get("download") == "1"
	if download {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(rawOut.RelPath)))
	}

	// 每次都向服务端确认（Last-Modified 在，304 很便宜）；不能让它按启发式缓存
	// 一直用旧字节 —— 文件被改写后同名资源必须立刻能取到新版本。
	w.Header().Set("Cache-Control", "no-cache")

	// HTML 预览要先加工（注入错误探针 + 根绝对路径兜底，见 raw_asset_html.go）；
	// 下载与超大文件保持原字节。视频等其它类型继续流式 + Range。
	if isHTML && !download && rawOut.Info.Size() <= maxRewritableHTMLBytes {
		if raw, err := io.ReadAll(rawOut.File); err == nil {
			http.ServeContent(w, r, rawOut.RelPath, rawOut.Info.ModTime(),
				bytes.NewReader(prepareHTMLForPreview(raw, rawOut.RelPath)))
			return
		}
		// 读失败就退回原文件；偏移已经乱了，先 Seek 回起点。
		if _, err := rawOut.File.Seek(0, io.SeekStart); err != nil {
			return
		}
	}
	// ServeContent 处理 Range（206）与 If-Modified-Since；视频的进度条靠它。
	http.ServeContent(w, r, rawOut.RelPath, rawOut.Info.ModTime(), rawOut.File)
}

// rawAssetContentType 修正系统 mime 表对前端扩展名的误判：
//
//	.ts  → text/vnd.trolltech.linguist、.tsx → application/x-tiled-tsx（都是噪音）
//	.cjs / .jsx → 空值（会落到 application/octet-stream）
//
// .mjs/.cjs 是真 JS，按脚本回；.ts/.tsx/.jsx 浏览器执行不了，用 text/plain 让
// 「模块脚本被拒」的错误一眼能懂，而不是伪装成能跑。
func rawAssetContentType(ext string) string {
	switch strings.ToLower(ext) {
	case ".mjs", ".cjs":
		return "text/javascript; charset=utf-8"
	case ".ts", ".tsx", ".jsx":
		return "text/plain; charset=utf-8"
	}
	if contentType := mime.TypeByExtension(ext); contentType != "" {
		return contentType
	}
	return "application/octet-stream"
}
