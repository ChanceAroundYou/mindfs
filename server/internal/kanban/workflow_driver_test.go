package kanban

import (
	"context"
	"strings"
	"testing"
)

func TestWorkflowAdvanceRunsReadyNodesOnceAndProjectsSuccess(t *testing.T) {
	ctx := context.Background()
	store, svc, run, _ := newWorkflowAttemptFixture(t, "wf-advance-task", "wf-advance-run")
	// 换成 notify -> agent -> notify 的线性图。
	run.Graph = WorkflowDefinition{
		Nodes: []WorkflowNode{
			{ID: "start", Kind: NodeKindNotify, Params: map[string]any{"message": "开工"}},
			{ID: "build", Kind: NodeKindAgent, Params: map[string]any{"prompt": "实现登录页"}},
			{ID: "done", Kind: NodeKindNotify, Params: map[string]any{"message": "完成"}},
		},
		Edges: []WorkflowEdge{{From: "start", To: "build"}, {From: "build", To: "done"}},
	}
	if err := store.SaveWorkflowRun(ctx, run); err != nil {
		t.Fatalf("SaveWorkflowRun: %v", err)
	}
	runner := &scriptedWorkflowNodeRunner{outcomes: []WorkflowNodeResult{{Outcome: WorkflowNodeDone, Output: map[string]any{"summary": "ok"}}}}
	svc.SetWorkflowNodeRunner(runner)

	if err := svc.AdvanceWorkflowRun(ctx, store, run.TaskID); err != nil {
		t.Fatalf("AdvanceWorkflowRun: %v", err)
	}
	if runner.runCalls != 1 {
		t.Fatalf("agent dispatched %d times, want 1", runner.runCalls)
	}
	reloaded, err := store.WorkflowRunForTask(ctx, run.TaskID)
	if err != nil {
		t.Fatalf("WorkflowRunForTask: %v", err)
	}
	for _, id := range []string{"start", "build", "done"} {
		if reloaded.NodeStates[id] != NodeRunSuccess {
			t.Fatalf("node %s state = %q, want success (states=%v)", id, reloaded.NodeStates[id], reloaded.NodeStates)
		}
	}
	task, err := store.GetTask(ctx, run.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != StatusSuccess {
		t.Fatalf("task status = %q, want success", task.Status)
	}
	nodes, err := store.ListWorkflowNodeRuns(ctx, run.ID)
	if err != nil {
		t.Fatalf("ListWorkflowNodeRuns: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("node runs = %d, want 3 (one per node)", len(nodes))
	}
	// 再推进一次不得重复派发或新增 NodeRun。
	if err := svc.AdvanceWorkflowRun(ctx, store, run.TaskID); err != nil {
		t.Fatalf("second AdvanceWorkflowRun: %v", err)
	}
	if runner.runCalls != 1 {
		t.Fatalf("second advance dispatched agent %d times, want still 1", runner.runCalls)
	}
	nodes, err = store.ListWorkflowNodeRuns(ctx, run.ID)
	if err != nil {
		t.Fatalf("ListWorkflowNodeRuns after second advance: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("node runs after second advance = %d, want 3", len(nodes))
	}
}

func TestWorkflowAdvanceConditionSelectsRouteAndSkipsUnselectedJoin(t *testing.T) {
	ctx := context.Background()
	store, svc, run, _ := newWorkflowAttemptFixture(t, "wf-cond-task", "wf-cond-run")
	run.Graph = WorkflowDefinition{
		Nodes: []WorkflowNode{
			{ID: "build", Kind: NodeKindAgent, Params: map[string]any{"prompt": "实现登录页"}},
			{ID: "c", Kind: NodeKindCondition, Params: map[string]any{"expression": "build.output.files > 0", "routes": []any{"pass", "fail"}}},
			{ID: "hit", Kind: NodeKindNotify, Params: map[string]any{"message": "有文件"}},
			{ID: "miss", Kind: NodeKindNotify, Params: map[string]any{"message": "没文件"}},
			{ID: "join", Kind: NodeKindNotify, Params: map[string]any{"message": "汇合"}},
		},
		Edges: []WorkflowEdge{
			{From: "build", To: "c"},
			{From: "c", To: "hit", Route: "pass"},
			{From: "c", To: "miss", Route: "fail"},
			{From: "hit", To: "join"},
			{From: "miss", To: "join"},
		},
	}
	if err := store.SaveWorkflowRun(ctx, run); err != nil {
		t.Fatalf("SaveWorkflowRun: %v", err)
	}
	runner := &scriptedWorkflowNodeRunner{outcomes: []WorkflowNodeResult{{Outcome: WorkflowNodeDone, Output: map[string]any{"files": float64(2)}}}}
	svc.SetWorkflowNodeRunner(runner)

	if err := svc.AdvanceWorkflowRun(ctx, store, run.TaskID); err != nil {
		t.Fatalf("AdvanceWorkflowRun: %v", err)
	}
	reloaded, err := store.WorkflowRunForTask(ctx, run.TaskID)
	if err != nil {
		t.Fatalf("WorkflowRunForTask: %v", err)
	}
	if got := reloaded.SelectedRoutes["c"]; got != "pass" {
		t.Fatalf("selected route = %q, want pass", got)
	}
	if reloaded.NodeStates["miss"] != NodeRunSkipped {
		t.Fatalf("miss state = %q, want skipped", reloaded.NodeStates["miss"])
	}
	for _, id := range []string{"build", "c", "hit", "join"} {
		if reloaded.NodeStates[id] != NodeRunSuccess {
			t.Fatalf("node %s state = %q, want success (states=%v)", id, reloaded.NodeStates[id], reloaded.NodeStates)
		}
	}
	task, err := store.GetTask(ctx, run.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != StatusSuccess {
		t.Fatalf("task status = %q, want success", task.Status)
	}
	miss, err := store.WorkflowNodeRunByKey(ctx, run.ID, "miss", "", 0, 1)
	if err != nil {
		t.Fatalf("miss node run: %v", err)
	}
	if miss.Status != NodeRunSkipped {
		t.Fatalf("miss node run status = %q, want skipped", miss.Status)
	}
	// 选中的 route 必须落盘：重开同一个 task-node DB 后仍能解释既有前缀。
	root := store.root
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("reopen TaskStore: %v", err)
	}
	defer reopened.Close()
	persisted, err := reopened.WorkflowRunForTask(ctx, run.TaskID)
	if err != nil {
		t.Fatalf("reopened WorkflowRunForTask: %v", err)
	}
	if persisted.SelectedRoutes["c"] != "pass" || persisted.NodeStates["miss"] != NodeRunSkipped {
		t.Fatalf("reopened run routes=%v states=%v, want c=pass / miss=skipped", persisted.SelectedRoutes, persisted.NodeStates)
	}
}

func TestWorkflowAdvanceConditionMissingFieldBecomesWaitingUser(t *testing.T) {
	ctx := context.Background()
	store, svc, run, _ := newWorkflowAttemptFixture(t, "wf-cond-bad-task", "wf-cond-bad-run")
	run.Graph = WorkflowDefinition{
		Nodes: []WorkflowNode{
			{ID: "build", Kind: NodeKindAgent, Params: map[string]any{"prompt": "实现登录页"}},
			{ID: "c", Kind: NodeKindCondition, Params: map[string]any{"expression": "build.output.missing > 0", "routes": []any{"pass", "fail"}}},
			{ID: "hit", Kind: NodeKindNotify, Params: map[string]any{"message": "有"}},
			{ID: "miss", Kind: NodeKindNotify, Params: map[string]any{"message": "无"}},
		},
		Edges: []WorkflowEdge{
			{From: "build", To: "c"},
			{From: "c", To: "hit", Route: "pass"},
			{From: "c", To: "miss", Route: "fail"},
		},
	}
	if err := store.SaveWorkflowRun(ctx, run); err != nil {
		t.Fatalf("SaveWorkflowRun: %v", err)
	}
	runner := &scriptedWorkflowNodeRunner{outcomes: []WorkflowNodeResult{{Outcome: WorkflowNodeDone, Output: map[string]any{"files": float64(2)}}}}
	svc.SetWorkflowNodeRunner(runner)

	if err := svc.AdvanceWorkflowRun(ctx, store, run.TaskID); err != nil {
		t.Fatalf("AdvanceWorkflowRun: %v", err)
	}
	cond, err := store.WorkflowNodeRunByKey(ctx, run.ID, "c", "", 0, 1)
	if err != nil {
		t.Fatalf("condition node run: %v", err)
	}
	if cond.Status != NodeRunWaitingUser {
		t.Fatalf("condition status = %q, want waiting_user (never silently false)", cond.Status)
	}
	if reason, _ := cond.Output["error"].(string); !strings.Contains(reason, "not found") {
		t.Fatalf("condition output = %#v, want missing-field error", cond.Output)
	}
	if _, err := store.WorkflowNodeRunByKey(ctx, run.ID, "hit", "", 0, 1); err == nil {
		t.Fatal("unselected branch must not start after condition error")
	}
	task, err := store.GetTask(ctx, run.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != StatusWaitingUser {
		t.Fatalf("task status = %q, want waiting_user", task.Status)
	}
	acts, err := store.ListActivities(ctx, run.TaskID)
	if err != nil {
		t.Fatalf("ListActivities: %v", err)
	}
	var attention *TaskActivity
	for i := range acts {
		if acts[i].Type == ActivityTypeAttention {
			attention = &acts[i]
		}
	}
	if attention == nil || !strings.Contains(attention.Body, "not found") {
		t.Fatalf("activities = %#v, want attention with missing-field reason", acts)
	}
	if attention.RunID != run.ID || attention.NodeRunID != "wf-cond-bad-run:c::0:1" {
		t.Fatalf("attention correlation = %#v", attention)
	}
}

func TestWorkflowAdvanceNotifyProducesActivityWithoutSessionRunner(t *testing.T) {
	ctx := context.Background()
	store, svc, run, _ := newWorkflowAttemptFixture(t, "wf-notify-task", "wf-notify-run")
	run.Graph = WorkflowDefinition{Nodes: []WorkflowNode{{ID: "n", Kind: NodeKindNotify, Params: map[string]any{"message": "任务已开始"}}}}
	if err := store.SaveWorkflowRun(ctx, run); err != nil {
		t.Fatalf("SaveWorkflowRun: %v", err)
	}
	runner := &scriptedWorkflowNodeRunner{}
	svc.SetWorkflowNodeRunner(runner)

	if err := svc.AdvanceWorkflowRun(ctx, store, run.TaskID); err != nil {
		t.Fatalf("AdvanceWorkflowRun: %v", err)
	}
	if runner.ensureCalls != 0 || runner.runCalls != 0 {
		t.Fatalf("notify used session runner: ensure=%d run=%d", runner.ensureCalls, runner.runCalls)
	}
	nr, err := store.WorkflowNodeRunByKey(ctx, run.ID, "n", "", 0, 1)
	if err != nil {
		t.Fatalf("notify node run: %v", err)
	}
	if nr.Status != NodeRunSuccess {
		t.Fatalf("notify node run status = %q, want success", nr.Status)
	}
	acts, err := store.ListActivities(ctx, run.TaskID)
	if err != nil {
		t.Fatalf("ListActivities: %v", err)
	}
	if len(acts) != 1 || acts[0].Type != ActivityTypeSystemEvent || acts[0].Body != "任务已开始" {
		t.Fatalf("activities = %#v, want one system_event with the message", acts)
	}
	if acts[0].RunID != run.ID || acts[0].NodeRunID != nr.ID {
		t.Fatalf("activity correlation = %#v, want run/node ids", acts[0])
	}
}
