package kanban

import (
	"context"
	"testing"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/fs"
)

// fakeSteerPort 实现 SteerPort。
type fakeSteerPort struct {
	supports bool
	receipt  SteerReceipt
	err      error
	calls    []string
}

func (f *fakeSteerPort) SupportsSteer(sessionKey string) bool { return f.supports }
func (f *fakeSteerPort) Steer(ctx context.Context, sessionKey, msg string) (SteerReceipt, error) {
	f.calls = append(f.calls, sessionKey+":"+msg)
	if f.err != nil {
		return f.receipt, f.err
	}
	return f.receipt, nil
}

type fakeProbe struct{ active bool }

func (f fakeProbe) SessionActive(sessionKey string) bool { return f.active }

// nilSession 是未实现 SteerCapability 的最小 Session 替身。
type nilSession struct{ agenttypes.Session }

func (nilSession) SupportsSteer() bool { return agenttypes.DefaultSteerSupport() }

// TestSteerReceiptMirrorsTypes 锁定 kanban 本地 SteerReceipt 镜像与
// agent/types 常量的字符串值一致（spec 要求 confirmed/sent/rejected/unknown）。
func TestSteerReceiptMirrorsTypes(t *testing.T) {
	if string(SteerReceiptConfirmed) != string(agenttypes.SteerReceiptConfirmed) {
		t.Fatal("confirmed mismatch")
	}
	if string(SteerReceiptSent) != string(agenttypes.SteerReceiptSent) {
		t.Fatal("sent mismatch")
	}
	if string(SteerReceiptRejected) != string(agenttypes.SteerReceiptRejected) {
		t.Fatal("rejected mismatch")
	}
	if string(SteerReceiptUnknown) != string(agenttypes.SteerReceiptUnknown) {
		t.Fatal("unknown mismatch")
	}
	_ = nilSession{}
	if string(agenttypes.SteerReceiptUnknown) != "unknown" {
		t.Fatal("types unknown mismatch")
	}
	_ = nilSession{}.SupportsSteer()
}

func newOutbox(t *testing.T) (*TaskStore, *OutboxService) {
	t.Helper()
	root := fs.NewRootInfo("root", "root", t.TempDir())
	store, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, &OutboxService{Store: store}
}

// Steer confirmed 仅真实回执。
func TestOutbox_SteerConfirmedOnlyRealReceipt(t *testing.T) {
	_, o := newOutbox(t)
	ctx := context.Background()
	rec, err := o.RecordComment(ctx, ContinueRequestRecord{ID: "cr1", TaskID: "t1", CommentID: "c1", NodeRunID: "nr1", SessionKey: "sess1", Message: "hello", Author: "user"})
	if err != nil {
		t.Fatalf("RecordComment: %v", err)
	}
	o.port = &fakeSteerPort{supports: true, receipt: SteerReceiptConfirmed}
	if err := o.Consume(ctx, "t1", DefaultSteerPolicy{Port: o.port}, fakeProbe{active: true}, nil); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	got, _ := o.GetRequest(ctx, rec.ID)
	if got.Status != OutboxSteerConfirmed {
		t.Fatalf("expected steer_confirmed, got %s", got.Status)
	}
	// sent 绝不升级为 confirmed。
	_, o2 := newOutbox(t)
	rec2, _ := o2.RecordComment(ctx, ContinueRequestRecord{ID: "cr2", TaskID: "t1", CommentID: "c2", NodeRunID: "nr1", SessionKey: "sess1", Message: "hi", Author: "user"})
	o2.port = &fakeSteerPort{supports: true, receipt: SteerReceiptSent}
	if err := o2.Consume(ctx, "t1", DefaultSteerPolicy{Port: o2.port}, fakeProbe{active: true}, nil); err != nil {
		t.Fatal(err)
	}
	got2, _ := o2.GetRequest(ctx, rec2.ID)
	if got2.Status == OutboxSteerConfirmed {
		t.Fatalf("sent must not become confirmed, got %s", got2.Status)
	}
	if got2.Status != OutboxSteerSent {
		t.Fatalf("expected steer_sent, got %s", got2.Status)
	}
}

