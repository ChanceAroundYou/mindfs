package main

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"mindfs/internal/deploy"
)

func TestServerRunningUsesDeployPrefix(t *testing.T) {
	previous := deploy.Prefix
	deploy.Prefix = "/mindfs"
	t.Cleanup(func() { deploy.Prefix = previous })

	var requestedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if !serverRunning(server.Listener.Addr().String(), false) {
		t.Fatal("serverRunning() = false, want true for prefixed health endpoint")
	}
	if requestedPath != "/mindfs/health" {
		t.Fatalf("health check path = %q, want /mindfs/health", requestedPath)
	}
}

func TestNormalizeTaskRootFirstArgs(t *testing.T) {
	got := normalizeTaskRootFirstArgs([]string{"mindfs", "-task", "12", "-next"})
	want := []string{"-task", "12", "-next", "mindfs"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestTaskCLIActionDefaultsToStatus(t *testing.T) {
	if got := taskCLIAction(false, false, false); got != "status" {
		t.Fatalf("action = %q, want status", got)
	}
	if got := taskCLIAction(false, true, true); got != "" {
		t.Fatalf("conflicting action = %q, want empty", got)
	}
}
