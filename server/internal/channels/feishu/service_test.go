package feishu

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixedNow() time.Time {
	return time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
}

func noLog(string, ...any) {}

// fakeLarkLogged writes a script that appends every call to logPath and
// replies with a successful send shape. Tests read the log to see both what
// was sent and in which order relative to the answer handler.
func fakeLarkLogged(t *testing.T, logPath string, mu *sync.Mutex) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "lark-cli")
	script := "#!/bin/sh\n" +
		"printf '%s ' \"$@\" >> " + logPath + "\n" +
		"echo >> " + logPath + "\n" +
		"cat <<'JSON'\n{\"ok\":true,\"data\":{\"message_id\":\"om_1\"}}\nJSON\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func appendLine(t *testing.T, path, line string, mu *sync.Mutex) {
	t.Helper()
	if mu != nil {
		mu.Lock()
		defer mu.Unlock()
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
}

func newTestService(t *testing.T, cfg Config) (*Service, *Store, string) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "calls.txt")
	var mu sync.Mutex
	bin := fakeLarkLogged(t, logPath, &mu)
	if cfg.Now == nil {
		cfg.Now = fixedNow
	}
	if cfg.Logf == nil {
		cfg.Logf = noLog
	}
	if cfg.CardTTL == 0 {
		cfg.CardTTL = time.Hour
	}
	st := NewStore(filepath.Join(t.TempDir(), "cards.json"))
	if err := st.Load(); err != nil {
		t.Fatal(err)
	}
	return New(cfg, st, &CLI{Path: bin, Timeout: time.Second}), st, logPath
}

func gateNotice(id string) Notice {
	return Notice{CardID: id, Kind: KindGate, RootID: "root", TaskID: "task-1",
		TaskTitle: "修复登录", Prompt: "确认继续", Options: []string{"批准", "驳回"}}
}

func TestServiceSendsUnsentCardOnceWithStableIdempotencyKey(t *testing.T) {
	svc, st, logPath := newTestService(t, Config{OwnerOpenID: "ou_owner"})
	if err := svc.Notify(context.Background(), gateNotice("gate-nr-1")); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeliverOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	calls := readText(t, logPath)
	if !strings.Contains(calls, "mindfs-card-gate-nr-1") {
		t.Fatalf("idempotency key missing:\n%s", calls)
	}
	c, ok := st.Get("gate-nr-1")
	if !ok || c.MessageID != "om_1" || c.RenderedVersion != c.Version || c.State != StateOpen {
		t.Fatalf("card = %+v", c)
	}
	if err := svc.DeliverOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Count(readText(t, logPath), "messages-send") != 1 {
		t.Fatalf("card sent twice:\n%s", readText(t, logPath))
	}
}

