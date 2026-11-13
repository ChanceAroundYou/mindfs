package kanban

import (
	"context"
	"strings"
	"testing"
	"time"
)

// 内置 workflow 的 agent 节点 prompt 就是 `{previous_input}`。这条链路曾经把
// 字面量直接发给 agent：workflow 路径没有 legacy 的 BuildAgentPrompt 替换，
// 用户留言于是从没到过模型，agent 只能对着「{previous_input}」自行发挥。
func TestWorkflowNodePromptResolvesPreviousInputFromUserActivity(t *testing.T) {
	ctx := context.Background()
	store, svc, run, _ := newWorkflowAttemptFixture(t, "wf-prompt-task", "wf-prompt-run")
	insertUserActivity(t, store, "act-1", "wf-prompt-task", "把登录页做出来")
	runner := &scriptedWorkflowNodeRunner{}
	svc.SetWorkflowNodeRunner(runner)

	node := WorkflowNode{ID: "build", Kind: NodeKindAgent, Params: map[string]any{"prompt": "{previous_input}"}}
	exec, err := svc.PrepareWorkflowNode(ctx, store, run, node, attemptNodeRun(run.ID))
	if err != nil {
		t.Fatalf("PrepareWorkflowNode: %v", err)
	}
	if exec.Prompt != "把登录页做出来" {
		t.Fatalf("prompt = %q, want the user's message", exec.Prompt)
	}
	if strings.Contains(exec.Prompt, "{previous_input}") {
		t.Fatal("literal placeholder leaked into the agent prompt")
	}
}

// RunWorkflowNodeAttempt 是生产路径：断言 runner 收到的就是替换后的 prompt，
// 而不只是 Prepare 的返回值正确。
func TestWorkflowNodeAttemptSendsResolvedPromptToRunner(t *testing.T) {
	ctx := context.Background()
	store, svc, run, _ := newWorkflowAttemptFixture(t, "wf-prompt-task-2", "wf-prompt-run-2")
	insertUserActivity(t, store, "act-2", "wf-prompt-task-2", "修复压缩阈值")
	runner := &scriptedWorkflowNodeRunner{}
	svc.SetWorkflowNodeRunner(runner)

	node := WorkflowNode{ID: "build", Kind: NodeKindAgent, Params: map[string]any{"prompt": "需求：{previous_input}"}}
	if _, err := svc.RunWorkflowNodeAttempt(ctx, store, run, node, attemptNodeRun(run.ID)); err != nil {
		t.Fatalf("RunWorkflowNodeAttempt: %v", err)
	}
	if len(runner.execs) != 1 {
		t.Fatalf("runner ran %d times, want 1", len(runner.execs))
	}
	if got := runner.execs[0].Prompt; got != "需求：修复压缩阈值" {
		t.Fatalf("runner prompt = %q, want the substituted message", got)
	}
}

// 没有用户输入时宁可失败并说明原因，也不能把 `{previous_input}` 原样发出去。
func TestWorkflowNodePromptFailsClosedWithoutUserInput(t *testing.T) {
	ctx := context.Background()
	store, svc, run, _ := newWorkflowAttemptFixture(t, "wf-prompt-task-3", "wf-prompt-run-3")
	runner := &scriptedWorkflowNodeRunner{}
	svc.SetWorkflowNodeRunner(runner)

	node := WorkflowNode{ID: "build", Kind: NodeKindAgent, Params: map[string]any{"prompt": "{previous_input}"}}
	_, err := svc.PrepareWorkflowNode(ctx, store, run, node, attemptNodeRun(run.ID))
	if err == nil {
		t.Fatal("want an error instead of sending the raw placeholder")
	}
	if !strings.Contains(err.Error(), "previous_input") {
		t.Fatalf("err = %v, want it to name the unresolved placeholder", err)
	}
	if runner.runCalls != 0 {
		t.Fatalf("runner must not run, ran %d times", runner.runCalls)
	}
}

// 触发 run 的那条留言优先于「最近一条留言」；task_initial_input 固定取第一条。
func TestWorkflowPromptValuesPrefersTriggeringActivity(t *testing.T) {
	ctx := context.Background()
	store, svc, run, _ := newWorkflowAttemptFixture(t, "wf-prompt-task-4", "wf-prompt-run-4")
	_ = svc
	insertUserActivityAt(t, store, "act-old", "wf-prompt-task-4", "最初的需求", time.Now().UTC().Add(-2*time.Hour))
	insertUserActivityAt(t, store, "act-trigger", "wf-prompt-task-4", "触发这次 run 的留言", time.Now().UTC().Add(-time.Hour))
	insertUserActivityAt(t, store, "act-later", "wf-prompt-task-4", "之后补充的留言", time.Now().UTC())

	// run.ActivityID 是创建 Run 时写入的审计字段；这里直接构造同一个形状。
	run.ActivityID = "act-trigger"
	task, err := store.GetTask(ctx, "wf-prompt-task-4")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	values := WorkflowPromptValues(ctx, store, run, task)
	if values["previous_input"] != "触发这次 run 的留言" {
		t.Fatalf("previous_input = %q, want the triggering activity", values["previous_input"])
	}
	if values["task_initial_input"] != "最初的需求" {
		t.Fatalf("task_initial_input = %q, want the first user input", values["task_initial_input"])
	}
}

func insertUserActivity(t *testing.T, store *TaskStore, id, taskID, body string) {
	t.Helper()
	insertUserActivityAt(t, store, id, taskID, body, time.Now().UTC())
}

func insertUserActivityAt(t *testing.T, store *TaskStore, id, taskID, body string, at time.Time) {
	t.Helper()
	activity := &TaskActivity{
		ID:        id,
		TaskID:    taskID,
		Type:      ActivityTypeUserMessage,
		Author:    "user",
		Role:      RoleUser,
		Body:      body,
		CreatedAt: at,
	}
	if err := NewActivityStore(store.db).Insert(context.Background(), activity); err != nil {
		t.Fatalf("insert activity %s: %v", id, err)
	}
}
