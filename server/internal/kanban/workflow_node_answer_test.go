package kanban

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// answerRunner 记录每次节点回合看到的 prompt 与会话，用来证明「答复真的进了
// 下一次节点回合」，而不只是落了一行数据库。
type answerRunner struct {
	mu       sync.Mutex
	prompts  []string
	sessions []string
	outcomes []WorkflowNodeResult
}

func (r *answerRunner) EnsureWorkflowNodeSession(_ context.Context, exec WorkflowNodeExecution) (string, error) {
	if key := strings.TrimSpace(exec.NodeRun.SessionKey); key != "" {
		return key, nil
	}
	return "wf-session:" + exec.NodeRun.ID, nil
}

func (r *answerRunner) RunWorkflowNode(_ context.Context, exec WorkflowNodeExecution) (WorkflowNodeResult, error) {
	r.mu.Lock()
	i := len(r.prompts)
	r.prompts = append(r.prompts, exec.Prompt)
	r.sessions = append(r.sessions, exec.NodeRun.SessionKey)
	outcome := WorkflowNodeResult{Outcome: WorkflowNodeDone, Output: map[string]any{"summary": "ok"}}
	if i < len(r.outcomes) {
		outcome = r.outcomes[i]
	}
	r.mu.Unlock()
	return outcome, nil
}

func (r *answerRunner) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.prompts)
}

func (r *answerRunner) promptAt(i int) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i < 0 || i >= len(r.prompts) {
		return ""
	}
	return r.prompts[i]
}

func (r *answerRunner) sessionAt(i int) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i < 0 || i >= len(r.sessions) {
		return ""
	}
	return r.sessions[i]
}

func answerHarness(t *testing.T, runID string) (*Service, *answerRunner, *TaskStore, Task, *WorkflowRun) {
	t.Helper()
	svc, root, store, task, run := newRuntimeHarness(t, agentGraph("build"), runID)
	runner := &answerRunner{outcomes: []WorkflowNodeResult{
		{Outcome: WorkflowNodeBlocked, Reason: "需要用户在 A/B 之间选择", Output: map[string]any{"question": "选 A 还是 B？"}},
		{Outcome: WorkflowNodeDone, Output: map[string]any{"summary": "按 A 完成"}},
	}}
	svc.SetWorkflowNodeRunner(runner)
	if err := svc.WakeWorkflowRun(context.Background(), root.ID, task.ID, "test"); err != nil {
		t.Fatalf("WakeWorkflowRun: %v", err)
	}
	waitRuntimeIdle(t, svc, root.ID, task.ID)
	return svc, runner, store, task, run
}

// 用户答复必须真的进入下一次节点回合：新尝试复用原会话、带着答复重跑、
// 原行证据不被改写。
func TestAnswerWaitingNodeRetriesWithAnswer(t *testing.T) {
	ctx := context.Background()
	svc, runner, store, task, run := answerHarness(t, "run-answer-retry")
	orig, err := store.WorkflowNodeRunByKey(ctx, run.ID, "build", "", 0, 1)
	if err != nil {
		t.Fatalf("original node run: %v", err)
	}
	if orig.Status != NodeRunWaitingUser {
		t.Fatalf("original status = %q, want waiting_user", orig.Status)
	}

	answered, err := svc.AnswerWaitingWorkflowNode(ctx, task.RootID, task.ID, orig.ID, "我希望是A", "act-answer")
	if err != nil {
		t.Fatalf("AnswerWaitingWorkflowNode: %v", err)
	}
	if answered.Attempt != 2 || answered.PrevAttemptID != orig.ID || answered.NodeID != "build" {
		t.Fatalf("answered row = %+v, want attempt 2 linked to %s", answered, orig.ID)
	}
	if answered.Input["user_answer"] != "我希望是A" || answered.Input["user_answer_activity_id"] != "act-answer" {
		t.Fatalf("answered input = %#v, want the user answer carried on the row", answered.Input)
	}
	if answered.SessionKey != orig.SessionKey || answered.SessionKey == "" {
		t.Fatalf("answered session = %q, want the original session %q", answered.SessionKey, orig.SessionKey)
	}

	waitRuntimeIdle(t, svc, task.RootID, task.ID)
	if runner.calls() != 2 {
		t.Fatalf("runner calls = %d, want 2 (blocked + answer)", runner.calls())
	}
	prompt := runner.promptAt(1)
	for _, want := range []string{"我希望是A", "选 A 还是 B？", "用户答复"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("second prompt missing %q:\n%s", want, prompt)
		}
	}
	if got := runner.sessionAt(1); got != orig.SessionKey {
		t.Fatalf("second turn session = %q, want %q (continue the same conversation)", got, orig.SessionKey)
	}

	reloaded, err := store.WorkflowNodeRunByKey(ctx, run.ID, "build", "", 0, 2)
	if err != nil {
		t.Fatalf("reload answer attempt: %v", err)
	}
	if reloaded.Status != NodeRunSuccess {
		t.Fatalf("answer attempt status = %q, want success", reloaded.Status)
	}
	original, err := store.WorkflowNodeRunByKey(ctx, run.ID, "build", "", 0, 1)
	if err != nil {
		t.Fatalf("reload original: %v", err)
	}
	if original.Status != NodeRunWaitingUser || original.Output["reason"] != "需要用户在 A/B 之间选择" {
		t.Fatalf("original evidence changed: %#v", original)
	}

	events, err := store.ListWorkflowRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatalf("ListWorkflowRunEvents: %v", err)
	}
	found := false
	for _, event := range events {
		if event.Type != WorkflowEventNodeRetried || event.NodeRunID != answered.ID {
			continue
		}
		found = true
		if event.Payload["prev_attempt_id"] != orig.ID || event.Payload["answer_activity_id"] != "act-answer" {
			t.Fatalf("retry payload = %#v, want the answer linkage", event.Payload)
		}
	}
	if !found {
		t.Fatal("no node_retried ledger event for the answered attempt")
	}
}

