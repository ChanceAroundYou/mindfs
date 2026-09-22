package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

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

func TestWaitForForegroundAppExitWaitsForCleanupAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	errCh := make(chan error, 1)
	want := errors.New("cleanup complete")
	done := make(chan error, 1)
	go func() {
		done <- waitForForegroundAppExit(ctx, errCh)
	}()

	select {
	case <-done:
		t.Fatal("returned before app cleanup completed")
	case <-time.After(20 * time.Millisecond):
	}
	errCh <- want
	select {
	case got := <-done:
		if !errors.Is(got, want) {
			t.Fatalf("wait error = %v, want %v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for app cleanup result")
	}
}

func TestRestoreDefaultSignalsAfterCancellationCallsStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	restoreDefaultSignalsAfterCancellation(ctx, func() { close(stopped) })

	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("signal notifications were not stopped after cancellation")
	}
}
