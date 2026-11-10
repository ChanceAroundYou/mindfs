package codexsync

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunnerReportsUnavailableCheckoutWithoutBlockingStartup(t *testing.T) {
	paths := testPaths(t)
	runner := NewRunner(func(ctx context.Context) (Result, error) {
		return RunAt(ctx, ModeSync, paths)
	}, time.Hour, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner.Start(ctx)
	status := waitRunnerState(t, runner, "unavailable")
	if status.Message == "" {
		t.Fatal("unavailable state has no safe message")
	}
}

func TestRunnerCoalescesDuplicateRequests(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{}, 4)
	release := make(chan struct{}, 4)
	runner := NewRunner(func(context.Context) (Result, error) {
		calls.Add(1)
		started <- struct{}{}
		<-release
		return Result{State: "synced", Message: "done"}, nil
	}, time.Hour, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner.Start(ctx)
	<-started // startup run
	for i := 0; i < 20; i++ {
		runner.Request(context.Background())
	}
	release <- struct{}{}
	<-started // one coalesced run
	release <- struct{}{}
	if status := waitRunnerState(t, runner, "synced"); status.Message != "done" {
		t.Fatalf("status = %+v", status)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("run count = %d, want startup + one coalesced request", got)
	}
}

func TestRunnerTimesOutAndCanBeRetried(t *testing.T) {
	var calls atomic.Int32
	runner := NewRunner(func(ctx context.Context) (Result, error) {
		if calls.Add(1) == 1 {
			<-ctx.Done()
			return Result{State: "pending", Message: "timed out"}, ctx.Err()
		}
		return Result{State: "synced", Message: "retry succeeded"}, nil
	}, time.Hour, 15*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner.Start(ctx)
	waitRunnerState(t, runner, "pending")
	runner.Request(context.Background())
	status := waitRunnerState(t, runner, "synced")
	if status.LastSuccessAt == "" {
		t.Fatal("successful retry did not record last success time")
	}
}

func TestRunnerHidesRawErrorTextAndKeepsSafeState(t *testing.T) {
	const secret = "raw-command-secret"
	runner := NewRunner(func(context.Context) (Result, error) {
		return Result{State: "pending", Message: "safe retry message"}, errors.New(secret)
	}, time.Hour, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner.Start(ctx)
	status := waitRunnerState(t, runner, "pending")
	if status.Message != "safe retry message" || status.Message == secret {
		t.Fatalf("unsafe result: %+v", status)
	}
}

func waitRunnerState(t *testing.T, runner *Runner, state string) Result {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if current := runner.Status(); current.State == state {
			return current
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("runner did not reach state %q; last status=%+v", state, runner.Status())
	return Result{}
}
