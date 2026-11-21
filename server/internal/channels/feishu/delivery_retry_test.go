package feishu

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Feishu rejects --idempotency-key longer than this, verbatim:
// "validation：--idempotency-key exceeds the maximum of 50 characters".
const feishuKeyLimit = 50

const (
	okReply   = `echo '{"ok":true,"data":{"message_id":"om_1"}}'`
	failReply = `echo '{"ok":false,"error":{"type":"validation","message":"--idempotency-key exceeds the maximum of 50 characters (got 68)"}}'`
)

type testClock struct{ t time.Time }

func (c *testClock) Now() time.Time          { return c.t }
func (c *testClock) advance(d time.Duration) { c.t = c.t.Add(d) }
func newTestClock() *testClock               { return &testClock{t: time.Date(2026, 11, 21, 23, 0, 0, 0, time.UTC)} }

// newDeliveryService builds a service whose lark-cli is a shell stub that
// logs every invocation and then runs script (which decides success/failure).
func newDeliveryService(t *testing.T, clock *testClock, script string) (*Service, *Store, string) {
	t.Helper()
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls.log")
	bin := filepath.Join(dir, "lark-cli")
	body := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> '" + calls + "'\n" +
		script + "\n"
	if err := os.WriteFile(bin, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	st := NewStore(filepath.Join(dir, "cards.json"))
	if err := st.Load(); err != nil {
		t.Fatal(err)
	}
	svc := New(Config{
		OwnerOpenID: "ou_owner",
		Now:         clock.Now,
		Logf:        func(string, ...any) {},
	}, st, &CLI{Path: bin, Timeout: 5 * time.Second})
	return svc, st, calls
}

func callCount(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(raw), "\n")
}

func lastCall(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	return lines[len(lines)-1]
}

func noticeCard(id string) Notice {
	return Notice{CardID: id, Kind: KindNotice, TaskID: "task_8187f1159220980f", Text: "任务已完成"}
}

// Terminal notices and scheduled notices build ids of 55+ characters. The
// delivered key must still fit Feishu's cap, stay deterministic (a retry of
// the same card has to dedupe upstream) and stay distinct per card.
func TestIdempotencyKeyFitsFeishuLimit(t *testing.T) {
	ids := []string{
		"notice-task_8187f1159220980f-success-1795275026781232682",
		"scheduled-task_8187f1159220980f-success-1795275026781232682",
		"gate-run-deb14034340d96d8:gate::0:1",
	}
	seen := map[string]string{}
	for _, id := range ids {
		key := idempotencyKey(id)
		if len(key) > feishuKeyLimit {
			t.Fatalf("key for %q is %d chars, Feishu allows %d", id, len(key), feishuKeyLimit)
		}
		if again := idempotencyKey(id); again != key {
			t.Fatalf("key for %q is not deterministic: %q vs %q", id, key, again)
		}
		if other, dup := seen[key]; dup {
			t.Fatalf("cards %q and %q share key %q", other, id, key)
		}
		seen[key] = id
	}
}

func TestSendCardPassesShortKeyToCLI(t *testing.T) {
	clock := newTestClock()
	svc, _, calls := newDeliveryService(t, clock, okReply)
	id := "notice-task_8187f1159220980f-success-1795275026781232682"
	if err := svc.Notify(context.Background(), noticeCard(id)); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeliverOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	line := lastCall(t, calls)
	const flag = "--idempotency-key "
	at := strings.Index(line, flag)
	if at < 0 {
		t.Fatalf("no idempotency key in %q", line)
	}
	key := strings.Fields(line[at+len(flag):])[0]
	if len(key) > feishuKeyLimit {
		t.Fatalf("sent key %q is %d chars", key, len(key))
	}
	if key != idempotencyKey(id) {
		t.Fatalf("sent key = %q, want %q", key, idempotencyKey(id))
	}
}

