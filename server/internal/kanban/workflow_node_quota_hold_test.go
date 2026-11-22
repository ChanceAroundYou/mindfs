package kanban

import (
	"context"
	"strings"
	"testing"
)

// quotaFailedNodeRun 造一行配额类失败的 NodeRun：这是 retry_node 与自动补做
// 真正会撞上的形状（上游 429 / usage limit）。
func quotaFailedNodeRun(t *testing.T, store *TaskStore, run *WorkflowRun, id string) NodeRun {
	t.Helper()
	return quotaFailedNodeRunOn(t, store, run, id, "agent-1", 1)
}

// 唯一键是 (run_id,node_id,loop_path,iteration,attempt)，换 id 不换 node/attempt
// 会撞唯一约束：同一节点换个波次、或换个节点，都要显式说清。
func quotaFailedNodeRunOn(t *testing.T, store *TaskStore, run *WorkflowRun, id, nodeID string, attempt int) NodeRun {
	t.Helper()
	nr := NodeRun{
		ID: id, RunID: run.ID, NodeID: nodeID, Attempt: attempt,
		Status: NodeRunFail,
		Output: map[string]any{"error": "usage limit reached, resets at 2026-11-22T10:00:00Z"},
	}
	must(t, store.UpsertWorkflowNodeRun(context.Background(), nr), "UpsertWorkflowNodeRun")
	return nr
}

func loadTaskStatus(t *testing.T, svc *Service, rootID, taskID string) string {
	t.Helper()
	store, err := svc.taskStore(rootID)
	must(t, err, "taskStore")
	task, err := store.GetTask(context.Background(), taskID)
	must(t, err, "GetTask")
	return task.Status
}

func loadHoldsUsed(t *testing.T, store *TaskStore, runID string) int {
	t.Helper()
	run, err := store.WorkflowRunByID(context.Background(), runID)
	must(t, err, "WorkflowRunByID")
	return run.HoldsUsed
}

func holdNotices(t *testing.T, store *TaskStore, taskID string) []*TaskActivity {
	t.Helper()
	rows, err := NewActivityStore(store.db).List(context.Background(), taskID)
	must(t, err, "List")
	var out []*TaskActivity
	for _, row := range rows {
		if strings.HasPrefix(row.ID, "quota-hold:") {
			out = append(out, row)
		}
	}
	return out
}

func nodeRunCount(t *testing.T, store *TaskStore, runID string) int {
	t.Helper()
	rows, err := store.ListWorkflowNodeRuns(context.Background(), runID)
	must(t, err, "ListWorkflowNodeRuns")
	return len(rows)
}

// 正常挂起不改变任务状态：waiting_user 是「需要人说话」，而挂起会自己到点继续。
func TestQuotaHoldKeepsTaskRunningAndNotesIt(t *testing.T) {
	svc, store, run := newHoldFixture(t)
	rootID := store.root.ID
	ctx := context.Background()
	nr := quotaFailedNodeRun(t, store, run, "nr-quota")

	retried, err := svc.RetryFailedWorkflowNode(ctx, rootID, run.TaskID, nr.ID, "")
	must(t, err, "RetryFailedWorkflowNode")
	if retried.Status != NodeRunQueued || retried.NotBefore == "" {
		t.Fatalf("retried node = %s not_before=%q, want queued with a hold", retried.Status, retried.NotBefore)
	}
	if got := loadTaskStatus(t, svc, rootID, run.TaskID); got != StatusRunning {
		t.Fatalf("task status = %q, want %q (a quota hold is not a request for a human)", got, StatusRunning)
	}
	if got := loadHoldsUsed(t, store, run.ID); got != 1 {
		t.Fatalf("holds_used = %d, want 1", got)
	}
	notices := holdNotices(t, store, run.TaskID)
	if len(notices) != 1 {
		t.Fatalf("hold notices = %d, want 1", len(notices))
	}
	for _, want := range []string{"agent-1", "用量上限", "1/3", "UTC"} {
		if !strings.Contains(notices[0].Body, want) {
			t.Fatalf("hold notice %q missing %q", notices[0].Body, want)
		}
	}
	if notices[0].Author != "system" || notices[0].Status != ActivityStatusCompleted {
		t.Fatalf("hold notice = %s/%s, want a completed system row", notices[0].Author, notices[0].Status)
	}
}

