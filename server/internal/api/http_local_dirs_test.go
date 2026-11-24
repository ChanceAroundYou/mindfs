package api

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"mindfs/server/internal/fs"
)

func newLocalDirsHandler(t *testing.T) *HTTPHandler {
	t.Helper()
	registry := fs.NewRegistry(filepath.Join(t.TempDir(), "registry.json"))
	return &HTTPHandler{AppContext: &AppContext{Dirs: registry}}
}

func TestLocalDirsCreateMakesFolder(t *testing.T) {
	h := newLocalDirsHandler(t)
	parent := t.TempDir()

	rec := doReq(t, h.Routes(), http.MethodPost, "/api/local_dirs",
		`{"path":"`+parent+`","name":"fresh"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201（body: %s）", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	want := filepath.Join(parent, "fresh")
	if body["path"] != want {
		t.Fatalf("path = %v, want %s", body["path"], want)
	}
	if info, err := os.Stat(want); err != nil || !info.IsDir() {
		t.Fatalf("folder was not created: %v", err)
	}
}

func TestLocalDirsCreateReportsConflict(t *testing.T) {
	h := newLocalDirsHandler(t)
	parent := t.TempDir()
	if err := os.Mkdir(filepath.Join(parent, "taken"), 0o755); err != nil {
		t.Fatal(err)
	}

	rec := doReq(t, h.Routes(), http.MethodPost, "/api/local_dirs",
		`{"path":"`+parent+`","name":"taken"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409（body: %s）", rec.Code, rec.Body.String())
	}
}

func TestLocalDirsCreateReportsMissingParent(t *testing.T) {
	h := newLocalDirsHandler(t)
	missing := filepath.Join(t.TempDir(), "nope")

	rec := doReq(t, h.Routes(), http.MethodPost, "/api/local_dirs",
		`{"path":"`+missing+`","name":"child"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404（body: %s）", rec.Code, rec.Body.String())
	}
}

func TestLocalDirsCreateRejectsInvalidName(t *testing.T) {
	h := newLocalDirsHandler(t)
	parent := t.TempDir()

	rec := doReq(t, h.Routes(), http.MethodPost, "/api/local_dirs",
		`{"path":"`+parent+`","name":"../escape"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400（body: %s）", rec.Code, rec.Body.String())
	}
}

func TestLocalDirsCreateRejectsMalformedBody(t *testing.T) {
	h := newLocalDirsHandler(t)

	rec := doReq(t, h.Routes(), http.MethodPost, "/api/local_dirs", `{"path":`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400（body: %s）", rec.Code, rec.Body.String())
	}
}