func TestDeliverOnceBacksOffAfterSendFailure(t *testing.T) {
	clock := newTestClock()
	svc, _, calls := newDeliveryService(t, clock, failReply)
	ctx := context.Background()
	if err := svc.Notify(ctx, noticeCard("notice-1")); err != nil {
		t.Fatal(err)
	}

	if err := svc.DeliverOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if n := callCount(t, calls); n != 1 {
		t.Fatalf("calls after first failure = %d, want 1", n)
	}
	// Same instant: the failed card must wait out its backoff instead of
	// hammering Feishu every tick.
	if err := svc.DeliverOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if n := callCount(t, calls); n != 1 {
		t.Fatalf("retried inside the backoff window: calls = %d", n)
	}
	clock.advance(deliveryBackoffBase)
	if err := svc.DeliverOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if n := callCount(t, calls); n != 2 {
		t.Fatalf("calls after first backoff = %d, want 2", n)
	}
	// Second failure doubles the wait: attempt 2 was scheduled at t+10s.
	clock.advance(2*deliveryBackoffBase - time.Millisecond)
	if err := svc.DeliverOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if n := callCount(t, calls); n != 2 {
		t.Fatalf("retried before the doubled backoff: calls = %d", n)
	}
	clock.advance(time.Millisecond)
	if err := svc.DeliverOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if n := callCount(t, calls); n != 3 {
		t.Fatalf("calls after doubled backoff = %d, want 3", n)
	}
}

func TestDeliverOnceBacksOffAfterPatchFailure(t *testing.T) {
	clock := newTestClock()
	svc, st, calls := newDeliveryService(t, clock, failReply)
	ctx := context.Background()
	now := clock.Now()
	if _, err := st.Put(Card{
		ID: "gate-1", Kind: KindGate, State: StateOpen, MessageID: "om_1",
		RenderedVersion: 1, Version: 2, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeliverOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if n := callCount(t, calls); n != 1 {
		t.Fatalf("patch calls = %d, want 1", n)
	}
	if err := svc.DeliverOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if n := callCount(t, calls); n != 1 {
		t.Fatalf("patched again inside the backoff window: calls = %d", n)
	}
	clock.advance(deliveryBackoffBase)
	if err := svc.DeliverOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if n := callCount(t, calls); n != 2 {
		t.Fatalf("patch calls after backoff = %d, want 2", n)
	}
}

func TestDeliverOnceGivesUpOnUnsendableCard(t *testing.T) {
	clock := newTestClock()
	svc, st, calls := newDeliveryService(t, clock, failReply)
	ctx := context.Background()
	const id = "notice-task_8187f1159220980f-success-1795275026781232682"
	if err := svc.Notify(ctx, noticeCard(id)); err != nil {
		t.Fatal(err)
	}

	for i := 1; i <= deliveryAttemptCap; i++ {
		if err := svc.DeliverOnce(ctx); err != nil {
			t.Fatal(err)
		}
		if n := callCount(t, calls); n != i {
			t.Fatalf("attempt %d: calls = %d", i, n)
		}
		clock.advance(deliveryBackoffCap)
	}
	c, ok := st.Get(id)
	if !ok {
		t.Fatal("card vanished")
	}
	if !c.DeliveryStopped {
		t.Fatalf("card kept retrying after %d attempts: %+v", deliveryAttemptCap, c)
	}
	if c.LastError == "" {
		t.Fatal("gave up without recording why")
	}
	for i := 0; i < 5; i++ {
		clock.advance(24 * time.Hour)
		if err := svc.DeliverOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if n := callCount(t, calls); n != deliveryAttemptCap {
		t.Fatalf("stopped card was retried: calls = %d, want %d", n, deliveryAttemptCap)
	}
}

func TestSuccessfulRetryClearsDeliveryState(t *testing.T) {
	clock := newTestClock()
	// Fails once, then succeeds: the stub leaves a marker next to itself.
	svc, st, calls := newDeliveryService(t, clock,
		`if [ -f "$(dirname "$0")/sent" ]; then `+okReply+`; else touch "$(dirname "$0")/sent"; `+failReply+`; fi`)
	ctx := context.Background()
	const id = "gate-1"
	if err := svc.Notify(ctx, Notice{CardID: id, Kind: KindGate, TaskID: "task-1", Prompt: "确认继续"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeliverOnce(ctx); err != nil {
		t.Fatal(err)
	}
	c, _ := st.Get(id)
	if c.DeliveryAttempts != 1 {
		t.Fatalf("attempts after failure = %d, want 1", c.DeliveryAttempts)
	}
	clock.advance(deliveryBackoffBase)
	if err := svc.DeliverOnce(ctx); err != nil {
		t.Fatal(err)
	}
	c, _ = st.Get(id)
	if c.MessageID == "" {
		t.Fatal("second attempt did not deliver")
	}
	if c.DeliveryAttempts != 0 || !c.NextAttemptAt.IsZero() || c.LastError != "" || c.DeliveryStopped {
		t.Fatalf("delivery state survived success: %+v", c)
	}
	if err := svc.DeliverOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if n := callCount(t, calls); n != 2 {
		t.Fatalf("calls = %d, want 2", n)
	}
}
