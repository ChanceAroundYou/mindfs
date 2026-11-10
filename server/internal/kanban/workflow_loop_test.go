package kanban

import (
	"context"
	"errors"
	"testing"
	"time"

	"mindfs/server/internal/fs"
)

// loopTestGraph wraps one inner DAG in a Loop node and appends a downstream
// notify node so the outer graph can prove the loop actually finished.
func loopTestGraph(loopID string, inner WorkflowDefinition, exitExpr string, max int) WorkflowDefinition {
	return WorkflowDefinition{
		Nodes: []WorkflowNode{
			{ID: loopID, Kind: NodeKindLoop, Params: map[string]any{
				"max_iterations":  max,
				"exit_expression": exitExpr,
				"graph":           inner,
			}},
			{ID: "after", Kind: NodeKindNotify, Params: map[string]any{"message": "循环结束"}},
		},
		Edges: []WorkflowEdge{{From: loopID, To: "after"}},
	}
}

func newLoopFixture(t *testing.T, taskID, runID string, graph WorkflowDefinition) (*TaskStore, *Service, *WorkflowRun) {
	t.Helper()
	ctx := context.Background()
	store, svc, run, _ := newWorkflowAttemptFixture(t, taskID, runID)
	run.Graph = graph
	if err := store.SaveWorkflowRun(ctx, run); err != nil {
		t.Fatalf("SaveWorkflowRun: %v", err)
	}
	return store, svc, run
}

func loopNodeRuns(t *testing.T, store *TaskStore, runID string) []NodeRun {
	t.Helper()
	runs, err := store.ListWorkflowNodeRuns(context.Background(), runID)
	if err != nil {
		t.Fatalf("ListWorkflowNodeRuns: %v", err)
	}
	return runs
}

func findLoopNodeRun(runs []NodeRun, nodeID string, iteration int) (NodeRun, bool) {
	for _, nr := range runs {
		if nr.NodeID == nodeID && nr.LoopPath == "loop1" && nr.Iteration == iteration && nr.Attempt == 1 {
			return nr, true
		}
	}
	return NodeRun{}, false
}