// 同一次挂起重放（coordinate 重投、进程重启对账）只能有一行，否则对话流会被刷屏。
func TestQuotaHoldNoticeIsIdempotent(t *testing.T) {
	svc, store, run := newHoldFixture(t)
	rootID := store.root.ID
	ctx := context.Background()
	nr := quotaFailedNodeRun(t, store, run, "nr-quota")

	_, err := svc.RetryFailedWorkflowNode(ctx, rootID, run.TaskID, nr.ID, "")
	must(t, err, "RetryFailedWorkflowNode")
	task, err := store.GetTask(ctx, run.TaskID)
	must(t, err, "GetTask")
	// 重放的是同一次挂起：提示行的身份是原节点行（不是新建的 attempt 行），
	// 所以重放会命中同一行 —— 按 retried.ID 另起一行才是 bug。
	svc.noteQuotaHold(ctx, store, task, quotaHoldNoticeID(nr.ID), "重放时重算的提示", "")
	if got := len(holdNotices(t, store, run.TaskID)); got != 1 {
		t.Fatalf("hold notices = %d after replay, want 1 (a replayed hold is the same hold)", got)
	}
	if body := holdNotices(t, store, run.TaskID)[0].Body; !strings.Contains(body, "1/3") {
		t.Fatalf("replay overwrote the first notice body: %q", body)
	}
}

// 额度用完：不再建 attempt，交给人。
func TestHoldsExhaustedParksTaskWithoutNewAttempt(t *testing.T) {
	svc, store, run := newHoldFixture(t)
	rootID := store.root.ID
	ctx := context.Background()
	for i := 0; i < supervisionMaxHoldsPerRun-1; i++ {
		if ok, _, err := store.IncrementWorkflowRunHolds(ctx, run.ID, supervisionMaxHoldsPerRun); err != nil || !ok {
			t.Fatalf("seed quota hold #%d = (ok=%v, err=%v)", i+1, ok, err)
		}
	}
	nr := quotaFailedNodeRun(t, store, run, "nr-quota-last")

	if _, err := svc.RetryFailedWorkflowNode(ctx, rootID, run.TaskID, nr.ID, ""); err != nil {
		t.Fatalf("the last hold inside the cap must be granted: %v", err)
	}
	// 第二次撞限额 = 第 4 次挂起，额度已用完。换个节点与波次（唯一键含
	// node_id/attempt），也符合现场：同一轮里不同节点各自撞上上游限额。
	again := quotaFailedNodeRunOn(t, store, run, "nr-quota-over", "agent-2", 1)
	_, err := svc.RetryFailedWorkflowNode(ctx, rootID, run.TaskID, again.ID, "")
	if err == nil {
		t.Fatal("retry past the quota hold cap must fail")
	}
	if !IsQuotaHoldsExhausted(err) {
		t.Fatalf("err = %v, want the typed quota-holds-exhausted refusal", err)
	}
	if got := nodeRunCount(t, store, run.ID); got != 3 {
		t.Fatalf("node runs = %d, want 3 (two failed originals + the one granted retry, none past the cap)", got)
	}
	if got := loadTaskStatus(t, svc, rootID, run.TaskID); got != StatusWaitingUser {
		t.Fatalf("task status = %q, want %q once the hold cap is spent", got, StatusWaitingUser)
	}
	notices := holdNotices(t, store, run.TaskID)
	if len(notices) != 2 {
		t.Fatalf("hold notices = %d, want 2 (the granted hold plus the exhaustion)", len(notices))
	}
	exhausted := notices[1].Body
	for _, want := range []string{"额度", "3/3", "需要你介入"} {
		if !strings.Contains(exhausted, want) {
			t.Fatalf("exhaustion notice %q missing %q", exhausted, want)
		}
	}
	if got := loadHoldsUsed(t, store, run.ID); got != supervisionMaxHoldsPerRun {
		t.Fatalf("holds_used = %d, want %d (a refused hold must not consume quota)", got, supervisionMaxHoldsPerRun)
	}
}

