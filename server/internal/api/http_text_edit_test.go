package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"mindfs/server/internal/fs"
)

func TestFileEditingHTTP(t *testing.T) {
	dir := t.TempDir()
	registry := fs.NewRegistry(filepath.Join(t.TempDir(), "registry.json"))
	root, err := registry.Upsert(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("original"), 0600); err != nil {
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
	handler := (&HTTPHandler{AppContext: app}).Routes()
	target := "/api/file?" + url.Values{"root": {root.ID}, "path": {"notes.txt"}}.Encode()
	request := func(method, target string, payload any) (int, map[string]json.RawMessage) {
		t.Helper()
		var body []byte
		if payload != nil {
			body, _ = json.Marshal(payload)
		}
		req := httptest.NewRequest(method, target, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		var result map[string]json.RawMessage
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatalf("response: %s (%v)", response.Body.String(), err)
		}
		return response.Code, result
	}
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, folder := range []string{".", "nested"} {
		body := map[string]any{"dir": folder, "name": "空白.txt"}
		if status, _ := request("POST", target, body); status != http.StatusCreated {
			t.Fatalf("create in %s: %d", folder, status)
		}
		createdPath := filepath.Join(dir, folder, "空白.txt")
		if info, err := os.Stat(createdPath); err != nil || info.Size() != 0 {
			t.Fatalf("expected empty file: %v %v", info, err)
		}
		if err := os.WriteFile(createdPath, []byte("keep"), 0644); err != nil {
			t.Fatal(err)
		}
		if status, _ := request("POST", target, body); status != http.StatusConflict {
			t.Fatalf("duplicate create: %d", status)
		}
		if data, err := os.ReadFile(createdPath); err != nil || string(data) != "keep" {
			t.Fatalf("existing content changed: %q %v", data, err)
		}
	}
	for _, name := range []string{"", " ", ".", "..", "../escape.txt", "a/b", `a\b`} {
		if status, _ := request("POST", target, map[string]any{"dir": ".", "name": name}); status != http.StatusBadRequest {
			t.Fatalf("invalid name %q accepted: %d", name, status)
		}
	}
	if status, _ := request("POST", target, map[string]any{"dir": "..", "name": "escape.txt"}); status != http.StatusBadRequest {
		t.Fatalf("outside root accepted: %d", status)
	}
	status, payload := request("GET", target+"&edit=1", nil)
	if status != 200 {
		t.Fatalf("read status: %d %s", status, payload)
	}
	var file fs.ReadResult
	if err := json.Unmarshal(payload["file"], &file); err != nil {
		t.Fatal(err)
	}
	if file.Content != "original" || file.Revision == "" {
		t.Fatalf("read file: %+v", file)
	}
	status, _ = request("PUT", target, map[string]any{"content": "中文\r\n", "base_revision": file.Revision})
	if status != 200 {
		t.Fatalf("save status: %d", status)
	}
	status, _ = request("PUT", target, map[string]any{"content": "stale", "base_revision": file.Revision})
	if status != 409 {
		t.Fatalf("conflict status: %d", status)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "notes.txt"))
	if string(data) != "中文\r\n" {
		t.Fatalf("content: %q", data)
	}
	status, _ = request("PUT", target, map[string]any{"base_revision": file.Revision})
	if status != 400 {
		t.Fatalf("missing content accepted: %d", status)
	}
}
