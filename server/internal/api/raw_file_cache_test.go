package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"mindfs/server/internal/fs"
)

// 原始字节的两个端点都不许让浏览器「启发式缓存」旧内容：
//   - 路径式（网页/视频）：no-cache + Last-Modified → 每次回源确认，304 很便宜；
//   - 查询式（blob 拉取）：no-store，前端已按 mtime 版本分键，HTTP 层不能再留一份。
//
// 少了任何一条，同名文件被改写后预览仍可能拿到旧字节 —— 前端缓存键改对了也白搭。
func TestRawFileResponsesDisableHeuristicCaching(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "site"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "site", "index.html"), []byte("<!doctype html>"), 0600); err != nil {
		t.Fatal(err)
	}
	registry := fs.NewRegistry(filepath.Join(t.TempDir(), "registry.json"))
	root, err := registry.Upsert(dir)
	if err != nil {
		t.Fatal(err)
	}
	app := &AppContext{Dirs: registry}
	t.Cleanup(func() {
		for _, ctx := range app.roots {
			if ctx.Watcher != nil {
				ctx.Watcher.Close()
			}
		}
	})

	assetTarget := "/api/file/raw/" + url.PathEscape("acct-1") + "/" + url.PathEscape(root.ID) + "/site/index.html"
	assetResponse := httptest.NewRecorder()
	WithRawAccountPath((&HTTPHandler{AppContext: app}).Routes()).
		ServeHTTP(assetResponse, httptest.NewRequest(http.MethodGet, assetTarget, nil))
	if assetResponse.Code != http.StatusOK {
		t.Fatalf("raw asset status = %d, want 200", assetResponse.Code)
	}
	if got := assetResponse.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("raw asset Cache-Control = %q, want no-cache", got)
	}

	queryTarget := "/api/file?" + url.Values{
		"root": {root.ID}, "path": {"site/index.html"}, "raw": {"1"}, "v": {"1234"},
	}.Encode()
	queryResponse := httptest.NewRecorder()
	(&HTTPHandler{AppContext: app}).Routes().
		ServeHTTP(queryResponse, httptest.NewRequest(http.MethodGet, queryTarget, nil))
	if queryResponse.Code != http.StatusOK {
		t.Fatalf("query raw status = %d, want 200", queryResponse.Code)
	}
	if got := queryResponse.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("query raw Cache-Control = %q, want no-store", got)
	}
}
