package kanban

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// RED tests for WP-05/06 — written first, implementation follows.

func wfNode(id, kind string) WorkflowNode {
	return WorkflowNode{ID: id, Name: id, Kind: kind, Params: map[string]any{}}
}

func wfDef(nodes ...WorkflowNode) WorkflowDefinition {
	return WorkflowDefinition{Nodes: nodes}
}

func TestWorkflowValidateRejectsCycle(t *testing.T) {
	d := wfDef(wfNode("a", NodeKindAgent), wfNode("b", NodeKindAgent))
	d.Edges = []WorkflowEdge{{From: "a", To: "b"}, {From: "b", To: "a"}}
	if err := ValidateWorkflowDefinition(d); err == nil {
		t.Fatal("expected cycle rejection")
	}
}

func TestWorkflowValidateRejectsNestedLoop(t *testing.T) {
	// inner graph containing a Loop node must be rejected
	inner := wfDef(WorkflowNode{ID: "inner-l", Name: "inner", Kind: NodeKindLoop, Params: map[string]any{"max_iterations": 3}})
	loop := wfNode("l", NodeKindLoop)
	loop.Params["max_iterations"] = 5
	loop.Params["graph"] = inner
	d := wfDef(loop)
	if err := ValidateWorkflowDefinition(d); err == nil {
		t.Fatal("expected nested loop rejection")
	}
}

func TestLoopMaxIterationsBounds(t *testing.T) {
	for _, n := range []int{0, 21, -1} {
		d := wfDef(WorkflowNode{ID: "l", Name: "l", Kind: NodeKindLoop, Params: map[string]any{"max_iterations": n}})
		if err := ValidateWorkflowDefinition(d); err == nil {
			t.Fatalf("expected rejection for max_iterations=%d", n)
		}
	}
	d := wfDef(WorkflowNode{ID: "l", Name: "l", Kind: NodeKindLoop, Params: map[string]any{"max_iterations": 5}})
	if err := ValidateWorkflowDefinition(d); err != nil {
		t.Fatalf("valid loop rejected: %v", err)
	}
}

func TestSafeExpressionReadsOnlyJSONFields(t *testing.T) {
	out := map[string]any{"result": map[string]any{"score": float64(90)}, "status": "ok"}
	// allowed: field comparison
	v, err := EvalSafeExpression("result.score >= 80", out)
	if err != nil || v != true {
		t.Fatalf("field expr = %v,%v want true,nil", v, err)
	}
	// forbidden: eval/script/network tokens and function calls
	for _, bad := range []string{`exec("rm")`, "fetch(url)", "eval(x)", "result.score; drop", "system(cmd)"} {
		if _, err := EvalSafeExpression(bad, out); err == nil {
			t.Fatalf("expected rejection for %q", bad)
		}
	}
}

func TestEngineConditionSelectsRouteAndJoinSkipsUnselected(t *testing.T) {
	// a(cond routes) -> b (route "hit") -> join ; a -> c (route "miss") -> join
	cond := WorkflowNode{ID: "c", Name: "c", Kind: NodeKindCondition,
		Params: map[string]any{"expression": "result.score >= 80", "routes": []any{"hit", "miss"}}}
	d := WorkflowDefinition{Nodes: []WorkflowNode{
		wfNode("start", NodeKindNotify), cond, wfNode("hit", NodeKindNotify), wfNode("miss", NodeKindNotify), wfNode("join", NodeKindNotify),
	}}
	d.Edges = []WorkflowEdge{
		{From: "start", To: "c"},
		{From: "c", To: "hit", Route: "hit"},
		{From: "c", To: "miss", Route: "miss"},
		{From: "hit", To: "join"},
		{From: "miss", To: "join"},
	}
	run := &WorkflowRun{ID: "r1", TaskID: "t1", Revision: 1, Graph: d}
	// simulate: start done, cond evaluated with score 90 -> hit selected
	run.NodeStates = map[string]string{"start": NodeRunSuccess, "c": NodeRunSuccess}
	run.NodeOutputs = map[string]map[string]any{"c": {"result": map[string]any{"score": float64(90)}}}
	run.SelectedRoutes = map[string]string{"c": "hit"}
	// hit ready, miss must be skipped not blocking
	ready, skipped, err := EngineReadyNodes(run)
	if err != nil {
		t.Fatal(err)
	}
	if !wfContains(ready, "hit") || !wfContains(skipped, "miss") {
		t.Fatalf("ready=%v skipped=%v", ready, skipped)
	}
	// complete hit -> join ready, miss remains skipped
	run.NodeStates["hit"] = NodeRunSuccess
	ready, _, err = EngineReadyNodes(run)
	if err != nil {
		t.Fatal(err)
	}
	if !wfContains(ready, "join") {
		t.Fatalf("join should be ready after hit success, ready=%v", ready)
	}
}

