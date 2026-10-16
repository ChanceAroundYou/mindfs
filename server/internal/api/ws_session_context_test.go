package api

import (
	"context"
	"testing"
	"time"

	"mindfs/server/internal/agent"
	"mindfs/server/internal/session"
)

func TestSessionMessageContextHasNoDeadlineWithoutAppContext(t *testing.T) {
	handler := &WSHandler{}

	ctx, cancel := handler.sessionMessageContext()
	defer cancel()

	if _, ok := ctx.Deadline(); ok {
		t.Fatal("session message context unexpectedly has a deadline")
	}
}

func TestSessionMessageContextUsesAgentPoolLifecycle(t *testing.T) {
	pool := agent.NewPool(agent.Config{})
	defer pool.CloseAll()

	handler := &WSHandler{AppContext: &AppContext{Agents: pool}}
	ctx, cancel := handler.sessionMessageContext()
	defer cancel()

	if _, ok := ctx.Deadline(); ok {
		t.Fatal("session message context unexpectedly has a deadline")
	}
	pool.CloseAll()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("expected session message context to be canceled when agent pool closes")
	}
}

func TestSessionRuntimeRootPathUsesRelatedWorktree(t *testing.T) {
	current := &session.Session{
		Source: "worktree",
		RelatedWorktree: &session.RelatedWorktree{
			Path: "  /tmp/project-worktree  ",
		},
	}
	if got := sessionRuntimeRootPath(current); got != "/tmp/project-worktree" {
		t.Fatalf("sessionRuntimeRootPath() = %q, want %q", got, "/tmp/project-worktree")
	}
	if got := sessionRuntimeRootPath(&session.Session{}); got != "" {
		t.Fatalf("sessionRuntimeRootPath() without worktree = %q, want empty", got)
	}
	relatedOnly := &session.Session{
		RelatedWorktree: &session.RelatedWorktree{Path: "/tmp/observed-worktree"},
	}
	if got := sessionRuntimeRootPath(relatedOnly); got != "" {
		t.Fatalf("sessionRuntimeRootPath() for incidental relation = %q, want empty", got)
	}
}

func TestTurnUpdateTrackerWaitIdleWaitsForSettleWindow(t *testing.T) {
	tracker := newTurnUpdateTracker()
	tracker.Begin()
	done := make(chan bool, 1)
	go func() {
		done <- tracker.WaitIdle(context.Background(), 30*time.Millisecond, 500*time.Millisecond)
	}()

	select {
	case <-done:
		t.Fatal("WaitIdle returned while update was in-flight")
	case <-time.After(20 * time.Millisecond):
	}

	tracker.End()
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("WaitIdle returned false after update finished")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("WaitIdle did not return after settle window")
	}
}

func TestTurnUpdateTrackerWaitIdleTimesOutWhenUpdateNeverEnds(t *testing.T) {
	tracker := newTurnUpdateTracker()
	tracker.Begin()

	if tracker.WaitIdle(context.Background(), 10*time.Millisecond, 30*time.Millisecond) {
		t.Fatal("expected WaitIdle to time out while update remains in-flight")
	}
}
