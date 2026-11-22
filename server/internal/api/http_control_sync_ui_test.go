package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/server/internal/controlsync"
)

// 前端镜像端点：边缘靠它把主节点的构建产物搬到本机，之后主节点不可达也能出页面。
//
// 三件事必须守住：没配 token 时端点根本不存在（能整包吐出前端，等于一个匿名下载站）；
// 清单与包必须同源（包按清单打，清单按产物生成）；清单坏掉时不能发出半份资源
// 让边缘当成一份完整构建。

func writeUIStatic(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return dir
}

func newUITestServer(t *testing.T, staticDir, token string) http.Handler {
	t.Helper()
	return (&HTTPHandler{
		AppContext:       &AppContext{},
		StaticDir:        staticDir,
		ControlSyncToken: token,
	}).Routes()
}

// installUIManifest 生成真实的清单文件，返回清单本身。
func installUIManifest(t *testing.T, dir string) *controlsync.UIManifest {
	t.Helper()
	m, err := controlsync.BuildUIManifest(dir, "v9.9.9", "v9.9.0")
	if err != nil {
		t.Fatalf("BuildUIManifest: %v", err)
	}
	raw, err := controlsync.MarshalUIManifest(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, controlsync.UIManifestName), raw, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return m
}

func TestControlSyncUIEndpointsAbsentWithoutToken(t *testing.T) {
	dir := writeUIStatic(t, map[string]string{"index.html": "<html></html>"})
	installUIManifest(t, dir)
	h := newUITestServer(t, dir, "")
	for _, path := range []string{"/api/control-sync/ui/manifest", "/api/control-sync/ui/bundle"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("未配 token 时 %s = %d，应为 404", path, rec.Code)
		}
	}
}

func TestControlSyncUIRequiresSignature(t *testing.T) {
	dir := writeUIStatic(t, map[string]string{"index.html": "<html></html>"})
	installUIManifest(t, dir)
	h := newUITestServer(t, dir, feedTestToken)
	for _, path := range []string{"/api/control-sync/ui/manifest", "/api/control-sync/ui/bundle"} {
		// 只带 bearer、不签名：能过 requireSyncToken 的校验就等于把整包前端公开了。
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+feedTestToken)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s 未签名 = %d，应为 401", path, rec.Code)
		}
	}
}

func TestControlSyncUIManifestServesBuildIndex(t *testing.T) {
	dir := writeUIStatic(t, map[string]string{
		"index.html":  "<html></html>",
		"assets/a.js": "aaa",
	})
	want := installUIManifest(t, dir)
	h := newUITestServer(t, dir, feedTestToken)

	rec := serve(h, http.MethodGet, "/api/control-sync/ui/manifest", feedTestToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("manifest = %d（body=%s）", rec.Code, rec.Body.String())
	}
	var got controlsync.UIManifest
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("响应不是合法清单: %v", err)
	}
	if got.BuildVersion != "v9.9.9" || got.MinServerBuild != "v9.9.0" {
		t.Fatalf("版本字段不对: %+v", got)
	}
	if len(got.Files) != len(want.Files) {
		t.Fatalf("文件数 = %d，应为 %d", len(got.Files), len(want.Files))
	}
	for _, f := range got.Files {
		if f.Path == controlsync.UIManifestName {
			// 清单不能列自己：那要求文件内容包含自己的 sha256，做不到。
			t.Fatal("清单不得把自己列进 files（自哈希是不动点）")
		}
		if !strings.HasPrefix(f.SHA256, "") || len(f.SHA256) != 64 || f.Size <= 0 {
			t.Fatalf("条目缺少 sha256/size: %+v", f)
		}
	}
}

func TestControlSyncUIBundleIsGzipTarMatchingManifest(t *testing.T) {
	dir := writeUIStatic(t, map[string]string{
		"index.html":  "<html>bundle</html>",
		"assets/a.js": "console.log(1)",
	})
	installUIManifest(t, dir)
	h := newUITestServer(t, dir, feedTestToken)

	rec := serve(h, http.MethodGet, "/api/control-sync/ui/bundle", feedTestToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("bundle = %d（body=%s）", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/gzip" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if v := rec.Header().Get("X-MindFS-Build-Version"); v != "v9.9.9" {
		t.Fatalf("X-MindFS-Build-Version = %q", v)
	}

	gz, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("不是 gzip: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	got := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar: %v", err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("tar read: %v", err)
		}
		got[hdr.Name] = string(body)
	}
	if got["index.html"] != "<html>bundle</html>" || got["assets/a.js"] != "console.log(1)" {
		t.Fatalf("包内容不对: %v", got)
	}
	if _, ok := got[controlsync.UIManifestName]; !ok {
		t.Fatal("包里必须带清单文件")
	}
}

func TestControlSyncUIWithoutBuildIs404(t *testing.T) {
	// 运行节点（role=worker）与没装前端的机器都是这个形状：没有可镜像的东西。
	h := newUITestServer(t, "", feedTestToken)
	for _, path := range []string{"/api/control-sync/ui/manifest", "/api/control-sync/ui/bundle"} {
		rec := serve(h, http.MethodGet, path, feedTestToken)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s = %d，应为 404", path, rec.Code)
		}
	}
	// 目录在但清单缺失：同样是 404，不是空清单 —— 空清单会让边缘以为「没有文件」。
	dir := writeUIStatic(t, map[string]string{"index.html": "x"})
	h2 := newUITestServer(t, dir, feedTestToken)
	rec := serve(h2, http.MethodGet, "/api/control-sync/ui/manifest", feedTestToken)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("缺清单 = %d，应为 404", rec.Code)
	}
}

// 清单被换坏（有人在产物里塞了 ../）时，两个端点都必须拒绝，且响应里不能夹带文件内容。
func TestControlSyncUIRejectsTraversalManifest(t *testing.T) {
	dir := writeUIStatic(t, map[string]string{"index.html": "SECRET-BODY"})
	evil := `{"build_version":"v1","min_server_build":"v1","files":[{"path":"../etc/passwd","size":3,"sha256":"` + strings.Repeat("a", 64) + `"}]}`
	if err := os.WriteFile(filepath.Join(dir, controlsync.UIManifestName), []byte(evil), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	h := newUITestServer(t, dir, feedTestToken)
	for _, path := range []string{"/api/control-sync/ui/manifest", "/api/control-sync/ui/bundle"} {
		rec := serve(h, http.MethodGet, path, feedTestToken)
		if rec.Code == http.StatusOK {
			t.Fatalf("%s 对路径穿越清单返回了 200", path)
		}
		if strings.Contains(rec.Body.String(), "SECRET-BODY") {
			t.Fatalf("%s 的响应里夹带了文件内容", path)
		}
	}
}
