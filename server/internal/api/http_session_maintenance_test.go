package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/server/internal/fs"
)

func newSessionMaintenanceHandler(t *testing.T) *HTTPHandler {
	t.Helper()
	registry := fs.NewRegistry(filepath.Join(t.TempDir(), "registry.json"))
	if err := registry.Load(); err != nil {
		t.Fatal(err)
	}
	return &HTTPHandler{AppContext: &AppContext{Dirs: registry}}
}

func TestSessionMaintenanceEndpointsRejectMissingRootAndConfirmation(t *testing.T) {
	h := newSessionMaintenanceHandler(t)

	diagnose := httptest.NewRecorder()
	h.handleSessionMaintenanceDiagnose(diagnose, httptest.NewRequest(http.MethodGet,
		"/api/sessions/maintenance/diagnose?root=missing", nil))
	if diagnose.Code != http.StatusBadRequest {
		t.Fatalf("diagnose status=%d body=%s, want 400", diagnose.Code, diagnose.Body.String())
	}

	images := httptest.NewRecorder()
	h.handleSessionMaintenanceCleanImages(images, httptest.NewRequest(http.MethodPost,
		"/api/sessions/key/maintenance/images/clean?root=missing", strings.NewReader(`{}`)))
	if images.Code != http.StatusBadRequest {
		t.Fatalf("clean images status=%d body=%s, want 400", images.Code, images.Body.String())
	}

	purge := httptest.NewRecorder()
	h.handleSessionMaintenancePurgeEmpty(purge, httptest.NewRequest(http.MethodPost,
		"/api/sessions/key/maintenance/purge?root=missing", strings.NewReader(`{}`)))
	if purge.Code != http.StatusBadRequest || !strings.Contains(purge.Body.String(), "confirm") {
		t.Fatalf("purge status=%d body=%s, want 400 naming confirm", purge.Code, purge.Body.String())
	}
}

func TestSessionMaintenanceCleanLocksWithoutConfiguredDirs(t *testing.T) {
	h := newSessionMaintenanceHandler(t)
	response := httptest.NewRecorder()
	h.handleSessionMaintenanceCleanLocks(response, httptest.NewRequest(http.MethodPost,
		"/api/sessions/maintenance/locks/clean", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("clean locks status=%d body=%s, want 200", response.Code, response.Body.String())
	}
}

func TestRuntimeIssuesSweepReadsRootQueryParameter(t *testing.T) {
	h := newSessionMaintenanceHandler(t)
	response := httptest.NewRecorder()
	h.handleSessionRuntimeIssuesSweep(response, httptest.NewRequest(http.MethodPost,
		"/api/sessions/runtime-issues/sweep?root=proj", strings.NewReader(`{}`)))
	if strings.Contains(response.Body.String(), "root_id is required") {
		t.Fatalf("sweep must accept the same ?root= query as diagnose: %s", response.Body.String())
	}
}
