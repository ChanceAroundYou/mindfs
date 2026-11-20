package kanban

import (
	"context"
	"strings"
	"testing"
)

// complete_task 的应用路径（Plan 2026-11-20 Task 5）：证据门通过 → 正式收尾；
// 不通过 → 停在原地，把缺口编号写给用户看。

func completionAction(summary string) RouterAction {
	return RouterAction{Kind: RouterActionComplete, Summary: summary, Reason: "按契约核对"}
}

func completionDecision(f *completionFixture, id string) RouterDecision {
	return RouterDecision{ID: id, TaskID: f.task.ID, RootID: f.root.ID, TriggerKind: RouterTriggerUserMessage}
}

// completionSystemRows 只取这次决策写的系统行，避免把任务里其它活动算进来。
func completionSystemRows(t *testing.T, f *completionFixture, prefix string) []*TaskActivity {
	t.Helper()
	rows, err := f.acts.ListIncludingHidden(context.Background(), f.task.ID)
	must(t, err, "ListIncludingHidden")
	var out []*TaskActivity
	for _, row := range rows {
		if strings.HasPrefix(row.ID, prefix) {
			out = append(out, row)
		}
	}
	return out
}

func countTaskEvents(t *testing.T, f *completionFixture, eventType string) int {
	t.Helper()
	events, err := f.store.ListEvents(context.Background(), f.task.ID)
	must(t, err, "ListEvents")
	count := 0
	for _, event := range events {
		if event.Type == eventType {
			count++
		}
	}
	return count
}

func TestCompleteTaskCommitsOnPass(t *testing.T) {
	ctx := context.Background()
	f := newCompletionFixture(t, &CompletionContract{
		Requirements: []CompletionRequirement{
			{ID: "build", Label: "构建节点完成", Kind: CompletionKindNodeDone, NodeID: "build"},
		},
	})
	f.addAttempt(t, "build", NodeRunSuccess, map[string]any{"outcome": "done"}, "")

	decision := completionDecision(f, "dec-pass")
	if err := f.svc.applyCompleteTask(ctx, f.store, f.task, decision, completionAction("三个文件改完，测试跑过")); err != nil {
		t.Fatalf("applyCompleteTask: %v", err)
	}

	task, err := f.store.GetTask(ctx, f.task.ID)
	must(t, err, "GetTask")
	if task.Status != StatusSuccess {
		t.Fatalf("task status = %q, want success", task.Status)
	}
	if task.ResultSummary != "三个文件改完，测试跑过" {
		t.Fatalf("result summary = %q", task.ResultSummary)
	}

	completion, found, err := f.store.GetTaskCompletion(ctx, f.task.ID)
	must(t, err, "GetTaskCompletion")
	if !found {
		t.Fatal("task_completion row missing after a passing completion")
	}
	if completion.Status != CompletionStatusCompleted {
		t.Fatalf("completion status = %q, want completed", completion.Status)
	}
	if completion.DecisionID != "dec-pass" || completion.RunID != f.run.ID {
		t.Fatalf("completion = %+v, want decision dec-pass on run %s", completion, f.run.ID)
	}
	if !strings.Contains(completion.ReportJSON, `"pass":true`) && !strings.Contains(completion.ReportJSON, `"Passed":true`) {
		t.Fatalf("report json does not record the passing verdict: %s", completion.ReportJSON)
	}

	rows := completionSystemRows(t, f, "completion-evidence:dec-pass")
	if len(rows) != 1 {
		t.Fatalf("evidence rows = %d, want 1", len(rows))
	}
	if !strings.Contains(rows[0].Body, "三个文件改完") {
		t.Fatalf("evidence row body = %q, want the summary", rows[0].Body)
	}
	if countTaskEvents(t, f, "completed") != 1 {
		t.Fatal("passing completion must finish the task exactly once")
	}
}

