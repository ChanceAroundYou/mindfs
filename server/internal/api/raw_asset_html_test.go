package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/internal/deploy"
)

func serveRawFile(t *testing.T, dir, relPath string, query string) *httptest.ResponseRecorder {
	t.Helper()
	handler, root := newRawAssetHandler(t, dir)
	target := "/api/file/raw/" + url.PathEscape("acct-1") + "/" + url.PathEscape(root.ID) + "/" + relPath
	if query != "" {
		target += "?" + query
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
	return response
}

// HTML 预览必须放开同源：页面要像普通标签页一样跑（localStorage / IndexedDB 等）。
// 前端 iframe 的 sandbox 属性与本响应头是同一条决策的两半，改一边就会自相矛盾。
func TestRawAssetHTMLAllowsSameOrigin(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html></html>"), 0600); err != nil {
		t.Fatal(err)
	}
	response := serveRawFile(t, dir, "index.html", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	csp := response.Header().Get("Content-Security-Policy")
	for _, flag := range []string{"sandbox", "allow-scripts", "allow-same-origin"} {
		if !strings.Contains(csp, flag) {
			t.Fatalf("csp = %q, want it to contain %q", csp, flag)
		}
	}
}

// 根绝对路径兜底：/assets/app.js 从站点根算起，在预览里要落回项目根。
func TestRawAssetHTMLRewritesRootAbsoluteRefs(t *testing.T) {
	dir := t.TempDir()
	site := filepath.Join(dir, "site")
	if err := os.Mkdir(site, 0755); err != nil {
		t.Fatal(err)
	}
	page := `<html><head><link rel="stylesheet" href="/assets/site.css"></head>` +
		`<body><script src='/assets/app.js'></script><video poster="/media/cover.png"></video></body></html>`
	if err := os.WriteFile(filepath.Join(site, "index.html"), []byte(page), 0600); err != nil {
		t.Fatal(err)
	}
	body := serveRawFile(t, dir, "site/index.html", "").Body.String()

	for _, want := range []string{`href="../assets/site.css"`, `src='../assets/app.js'`, `poster="../media/cover.png"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %q, want it to contain %q", body, want)
		}
	}
}

func TestRawAssetHTMLRootFileUsesDotPrefix(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(`<script src="/assets/app.js"></script>`), 0600); err != nil {
		t.Fatal(err)
	}
	body := serveRawFile(t, dir, "index.html", "").Body.String()
	if !strings.Contains(body, `src="./assets/app.js"`) {
		t.Fatalf("body = %q, want a ./ prefix", body)
	}
}

func TestRawAssetHTMLRewriteSkipsUnsafeTargets(t *testing.T) {
	dir := t.TempDir()
	page := `<base href="/"><img src="/a.png"><script src="//cdn.example.com/x.js"></script>` +
		`<a href="/api/nodes">api</a><a href="/ws">ws</a>`
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(page), 0600); err != nil {
		t.Fatal(err)
	}
	if body := serveRawFile(t, dir, "index.html", "").Body.String(); !strings.Contains(body, `src="/a.png"`) {
		t.Fatalf("base-tag document must not be rewritten, got %q", body)
	}

	// 去掉 <base> 之后，只有协议相对与 /api、/ws 被跳过，其余仍要改写。
	noBase := `<img src="/a.png"><script src="//cdn.example.com/x.js"></script>` +
		`<a href="/api/nodes">api</a><a href="/ws">ws</a>`
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(noBase), 0600); err != nil {
		t.Fatal(err)
	}
	body := serveRawFile(t, dir, "index.html", "").Body.String()
	for _, keep := range []string{`src="//cdn.example.com/x.js"`, `href="/api/nodes"`, `href="/ws"`} {
		if !strings.Contains(body, keep) {
			t.Fatalf("body = %q, want %q untouched", body, keep)
		}
	}
	if !strings.Contains(body, `src="./a.png"`) {
		t.Fatalf("body = %q, want the image rewritten", body)
	}
}

func TestRawAssetHTMLRewriteSkipsDeployPrefix(t *testing.T) {
	previous := deploy.Prefix
	deploy.Prefix = "/mindfs"
	t.Cleanup(func() { deploy.Prefix = previous })

	dir := t.TempDir()
	page := `<script type="module" src="/mindfs/assets/app.js"></script><img src="/assets/logo.png">`
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(page), 0600); err != nil {
		t.Fatal(err)
	}
	body := serveRawFile(t, dir, "index.html", "").Body.String()
	if !strings.Contains(body, `src="/mindfs/assets/app.js"`) {
		t.Fatalf("deploy-prefixed asset must stay absolute, got %q", body)
	}
	if !strings.Contains(body, `src="./assets/logo.png"`) {
		t.Fatalf("project asset should still be rewritten, got %q", body)
	}
}

func TestRawAssetHTMLDownloadIsNotRewritten(t *testing.T) {
	dir := t.TempDir()
	page := `<script src="/assets/app.js"></script>`
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(page), 0600); err != nil {
		t.Fatal(err)
	}
	body := serveRawFile(t, dir, "index.html", "download=1").Body.String()
	if body != page {
		t.Fatalf("download must return the raw bytes, got %q", body)
	}
}

// 错误探针必须出现在 doctype 之后、页面自己的脚本之前：iframe 的 load 事件要等脚本
// 跑完才触发，前端到那时再挂监听就看不到解析期抛的错。
func TestRawAssetHTMLInjectsPreviewErrorProbeBeforePageScripts(t *testing.T) {
	dir := t.TempDir()
	page := "<!doctype html><html><body><script>throw new Error('x')</script></body></html>"
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(page), 0600); err != nil {
		t.Fatal(err)
	}
	body := serveRawFile(t, dir, "index.html", "").Body.String()
	probe := strings.Index(body, previewErrorProbeMarker)
	if probe < 0 {
		t.Fatalf("body = %q, want the error probe", body)
	}
	if strings.Index(strings.ToLower(body), "<!doctype") > probe {
		t.Fatalf("probe must come after the doctype, got %q", body)
	}
	if strings.Index(body, "throw new Error") < probe {
		t.Fatalf("probe must run before page scripts, got %q", body)
	}
	// 同一份字节再加工一次不能注入第二份。
	if again := string(prepareHTMLForPreview([]byte(body), "index.html")); strings.Count(again, previewErrorProbeMarker) != 1 {
		t.Fatalf("probe injected twice: %q", again)
	}
}

// 系统 mime 表把 .ts 认成 text/vnd.trolltech.linguist、.tsx 认成 application/x-tiled-tsx。
// 这些噪音类型会让「模块脚本被拒」的报错难以理解。
func TestRawAssetContentTypeNormalizesWebExtensions(t *testing.T) {
	cases := map[string]string{
		".mjs": "text/javascript",
		".cjs": "text/javascript",
		".ts":  "text/plain",
		".tsx": "text/plain",
		".jsx": "text/plain",
		".js":  "text/javascript",
	}
	for ext, want := range cases {
		if got := rawAssetContentType(ext); !strings.HasPrefix(got, want) {
			t.Fatalf("%s content-type = %q, want prefix %q", ext, got, want)
		}
	}
}
