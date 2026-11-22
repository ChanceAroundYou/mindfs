package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"mindfs/server/internal/nodeinfo"
)

// 边缘的前端版本在运行期可变：镜像切到新版后不需要重启进程就该出新页面。
// 这里钉的是「每次请求都重新问一次」，不是「构造 handler 时问一次」——
// 后者会让切换只能靠重启生效，正是设计里明确不要的行为。
func TestServingStaticDirFollowsProviderAtRequestTime(t *testing.T) {
	installDir := writeFrontendFixture(t, "install", "<html>install</html>")
	mirrorDir := writeFrontendFixture(t, "mirror", "<html>mirror</html>")

	var current atomic.Value
	current.Store("") // 还没镜像：回落安装目录
	h := &HTTPHandler{
		StaticDir: installDir,
		StaticDirProvider: func() string {
			s, _ := current.Load().(string)
			return s
		},
	}

	if body := getIndex(t, h); !strings.Contains(body, "install") {
		t.Fatalf("没有镜像时应出安装目录那版，实际 %q", body)
	}
	current.Store(mirrorDir)
	if body := getIndex(t, h); !strings.Contains(body, "mirror") {
		t.Fatalf("镜像可用后应立刻出镜像那版（不重启），实际 %q", body)
	}
	current.Store("")
	if body := getIndex(t, h); !strings.Contains(body, "install") {
		t.Fatalf("镜像不可用（指针失效）时应回落到安装目录，实际 %q", body)
	}
}

func TestServingStaticDirWithoutProviderUsesInstallDir(t *testing.T) {
	installDir := writeFrontendFixture(t, "install-only", "<html>install</html>")
	h := &HTTPHandler{StaticDir: installDir}
	if got := h.servingStaticDir(); got != installDir {
		t.Fatalf("没有 provider 时应返回 StaticDir，实际 %q", got)
	}
}

// 只用镜像出页面的边缘也算「能打开网页」：node-info 的 static 必须与真实服务
// 路径同源，否则前端会把它的「打开网页」入口藏掉。
func TestNodeInfoStaticFlagFollowsMirror(t *testing.T) {
	mirrorDir := writeFrontendFixture(t, "mirror", "<html>mirror</html>")
	h := &HTTPHandler{
		NodeRole:          nodeinfo.RoleControl,
		StaticDirProvider: func() string { return mirrorDir },
	}
	rec := httptest.NewRecorder()
	h.handleNodeInfo(rec, httptest.NewRequest(http.MethodGet, "/api/node-info", nil))
	if !strings.Contains(rec.Body.String(), `"static":true`) {
		t.Fatalf("有镜像却没有 static:true，body=%s", rec.Body.String())
	}

	// 没有任何静态资源（既无安装目录也无镜像）：static:false。
	empty := &HTTPHandler{NodeRole: nodeinfo.RoleControl}
	rec = httptest.NewRecorder()
	empty.handleNodeInfo(rec, httptest.NewRequest(http.MethodGet, "/api/node-info", nil))
	if !strings.Contains(rec.Body.String(), `"static":false`) {
		t.Fatalf("没有静态资源却报 static:true，body=%s", rec.Body.String())
	}
}

// 前端资源目录必须自证是前端（有 index.html），否则 serveStaticAsset 不会命中。
func writeFrontendFixture(t *testing.T, name, index string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(index), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatalf("write asset: %v", err)
	}
	return dir
}

func getIndex(t *testing.T, h *HTTPHandler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	if !h.serveStaticAsset(rec, httptest.NewRequest(http.MethodGet, "/", nil)) {
		t.Fatal("serveStaticAsset 没有命中")
	}
	return rec.Body.String()
}
