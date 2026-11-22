package api

import (
	"errors"
	"log"
	"net/http"
	"os"
	"strings"

	"mindfs/server/internal/controlsync"
)

// 前端资源镜像的主节点端点。
//
// 边缘靠这两个端点把主节点的构建产物搬到本机，之后即使主节点不可达也能出页面。
// 它们与 /head /changes 同级（同样能一次吐出一份完整资源），所以同样只走
// token + HMAC：`requireSyncToken` 在路由注册时就套上，这里不再判 token。
//
// 清单直接读构建产物里的 `ui-manifest.json`，**不在请求时重新计算** ——
// 请求时算出来的清单与缓存里的旧清单可能不是同一份，而边缘是拿清单去校验包的，
// 两者一旦不同源，坏的是「明明一致的包被判成坏包」。清单与目录脱节（产物被换过）
// 由打包时的逐文件哈希重算兜住：不符就中断，边缘拿到的是截断包，保留旧版本。

// staticUIRoot 返回可镜像的静态目录；为空或不是目录时返回 ""。
func (h *HTTPHandler) staticUIRoot() string {
	dir := strings.TrimSpace(h.StaticDir)
	if dir == "" {
		return ""
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return ""
	}
	return dir
}

func (h *HTTPHandler) handleControlSyncUIManifest(w http.ResponseWriter, r *http.Request) {
	dir := h.staticUIRoot()
	if dir == "" {
		// 运行节点与未装前端的安装都是这个形状：没有可镜像的东西，不是错误配置。
		respondError(w, http.StatusNotFound, errInvalidRequest("control sync: no ui build on this node"))
		return
	}
	manifest, err := controlsync.LoadUIManifest(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			respondError(w, http.StatusNotFound, errInvalidRequest("control sync: ui manifest missing"))
			return
		}
		log.Printf("[control-sync] ui.manifest.error dir=%s err=%v", dir, err)
		respondError(w, http.StatusInternalServerError, errInvalidRequest("control sync: ui manifest unreadable"))
		return
	}
	// 回写解析后的结构而不是原始字节：坏清单在这里就已经被 Validate 拒绝，
	// 边缘拿到的永远是能通过同一套校验的 JSON。
	respondJSON(w, http.StatusOK, manifest)
}

func (h *HTTPHandler) handleControlSyncUIBundle(w http.ResponseWriter, r *http.Request) {
	dir := h.staticUIRoot()
	if dir == "" {
		respondError(w, http.StatusNotFound, errInvalidRequest("control sync: no ui build on this node"))
		return
	}
	manifest, err := controlsync.LoadUIManifest(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			respondError(w, http.StatusNotFound, errInvalidRequest("control sync: ui manifest missing"))
			return
		}
		log.Printf("[control-sync] ui.bundle.manifest_error dir=%s err=%v", dir, err)
		respondError(w, http.StatusInternalServerError, errInvalidRequest("control sync: ui manifest unreadable"))
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="ui-bundle.tar.gz"`)
	w.Header().Set("X-MindFS-Build-Version", manifest.BuildVersion)
	w.WriteHeader(http.StatusOK)

	// 走到这里响应头已经发出，中途失败只能截断连接：边缘收不满包，校验不过，
	// 保留旧版本。这是有意的失败方式 —— 半份资源绝不能看起来像一份。
	if err := controlsync.WriteUIBundle(w, dir, manifest); err != nil {
		log.Printf("[control-sync] ui.bundle.error dir=%s err=%v", dir, err)
		return
	}
	log.Printf("[control-sync] ui.bundle.served version=%s files=%d", manifest.BuildVersion, len(manifest.Files))
}