// Task 6 Step 1: a completed iteration increments exactly once and the next
// iteration's inner NodeRuns carry the loop path and the new iteration number.
func TestWorkflowLoopIterationAdvancesOnceWithScopedNodeRuns(t *testing.T) {
	ctx := context.Background()
	inner := WorkflowDefinition{
		Nodes: []WorkflowNode{
			{ID: "write", Kind: NodeKindAgent, Params: map[string]any{"prompt": "写一版"}},
			{ID: "tick", Kind: NodeKindNotify, Params: map[string]any{"message": "一轮完成"}},
		},
		Edges: []WorkflowEdge{{From: "write", To: "tick"}},
	}
	store, svc, run := newLoopFixture(t, "wf-loop-advance-task", "wf-loop-advance-run",
		loopTestGraph("loop1", inner, "write.output.done == true", 3))
	runner := &scriptedWorkflowNodeRunner{outcomes: []WorkflowNodeResult{
		{Outcome: WorkflowNodeDone, Output: map[string]any{"done": false}},
		{Outcome: WorkflowNodeDone, Output: map[string]any{"done": true}},
	}}
	svc.SetWorkflowNodeRunner(runner)

	if err := svc.AdvanceWorkflowRun(ctx, store, run.TaskID); err != nil {
		t.Fatalf("AdvanceWorkflowRun: %v", err)
	}
	if runner.runCalls != 2 {
		t.Fatalf("inner agent ran %d times, want 2 (one per iteration)", runner.runCalls)
	}
	runs := loopNodeRuns(t, store, run.ID)
	for _, want := range []struct {
		node string
		iter int
	}{
		{"write", 1}, {"tick", 1}, {"write", 2}, {"tick", 2},
	} {
		if _, ok := findLoopNodeRun(runs, want.node, want.iter); !ok {
			t.Fatalf("missing NodeRun node=%s iteration=%d, runs=%+v", want.node, want.iter, runs)
		}
	}
	// Older iterations are history, never overwritten by the next one.
	if nr, _ := findLoopNodeRun(runs, "write", 1); nr.Status != NodeRunSuccess || nr.Output["done"] != false {
		t.Fatalf("iteration-1 output = %#v status=%s, want preserved done=false", nr.Output, nr.Status)
	}
	reloaded, err := store.WorkflowRunForTask(ctx, run.TaskID)
	if err != nil {
		t.Fatalf("WorkflowRunForTask: %v", err)
	}
	ls := reloaded.LoopState["loop1"]
	if ls == nil || ls.Iteration != 2 || ls.MaxIterations != 3 {
		t.Fatalf("loop state = %#v, want iteration 2 of max 3", ls)
	}
	if reloaded.NodeStates["loop1"] != NodeRunSuccess || reloaded.NodeStates["after"] != NodeRunSuccess {
		t.Fatalf("outer states = %v, want loop1+after success", reloaded.NodeStates)
	}
	if got := reloaded.NodeOutputs["loop1"]; got == nil {
		t.Fatalf("loop output = %#v, want iterations=2", got)
	} else if iterations, ok := toInt(got["iterations"]); !ok || iterations != 2 {
		t.Fatalf("loop output = %#v, want iterations=2", got)
	}
	task, err := store.GetTask(ctx, run.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != StatusSuccess {
		t.Fatalf("task status = %q, want success", task.Status)
	}
}

// Task 6 Step 2: a restart between "iteration 1 finished" and "iteration 2
// scheduled" must neither repeat iteration 1 nor skip the increment.
func TestWorkflowLoopRestartBetweenIterationsDoesNotRepeatOrMissIncrement(t *testing.T) {
	ctx := context.Background()
	inner := WorkflowDefinition{
		Nodes: []WorkflowNode{{ID: "write", Kind: NodeKindAgent, Params: map[string]any{"prompt": "写一版"}}},
	}
	store, svc, run := newLoopFixture(t, "wf-loop-restart-task", "wf-loop-restart-run",
		loopTestGraph("loop1", inner, "write.output.done == true", 3))

	// Persisted state at the crash point: iteration 1's inner node already
	// finished, LoopState still says iteration 1, nothing scheduled for 2.
	first := NodeRun{ID: workflowNodeRunID(run.ID, "write", "loop1", 1, 1), RunID: run.ID,
		NodeID: "write", LoopPath: "loop1", Iteration: 1, Attempt: 1, Status: NodeRunQueued}
	claimed, won, err := store.ClaimWorkflowNodeRun(ctx, first)
	if err != nil || !won {
		t.Fatalf("claim iteration 1: won=%v err=%v", won, err)
	}
	if err := store.BeginWorkflowNodeRun(ctx, claimed, time.Now().UTC()); err != nil {
		t.Fatalf("BeginWorkflowNodeRun: %v", err)
	}
	if err := store.FinishWorkflowNodeRun(ctx, claimed, NodeRunSuccess, map[string]any{"done": false},
		time.Now().UTC(), WorkflowEventNodeSucceeded, nil); err != nil {
		t.Fatalf("FinishWorkflowNodeRun: %v", err)
	}
	run.LoopState = map[string]*LoopState{"loop1": {Iteration: 1, MaxIterations: 3}}
	run.NodeStates["loop1"] = NodeRunRunning
	if err := store.SaveWorkflowRun(ctx, run); err != nil {
		t.Fatalf("SaveWorkflowRun: %v", err)
	}

	runner := &scriptedWorkflowNodeRunner{outcomes: []WorkflowNodeResult{{Outcome: WorkflowNodeDone, Output: map[string]any{"done": true}}}}
	svc.SetWorkflowNodeRunner(runner)
	if err := svc.AdvanceWorkflowRun(ctx, store, run.TaskID); err != nil {
		t.Fatalf("AdvanceWorkflowRun after restart: %v", err)
	}
	if runner.runCalls != 1 {
		t.Fatalf("restart ran the agent %d times, want 1 (iteration 2 only)", runner.runCalls)
	}
	runs := loopNodeRuns(t, store, run.ID)
	iterOne := 0
	for _, nr := range runs {
		if nr.NodeID == "write" && nr.LoopPath == "loop1" && nr.Iteration == 1 {
			iterOne++
		}
	}
	if iterOne != 1 {
		t.Fatalf("iteration 1 has %d NodeRuns, want exactly 1 (no repeat)", iterOne)
	}
	if _, ok := findLoopNodeRun(runs, "write", 2); !ok {
		t.Fatalf("iteration 2 was never scheduled, runs=%+v", runs)
	}
	reloaded, err := store.WorkflowRunForTask(ctx, run.TaskID)
	if err != nil {
		t.Fatalf("WorkflowRunForTask: %v", err)
	}
	if reloaded.LoopState["loop1"].Iteration != 2 {
		t.Fatalf("loop iteration = %d, want 2", reloaded.LoopState["loop1"].Iteration)
	}
	// A second advance must not add a third iteration or redispatch.
	if err := svc.AdvanceWorkflowRun(ctx, store, run.TaskID); err != nil {
		t.Fatalf("second AdvanceWorkflowRun: %v", err)
	}
	if runner.runCalls != 1 {
		t.Fatalf("second advance dispatched %d times, want still 1", runner.runCalls)
	}
	if runs := loopOnlyNodeRuns(loopNodeRuns(t, store, run.ID)); len(runs) != 2 {
		t.Fatalf("loop node runs after second advance = %d, want 2", len(runs))
	}
}

// loopOnlyNodeRuns isolates per-iteration rows; the outer "after" node also
// persists a NodeRun once the loop exits and is not part of this count.
func loopOnlyNodeRuns(runs []NodeRun) []NodeRun {
	var out []NodeRun
	for _, nr := range runs {
		if nr.LoopPath == "loop1" {
			out = append(out, nr)
		}
	}
	return out
}

// Task 6 Step 3: the bound is respected; the final non-exiting pass pauses the
// task for a human while every earlier iteration's output stays queryable.
func TestWorkflowLoopMaxIterationsStopsInWaitingUserAndKeepsOutputs(t *testing.T) {
	ctx := context.Background()
	inner := WorkflowDefinition{
		Nodes: []WorkflowNode{{ID: "write", Kind: NodeKindAgent, Params: map[string]any{"prompt": "再改一版"}}},
	}
	store, svc, run := newLoopFixture(t, "wf-loop-max-task", "wf-loop-max-run",
		loopTestGraph("loop1", inner, "write.output.done == true", 2))
	runner := &scriptedWorkflowNodeRunner{outcomes: []WorkflowNodeResult{
		{Outcome: WorkflowNodeDone, Output: map[string]any{"done": false, "version": 1}},
		{Outcome: WorkflowNodeDone, Output: map[string]any{"done": false, "version": 2}},
		{Outcome: WorkflowNodeDone, Output: map[string]any{"done": false, "version": 3}},
	}}
	svc.SetWorkflowNodeRunner(runner)

	if err := svc.AdvanceWorkflowRun(ctx, store, run.TaskID); err != nil {
		t.Fatalf("AdvanceWorkflowRun: %v", err)
	}
	if runner.runCalls != 2 {
		t.Fatalf("agent ran %d times, want 2 (max_iterations)", runner.runCalls)
	}
	reloaded, err := store.WorkflowRunForTask(ctx, run.TaskID)
	if err != nil {
		t.Fatalf("WorkflowRunForTask: %v", err)
	}
	if got := reloaded.LoopState["loop1"]; got == nil || got.Iteration != 2 || got.MaxIterations != 2 {
		t.Fatalf("loop state = %#v, want iteration 2 of max 2 (no overflow)", got)
	}
	if reloaded.NodeStates["loop1"] != NodeRunWaitingUser {
		t.Fatalf("loop state = %q, want waiting_user", reloaded.NodeStates["loop1"])
	}
	if _, ran := reloaded.NodeStates["after"]; ran {
		t.Fatalf("downstream node ran after loop hit its bound: %v", reloaded.NodeStates)
	}
	loopOut := reloaded.NodeOutputs["loop1"]
	if loopOut == nil {
		t.Fatalf("loop output = %#v, want iterations=2", loopOut)
	}
	if iterations, ok := toInt(loopOut["iterations"]); !ok || iterations != 2 {
		t.Fatalf("loop output = %#v, want iterations=2", loopOut)
	}
	for iteration, version := range map[int]float64{1: 1, 2: 2} {
		nr, ok := findLoopNodeRun(loopNodeRuns(t, store, run.ID), "write", iteration)
		if !ok {
			t.Fatalf("iteration %d output missing", iteration)
		}
		if nr.Output["version"] != version {
			t.Fatalf("iteration %d output = %#v, want version %v", iteration, nr.Output, version)
		}
	}
	task, err := store.GetTask(ctx, run.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != StatusWaitingUser {
		t.Fatalf("task status = %q, want waiting_user (needs-user, never silent success)", task.Status)
	}
	// Repeated advance must not start iteration 3 past the bound.
	if err := svc.AdvanceWorkflowRun(ctx, store, run.TaskID); err != nil {
		t.Fatalf("second AdvanceWorkflowRun: %v", err)
	}
	if runner.runCalls != 2 {
		t.Fatalf("advance past bound dispatched %d times, want 2", runner.runCalls)
	}
}

// Task 6 wiring: comment binding classifies running NodeRuns by kind through
// loop inner graphs. An inner agent is a real active agent (D3 target), while a
// running inner notify must not be offered as one.
func TestWorkflowLoopInnerAgentIsVisibleToCommentBinding(t *testing.T) {
	ctx := context.Background()
	inner := WorkflowDefinition{
		Nodes: []WorkflowNode{
			{ID: "write", Kind: NodeKindAgent, Params: map[string]any{"prompt": "写一版"}},
			{ID: "tick", Kind: NodeKindNotify, Params: map[string]any{"message": "一轮完成"}},
		},
		Edges: []WorkflowEdge{{From: "write", To: "tick"}},
	}
	store, _, run := newLoopFixture(t, "wf-loop-comment-task", "wf-loop-comment-run",
		loopTestGraph("loop1", inner, "write.output.done == true", 2))
	agent := NodeRun{ID: workflowNodeRunID(run.ID, "write", "loop1", 1, 1), RunID: run.ID,
		NodeID: "write", LoopPath: "loop1", Iteration: 1, Attempt: 1, Status: NodeRunQueued}
	claimed, won, err := store.ClaimWorkflowNodeRun(ctx, agent)
	if err != nil || !won {
		t.Fatalf("claim inner agent: won=%v err=%v", won, err)
	}
	if err := store.BeginWorkflowNodeRun(ctx, claimed, time.Now().UTC()); err != nil {
		t.Fatalf("BeginWorkflowNodeRun: %v", err)
	}
	notify := NodeRun{ID: workflowNodeRunID(run.ID, "tick", "loop1", 1, 1), RunID: run.ID,
		NodeID: "tick", LoopPath: "loop1", Iteration: 1, Attempt: 1, Status: NodeRunQueued}
	claimedNotify, won, err := store.ClaimWorkflowNodeRun(ctx, notify)
	if err != nil || !won {
		t.Fatalf("claim inner notify: won=%v err=%v", won, err)
	}
	if err := store.BeginWorkflowNodeRun(ctx, claimedNotify, time.Now().UTC()); err != nil {
		t.Fatalf("BeginWorkflowNodeRun: %v", err)
	}

	active, err := store.ActiveWorkflowAgentNodeRuns(ctx, run.ID)
	if err != nil {
		t.Fatalf("ActiveWorkflowAgentNodeRuns: %v", err)
	}
	if len(active) != 1 || active[0].NodeID != "write" || active[0].LoopPath != "loop1" {
		t.Fatalf("active = %#v, want exactly the inner agent write@loop1", active)
	}
	if node, ok := workflowNodeByID(run.Graph, "write"); !ok || node.Kind != NodeKindAgent {
		t.Fatalf("workflowNodeByID(inner) = %#v ok=%v, want agent", node, ok)
	}
	if node, ok := workflowNodeByID(run.Graph, "tick"); !ok || node.Kind != NodeKindNotify {
		t.Fatalf("workflowNodeByID(inner notify) = %#v ok=%v, want notify", node, ok)
	}
}

// Task 6 Step 4: triggered nodes and the edges touching them are frozen, while
// the untriggered subtree still edits under revision CAS.
func TestWorkflowRunGraphEditFreezesTriggeredNodesAndIncidentEdges(t *testing.T) {
	ctx := context.Background()
	root := fs.NewRootInfo("wf-edit-root", "wf-edit-root", t.TempDir())
	store, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if _, err := store.CreateTask(ctx, Task{ID: "wf-edit-task", RootID: root.ID, Status: StatusPending, Stages: []StageTemplate{userStage("u")}},
		StageRun{ID: "wf-edit-stage", TaskID: "wf-edit-task", StageIndex: 0, StageName: "u", Role: RoleUser, Status: StageStatusPending},
		TaskEvent{ID: "wf-edit-event", TaskID: "wf-edit-task", Type: "created"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	graph := WorkflowDefinition{
		Nodes: []WorkflowNode{
			{ID: "a", Kind: NodeKindAgent, Params: map[string]any{"prompt": "实现"}},
			{ID: "b", Kind: NodeKindNotify, Params: map[string]any{"message": "通知"}},
			{ID: "c", Kind: NodeKindNotify, Params: map[string]any{"message": "汇总"}},
		},
		Edges: []WorkflowEdge{{From: "a", To: "b"}},
	}
	run := &WorkflowRun{ID: "wf-edit-run", TaskID: "wf-edit-task", Revision: 1, Graph: graph, NodeStates: map[string]string{}, NodeOutputs: map[string]map[string]any{}, SelectedRoutes: map[string]string{}, LoopState: map[string]*LoopState{}, LoopExitExpr: map[string]string{}}
	if err := store.CreateWorkflowRunAndSetEngine(ctx, run); err != nil {
		t.Fatalf("CreateWorkflowRunAndSetEngine: %v", err)
	}
	run.NodeStates["a"] = NodeRunQueued
	if err := store.SaveWorkflowRun(ctx, run); err != nil {
		t.Fatalf("SaveWorkflowRun: %v", err)
	}

	// Editable: config of an untriggered node.
	edited := cloneGraph(graph)
	for i := range edited.Nodes {
		if edited.Nodes[i].ID == "b" {
			edited.Nodes[i].Name = "改名通知"
		}
	}
	if err := store.EditWorkflowRunGraph(ctx, run.ID, run.Revision, edited); err != nil {
		t.Fatalf("edit untriggered node: %v", err)
	}
	current, err := store.WorkflowRunByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("WorkflowRunByID: %v", err)
	}
	// Editable: a future edge that touches no triggered node.
	future := cloneGraph(current.Graph)
	future.Edges = append(future.Edges, WorkflowEdge{From: "b", To: "c"})
	if err := store.EditWorkflowRunGraph(ctx, run.ID, current.Revision, future); err != nil {
		t.Fatalf("add future edge: %v", err)
	}
	current, err = store.WorkflowRunByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("WorkflowRunByID: %v", err)
	}

	// Frozen: rewire the edge incident to triggered node a.
	rewired := cloneGraph(current.Graph)
	rewired.Edges = []WorkflowEdge{{From: "a", To: "c"}}
	if err := store.EditWorkflowRunGraph(ctx, run.ID, current.Revision, rewired); !errors.Is(err, ErrFrozenNodeEdit) {
		t.Fatalf("rewire edge from triggered node err = %v, want ErrFrozenNodeEdit", err)
	}
	// Frozen: adding a new edge incident to triggered node a.
	added := cloneGraph(current.Graph)
	added.Edges = append(added.Edges, WorkflowEdge{From: "a", To: "c"})
	if err := store.EditWorkflowRunGraph(ctx, run.ID, current.Revision, added); !errors.Is(err, ErrFrozenNodeEdit) {
		t.Fatalf("add edge from triggered node err = %v, want ErrFrozenNodeEdit", err)
	}
	// Stale revision is still the CAS conflict, not a frozen-edit error.
	if err := store.EditWorkflowRunGraph(ctx, run.ID, current.Revision-1, current.Graph); !errors.Is(err, ErrRunConflict) {
		t.Fatalf("stale revision err = %v, want ErrRunConflict", err)
	}
}
