package kanban

import (
	"context"
	"testing"

	"mindfs/server/internal/fs"
)

// The #86 settlement only covers replies bound to a workflow run (run_id non-empty).
// A direct turn (Router `message_agent` / 纯对话) leaves run_id empty, and its
// completion is written by the agent goroutine that the restart killed. Nothing
// else ever closes it: the activity spins as `running` forever and the task row
// stays `running`, so it never reaches 待审核 nor the archive sweeper (task #95
// 现场：`act_1f1792a2c1fa2b8f` 从 04:21 转到次日).
func TestStartupRecoverySettlesOrphanDirectReply(t *testing.T) {
	ctx := context.Background()
	store, svc, root := newDirectReplyRecoveryFixture(t, "task-direct", "sess-direct", StatusRunning)
	acts := NewActivityStore(store.db)
	if err := acts.Insert(ctx, &TaskActivity{
		ID: "act-direct", TaskID: "task-direct", RootID: root.ID, Type: ActivityTypeAgentReply,
		Role: RoleAgent, Status: ActivityStatusRunning, Body: "步骤 1/2: 改代码",
	}); err != nil {
		t.Fatalf("Insert activity: %v", err)
	}

	if _, err := svc.RecoverOnStartupForRoot(ctx, root.ID, nil); err != nil {
		t.Fatalf("RecoverOnStartupForRoot: %v", err)
	}

	activity, err := acts.Get(ctx, "act-direct")
	if err != nil {
		t.Fatalf("Get activity: %v", err)
	}
	if activity.Status != ActivityStatusCancelled {
		t.Fatalf("直连回复状态 = %q, want cancelled（否则一直转圈）", activity.Status)
	}
	if activity.Summary == "" {
		t.Fatal("结算必须留一句可读说明")
	}
	if activity.Body != "" {
		t.Fatalf("进度正文要清空，got %q", activity.Body)
	}
	task, err := store.GetTask(ctx, "task-direct")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != StatusWaitingUser {
		t.Fatalf("任务状态 = %q, want waiting_user（否则卡片永远停在执行中、也不会被自动归档）", task.Status)
	}
}

// Route 模式的占位回复在进程死掉时停在 pending：用户点发送后看到的「正在想」
// 再也不会变成结果。它同样是 run_id 为空、没人推进的直连回复。
func TestStartupRecoverySettlesOrphanPendingDirectReply(t *testing.T) {
	ctx := context.Background()
	store, svc, root := newDirectReplyRecoveryFixture(t, "task-pending", "sess-pending", StatusRunning)
	acts := NewActivityStore(store.db)
	if err := acts.Insert(ctx, &TaskActivity{
		ID: "act-pending", TaskID: "task-pending", RootID: root.ID, Type: ActivityTypeAgentReply,
		Role: RoleAgent, Status: ActivityStatusPending,
	}); err != nil {
		t.Fatalf("Insert activity: %v", err)
	}

	if _, err := svc.RecoverOnStartupForRoot(ctx, root.ID, nil); err != nil {
		t.Fatalf("RecoverOnStartupForRoot: %v", err)
	}

	activity, err := acts.Get(ctx, "act-pending")
	if err != nil {
		t.Fatalf("Get activity: %v", err)
	}
	if activity.Status != ActivityStatusCancelled {
		t.Fatalf("pending 占位状态 = %q, want cancelled", activity.Status)
	}
	if activity.Summary == "" {
		t.Fatal("结算必须留一句可读说明")
	}
}

