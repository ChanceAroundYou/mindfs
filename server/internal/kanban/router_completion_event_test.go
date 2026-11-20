package kanban

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// completionBlockedEvents 只取 completion_blocked 事件，避免把同一任务里
// 其它来源的事件算进来。
func completionBlockedEvents(t *testing.T, f *completionFixture) []RouterEvent {
	t.Helper()
	events, err := NewRouterStore(f.store.db).ListEventsByTask(context.Background(), f.task.ID)
	must(t, err, "ListEventsByTask")
	var out []RouterEvent
	for _, event := range events {
		if event.Kind == RouterEventCompletionBlocked {
			out = append(out, event)
		}
	}
	return out
}

func blockCompletionOnce(t *testing.T, f *completionFixture, decisionID string) {
	t.Helper()
	if err := f.svc.applyCompleteTask(context.Background(), f.store, f.task,
		completionDecision(f, decisionID), completionAction("还没做完")); err != nil {
		t.Fatalf("applyCompleteTask(%s): %v", decisionID, err)
	}
}

func TestCompletionBlockedEventDeduped(t *testing.T) {
	f := newCompletionFixture(t, nodeDoneContract())
	f.addAttempt(t, "build", NodeRunFail, map[string]any{"outcome": "blocked"}, "")

	blockCompletionOnce(t, f, "dec-block-1")
	blockCompletionOnce(t, f, "dec-block-2")

	events := completionBlockedEvents(t, f)
	if len(events) != 1 {
		t.Fatalf("completion_blocked events = %d, want 1: the same gap set must dedupe", len(events))
	}
	want := "completion_blocked:" + f.run.ID + ":"
	if !strings.HasPrefix(events[0].InstanceKey, want) {
		t.Fatalf("instance key = %q, want prefix %q", events[0].InstanceKey, want)
	}
	if events[0].SourceDecisionID != "dec-block-1" {
		t.Fatalf("source decision = %q, want the first decision kept", events[0].SourceDecisionID)
	}
}

// TestCompletionBlockedNewGapIsNewEvent 钉住唯一键的另一半：缺口**变了**就是
// 新事实，必须再落一条。只有「同缺口去重」被钉住时，把哈希从键里删掉测试仍
// 全绿 —— 那样补做完一项之后再也不会触发补做。
func TestCompletionBlockedNewGapIsNewEvent(t *testing.T) {
	f := newCompletionFixture(t, &CompletionContract{Requirements: []CompletionRequirement{
		{ID: "build-ran", Kind: CompletionKindNodeDone, NodeID: "build"},
		{ID: "build-summary", Kind: CompletionKindNodeDone, NodeID: "build", Keys: []string{"summary"}},
	}})
	f.addAttempt(t, "build", NodeRunFail, map[string]any{"outcome": "blocked"}, "")
	blockCompletionOnce(t, f, "dec-gap-1")
	if events := completionBlockedEvents(t, f); len(events) != 1 {
		t.Fatalf("events after first block = %d, want 1", len(events))
	}

	// 补做后节点成功，但产出里仍缺 summary：缺口从 {build-ran, build-summary}
	// 收窄到 {build-summary}。
	f.addAttempt(t, "build", NodeRunSuccess, map[string]any{"outcome": "done"}, "")
	blockCompletionOnce(t, f, "dec-gap-2")

	events := completionBlockedEvents(t, f)
	if len(events) != 2 {
		t.Fatalf("events after the gap set changed = %d, want 2: a narrower gap set is a new fact", len(events))
	}
	if events[1].SourceDecisionID != "dec-gap-2" {
		t.Fatalf("second event decision = %q, want dec-gap-2", events[1].SourceDecisionID)
	}
}

func TestCompletionBlockedAllowsEmptyRunID(t *testing.T) {
	ctx := context.Background()
	f := newCompletionFixture(t, nodeDoneContract())
	store := NewRouterStore(f.store.db)

	if _, _, err := store.InsertEvent(ctx, RouterEvent{
		RootID: f.root.ID, TaskID: f.task.ID, Kind: RouterEventCompletionBlocked,
		InstanceKey: "completion_blocked:" + f.task.ID + ":abc",
	}); err != nil {
		t.Fatalf("completion_blocked without a run id must insert (direct tasks have no run): %v", err)
	}
	if _, _, err := store.InsertEvent(ctx, RouterEvent{
		RootID: f.root.ID, TaskID: f.task.ID, Kind: RouterEventRunSettled,
		InstanceKey: "run-c",
	}); err == nil {
		t.Fatal("run_settled without a run id must still be rejected")
	}
}