// 超时 unknown 不重放。
func TestOutbox_TimeoutUnknownNoReplay(t *testing.T) {
	_, o := newOutbox(t)
	ctx := context.Background()
	rec, _ := o.RecordComment(ctx, ContinueRequestRecord{ID: "cr1", TaskID: "t1", CommentID: "c1", NodeRunID: "nr1", SessionKey: "sess1", Message: "hello", Author: "user"})
	// Steer 返回 error（模拟超时无法判定）。
	o.port = &fakeSteerPort{supports: true, receipt: SteerReceiptUnknown, err: context.DeadlineExceeded}
	if err := o.Consume(ctx, "t1", DefaultSteerPolicy{Port: o.port}, fakeProbe{active: true}, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := o.GetRequest(ctx, rec.ID)
	if got.Status != OutboxUnknown {
		t.Fatalf("expected unknown, got %s", got.Status)
	}
	// 第二次 Consume 不应重发（pending 不含 unknown）。
	if err := o.Consume(ctx, "t1", DefaultSteerPolicy{Port: o.port}, fakeProbe{active: true}, nil); err != nil {
		t.Fatal(err)
	}
	if len(o.port.(*fakeSteerPort).calls) != 1 {
		t.Fatalf("unknown must not replay, calls=%d", len(o.port.(*fakeSteerPort).calls))
	}
}

// FIFO 保序同 session。
func TestOutbox_FIFOSameSessionPreservesOrder(t *testing.T) {
	_, o := newOutbox(t)
	ctx := context.Background()
	for _, id := range []string{"c1", "c2", "c3"} {
		if _, err := o.RecordComment(ctx, ContinueRequestRecord{ID: "cr-" + id, TaskID: "t1", CommentID: id, NodeRunID: "nr1", SessionKey: "sess1", Message: "msg-" + id, Author: "user"}); err != nil {
			t.Fatal(err)
		}
	}
	// 默认 policy（无 steer）→ queued。
	if err := o.Consume(ctx, "t1", DefaultSteerPolicy{Port: nil}, fakeProbe{active: false}, nil); err != nil {
		t.Fatal(err)
	}
	var sent []string
	err := o.DrainAfterNodeRun(ctx, "t1", func(ctx context.Context, sessionKey, msg string) error {
		sent = append(sent, msg)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sent) != 3 || sent[0] != "msg-c1" || sent[1] != "msg-c2" || sent[2] != "msg-c3" {
		t.Fatalf("FIFO order broken: %v", sent)
	}
	// 全部 consumed。
	for _, id := range []string{"cr-c1", "cr-c2", "cr-c3"} {
		got, _ := o.GetRequest(ctx, id)
		if got.Status != OutboxConsumed {
			t.Fatalf("expected consumed for %s, got %s", id, got.Status)
		}
	}
}

// 新 attempt 不复制留言。
func TestOutbox_NewAttemptDoesNotCopyComment(t *testing.T) {
	_, o := newOutbox(t)
	ctx := context.Background()
	orig, _ := o.RecordComment(ctx, ContinueRequestRecord{ID: "cr1", TaskID: "t1", CommentID: "c1", NodeRunID: "nr1", SessionKey: "sess1", Message: "original body", Author: "user"})
	attempt, err := o.CreateNewAttempt(ctx, orig, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if attempt.ID == orig.NodeRunID {
		t.Fatal("new attempt must have a distinct NodeRun id")
	}
	// 新 attempt 的 ContinueRequest 不应携带原 message。
	list, _ := o.ListForTask(ctx, "t1")
	found := false
	for _, r := range list {
		if r.PrevAttemptID == orig.ID {
			found = true
			if r.Message == "original body" {
				t.Fatalf("new attempt must not copy the comment body")
			}
			if r.NodeRunID == orig.NodeRunID {
				t.Fatalf("new attempt must link a new NodeRun, not reuse %s", orig.NodeRunID)
			}
		}
	}
	if !found {
		t.Fatal("new attempt link not found")
	}
}

// 幂等：重复提交同一 comment 不双跑。
func TestOutbox_RecordCommentIdempotent(t *testing.T) {
	_, o := newOutbox(t)
	ctx := context.Background()
	in := ContinueRequestRecord{ID: "cr1", TaskID: "t1", CommentID: "c1", NodeRunID: "nr1", SessionKey: "sess1", Message: "hello", Author: "user"}
	r1, _ := o.RecordComment(ctx, in)
	r2, _ := o.RecordComment(ctx, in)
	if r1.ID != r2.ID {
		t.Fatalf("idempotent ids differ: %s vs %s", r1.ID, r2.ID)
	}
	if _, err := o.RecordComment(ctx, ContinueRequestRecord{ID: "crX", TaskID: "t1", CommentID: "c1", NodeRunID: "nr1", SessionKey: "sess1", Message: "tampered", Author: "user"}); err != ErrOutboxCommentConflict {
		t.Fatalf("expected conflict, got %v", err)
	}
}