func TestEngineLoopExceedsMaxNeedsUser(t *testing.T) {
	run := &WorkflowRun{ID: "r2", TaskID: "t1", Revision: 1,
		Graph:        wfDef(WorkflowNode{ID: "l", Name: "l", Kind: NodeKindLoop, Params: map[string]any{"max_iterations": 2}}),
		NodeStates:   map[string]string{},
		LoopState:    map[string]*LoopState{"l": {Iteration: 2, MaxIterations: 2}},
		LoopExitExpr: map[string]string{"l": "result.done == true"},
	}
	st, err := EngineEvaluateLoop(run, "l", map[string]any{"result": map[string]any{"done": false}})
	if err != nil {
		t.Fatal(err)
	}
	if st != NodeRunWaitingUser {
		t.Fatalf("expected waiting_user (needs-user) got %s", st)
	}
}

func TestDraftCASAndPublishRevisionPin(t *testing.T) {
	s := newWorkflowStore(t.TempDir())
	ctx := context.Background()
	d, err := s.CreateDraft(ctx, "wf1", "", "agent", wfDef(wfNode("a", NodeKindAgent)))
	if err != nil {
		t.Fatal(err)
	}
	if d.ResolvedRevision() != "r1" {
		t.Fatalf("first revision = %s", d.ResolvedRevision())
	}
	// CAS success
	d2, err := s.UpdateDraft(ctx, "wf1", d.ResolvedRevision(), "agent", wfDef(wfNode("a", NodeKindAgent), wfNode("b", NodeKindNotify)))
	if err != nil {
		t.Fatal(err)
	}
	if d2.ResolvedRevision() == "r1" {
		t.Fatal("revision did not advance")
	}
	// CAS conflict with stale base
	if _, err := s.UpdateDraft(ctx, "wf1", "r1", "agent", wfDef()); err == nil {
		t.Fatal("expected CAS conflict")
	}
	// publish must pin exact revision; stale pin rejected
	if _, err := s.PublishDraft(ctx, "wf1", "r1", "user"); err == nil {
		t.Fatal("expected stale publish rejection")
	}
	v, err := s.PublishDraft(ctx, "wf1", d2.ResolvedRevision(), "user")
	if err != nil {
		t.Fatal(err)
	}
	if v.WorkflowID != "wf1" || v.Version == "" {
		t.Fatalf("bad version %+v", v)
	}
	// version immutable: publishing again creates new snapshot, doesn't mutate old
	if _, err := s.GetVersion(ctx, v.Version); err != nil {
		t.Fatal(err)
	}
}

func TestRunRevisionCASAndFrozenNodes(t *testing.T) {
	s := newWorkflowStore(t.TempDir())
	ctx := context.Background()
	if _, err := s.CreateDraft(ctx, "wf-r", "", "agent", wfDef(wfNode("a", NodeKindAgent), wfNode("b", NodeKindNotify))); err != nil {
		t.Fatal(err)
	}
	ver, err := s.PublishDraft(ctx, "wf-r", "r1", "user")
	if err != nil {
		t.Fatal(err)
	}
	_ = ver
	// Build run directly via store helper
	run, err := s.CreateRun(ctx, "task-1", ver.Version, ver.Graph)
	if err != nil {
		t.Fatal(err)
	}
	// mark a triggered
	run.NodeStates["a"] = NodeRunQueued
	if err := s.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	// edit trying to modify triggered node a -> rejected; edit untriggered b -> ok
	edited := run.Graph
	edited.Nodes = append([]WorkflowNode{}, edited.Nodes...)
	for i := range edited.Nodes {
		if edited.Nodes[i].ID == "a" {
			edited.Nodes[i].Name = "changed"
		}
	}
	if err := s.EditRunGraph(ctx, run.ID, run.Revision, edited); err == nil {
		t.Fatal("expected frozen node rejection")
	}
	// CAS conflict
	if err := s.EditRunGraph(ctx, run.ID, 999, run.Graph); err == nil {
		t.Fatal("expected revision conflict")
	}
}

