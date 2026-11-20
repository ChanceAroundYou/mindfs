package kanban

import (
	"context"
	"testing"
	"time"

	"mindfs/server/internal/fs"
)

// 配额挂起（Plan 2026-11-20 Task 1）：撞到用量上限的节点不在几秒内烧光重试预算，
// 而是挂到重置时刻由扫描唤醒。这里只钉三件事：重置时间解析有界、挂起时不被 claim、
// 到点被释放且终态任务不被复活。

func TestQuotaHoldUntilParsesResetAndClamps(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	hold := 15 * time.Minute
	cases := []struct {
		name string
		text string
		want time.Time
	}{
		{"iso reset", "usage limit reached, resets at 2026-11-20T18:30:00Z", time.Date(2026, 11, 20, 18, 30, 0, 0, time.UTC)},
		{"zh reset", "用量已达上限，重置于 2026-11-20T18:30:00Z", time.Date(2026, 11, 20, 18, 30, 0, 0, time.UTC)},
		{"beyond a week is misread", "resets at 2030-01-01T00:00:00Z", now.Add(hold)},
		{"in the past falls back", "resets at 2026-11-20T09:00:00Z", now.Add(hold)},
		{"no time at all", "rate limit exceeded", now.Add(hold)},
	}
	for _, tc := range cases {
		if got := quotaHoldUntil(tc.text, now, hold); !got.Equal(tc.want) {
			t.Fatalf("%s: quotaHoldUntil = %s, want %s", tc.name, got, tc.want)
		}
	}
}