// 幂等：apply 重放（或用户重复提交）不能把同一段答复变成第二次重跑。
func TestAnswerWaitingNodeIsIdempotent(t *testing.T) {
	ctx := context.Background()
	svc, runner, store, task, run := answerHarness(t, "run-answer-idem")
	orig, err := store.WorkflowNodeRunByKey(ctx, run.ID, "build", "", 0, 1)
	if err != nil {
		t.Fatalf("original node run: %v", err)
	}
	first, err := svc.AnswerWaitingWorkflowNode(ctx, task.RootID, task.ID, orig.ID, "我希望是A", "act-answer")
	if err != nil {
		t.Fatalf("first answer: %v", err)
	}
	waitRuntimeIdle(t, svc, task.RootID, task.ID)
	second, err := svc.AnswerWaitingWorkflowNode(ctx, task.RootID, task.ID, orig.ID, "我希望是A", "act-answer")
	if err != nil {
		t.Fatalf("replayed answer: %v", err)
	}
	waitRuntimeIdle(t, svc, task.RootID, task.ID)
	if second.ID != first.ID {
		t.Fatalf("replay created %s, want the existing attempt %s", second.ID, first.ID)
	}
	if runner.calls() != 2 {
		t.Fatalf("runner calls = %d, want 2 (a replay must not start another turn)", runner.calls())
	}
}

// 目标不合法时必须明确失败，绝不静默挑一个节点重跑。
func TestAnswerWaitingNodeRejectsBadTargets(t *testing.T) {
	ctx := context.Background()

	t.Run("not_waiting_user", func(t *testing.T) {
		svc, _, store, task, run := answerHarness(t, "run-answer-done")
		if _, err := svc.AnswerWaitingWorkflowNode(ctx, task.RootID, task.ID, "", "答复", "act-1"); err != nil {
			// The harness leaves exactly one waiting_user node, so answering it once is
			// legal; the second call has no waiting target left.
			t.Fatalf("first answer: %v", err)
		}
		waitRuntimeIdle(t, svc, task.RootID, task.ID)
		if _, err := svc.AnswerWaitingWorkflowNode(ctx, task.RootID, task.ID, "", "再来一次", "act-2"); err == nil {
			t.Fatal("answering a task with no waiting node succeeded")
		}
		orig, err := store.WorkflowNodeRunByKey(ctx, run.ID, "build", "", 0, 1)
		if err != nil || orig.Status != NodeRunWaitingUser {
			t.Fatalf("original row changed: %#v err=%v", orig, err)
		}
	})

	t.Run("human_gate_is_not_an_answer_target", func(t *testing.T) {
		store, svc, run, root := newWorkflowCommentFixture(t, "task-answer-gate", "run-answer-gate", humanGateGraph())
		if err := svc.AdvanceWorkflowRun(ctx, store, run.TaskID); err != nil {
			t.Fatalf("AdvanceWorkflowRun: %v", err)
		}
		gate, err := store.WorkflowNodeRunByKey(ctx, run.ID, "gate", "", 0, 1)
		if err != nil || gate.Status != NodeRunWaitingUser {
			t.Fatalf("gate row = %#v err=%v", gate, err)
		}
		if _, err := svc.AnswerWaitingWorkflowNode(ctx, root.ID, run.TaskID, gate.ID, "批准", "act-1"); err == nil {
			t.Fatal("a normal answer replaced the HumanGate decision (D4)")
		}
	})

	t.Run("empty_answer_is_rejected", func(t *testing.T) {
		svc, _, store, task, run := answerHarness(t, "run-answer-empty")
		orig, err := store.WorkflowNodeRunByKey(ctx, run.ID, "build", "", 0, 1)
		if err != nil {
			t.Fatalf("original node run: %v", err)
		}
		if _, err := svc.AnswerWaitingWorkflowNode(ctx, task.RootID, task.ID, orig.ID, "   ", "act-1"); err == nil {
			t.Fatal("an empty answer was accepted")
		}
		if _, err := svc.AnswerWaitingWorkflowNode(ctx, task.RootID, task.ID, "run-missing:build::0:1", "答复", "act-1"); err == nil {
			t.Fatal("an unknown node run id was accepted")
		}
	})
}

// 没有答复的普通节点回合不能被这段注入污染：只出现在带着答复的重跑上。
func TestAnswerPromptBlockOnlyAppearsForAnsweredAttempts(t *testing.T) {
	ctx := context.Background()
	svc, runner, store, task, run := answerHarness(t, "run-answer-plain")
	orig, err := store.WorkflowNodeRunByKey(ctx, run.ID, "build", "", 0, 1)
	if err != nil {
		t.Fatalf("original node run: %v", err)
	}
	if strings.Contains(runner.promptAt(0), "用户答复") {
		t.Fatalf("first prompt already contains the answer block:\n%s", runner.promptAt(0))
	}
	if _, err := svc.AnswerWaitingWorkflowNode(ctx, task.RootID, task.ID, orig.ID, "我希望是A", "act-1"); err != nil {
		t.Fatalf("answer the waiting node: %v", err)
	}
	waitRuntimeIdle(t, svc, task.RootID, task.ID)
	if !strings.Contains(runner.promptAt(1), "用户答复") {
		t.Fatalf("answered attempt prompt lacks the answer block:\n%s", runner.promptAt(1))
	}
}