// 会话此刻真的在回复时绝不能碰：装配顺序将来若变了（例如把对账挪到后台），
// 结算一条活着的回复会把正在跑的回合从时间线上抹掉。
func TestStartupRecoveryKeepsLiveDirectReply(t *testing.T) {
	ctx := context.Background()
	store, svc, root := newDirectReplyRecoveryFixture(t, "task-live", "sess-live", StatusRunning)
	acts := NewActivityStore(store.db)
	if err := acts.Insert(ctx, &TaskActivity{
		ID: "act-live", TaskID: "task-live", RootID: root.ID, Type: ActivityTypeAgentReply,
		Role: RoleAgent, Status: ActivityStatusRunning, Body: "步骤 1/3: 读取",
	}); err != nil {
		t.Fatalf("Insert activity: %v", err)
	}
	svc.SetSessionRunningProbe(func(sessionKey string) bool { return sessionKey == "sess-live" })

	if _, err := svc.RecoverOnStartupForRoot(ctx, root.ID, nil); err != nil {
		t.Fatalf("RecoverOnStartupForRoot: %v", err)
	}

	activity, err := acts.Get(ctx, "act-live")
	if err != nil {
		t.Fatalf("Get activity: %v", err)
	}
	if activity.Status != ActivityStatusRunning {
		t.Fatalf("活着的回复被结算了：status = %q", activity.Status)
	}
	if activity.Body == "" {
		t.Fatal("活着的回合正文不能被清空")
	}
	task, err := store.GetTask(ctx, "task-live")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != StatusRunning {
		t.Fatalf("任务状态 = %q, want running（这一轮还在跑）", task.Status)
	}
}

// 绑定到 workflow run 的回复由 Run 侧结算（settleWorkflowReplyUnknown），
// 直连这一路必须让开，否则两条路径会各自写一次状态/说明。
func TestStartupRecoveryLeavesWorkflowBoundReplyAlone(t *testing.T) {
	ctx := context.Background()
	store, svc, root := newDirectReplyRecoveryFixture(t, "task-bound", "sess-bound", StatusRunning)
	acts := NewActivityStore(store.db)
	if err := acts.Insert(ctx, &TaskActivity{
		ID: "act-bound", TaskID: "task-bound", RootID: root.ID, Type: ActivityTypeAgentReply,
		Role: RoleAgent, Status: ActivityStatusRunning, RunID: "run-bound",
	}); err != nil {
		t.Fatalf("Insert activity: %v", err)
	}

	if _, err := svc.RecoverOnStartupForRoot(ctx, root.ID, nil); err != nil {
		t.Fatalf("RecoverOnStartupForRoot: %v", err)
	}

	activity, err := acts.Get(ctx, "act-bound")
	if err != nil {
		t.Fatalf("Get activity: %v", err)
	}
	if activity.Status != ActivityStatusRunning {
		t.Fatalf("Run 绑定的回复被直连路径改写了：status = %q", activity.Status)
	}
}

// 对账在每个进程启动时都会跑，重复结算必须是无操作：不能把已经写好的说明
// 再覆盖一遍，也不能把用户已经处理过的任务再拉回 waiting_user。
func TestStartupRecoveryDirectReplySettlementIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store, svc, root := newDirectReplyRecoveryFixture(t, "task-idem", "sess-idem", StatusRunning)
	acts := NewActivityStore(store.db)
	if err := acts.Insert(ctx, &TaskActivity{
		ID: "act-idem", TaskID: "task-idem", RootID: root.ID, Type: ActivityTypeAgentReply,
		Role: RoleAgent, Status: ActivityStatusRunning,
	}); err != nil {
		t.Fatalf("Insert activity: %v", err)
	}
	if _, err := svc.RecoverOnStartupForRoot(ctx, root.ID, nil); err != nil {
		t.Fatalf("first recover: %v", err)
	}
	first, err := acts.Get(ctx, "act-idem")
	if err != nil {
		t.Fatalf("Get activity: %v", err)
	}
	// 用户看过后把任务拉回 pending 继续聊；下一次启动不能把它再改成 waiting_user。
	if err := store.UpdateTaskStatus(ctx, "task-idem", StatusPending, false); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}
	// 再挂一条新的陈旧回复：它会被结算，但任务状态是操作员的（pending），
	// 结算不许把任务拽回 waiting_user。
	if err := acts.Insert(ctx, &TaskActivity{
		ID: "act-idem-2", TaskID: "task-idem", RootID: root.ID, Type: ActivityTypeAgentReply,
		Role: RoleAgent, Status: ActivityStatusRunning,
	}); err != nil {
		t.Fatalf("Insert activity: %v", err)
	}

	if _, err := svc.RecoverOnStartupForRoot(ctx, root.ID, nil); err != nil {
		t.Fatalf("second recover: %v", err)
	}
	second, err := acts.Get(ctx, "act-idem")
	if err != nil {
		t.Fatalf("Get activity: %v", err)
	}
	if second.Status != ActivityStatusCancelled || second.Summary != first.Summary {
		t.Fatalf("重复对账改写了已结算的行：status=%q summary=%q", second.Status, second.Summary)
	}
	if second.UpdatedAt != first.UpdatedAt {
		t.Fatalf("重复对账写了 updated_at：%q -> %q", first.UpdatedAt, second.UpdatedAt)
	}
	secondNew, err := acts.Get(ctx, "act-idem-2")
	if err != nil {
		t.Fatalf("Get activity: %v", err)
	}
	if secondNew.Status != ActivityStatusCancelled {
		t.Fatalf("第二次对账漏掉了新的陈旧回复：status = %q", secondNew.Status)
	}
	task, err := store.GetTask(ctx, "task-idem")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != StatusPending {
		t.Fatalf("任务状态 = %q, want pending（对账只结算自己那一行，不覆盖操作员状态）", task.Status)
	}
}