func newHoldFixture(t *testing.T) (*Service, *TaskStore, *WorkflowRun) {
	t.Helper()
	root := fs.NewRootInfo("root", "root", t.TempDir())
	svc := NewService(testRoots{root: root})
	store, err := svc.taskStore(root.ID)
	must(t, err, "taskStore")
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if _, err := store.CreateTask(ctx, Task{ID: "task-hold", RootID: root.ID, Status: StatusRunning},
		TaskEvent{ID: "ev-hold", TaskID: "task-hold", Type: "created"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	run := &WorkflowRun{
		ID: "run-hold", TaskID: "task-hold", Version: "wf:v1", Revision: 1,
		Graph: wfDef(wfNode("agent-1", NodeKindAgent)),
	}
	must(t, store.CreateWorkflowRunAndSetEngine(ctx, run), "CreateWorkflowRunAndSetEngine")
	return svc, store, run
}

func TestAdvanceSkipsHeldNode(t *testing.T) {
	svc, store, run := newHoldFixture(t)
	ctx := context.Background()
	nr := NodeRun{
		ID: "nr-held", RunID: run.ID, NodeID: "agent-1", Attempt: 1,
		Status:    NodeRunQueued,
		NotBefore: time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339),
	}
	must(t, store.UpsertWorkflowNodeRun(ctx, nr), "UpsertWorkflowNodeRun")

	node, ok := workflowNodeByID(run.Graph, "agent-1")
	if !ok {
		t.Fatal("node missing from graph")
	}
	advanced, err := svc.advanceWorkflowNode(ctx, store, run, node)
	if err != nil {
		t.Fatalf("advanceWorkflowNode: %v", err)
	}
	if advanced {
		t.Fatal("held node must not advance")
	}
	reloaded, err := store.WorkflowNodeRunByKey(ctx, run.ID, "agent-1", "", 0, 1)
	must(t, err, "WorkflowNodeRunByKey")
	if reloaded.Status != NodeRunQueued {
		t.Fatalf("held node status = %q, want queued", reloaded.Status)
	}
	if reloaded.SessionKey != "" {
		t.Fatalf("held node must not bind a session, got %q", reloaded.SessionKey)
	}
}

func TestReleaseDueNodeHoldsWakesRun(t *testing.T) {
	svc, store, run := newHoldFixture(t)
	ctx := context.Background()
	past := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	future := time.Now().UTC().Add(time.Minute).Format(time.RFC3339)
	must(t, store.UpsertWorkflowNodeRun(ctx, NodeRun{
		ID: "nr-due", RunID: run.ID, NodeID: "agent-1", Attempt: 1, Status: NodeRunQueued, NotBefore: past,
	}), "UpsertWorkflowNodeRun due")

	woken, err := svc.releaseDueNodeHolds(ctx, store, time.Now().UTC())
	must(t, err, "releaseDueNodeHolds")
	if len(woken) != 1 || woken[0] != run.ID {
		t.Fatalf("woken = %v, want [%s]", woken, run.ID)
	}
	reloaded, err := store.WorkflowNodeRunByKey(ctx, run.ID, "agent-1", "", 0, 1)
	must(t, err, "WorkflowNodeRunByKey")
	if reloaded.NotBefore != "" {
		t.Fatalf("released hold must be cleared, got %q", reloaded.NotBefore)
	}

	// 还没到点的一行不动，也不唤醒。
	must(t, store.UpsertWorkflowNodeRun(ctx, NodeRun{
		ID: "nr-future", RunID: run.ID, NodeID: "agent-1", Attempt: 2, Status: NodeRunQueued, NotBefore: future,
	}), "UpsertWorkflowNodeRun future")
	woken, err = svc.releaseDueNodeHolds(ctx, store, time.Now().UTC())
	must(t, err, "releaseDueNodeHolds future")
	if len(woken) != 0 {
		t.Fatalf("future hold must not wake, got %v", woken)
	}

	// 任务已取消：到期也不唤醒，避免把终态任务拉回运行。
	if _, err := store.db.ExecContext(ctx, `UPDATE workflow_node_runs SET not_before=? WHERE id='nr-future'`, past); err != nil {
		t.Fatalf("force due: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE tasks SET status=? WHERE id=?`, StatusCancelled, run.TaskID); err != nil {
		t.Fatalf("cancel task: %v", err)
	}
	woken, err = svc.releaseDueNodeHolds(ctx, store, time.Now().UTC())
	must(t, err, "releaseDueNodeHolds cancelled")
	if len(woken) != 0 {
		t.Fatalf("cancelled task must not be woken, got %v", woken)
	}
}

func TestQuotaFailureIsItsOwnClass(t *testing.T) {
	quota := NodeRun{Status: NodeRunFail, Output: map[string]any{"error": "429 Too Many Requests: rate limit exceeded"}}
	if kind, _ := classifySupervisionFailure(quota); kind != SupervisionFailureQuota {
		t.Fatalf("rate limit classified as %q, want quota", kind)
	}
	retryable := NodeRun{Status: NodeRunFail, Output: map[string]any{"error": "502 Bad Gateway"}}
	if kind, _ := classifySupervisionFailure(retryable); kind != SupervisionFailureRetryable {
		t.Fatalf("502 classified as %q, want retryable", kind)
	}
}

func TestSupervisionRetriesUsedSkipsQuotaAttempts(t *testing.T) {
	rows := []NodeRun{
		{ID: "a1", NodeID: "agent-1", Attempt: 1, Status: NodeRunFail,
			Output: map[string]any{"error": "usage limit reached"}},
		{ID: "a2", NodeID: "agent-1", Attempt: 2, Status: NodeRunQueued, PrevAttemptID: "a1"},
	}
	if used := supervisionRetriesUsed(rows, "agent-1"); used != 0 {
		t.Fatalf("quota-held attempt consumed budget: used=%d, want 0", used)
	}
	rows[0].Output = map[string]any{"error": "502 Bad Gateway"}
	if used := supervisionRetriesUsed(rows, "agent-1"); used != 1 {
		t.Fatalf("real retry not counted: used=%d, want 1", used)
	}
}

func TestSupervisionQuotaHoldNormalized(t *testing.T) {
	if got := (RouterSupervisionConfig{}).Normalized().QuotaHold; got != defaultSupervisionQuotaHold {
		t.Fatalf("default quota hold = %s, want %s", got, defaultSupervisionQuotaHold)
	}
	if got := (RouterSupervisionConfig{QuotaHold: time.Second}).Normalized().QuotaHold; got != supervisionQuotaHoldMin {
		t.Fatalf("short quota hold = %s, want clamped %s", got, supervisionQuotaHoldMin)
	}
	if got := (RouterSupervisionConfig{QuotaHold: 48 * time.Hour}).Normalized().QuotaHold; got != supervisionQuotaHoldMax {
		t.Fatalf("long quota hold = %s, want clamped %s", got, supervisionQuotaHoldMax)
	}
}