// 挂起额度落库：重启后仍然算数，否则额度每次重启清零。
func TestHoldsUsedSurvivesReload(t *testing.T) {
	_, store, run := newHoldFixture(t)
	ctx := context.Background()
	for i := 0; i < supervisionMaxHoldsPerRun; i++ {
		if ok, used, err := store.IncrementWorkflowRunHolds(ctx, run.ID, supervisionMaxHoldsPerRun); err != nil || !ok || used != i+1 {
			t.Fatalf("IncrementWorkflowRunHolds #%d = (ok=%v, used=%d, err=%v)", i+1, ok, used, err)
		}
	}
	if ok, used, err := store.IncrementWorkflowRunHolds(ctx, run.ID, supervisionMaxHoldsPerRun); err != nil || ok || used != supervisionMaxHoldsPerRun {
		t.Fatalf("claim past the cap = (ok=%v, used=%d, err=%v), want refused", ok, used, err)
	}
	// 关掉共享的 store 再真开一份：只有列真的落库，重开才读得到。
	reopened, err := NewTaskStore(store.root)
	must(t, err, "NewTaskStore")
	t.Cleanup(func() { _ = reopened.Close() })
	if got := loadHoldsUsed(t, reopened, run.ID); got != supervisionMaxHoldsPerRun {
		t.Fatalf("holds_used after reload = %d, want %d", got, supervisionMaxHoldsPerRun)
	}
	if ok, used, err := reopened.IncrementWorkflowRunHolds(ctx, run.ID, supervisionMaxHoldsPerRun); err != nil || ok || used != supervisionMaxHoldsPerRun {
		t.Fatalf("claim past the cap after reload = (ok=%v, used=%d, err=%v), want refused", ok, used, err)
	}
}

// 额度是**每轮 Run** 的，不是每个节点或每条任务：一个 Run 用满不许把下一轮也卡住。
func TestHoldsQuotaIsPerRun(t *testing.T) {
	svc, store, run := newHoldFixture(t)
	rootID := store.root.ID
	ctx := context.Background()
	for i := 0; i < supervisionMaxHoldsPerRun; i++ {
		if ok, _, err := store.IncrementWorkflowRunHolds(ctx, run.ID, supervisionMaxHoldsPerRun); err != nil || !ok {
			t.Fatalf("seed quota hold #%d = (ok=%v, err=%v)", i+1, ok, err)
		}
	}
	// 同一任务上的第二轮 Run 会被「绑定中的 Run 必须已终态」拒绝，所以换一条任务：
	// 额度是每轮 Run 的，跨任务串用与跨轮串用都不算数。
	if _, err := store.CreateTask(ctx, Task{ID: "task-hold-next", RootID: rootID, Status: StatusRunning},
		TaskEvent{ID: "ev-hold-next", TaskID: "task-hold-next", Type: "created"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	fresh := &WorkflowRun{ID: "run-hold-next", TaskID: "task-hold-next", Version: "wf:v1", Revision: 1, Graph: run.Graph}
	must(t, store.CreateWorkflowRunAndSetEngine(ctx, fresh), "CreateWorkflowRunAndSetEngine")
	nr := quotaFailedNodeRun(t, store, fresh, "nr-quota-next")
	if _, err := svc.RetryFailedWorkflowNode(ctx, rootID, fresh.TaskID, nr.ID, ""); err != nil {
		t.Fatalf("a fresh run must get its own quota: %v", err)
	}
	if got := loadHoldsUsed(t, store, fresh.ID); got != 1 {
		t.Fatalf("new run holds_used = %d, want 1", got)
	}
	if got := loadHoldsUsed(t, store, run.ID); got != supervisionMaxHoldsPerRun {
		t.Fatalf("the exhausted run holds_used = %d, want %d (a fresh run must not reset it)", got, supervisionMaxHoldsPerRun)
	}
}