// 任务上还有别的活着的回复（例如 workflow 侧刚被唤醒那一轮）时，结算一条陈旧
// 直连回复不能顺手把任务从 running 拉回 waiting_user。
func TestStartupRecoveryKeepsTaskRunningWhileAnotherReplyIsLive(t *testing.T) {
	ctx := context.Background()
	store, svc, root := newDirectReplyRecoveryFixture(t, "task-mixed", "sess-mixed", StatusRunning)
	acts := NewActivityStore(store.db)
	if err := acts.Insert(ctx, &TaskActivity{
		ID: "act-stale", TaskID: "task-mixed", RootID: root.ID, Type: ActivityTypeAgentReply,
		Role: RoleAgent, Status: ActivityStatusRunning,
	}); err != nil {
		t.Fatalf("Insert stale activity: %v", err)
	}
	if err := acts.Insert(ctx, &TaskActivity{
		ID: "act-live-run", TaskID: "task-mixed", RootID: root.ID, Type: ActivityTypeAgentReply,
		Role: RoleAgent, Status: ActivityStatusRunning, RunID: "run-mixed",
	}); err != nil {
		t.Fatalf("Insert live activity: %v", err)
	}

	if _, err := svc.RecoverOnStartupForRoot(ctx, root.ID, nil); err != nil {
		t.Fatalf("RecoverOnStartupForRoot: %v", err)
	}

	stale, err := acts.Get(ctx, "act-stale")
	if err != nil {
		t.Fatalf("Get stale activity: %v", err)
	}
	if stale.Status != ActivityStatusCancelled {
		t.Fatalf("陈旧直连回复状态 = %q, want cancelled", stale.Status)
	}
	task, err := store.GetTask(ctx, "task-mixed")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != StatusRunning {
		t.Fatalf("任务状态 = %q, want running（另一条回复还挂着）", task.Status)
	}
}

// newDirectReplyRecoveryFixture 造一个没有 workflow run 的任务（直连任务的原样），
// 并让 Service 直接复用同一个 TaskStore 连接。
func newDirectReplyRecoveryFixture(t *testing.T, taskID, sessionKey, status string) (*TaskStore, *Service, fs.RootInfo) {
	t.Helper()
	ctx := context.Background()
	root := fs.NewRootInfo("direct-recover-root", "direct-recover-root", t.TempDir())
	store, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateTask(ctx, Task{
		ID: taskID, RootID: root.ID, Status: status, MainSessionKey: sessionKey,
	}, TaskEvent{ID: taskID + "-event", TaskID: taskID, Type: "created"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	svc := NewService(testRoots{root: root})
	svc.stores = map[string]*TaskStore{root.ID: store}
	return store, svc, root
}
