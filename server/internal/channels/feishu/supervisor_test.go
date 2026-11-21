package feishu

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeEventCLI writes a script that prints one line per run (stdout or
// stderr, depending on the line) and then exits. A run counter lets a test
// see restarts without waiting on wall-clock guesses.
func fakeEventCLI(t *testing.T, line string, toStderr bool) (bin, counterPath string) {
	t.Helper()
	dir := t.TempDir()
	counterPath = filepath.Join(dir, "runs.txt")
	bin = filepath.Join(dir, "lark-cli")
	target := ">&1"
	if toStderr {
		target = ">&2"
	}
	script := "#!/bin/sh\n" +
		"echo run >> " + counterPath + "\n" +
		"cat <<'LINE' " + target + "\n" + line + "\nLINE\nexit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, counterPath
}

func runCount(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return len(strings.Fields(string(raw)))
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

func TestSupervisorDispatchesClickAndRestartsAfterExit(t *testing.T) {
	bin, counter := fakeEventCLI(t, `{"type":"card.action.trigger","operator_id":"ou_owner","message_id":"om_1","action_value":"{\"card\":\"gate-1\",\"choice\":0}"}`, false)
	seen := make(chan Action, 4)
	sup := &Supervisor{Path: bin, RestartDelay: 10 * time.Millisecond, ConflictDelay: 10 * time.Millisecond,
		Logf:   func(string, ...any) {},
		Handle: func(_ context.Context, a Action) error { seen <- a; return nil }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.Run(ctx)

	select {
	case a := <-seen:
		if a.CardID != "gate-1" || a.Choice != 0 || a.OperatorID != "ou_owner" {
			t.Fatalf("action = %+v", a)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("click not dispatched")
	}
	waitFor(t, 2*time.Second, func() bool { return runCount(counter) >= 2 })
}

func TestSupervisorIgnoresNonClickLinesAndSurvivesHandlerErrors(t *testing.T) {
	bin, _ := fakeEventCLI(t, `{"type":"im.message.receive_v1"}`, false)
	var calls atomic.Int32
	sup := &Supervisor{Path: bin, RestartDelay: 5 * time.Millisecond, ConflictDelay: 5 * time.Millisecond,
		Logf:   func(string, ...any) {},
		Handle: func(context.Context, Action) error { calls.Add(1); return fmt.Errorf("boom") }}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	sup.Run(ctx)
	if calls.Load() != 0 {
		t.Fatalf("handler called %d times for a non-click line", calls.Load())
	}
}

func TestSupervisorWaitsLongerOnConflictAndStopsOnContext(t *testing.T) {
	bin, counter := fakeEventCLI(t, "another event bus is already connected", true)
	var logged strings.Builder
	sup := &Supervisor{Path: bin, RestartDelay: 5 * time.Millisecond, ConflictDelay: 80 * time.Millisecond,
		Logf:   func(format string, args ...any) { logged.WriteString(fmt.Sprintf(format, args...)) },
		Handle: func(context.Context, Action) error { return nil }}
	ctx, cancel := context.WithTimeout(context.Background(), 130*time.Millisecond)
	defer cancel()
	sup.Run(ctx)
	if runs := runCount(counter); runs != 2 {
		t.Fatalf("runs = %d, want 2 (conflict delay must dominate the 5ms restart delay)", runs)
	}
	if got := strings.Count(logged.String(), "拒绝再连"); got != 1 {
		t.Fatalf("conflict logged %d times, want once per episode:\n%s", got, logged.String())
	}
}

func TestSupervisorStopsWhenContextIsCanceled(t *testing.T) {
	bin, counter := fakeEventCLI(t, "noise", false)
	sup := &Supervisor{Path: bin, RestartDelay: 5 * time.Millisecond, ConflictDelay: 5 * time.Millisecond,
		Logf: func(string, ...any) {}, Handle: func(context.Context, Action) error { return nil }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { sup.Run(ctx); close(done) }()
	waitFor(t, 2*time.Second, func() bool { return runCount(counter) >= 1 })
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
