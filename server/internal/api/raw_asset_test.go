package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/server/internal/fs"
)

func newRawAssetHandler(t *testing.T, dir string) (http.Handler, fs.RootInfo) {
	t.Helper()
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
	return WithRawAccountPath((&HTTPHandler{AppContext: app}).Routes()), root
}

func TestRawAssetServesFileByPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "site"), 0755); err != nil {
		t.Fatal(err)
	}
	page := "<!doctype html><link rel=stylesheet href=style.css>"
	if err := os.WriteFile(filepath.Join(dir, "site", "index.html"), []byte(page), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "site", "style.css"), []byte("body{}"), 0600); err != nil {
		t.Fatal(err)
	}
	handler, root := newRawAssetHandler(t, dir)

	target := "/api/file/raw/" + url.PathEscape("acct-1") + "/" + url.PathEscape(root.ID) + "/site/index.html"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", response.Code, response.Body.String())
	}
	if got := response.Body.String(); got != page {
		t.Fatalf("body = %q, want %q", got, page)
	}
	if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Fatalf("content-type = %q, want text/html", got)
	}
	if got := response.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("nosniff = %q, want nosniff", got)
	}
	if got := response.Header().Get("Content-Security-Policy"); !strings.Contains(got, "sandbox") {
		t.Fatalf("csp = %q, want sandbox", got)
	}
}

func TestRawAssetServesSiblingAssetWithSameAccount(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "style.css"), []byte("body{color:red}"), 0600); err != nil {
		t.Fatal(err)
	}
	handler, root := newRawAssetHandler(t, dir)

	target := "/api/file/raw/" + url.PathEscape("acct-1") + "/" + url.PathEscape(root.ID) + "/style.css"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/css") {
		t.Fatalf("content-type = %q, want text/css", got)
	}
	if got := response.Header().Get("Content-Security-Policy"); got != "" {
		t.Fatalf("css should not carry the html sandbox policy, got %q", got)
	}
}

func TestRawAssetSupportsRangeRequests(t *testing.T) {
	dir := t.TempDir()
	payload := []byte("0123456789")
	if err := os.WriteFile(filepath.Join(dir, "clip.mp4"), payload, 0600); err != nil {
		t.Fatal(err)
	}
	handler, root := newRawAssetHandler(t, dir)

	target := "/api/file/raw/" + url.PathEscape("acct-1") + "/" + url.PathEscape(root.ID) + "/clip.mp4"
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header.Set("Range", "bytes=2-5")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", response.Code)
	}
	if got := response.Body.String(); got != "2345" {
		t.Fatalf("body = %q, want 2345", got)
	}
	if got := response.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("accept-ranges = %q, want bytes", got)
	}
}

func TestRawAssetRejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	handler, root := newRawAssetHandler(t, dir)

	target := "/api/file/raw/acct-1/" + url.PathEscape(root.ID) + "/../../etc/passwd"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))

	if response.Code != http.StatusBadRequest {
		t.Fatalf("traversal must be rejected with 400, got %d", response.Code)
	}
}

func TestRawAssetMissingFileIs404(t *testing.T) {
	dir := t.TempDir()
	handler, root := newRawAssetHandler(t, dir)

	target := "/api/file/raw/acct-1/" + url.PathEscape(root.ID) + "/nope.html"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
}

func TestWithRawAccountPathMovesAccountToUserQuery(t *testing.T) {
	var gotPath, gotUser string
	handler := WithRawAccountPath(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotUser = r.URL.Query().Get("user")
	}))

	request := httptest.NewRequest(http.MethodGet, "/api/file/raw/acct-1/myroot/site/index.html", nil)
	handler.ServeHTTP(httptest.NewRecorder(), request)

	if gotUser != "acct-1" {
		t.Fatalf("user = %q, want acct-1", gotUser)
	}
	if gotPath != "/api/file/raw/myroot/site/index.html" {
		t.Fatalf("path = %q, want the account segment stripped", gotPath)
	}
}

func TestWithRawAccountPathLeavesOtherPathsAlone(t *testing.T) {
	var gotPath, gotUser string
	handler := WithRawAccountPath(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotUser = r.URL.Query().Get("user")
	}))

	request := httptest.NewRequest(http.MethodGet, "/api/file?root=myroot&path=notes.txt&user=acct-2", nil)
	handler.ServeHTTP(httptest.NewRecorder(), request)

	if gotPath != "/api/file" {
		t.Fatalf("path = %q, want /api/file", gotPath)
	}
	if gotUser != "acct-2" {
		t.Fatalf("user = %q, want acct-2", gotUser)
	}
}