func TestRunSnapshotInlineNoControlLookup(t *testing.T) {
	// Snapshot is embedded in run.Graph; deserializing run alone must suffice.
	run := &WorkflowRun{ID: "r", TaskID: "t", Revision: 1, Graph: wfDef(wfNode("a", NodeKindAgent))}
	b, _ := json.Marshal(run)
	var back WorkflowRun
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Graph.Nodes) != 1 {
		t.Fatal("snapshot not inlined")
	}
}

func TestProjectStatusFromRun(t *testing.T) {
	run := &WorkflowRun{Graph: wfDef(wfNode("a", NodeKindHumanGate)), NodeStates: map[string]string{"a": NodeRunWaitingUser}}
	if ProjectStatusFromRun(run) != StatusWaitingUser {
		t.Fatalf("human gate should project waiting_user got %s", ProjectStatusFromRun(run))
	}
	run.NodeStates["a"] = NodeRunSuccess
	if ProjectStatusFromRun(run) != StatusSuccess {
		t.Fatalf("all success should project success got %s", ProjectStatusFromRun(run))
	}
	// legacy untouched: no run -> empty projection
	if ProjectStatusFromRun(nil) != "" {
		t.Fatal("nil run should project empty (legacy drives status)")
	}
}

func TestWriterGateInterfaceDefined(t *testing.T) {
	// A4 not yet integrated: interface must exist, default impl serializes per task.
	var _ WriterGate = NewSerialWriterGate()
	g := NewSerialWriterGate()
	if g.Acquire("task1", "wt1") != nil {
		t.Fatal("acquire failed")
	}
	if err := g.Acquire("task1", "wt1"); err == nil {
		t.Fatal("same gate should block second acquire")
	}
	g.Release("task1", "wt1")
	// After release, a different worktree may acquire (per-task serial default
	// only blocks while held; A4 will make this per-worktree).
	if err := g.Acquire("task1", "wt2"); err != nil {
		t.Fatalf("acquire after release should succeed: %v", err)
	}
	g.Release("task1", "wt2")
}

func TestSuggestTemplateFromSuccess(t *testing.T) {
	run := &WorkflowRun{ID: "r9", TaskID: "t9", Graph: wfDef(wfNode("a", NodeKindAgent)), NodeStates: map[string]string{"a": NodeRunSuccess}}
	sug := SuggestTemplateFromRun(run, "user")
	if sug.Published {
		t.Fatal("suggestion must not auto-publish")
	}
	if sug.SourceRunID != "r9" {
		t.Fatalf("bad suggestion %+v", sug)
	}
	if !strings.Contains(sug.Note, "confirm") {
		t.Fatalf("suggestion should note user confirmation: %s", sug.Note)
	}
}

func newWorkflowStore(dir string) *WorkflowStore {
	return &WorkflowStore{dir: dir, now: func() time.Time { return time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC) }}
}

func wfContains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestSaveRunCASConflict(t *testing.T) {
	s := newWorkflowStore(t.TempDir())
	ctx := context.Background()
	if _, err := s.CreateDraft(ctx, "wf-cas", "", "agent", wfDef(wfNode("a", NodeKindAgent))); err != nil {
		t.Fatal(err)
	}
	ver, err := s.PublishDraft(ctx, "wf-cas", "r1", "user")
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateRun(ctx, "task-1", ver.Version, ver.Graph)
	if err != nil {
		t.Fatal(err)
	}
	// Two readers get the same revision
	a, err := s.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	a.NodeStates["a"] = NodeRunRunning
	if err := s.SaveRun(ctx, a); err != nil {
		t.Fatal(err)
	}
	// b still holds the old revision -> must conflict, not overwrite
	b.NodeStates["a"] = NodeRunSuccess
	if err := s.SaveRun(ctx, b); err == nil {
		t.Fatal("expected ErrRunConflict for stale revision write")
	} else if err != ErrRunConflict {
		t.Fatalf("expected ErrRunConflict, got %v", err)
	}
	// GetRun reflects the accepted write
	after, _ := s.GetRun(ctx, run.ID)
	if after.NodeStates["a"] != NodeRunRunning {
		t.Fatalf("stale write overwrote state: %v", after.NodeStates)
	}
}
