package api

import (
	"fmt"
	htmpl "html/template"
	"net/http"
	"os"
	stdpath "path"
	"path/filepath"
	"regexp"
	"strings"

	"mindfs/internal/deploy"
	"mindfs/server/internal/nodeinfo"
)

var indexResourceRefPattern = regexp.MustCompile(`(?i)\b(?:src|href)\s*=\s*["']([^"']+)["']`)

func (h *HTTPHandler) handleFrontend(w http.ResponseWriter, r *http.Request) {
	// 运行节点不提供前端：StaticDir 为空时下面会落到 renderFallbackFrontend，
	// 输出一张「前端资源缺失」的提示页 —— 那会让用户以为是装坏了，而实际上是
	// 这台机器按配置就不提供 UI。给一个明确的 403。
	if h.NodeRole == nodeinfo.RoleWorker {
		respondError(w, http.StatusForbidden, errInvalidRequest("node_is_worker"))
		return
	}
	if h.serveStaticAsset(w, r) {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(renderFallbackFrontend(indexHTML, frontendAssetMissingNotice(r.URL.Path))))
}

// handleHealth 顺带报角色：前端要靠它区分「对面挂了」和「对面是运行节点，
// 本来就不提供 UI」。原来返回纯文本 "ok"，改成 JSON —— 仓库内的消费者只有
// NodeManagerPanel.probeNode（用 protectedJSON，能解析即成功），deploy-all.sh
// 不碰这个端点。
func (h *HTTPHandler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"role": string(nodeinfo.Normalize(string(h.NodeRole))),
	})
}

func (h *HTTPHandler) handleNotFound(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api" || r.URL.Path == "/ws" || r.URL.Path == "/health" {
		http.NotFound(w, r)
		return
	}
	h.handleFrontend(w, r)
}

func (h *HTTPHandler) serveStaticAsset(w http.ResponseWriter, r *http.Request) bool {
	staticDir := strings.TrimSpace(h.StaticDir)
	if staticDir == "" {
		return false
	}

	cleanPath := pathForStaticAsset(r.URL.Path)
	if cleanPath == "" {
		cleanPath = "index.html"
	}

	assetPath := filepath.Join(staticDir, cleanPath)
	info, statErr := os.Stat(assetPath)
	if statErr == nil && !info.IsDir() {
		applyStaticCacheHeaders(w, cleanPath)
		if cleanPath == "index.html" {
			h.serveFrontendIndex(w, r, staticDir, assetPath)
			return true
		}
		http.ServeFile(w, r, assetPath)
		return true
	}

	if filepath.Ext(cleanPath) != "" {
		http.NotFound(w, r)
		return true
	}

	indexPath := filepath.Join(staticDir, "index.html")
	if info, err := os.Stat(indexPath); err == nil && !info.IsDir() {
		applyStaticCacheHeaders(w, "index.html")
		h.serveFrontendIndex(w, r, staticDir, indexPath)
		return true
	}

	return false
}

func (h *HTTPHandler) serveFrontendIndex(w http.ResponseWriter, r *http.Request, staticDir, indexPath string) {
	content, err := os.ReadFile(indexPath)
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(renderFallbackFrontend(indexHTML, frontendAssetMissingNotice(r.URL.Path))))
		return
	}
	if missing := missingFrontendIndexResource(staticDir, content); missing != "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(renderFallbackFrontend(indexHTML, frontendAssetMissingNotice(missing))))
		return
	}
	http.ServeFile(w, r, indexPath)
}

func missingFrontendIndexResource(staticDir string, indexContent []byte) string {
	matches := indexResourceRefPattern.FindAllSubmatch(indexContent, -1)
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		cleanPath := cleanFrontendResourcePath(string(match[1]))
		if cleanPath == "" {
			continue
		}
		info, err := os.Stat(filepath.Join(staticDir, cleanPath))
		if err != nil || info.IsDir() {
			return "/" + filepath.ToSlash(cleanPath)
		}
	}
	return ""
}

func cleanFrontendResourcePath(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" || strings.HasPrefix(value, "#") {
		return ""
	}
	lower := strings.ToLower(value)
	for _, prefix := range []string{"http://", "https://", "data:", "blob:", "mailto:", "tel:"} {
		if strings.HasPrefix(lower, prefix) {
			return ""
		}
	}
	if idx := strings.IndexAny(value, "?#"); idx >= 0 {
		value = value[:idx]
	}
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "./")
	value = strings.TrimPrefix(value, "/")
	// vite 以部署前缀为 base 时，index.html 引用形如 /<prefix>/assets/... /<prefix>/favicon.svg，
	// 需剥掉前缀再判本地文件是否存在，与 pathForStaticAsset 同构。空前缀（根部署）无需剥离。
	if prefix := deploy.NormalizedPrefix(); prefix != "" {
		value = strings.TrimPrefix(value, strings.TrimPrefix(prefix, "/")+"/")
	}
	value = filepath.Clean(value)
	if value == "." || strings.HasPrefix(value, ".."+string(filepath.Separator)) || value == ".." || filepath.IsAbs(value) {
		return ""
	}
	return value
}

func frontendAssetMissingNotice(requestPath string) string {
	cleanPath := pathForStaticAsset(requestPath)
	if cleanPath == "" {
		cleanPath = "index.html"
	}
	displayPath := filepath.ToSlash(cleanPath)
	return fmt.Sprintf("frontend assets missing: web/%s not found. Please reinstall or check the install directory.", displayPath)
}

func renderFallbackFrontend(content, notice string) string {
	out := content
	noticeHTML := ""
	if strings.TrimSpace(notice) != "" {
		noticeHTML = `<div class="notice is-visible">` + htmpl.HTMLEscapeString(notice) + `</div>`
	}
	out = strings.ReplaceAll(out, "__FALLBACK_NOTICE__", noticeHTML)
	// 兜底页文档 URL 由反代前缀决定（与运行时部署前缀一致）。
	out = strings.ReplaceAll(out, "/api/tree?", deploy.PrefixedPath("/api/tree")+"?")
	return out
}

func applyStaticCacheHeaders(w http.ResponseWriter, cleanPath string) {
	switch cleanPath {
	case "service-worker.js", "index.html":
		w.Header().Set("Cache-Control", "no-cache")
		return
	}

	if strings.HasPrefix(cleanPath, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
}

func isStandardReleaseVersion(version string) bool {
	version = strings.TrimSpace(version)
	if version == "" {
		return false
	}
	version, _ = strings.CutPrefix(version, "v")
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, ch := range part {
			if ch < '0' || ch > '9' {
				return false
			}
		}
	}
	return true
}

func pathForStaticAsset(requestPath string) string {
	// requestPath 是 URL path，分隔符固定为正斜杠。这里不能用 filepath，
	// 否则 Windows 会把前导 // 当成 UNC 路径，最终泄漏成 web/// 这类路径。
	// 部署前缀已在顶层 mux 统一剥离（StripDeployPrefix），此处收到的已是
	// /assets/...、/index.html 等去前缀路径。
	cleaned := stdpath.Clean("/" + requestPath)
	if cleaned == "/" {
		return ""
	}
	return strings.TrimPrefix(cleaned, "/")
}
