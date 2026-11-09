package kanban

import (
	"context"
	"errors"
	"testing"

	"mindfs/server/internal/fs"
)

// countRows 是集成断言用的最小计数帮手（只数本测试关心的表）。
func countRows(t *testing.T, svc *Service, rootID, query string, args ...any) int {
	t.Helper()
	store, err := svc.taskStore(rootID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	var n int
	if err := store.db.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

// WP-03 启动装配集成：Service 层入口必须把「判定」与「重发」分开。
//
// 可确定：只报告原 session，不重发 prompt、不建 attempt、不动任务状态。
func TestRecoverOnStartupForRoot_DeterminableOnlyReportsSession(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Name:   "recover ok",
		Stages: []StageTemplate{userStage("Describe")},
		Input:  "hello",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	o := &OutboxService{Store: store}
	rec, err := o.RecordComment(ctx, ContinueRequestRecord{
		ID: "cr-ok", TaskID: detail.Task.ID, CommentID: "c-ok",
		NodeRunID: "nr-ok", SessionKey: "sess-ok", Message: "继续", Author: "user",
	})
	if err != nil {
		t.Fatalf("RecordComment: %v", err)
	}
	plan, err := svc.RecoverOnStartupForRoot(ctx, root.ID, fakeSessionProbe{found: true, resumable: true})
	if err != nil {
		t.Fatalf("RecoverOnStartupForRoot: %v", err)
	}
	if plan.ResumeSession != "sess-ok" {
		t.Fatalf("resume session=%q, want sess-ok", plan.ResumeSession)
	}
	if plan.NeedsUser {
		t.Fatal("determinable recovery must not need user")
	}
	// 证据：请求状态仍是 recorded（没被标 unknown），任务状态没被改成 waiting_user。
	got, err := o.GetRequest(ctx, rec.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if got.Status != OutboxRecorded {
		t.Fatalf("request status=%s, want recorded（不得误标 unknown）", got.Status)
	}
	after, err := store.GetTask(ctx, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if after.Status != StatusPending {
		t.Fatalf("task status=%s, want pending（可确定恢复不得暂停任务）", after.Status)
	}
	// 不重发：outbox item 与 node_run 都只有原记录那一行。
	if n := countRows(t, svc, root.ID, `SELECT COUNT(*) FROM outbox_items WHERE task_id=?`, detail.Task.ID); n != 1 {
		t.Fatalf("outbox items=%d, want 1（不得重发）", n)
	}
	if n := countRows(t, svc, root.ID, `SELECT COUNT(*) FROM node_runs WHERE task_id=?`, detail.Task.ID); n != 1 {
		t.Fatalf("node runs=%d, want 1（不得新建 attempt）", n)
	}
}

// 不可确定：标 unknown + 任务 needs-user，绝不自动重发、不建新 attempt。
func TestRecoverOnStartupForRoot_UnknownMarksNeedsUserWithoutReplay(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Name:   "recover unknown",
		Stages: []StageTemplate{userStage("Describe")},
		Input:  "hello",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	o := &OutboxService{Store: store}
	rec, err := o.RecordComment(ctx, ContinueRequestRecord{
		ID: "cr-unknown", TaskID: detail.Task.ID, CommentID: "c-unknown",
		NodeRunID: "nr-unknown", SessionKey: "sess-unknown", Message: "继续", Author: "user",
	})
	if err != nil {
		t.Fatalf("RecordComment: %v", err)
	}
	// found=true 但 resumable=false：会话行在，但启动期无法证明上轮已交付。
	plan, err := svc.RecoverOnStartupForRoot(ctx, root.ID, fakeSessionProbe{found: true, resumable: false})
	if err != nil {
		t.Fatalf("RecoverOnStartupForRoot: %v", err)
	}
	if !plan.NeedsUser {
		t.Fatal("undeterminable recovery must need user")
	}
	if len(plan.UnknownIDs) != 1 || plan.UnknownIDs[0] != rec.ID {
		t.Fatalf("unknown ids=%v, want [%s]", plan.UnknownIDs, rec.ID)
	}
	if plan.ResumeSession != "" {
		t.Fatalf("must not resume, got %q", plan.ResumeSession)
	}
	got, err := o.GetRequest(ctx, rec.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if got.Status != OutboxUnknown {
		t.Fatalf("request status=%s, want unknown", got.Status)
	}
	after, err := store.GetTask(ctx, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if after.Status != StatusWaitingUser {
		t.Fatalf("task status=%s, want waiting_user（unknown 要人处理）", after.Status)
	}
	// 不重发、不建 attempt：两张表都还是原来那一行。
	if n := countRows(t, svc, root.ID, `SELECT COUNT(*) FROM outbox_items WHERE task_id=?`, detail.Task.ID); n != 1 {
		t.Fatalf("outbox items=%d, want 1（unknown 不得自动重发）", n)
	}
	if n := countRows(t, svc, root.ID, `SELECT COUNT(*) FROM node_runs WHERE task_id=?`, detail.Task.ID); n != 1 {
		t.Fatalf("node runs=%d, want 1（unknown 不得自动建 attempt）", n)
	}
}

// ctx 取消：同步返回 ctx.Err()，不留半写状态（请求仍是 recorded）。
func TestRecoverOnStartupForRoot_CanceledContextLeavesStateUntouched(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Name:   "recover canceled",
		Stages: []StageTemplate{userStage("Describe")},
		Input:  "hello",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	o := &OutboxService{Store: store}
	rec, err := o.RecordComment(ctx, ContinueRequestRecord{
		ID: "cr-cancel", TaskID: detail.Task.ID, CommentID: "c-cancel",
		NodeRunID: "nr-cancel", SessionKey: "sess-cancel", Message: "继续", Author: "user",
	})
	if err != nil {
		t.Fatalf("RecordComment: %v", err)
	}
	probe := fakeSessionProbe{found: true, resumable: false}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = svc.RecoverOnStartupForRoot(canceled, root.ID, probe)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want context.Canceled", err)
	}
	// 取消发生在查询阶段：状态不该被改（没有半写）。
	got, err := o.GetRequest(ctx, rec.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if got.Status != OutboxRecorded {
		t.Fatalf("request status=%s, want recorded（取消不得产生半写）", got.Status)
	}
	after, err := store.GetTask(ctx, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if after.Status != StatusPending {
		t.Fatalf("task status=%s, want pending", after.Status)
	}
}

// multiRootProvider 让一个 Service 同时看到多个 root（生产里每账户一个 Service、
// 但 root 有多个；探针必须按 root 区分）。
type multiRootProvider map[string]fs.RootInfo

func (m multiRootProvider) GetRoot(rootID string) (fs.RootInfo, error) {
	r, ok := m[rootID]
	if !ok {
		return fs.RootInfo{}, errors.New("root not found")
	}
	return r, nil
}

func (m multiRootProvider) ListRoots() []fs.RootInfo {
	out := make([]fs.RootInfo, 0, len(m))
	for _, r := range m {
		out = append(out, r)
	}
	return out
}

// 探针必须按 root 传：Service 跨 root 复用，若把探针挂在 Service 上，
// 后一个 root 会覆盖前一个 —— A root 的恢复会拿 B root 的会话表判定，
// 把可续跑误判成 unknown（或反过来）。这里两个 root 用不同探针，
// 断言各自拿到自己的结论。
func TestRecoverOnStartupForRoot_ProbeIsPerRoot(t *testing.T) {
	ctx := context.Background()
	rootA := fs.NewRootInfo("rootA", "rootA", t.TempDir())
	rootB := fs.NewRootInfo("rootB", "rootB", t.TempDir())
	svc := NewService(NewTemplateStoreAt(t.TempDir()), multiRootProvider{rootA.ID: rootA, rootB.ID: rootB})

	mk := func(root fs.RootInfo, name string) (string, *OutboxService) {
		detail, err := svc.CreateTask(ctx, CreateTaskInput{
			RootID: root.ID, Name: name,
			Stages: []StageTemplate{userStage("Describe")}, Input: "hello",
		})
		if err != nil {
			t.Fatalf("CreateTask(%s): %v", name, err)
		}
		store, err := svc.taskStore(root.ID)
		if err != nil {
			t.Fatalf("taskStore(%s): %v", name, err)
		}
		o := &OutboxService{Store: store}
		if _, err := o.RecordComment(ctx, ContinueRequestRecord{
			ID: "cr-" + name, TaskID: detail.Task.ID, CommentID: "c-" + name,
			NodeRunID: "nr-" + name, SessionKey: "sess-" + name, Message: "继续", Author: "user",
		}); err != nil {
			t.Fatalf("RecordComment(%s): %v", name, err)
		}
		return detail.Task.ID, o
	}

	taskA, oA := mk(rootA, "a")
	taskB, oB := mk(rootB, "b")

	// A 可确定、B 不可确定：两个 root 同时恢复，各自结论必须独立成立。
	planA, err := svc.RecoverOnStartupForRoot(ctx, rootA.ID, fakeSessionProbe{found: true, resumable: true})
	if err != nil {
		t.Fatalf("recover A: %v", err)
	}
	planB, err := svc.RecoverOnStartupForRoot(ctx, rootB.ID, fakeSessionProbe{found: true, resumable: false})
	if err != nil {
		t.Fatalf("recover B: %v", err)
	}
	if planA.ResumeSession != "sess-a" || planA.NeedsUser {
		t.Fatalf("root A plan=%+v, want resume sess-a & no needs-user（不得被 B 的探针污染）", planA)
	}
	if !planB.NeedsUser || planB.ResumeSession != "" {
		t.Fatalf("root B plan=%+v, want needs-user & no resume", planB)
	}
	// 各自落库：A 仍 recorded，B 已 unknown；A 任务仍 pending，B 任务 waiting_user。
	reqA, _ := oA.GetRequest(ctx, "cr-a")
	if reqA.Status != OutboxRecorded {
		t.Fatalf("A request status=%s, want recorded", reqA.Status)
	}
	reqB, _ := oB.GetRequest(ctx, "cr-b")
	if reqB.Status != OutboxUnknown {
		t.Fatalf("B request status=%s, want unknown", reqB.Status)
	}
	storeA, _ := svc.taskStore(rootA.ID)
	storeB, _ := svc.taskStore(rootB.ID)
	tA, _ := storeA.GetTask(ctx, taskA)
	if tA.Status != StatusPending {
		t.Fatalf("A task status=%s, want pending", tA.Status)
	}
	tB, _ := storeB.GetTask(ctx, taskB)
	if tB.Status != StatusWaitingUser {
		t.Fatalf("B task status=%s, want waiting_user", tB.Status)
	}
}