func TestServiceClickAcksBeforeApplyingAndAppliesOnce(t *testing.T) {
	svc, st, logPath := newTestService(t, Config{OwnerOpenID: "ou_owner"})
	var mu sync.Mutex
	if err := svc.Notify(context.Background(), gateNotice("gate-nr-1")); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeliverOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(logPath, 0); err != nil {
		t.Fatal(err)
	}
	var applied []Answer
	svc.SetAnswerHandler(func(_ context.Context, a Answer) error {
		appendLine(t, logPath, "apply", &mu)
		applied = append(applied, a)
		return nil
	})
	if err := svc.HandleAction(context.Background(), Action{CardID: "gate-nr-1", Choice: 0, OperatorID: "ou_owner", MessageID: "om_1"}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(readText(t, logPath))
	if len(lines) < 2 {
		t.Fatalf("calls = %v", lines)
	}
	joined := readText(t, logPath)
	ackAt := strings.Index(joined, "messages patch")
	applyAt := strings.Index(joined, "apply")
	if ackAt < 0 || applyAt < 0 || ackAt > applyAt {
		t.Fatalf("ack must be patched before the answer is applied:\n%s", joined)
	}
	if len(applied) != 1 || applied[0].Decision != "approve" || applied[0].Kind != KindGate || applied[0].TaskID != "task-1" {
		t.Fatalf("applied = %+v", applied)
	}
	// A second click, even the opposite one, must not apply again.
	if err := svc.HandleAction(context.Background(), Action{CardID: "gate-nr-1", Choice: 1, OperatorID: "ou_owner", MessageID: "om_1"}); err != nil {
		t.Fatal(err)
	}
	if len(applied) != 1 {
		t.Fatalf("second click applied: %+v", applied)
	}
	if c, _ := st.Get("gate-nr-1"); c.State != StateDone || c.Summary == "" {
		t.Fatalf("card = %+v", c)
	}
}

func TestServiceIgnoresNonOwnerAndForeignMessage(t *testing.T) {
	svc, st, _ := newTestService(t, Config{OwnerOpenID: "ou_owner"})
	var applied int
	svc.SetAnswerHandler(func(context.Context, Answer) error { applied++; return nil })
	if err := svc.Notify(context.Background(), gateNotice("gate-1")); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeliverOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := svc.HandleAction(context.Background(), Action{CardID: "gate-1", Choice: 0, OperatorID: "ou_stranger", MessageID: "om_1"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.HandleAction(context.Background(), Action{CardID: "gate-1", Choice: 0, OperatorID: "ou_owner", MessageID: "om_other"}); err != nil {
		t.Fatal(err)
	}
	if applied != 0 {
		t.Fatalf("applied = %d", applied)
	}
	if c, _ := st.Get("gate-1"); c.State != StateOpen {
		t.Fatalf("card state = %s", c.State)
	}
}

func TestServiceExpiredCardDoesNotApply(t *testing.T) {
	svc, st, _ := newTestService(t, Config{OwnerOpenID: "ou_owner", CardTTL: time.Minute})
	var applied int
	svc.SetAnswerHandler(func(context.Context, Answer) error { applied++; return nil })
	if err := svc.Notify(context.Background(), gateNotice("gate-1")); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeliverOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	svc.now = func() time.Time { return fixedNow().Add(2 * time.Minute) }
	if err := svc.HandleAction(context.Background(), Action{CardID: "gate-1", Choice: 0, OperatorID: "ou_owner", MessageID: "om_1"}); err != nil {
		t.Fatal(err)
	}
	if applied != 0 {
		t.Fatalf("expired card applied: %d", applied)
	}
	if c, _ := st.Get("gate-1"); c.State != StateExpired {
		t.Fatalf("state = %s", c.State)
	}
}

func TestServiceHandlerFailureMarksCardFailedAndSaysSo(t *testing.T) {
	svc, st, logPath := newTestService(t, Config{OwnerOpenID: "ou_owner"})
	svc.SetAnswerHandler(func(context.Context, Answer) error { return errors.New("已在网页处理") })
	if err := svc.Notify(context.Background(), gateNotice("gate-1")); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeliverOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := svc.HandleAction(context.Background(), Action{CardID: "gate-1", Choice: 0, OperatorID: "ou_owner", MessageID: "om_1"}); err != nil {
		t.Fatal(err)
	}
	c, _ := st.Get("gate-1")
	if c.State != StateFailed || !strings.Contains(c.Summary, "已在网页处理") {
		t.Fatalf("card = %+v", c)
	}
	if !strings.Contains(readText(t, logPath), "messages patch") {
		t.Fatalf("failure not patched:\n%s", readText(t, logPath))
	}
}

func TestServiceNoticeAcknowledgesWithoutHandler(t *testing.T) {
	svc, st, _ := newTestService(t, Config{OwnerOpenID: "ou_owner"})
	svc.SetAnswerHandler(func(context.Context, Answer) error { t.Fatal("notice must not call the answer handler"); return nil })
	if err := svc.Notify(context.Background(), Notice{CardID: "notice-1", Kind: KindNotice, TaskID: "task-1", Prompt: "任务完成"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeliverOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := svc.HandleAction(context.Background(), Action{CardID: "notice-1", Choice: 0, OperatorID: "ou_owner", MessageID: "om_1", Text: "已回复"}); err != nil {
		t.Fatal(err)
	}
	c, _ := st.Get("notice-1")
	if c.State != StateDone || c.Note != "已回复" {
		t.Fatalf("card = %+v", c)
	}
}

func TestServiceNotifyIsIdempotentForSameCardID(t *testing.T) {
	svc, _, logPath := newTestService(t, Config{OwnerOpenID: "ou_owner"})
	if err := svc.Notify(context.Background(), gateNotice("gate-1")); err != nil {
		t.Fatal(err)
	}
	first := gateNotice("gate-1")
	first.Prompt = "换一句话"
	if err := svc.Notify(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeliverOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(readText(t, logPath), "messages-send"); got != 1 {
		t.Fatalf("sent %d times", got)
	}
	c, _ := svc.Store().Get("gate-1")
	if c.Prompt != "确认继续" {
		t.Fatalf("existing card was rewritten: %+v", c)
	}
}

// 过期卡片要有人清：没人清的话 cards.json 随任务数一直长，而每次投递都在锁里
// 重写整个文件。投递一拍顺手清掉是最省事的位置。
func TestDeliverOnceDropsExpiredCards(t *testing.T) {
	svc, st, _ := newTestService(t, Config{OwnerOpenID: "ou_owner"})
	if _, err := st.Put(Card{
		ID: "notice-old", Kind: KindNotice, State: StateDone,
		ExpiresAt: fixedNow().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeliverOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.Get("notice-old"); ok {
		t.Fatal("expired card survived a delivery tick")
	}
}
