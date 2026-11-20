package kanban

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// complete_task 动作与工具（Plan 2026-11-20 Task 4）：Router 唯一能宣告「这轮
// 做完了」的入口。动作只表达意图，收尾是否成立由证据门判定。

func completeTaskEnv(t *testing.T) (*routerToolEnv, func(summary string) (string, error)) {
	t.Helper()
	env := &routerToolEnv{
		brief:     &SituationBrief{Task: RouterBriefTask{ID: "task-1", Status: StatusWaitingUser}},
		collector: newRouterTurnCollector(RouterConfig{}),
	}
	tool := env.completeTaskTool()
	return env, func(summary string) (string, error) {
		raw, err := json.Marshal(map[string]any{"summary": summary, "reason": "契约四项全过"})
		must(t, err, "marshal args")
		return tool.Handler(context.Background(), raw)
	}
}

func TestCompleteTaskToolRecords(t *testing.T) {
	env, call := completeTaskEnv(t)
	tool := env.completeTaskTool()
	if tool.Name != "router_complete_task" {
		t.Fatalf("tool name = %q, want router_complete_task", tool.Name)
	}
	if tool.Class != RouterToolWrite {
		t.Fatalf("tool class = %q, want write", tool.Class)
	}

	res, err := call("三个文件改完，测试跑过")
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(res, "recorded") {
		t.Fatalf("handler result = %q, want recorded", res)
	}
	actions := env.collector.snapshot()
	if len(actions) != 1 || actions[0].Kind != RouterActionComplete {
		t.Fatalf("actions = %+v, want one complete_task", actions)
	}
	if actions[0].Summary != "三个文件改完，测试跑过" {
		t.Fatalf("summary = %q, want the recorded text", actions[0].Summary)
	}

	if _, err := call("   "); err == nil {
		t.Fatal("empty summary must be rejected")
	}
}

func TestCompleteTaskConflictsWithExecution(t *testing.T) {
	brief := &SituationBrief{
		Task:      RouterBriefTask{ID: "task-1", Status: StatusWaitingUser},
		Workflows: []RouterBriefWorkflow{{ID: "wf-1", Name: "默认"}},
	}
	actions := []RouterAction{
		{Seq: 0, Kind: RouterActionComplete, Summary: "做完了"},
		{Seq: 1, Kind: RouterActionRunWorkflow, WorkflowID: "wf-1"},
	}
	err := validateRouterActions(brief, actions, RouterConfig{}.Normalized())
	if err == nil {
		t.Fatal("validateRouterActions accepted complete_task together with run_workflow")
	}
}

func TestCompleteTaskRejectedWithOpenNodes(t *testing.T) {
	brief := &SituationBrief{
		Task: RouterBriefTask{ID: "task-1", Status: StatusRunning},
		ActiveExecution: &RouterBriefExecution{
			Kind:         "workflow",
			RunID:        "run-1",
			Status:       StatusRunning,
			CurrentNodes: []RouterBriefNode{{NodeID: "build", Status: NodeRunRunning}},
		},
	}
	err := validateRouterActions(brief, []RouterAction{{Kind: RouterActionComplete, Summary: "做完了"}}, RouterConfig{}.Normalized())
	if err == nil {
		t.Fatal("validateRouterActions accepted completion while nodes are still running")
	}
}

func TestCompleteTaskRequiresSingleSummary(t *testing.T) {
	brief := &SituationBrief{Task: RouterBriefTask{ID: "task-1", Status: StatusWaitingUser}}
	if err := validateRouterActions(brief, []RouterAction{{Kind: RouterActionComplete}}, RouterConfig{}.Normalized()); err == nil {
		t.Fatal("complete_task without summary must be rejected")
	}
	two := []RouterAction{
		{Seq: 0, Kind: RouterActionComplete, Summary: "a"},
		{Seq: 1, Kind: RouterActionComplete, Summary: "b"},
	}
	if err := validateRouterActions(brief, two, RouterConfig{}.Normalized()); err == nil {
		t.Fatal("two complete_task actions must be rejected")
	}
}

func TestCompleteTaskRejectedOnTerminalTask(t *testing.T) {
	for _, status := range []string{StatusSuccess, StatusFail, StatusCancelled} {
		brief := &SituationBrief{Task: RouterBriefTask{ID: "task-1", Status: status}}
		err := validateRouterActions(brief, []RouterAction{{Kind: RouterActionComplete, Summary: "做完了"}}, RouterConfig{}.Normalized())
		if err == nil {
			t.Fatalf("complete_task accepted on terminal task (%s)", status)
		}
	}
}

func TestCompleteTaskAllowedInRuntimeEventTurn(t *testing.T) {
	brief := &SituationBrief{
		Task:  RouterBriefTask{ID: "task-1", Status: StatusWaitingUser},
		Event: &RouterBriefEvent{ID: "ev-1", Kind: string(RouterEventRunFailed)},
	}
	actions := []RouterAction{{Kind: RouterActionComplete, Summary: "做完了"}}
	if err := validateRuntimeEventActions(brief, actions, RouterConfig{}); err != nil {
		t.Fatalf("runtime event turn rejected complete_task: %v", err)
	}
}

func TestSuggestFinishEnumRemoved(t *testing.T) {
	brief := &SituationBrief{Task: RouterBriefTask{ID: "task-1", Status: StatusWaitingUser}}
	err := validateRouterActions(brief, []RouterAction{{Kind: "suggest_finish"}}, RouterConfig{}.Normalized())
	if err == nil || !strings.Contains(err.Error(), "invalid action kind") {
		t.Fatalf("suggest_finish = %v, want invalid action kind", err)
	}
}