func TestCompleteTaskBlocksWithNumberedGaps(t *testing.T) {
	ctx := context.Background()
	f := newCompletionFixture(t, &CompletionContract{
		Requirements: []CompletionRequirement{
			{ID: "build", Label: "构建节点完成", Kind: CompletionKindNodeDone, NodeID: "build", Keys: []string{"files"}},
			{ID: "dist", Label: "产物存在", Kind: CompletionKindFileExists, Path: "dist/app.js"},
		},
	})
	f.addAttempt(t, "build", NodeRunSuccess, map[string]any{"outcome": "done"}, "")
	must(t, f.store.UpdateTaskStatus(ctx, f.task.ID, StatusWaitingUser, false), "UpdateTaskStatus")

	decision := completionDecision(f, "dec-block")
	if err := f.svc.applyCompleteTask(ctx, f.store, f.task, decision, completionAction("做完了")); err != nil {
		t.Fatalf("applyCompleteTask: %v", err)
	}

	task, err := f.store.GetTask(ctx, f.task.ID)
	must(t, err, "GetTask")
	if task.Status != StatusWaitingUser {
		t.Fatalf("blocked completion moved the task to %q, want waiting_user", task.Status)
	}

	completion, found, err := f.store.GetTaskCompletion(ctx, f.task.ID)
	must(t, err, "GetTaskCompletion")
	if !found || completion.Status != CompletionStatusBlocked {
		t.Fatalf("completion = %+v found=%v, want a blocked row", completion, found)
	}

	rows := completionSystemRows(t, f, "completion-blocked:dec-block")
	if len(rows) != 1 {
		t.Fatalf("blocked rows = %d, want 1", len(rows))
	}
	body := rows[0].Body
	for _, want := range []string{"1. ", "2. ", "files", "dist/app.js"} {
		if !strings.Contains(body, want) {
			t.Fatalf("blocked body missing %q:\n%s", want, body)
		}
	}
	if countTaskEvents(t, f, "completed") != 0 {
		t.Fatal("blocked completion must not finish the task")
	}
}

func TestCompleteTaskIdempotent(t *testing.T) {
	ctx := context.Background()
	f := newCompletionFixture(t, &CompletionContract{
		Requirements: []CompletionRequirement{
			{ID: "build", Label: "构建节点完成", Kind: CompletionKindNodeDone, NodeID: "build"},
		},
	})
	f.addAttempt(t, "build", NodeRunSuccess, map[string]any{"outcome": "done"}, "")

	decision := completionDecision(f, "dec-twice")
	for i := 0; i < 2; i++ {
		if err := f.svc.applyCompleteTask(ctx, f.store, f.task, decision, completionAction("做完了")); err != nil {
			t.Fatalf("applyCompleteTask #%d: %v", i+1, err)
		}
	}
	if rows := completionSystemRows(t, f, "completion-evidence:dec-twice"); len(rows) != 1 {
		t.Fatalf("evidence rows = %d after replay, want 1", len(rows))
	}
	if countTaskEvents(t, f, "completed") != 1 {
		t.Fatalf("completed events = %d after replay, want 1", countTaskEvents(t, f, "completed"))
	}
}

func TestCompleteTaskNoRunUsesSetTaskStatus(t *testing.T) {
	ctx := context.Background()
	f := newDirectCompletionFixture(t)
	f.addActivity(t, TaskActivity{ID: "msg-1", Type: ActivityTypeUserMessage, Body: "看下这个问题"})
	f.addActivity(t, TaskActivity{ID: "reply-1", Type: ActivityTypeAgentReply, Status: ActivityStatusCompleted, ReplyToID: "msg-1"})
	must(t, f.store.UpdateTaskStatus(ctx, f.task.ID, StatusWaitingUser, false), "UpdateTaskStatus")

	decision := completionDecision(f, "dec-direct")
	if err := f.svc.applyCompleteTask(ctx, f.store, f.task, decision, completionAction("已经答复完了")); err != nil {
		t.Fatalf("applyCompleteTask: %v", err)
	}

	task, err := f.store.GetTask(ctx, f.task.ID)
	must(t, err, "GetTask")
	if task.Status != StatusSuccess {
		t.Fatalf("task status = %q, want success", task.Status)
	}
	completion, found, err := f.store.GetTaskCompletion(ctx, f.task.ID)
	must(t, err, "GetTaskCompletion")
	if !found || completion.Status != CompletionStatusCompleted || completion.RunID != "" {
		t.Fatalf("completion = %+v found=%v, want completed without a run", completion, found)
	}
	if countTaskEvents(t, f, "completed") != 1 {
		t.Fatal("direct completion must finish the task exactly once")
	}
}