func TestCompletionBlockedPayloadAvoidsForbiddenSubstrings(t *testing.T) {
	f := newCompletionFixture(t, nodeDoneContract())
	f.addAttempt(t, "build", NodeRunFail, map[string]any{"outcome": "blocked"}, "")

	blockCompletionOnce(t, f, "dec-block-payload")

	events := completionBlockedEvents(t, f)
	if len(events) != 1 {
		t.Fatalf("completion_blocked events = %d, want 1", len(events))
	}
	var payload struct {
		TaskID      string `json:"task_id"`
		RunID       string `json:"run_id"`
		Items       []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
			Pass bool   `json:"pass"`
		} `json:"items"`
		RepairsUsed int `json:"repairs_used"`
		RepairsMax  int `json:"repairs_max"`
	}
	if err := json.Unmarshal([]byte(events[0].PayloadJSON), &payload); err != nil {
		t.Fatalf("payload is not json: %v (%s)", err, events[0].PayloadJSON)
	}
	if payload.TaskID != f.task.ID || payload.RunID != f.run.ID {
		t.Fatalf("payload ids = %q/%q, want %q/%q", payload.TaskID, payload.RunID, f.task.ID, f.run.ID)
	}
	if len(payload.Items) != 1 || payload.Items[0].ID != "build-done" || payload.Items[0].Kind != CompletionKindNodeDone || payload.Items[0].Pass {
		t.Fatalf("payload items = %+v", payload.Items)
	}
	if payload.RepairsUsed != 1 || payload.RepairsMax != 2 {
		t.Fatalf("payload repairs = %d/%d, want 1/2", payload.RepairsUsed, payload.RepairsMax)
	}
	for _, forbidden := range []string{"output", "session", "git", "transcript"} {
		if strings.Contains(strings.ToLower(events[0].PayloadJSON), forbidden) {
			t.Fatalf("payload leaks %q: %s", forbidden, events[0].PayloadJSON)
		}
	}
}

func TestCompletionBlockedTurnCanRetryEvidenceFailedNode(t *testing.T) {
	brief := &SituationBrief{
		Task:    RouterBriefTask{ID: "task-c", Status: StatusWaitingUser},
		Trigger: RouterBriefTrigger{Kind: RouterTriggerCompletionBlocked},
		Supervision: &SupervisionObservation{
			Mode:              RouterSupervisionOn,
			RunID:             "run-c",
			MaxRetriesPerNode: 2,
			ActionBudget:      6,
			EvidenceFailedNodes: []SupervisionNodeState{{
				NodeID: "build", NodeRunID: "run-c-build-1", NodeKind: "agent",
				Status: NodeRunFail, Attempt: 3, FailureKind: SupervisionFailureEvidenceFailed,
			}},
		},
	}
	actions := []RouterAction{{Kind: RouterActionRetryNode, NodeRunID: "run-c-build-1", Reason: "build"}}
	if err := validateRouterActions(brief, actions, RouterConfig{}.Normalized()); err != nil {
		t.Fatalf("retry_node on an evidence-failed node must be allowed: %v", err)
	}
}

func TestCompletionBlockedBudgetExhaustedStops(t *testing.T) {
	f := newCompletionFixture(t, nodeDoneContract())
	f.addAttempt(t, "build", NodeRunFail, map[string]any{"outcome": "blocked"}, "")

	for _, id := range []string{"dec-ex-1", "dec-ex-2", "dec-ex-3"} {
		blockCompletionOnce(t, f, id)
	}

	if events := completionBlockedEvents(t, f); len(events) != 1 {
		t.Fatalf("completion_blocked events = %d, want 1 (same gaps dedupe)", len(events))
	}
	if rows := completionSystemRows(t, f, "completion-blocked:"); len(rows) != 2 {
		t.Fatalf("blocked rows = %d, want 2 before the budget runs out", len(rows))
	}
	exhausted := completionSystemRows(t, f, "completion-exhausted:")
	if len(exhausted) != 1 {
		t.Fatalf("exhausted rows = %d, want 1", len(exhausted))
	}
	if !strings.Contains(exhausted[0].Body, "需要你介入") {
		t.Fatalf("exhausted body = %q, want it to ask the user to step in", exhausted[0].Body)
	}
	completion, found, err := f.store.GetTaskCompletion(context.Background(), f.task.ID)
	must(t, err, "GetTaskCompletion")
	if !found || completion.RepairsUsed != 3 || completion.Status != CompletionStatusBlocked {
		t.Fatalf("completion = %+v found=%v, want blocked with 3 repairs used", completion, found)
	}
}

func TestCompletionBlockedBriefCarriesGaps(t *testing.T) {
	ctx := context.Background()
	f := newCompletionFixture(t, nodeDoneContract())
	f.addAttempt(t, "build", NodeRunFail, map[string]any{"outcome": "blocked"}, "")
	blockCompletionOnce(t, f, "dec-brief")

	completion := f.svc.routerBriefCompletion(ctx, f.store, f.task, f.run)
	if completion == nil || !completion.Present {
		t.Fatalf("brief completion = %+v, want present", completion)
	}
	if completion.RepairsUsed != 1 || completion.RepairsMax != 2 || completion.CanComplete {
		t.Fatalf("brief completion = %+v, want repairs 1/2 and not completable", completion)
	}
	if len(completion.Items) != 1 {
		t.Fatalf("brief items = %+v, want the single failing requirement", completion.Items)
	}
	item := completion.Items[0]
	if item.ID != "build-done" || item.Kind != CompletionKindNodeDone || item.Pass || item.Index != 1 {
		t.Fatalf("brief item = %+v", item)
	}
}
