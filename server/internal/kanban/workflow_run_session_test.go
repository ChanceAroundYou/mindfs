package kanban

import (
	"context"
	"testing"
	"time"

	"mindfs/server/internal/fs"
)

// 2026-11-20 用户决策：一轮 Run 一条会话。Run 上的 session_key 必须能写、
// 能读回，而且先到先得 —— 并行分支同时起步时只有一条会话能成为 Run 的会话。
func TestWorkflowRunSessionKeyFirstWriterWins(t *testing.T) {
	ctx := context.Background()
	root := fs.NewRootInfo("run-session-root", "run-session-root", t.TempDir())
	store, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	defer store.Close()
	if _, err := store.CreateTask(ctx, Task{ID: "run-session-task", RootID: root.ID, Status: StatusPending},
		TaskEvent{ID: "run-session-event", TaskID: "run-session-task", Type: "created"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	run := &WorkflowRun{ID: "run-session-1", TaskID: "run-session-task", Revision: 1,
		Graph: WorkflowDefinition{Nodes: []WorkflowNode{{ID: "n", Kind: NodeKindAgent}}}}
	if err := store.CreateWorkflowRunAndSetEngine(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if key, err := store.WorkflowRunSessionKey(ctx, run.ID); err != nil || key != "" {
		t.Fatalf("fresh run session = %q err=%v, want empty", key, err)
	}

	winner, err := store.SetWorkflowRunSessionKey(ctx, run.ID, "sess-a")
	if err != nil || winner != "sess-a" {
		t.Fatalf("first bind = %q err=%v, want sess-a", winner, err)
	}
	// 落败的写入不能覆盖已经生效的会话，返回的必须是库里那一条。
	loser, err := store.SetWorkflowRunSessionKey(ctx, run.ID, "sess-b")
	if err != nil || loser != "sess-a" {
		t.Fatalf("second bind = %q err=%v, want sess-a (first writer wins)", loser, err)
	}
	got, err := store.WorkflowRunByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("WorkflowRunByID: %v", err)
	}
	if got.SessionKey != "sess-a" {
		t.Fatalf("run session_key = %q, want sess-a", got.SessionKey)
	}
	// 绑定会话不是运行状态：不能推进 revision，否则正在跑的 driver 会撞 CAS。
	if got.Revision != 1 {
		t.Fatalf("revision = %d, want 1 (session binding must not bump CAS)", got.Revision)
	}
}

// Router 的 session_mode=continue 依赖「任务最近实际在用的会话」：活动优先，
// 其次上一轮 Run 的共享会话，最后才是任务主会话。
func TestLatestTaskSessionKeyOrder(t *testing.T) {
	ctx := context.Background()
	root := fs.NewRootInfo("latest-session-root", "latest-session-root", t.TempDir())
	store, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	defer store.Close()
	if _, err := store.CreateTask(ctx, Task{ID: "latest-task", RootID: root.ID, Status: StatusPending, MainSessionKey: "sess-main"},
		TaskEvent{ID: "latest-event", TaskID: "latest-task", Type: "created"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if key, err := store.LatestTaskSessionKey(ctx, "latest-task"); err != nil || key != "sess-main" {
		t.Fatalf("main fallback = %q err=%v, want sess-main", key, err)
	}

	run := &WorkflowRun{ID: "latest-run", TaskID: "latest-task", Revision: 1, SessionKey: "sess-run",
		Graph: WorkflowDefinition{Nodes: []WorkflowNode{{ID: "n", Kind: NodeKindAgent}}}}
	if err := store.CreateWorkflowRunAndSetEngine(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if key, err := store.LatestTaskSessionKey(ctx, "latest-task"); err != nil || key != "sess-run" {
		t.Fatalf("run fallback = %q err=%v, want sess-run", key, err)
	}

	activities := NewActivityStore(store.db)
	older := &TaskActivity{ID: "latest-act-1", TaskID: "latest-task", RootID: root.ID, Type: ActivityTypeUserMessage,
		Role: RoleUser, Body: "旧留言", SessionKey: "sess-older", CreatedAt: time.Now().UTC().Add(-time.Hour)}
	if err := activities.Insert(ctx, older); err != nil {
		t.Fatalf("insert older activity: %v", err)
	}
	if key, err := store.LatestTaskSessionKey(ctx, "latest-task"); err != nil || key != "sess-older" {
		t.Fatalf("activity fallback = %q err=%v, want sess-older", key, err)
	}
	newer := &TaskActivity{ID: "latest-act-2", TaskID: "latest-task", RootID: root.ID, Type: ActivityTypeUserMessage,
		Role: RoleUser, Body: "新留言", SessionKey: "sess-newer", CreatedAt: time.Now().UTC()}
	if err := activities.Insert(ctx, newer); err != nil {
		t.Fatalf("insert newer activity: %v", err)
	}
	if key, err := store.LatestTaskSessionKey(ctx, "latest-task"); err != nil || key != "sess-newer" {
		t.Fatalf("latest activity = %q err=%v, want sess-newer", key, err)
	}
}

// 任务归档的级联必须能看见 Run 的共享会话：它只记在 workflow_runs.session_key
// 上，节点行反查不到（2026-11-20 起的默认策略）。漏收会让归档后它继续留在侧栏。
func TestTaskRelatedSessionKeysIncludesRunSession(t *testing.T) {
	ctx := context.Background()
	root := fs.NewRootInfo("archive-run-session-root", "archive-run-session-root", t.TempDir())
	store, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	defer store.Close()
	if _, err := store.CreateTask(ctx, Task{ID: "archive-run-task", RootID: root.ID, Status: StatusSuccess, MainSessionKey: "sess-main"},
		TaskEvent{ID: "archive-run-event", TaskID: "archive-run-task", Type: "created"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	run := &WorkflowRun{ID: "archive-run", TaskID: "archive-run-task", Revision: 1, SessionKey: "sess-run",
		Graph: WorkflowDefinition{Nodes: []WorkflowNode{{ID: "n", Kind: NodeKindAgent}}}}
	if err := store.CreateWorkflowRunAndSetEngine(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	keys, err := store.TaskRelatedSessionKeys(ctx, "archive-run-task")
	if err != nil {
		t.Fatalf("TaskRelatedSessionKeys: %v", err)
	}
	found := map[string]bool{}
	for _, key := range keys {
		found[key] = true
	}
	if !found["sess-main"] || !found["sess-run"] {
		t.Fatalf("related keys = %v, want sess-main and sess-run", keys)
	}
}

// prepare 阶段的 run 策略必须把「第一个节点建的会话」写回 Run，第二个节点
// 拿到的是同一个 key；显式 always_new 的节点则不碰 Run 的会话。
type recordingSessionRunner struct {
	seen []WorkflowNodeExecution
}

func (r *recordingSessionRunner) EnsureWorkflowNodeSession(_ context.Context, exec WorkflowNodeExecution) (string, error) {
	r.seen = append(r.seen, exec)
	if exec.RunSessionKey != "" {
		return exec.RunSessionKey, nil
	}
	return "sess-" + exec.Node.ID, nil
}

func (r *recordingSessionRunner) RunWorkflowNode(_ context.Context, exec WorkflowNodeExecution) (WorkflowNodeResult, error) {
	return WorkflowNodeResult{Outcome: WorkflowNodeDone, SessionKey: exec.NodeRun.SessionKey}, nil
}

func TestPrepareWorkflowNodeSharesOneRunSession(t *testing.T) {
	ctx := context.Background()
	root := fs.NewRootInfo("share-session-root", "share-session-root", t.TempDir())
	store, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	defer store.Close()
	if _, err := store.CreateTask(ctx, Task{ID: "share-task", RootID: root.ID, Status: StatusPending, Name: "共享会话"},
		TaskEvent{ID: "share-event", TaskID: "share-task", Type: "created"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	graph := WorkflowDefinition{Nodes: []WorkflowNode{
		{ID: "build", Kind: NodeKindAgent, Params: map[string]any{"prompt": "实现"}},
		{ID: "review", Kind: NodeKindAgent, Params: map[string]any{"prompt": "审查"}},
		{ID: "probe", Kind: NodeKindAgent, Params: map[string]any{"prompt": "探针", "session_reuse_policy": SessionReuseAlwaysNew}},
	}}
	run := &WorkflowRun{ID: "share-run", TaskID: "share-task", Revision: 1, Graph: graph}
	if err := store.CreateWorkflowRunAndSetEngine(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	runner := &recordingSessionRunner{}
	svc := NewService(nil)
	svc.SetWorkflowNodeRunner(runner)

	first, err := svc.PrepareWorkflowNode(ctx, store, run, graph.Nodes[0], NodeRun{ID: "nr-1", RunID: run.ID, NodeID: "build", Attempt: 1})
	if err != nil {
		t.Fatalf("prepare build: %v", err)
	}
	if first.NodeRun.SessionKey != "sess-build" {
		t.Fatalf("build session = %q, want sess-build", first.NodeRun.SessionKey)
	}
	second, err := svc.PrepareWorkflowNode(ctx, store, run, graph.Nodes[1], NodeRun{ID: "nr-2", RunID: run.ID, NodeID: "review", Attempt: 1})
	if err != nil {
		t.Fatalf("prepare review: %v", err)
	}
	if second.NodeRun.SessionKey != "sess-build" {
		t.Fatalf("review session = %q, want the run session sess-build", second.NodeRun.SessionKey)
	}
	if len(runner.seen) != 2 || runner.seen[1].RunSessionKey != "sess-build" {
		t.Fatalf("runner did not receive the bound run session: %#v", runner.seen)
	}
	if key, err := store.WorkflowRunSessionKey(ctx, run.ID); err != nil || key != "sess-build" {
		t.Fatalf("run session = %q err=%v, want sess-build", key, err)
	}

	// always_new：显式另起一段对话，不能把 Run 的共享会话改掉。
	third, err := svc.PrepareWorkflowNode(ctx, store, run, graph.Nodes[2], NodeRun{ID: "nr-3", RunID: run.ID, NodeID: "probe", Attempt: 1})
	if err != nil {
		t.Fatalf("prepare probe: %v", err)
	}
	if third.NodeRun.SessionKey != "sess-probe" {
		t.Fatalf("probe session = %q, want sess-probe", third.NodeRun.SessionKey)
	}
	if key, _ := store.WorkflowRunSessionKey(ctx, run.ID); key != "sess-build" {
		t.Fatalf("always_new overwrote run session: %q", key)
	}
}
