package kanban

import (
	"context"
	"testing"
)

type fakeSessionProbe struct{ found, resumable bool }

func (f fakeSessionProbe) Probe(sessionKey string) (bool, bool) { return f.found, f.resumable }

// 重启可确定续跑原 session。
func TestRecover_DeterminableResumesOriginalSession(t *testing.T) {
	store, o := newOutbox(t)
	ctx := context.Background()
	if _, err := o.RecordComment(ctx, ContinueRequestRecord{ID: "cr1", TaskID: "t1", CommentID: "c1", NodeRunID: "nr1", SessionKey: "sess1", Message: "hello", Author: "user"}); err != nil {
		t.Fatal(err)
	}
	plan, err := RecoverOnStartup(ctx, store, fakeSessionProbe{found: true, resumable: true})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ResumeSession != "sess1" {
		t.Fatalf("expected resume sess1, got %q", plan.ResumeSession)
	}
	if plan.NeedsUser {
		t.Fatal("determinable recovery must not need user")
	}
}

// 重启不可确定 unknown，不重发 prompt，不自动建 attempt。
func TestRecover_UndeterminableMarksUnknownNoReplay(t *testing.T) {
	store, o := newOutbox(t)
	ctx := context.Background()
	rec, _ := o.RecordComment(ctx, ContinueRequestRecord{ID: "cr1", TaskID: "t1", CommentID: "c1", NodeRunID: "nr1", SessionKey: "sess1", Message: "hello", Author: "user"})
	plan, err := RecoverOnStartup(ctx, store, fakeSessionProbe{found: true, resumable: false})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.NeedsUser {
		t.Fatal("undeterminable recovery must need user")
	}
	if len(plan.UnknownIDs) != 1 || plan.UnknownIDs[0] != rec.ID {
		t.Fatalf("expected unknown [cr1], got %v", plan.UnknownIDs)
	}
	if plan.ResumeSession != "" {
		t.Fatalf("must not resume undeterminable session, got %q", plan.ResumeSession)
	}
	// 状态已标 unknown，证据保留。
	got, _ := o.GetRequest(ctx, rec.ID)
	if got.Status != OutboxUnknown {
		t.Fatalf("expected unknown, got %s", got.Status)
	}
	// 不得自动建 attempt：node_runs 只有原 nr1。
	if _, err := GetNodeRun(ctx, store, "nr1"); err != nil {
		t.Fatalf("original NodeRun evidence must remain: %v", err)
	}
	// 无探针时同样 unknown。
	store2, o2 := newOutbox(t)
	rec2, _ := o2.RecordComment(ctx, ContinueRequestRecord{ID: "cr1", TaskID: "t1", CommentID: "c1", NodeRunID: "nr1", SessionKey: "sess1", Message: "hello", Author: "user"})
	plan2, err := RecoverOnStartup(ctx, store2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !plan2.NeedsUser {
		t.Fatal("no probe must need user")
	}
	got2, _ := o2.GetRequest(ctx, rec2.ID)
	if got2.Status != OutboxUnknown {
		t.Fatalf("no-probe expected unknown, got %s", got2.Status)
	}
}
