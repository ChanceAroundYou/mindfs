package kanban

import (
	"context"
	"strings"
	"sync"
	"testing"
)

type fakeCardNotifier struct {
	mu      sync.Mutex
	notices []CardNotice
	err     error
}

func (f *fakeCardNotifier) NotifyCard(_ context.Context, n CardNotice) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notices = append(f.notices, n)
	return f.err
}

func (f *fakeCardNotifier) byKind(kind string) []CardNotice {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []CardNotice
	for _, n := range f.notices {
		if n.Kind == kind {
			out = append(out, n)
		}
	}
	return out
}

// A waiting human gate is a decision a person must make away from the
// keyboard, so it becomes exactly one card — and advancing the same run
// again must not make a second one.
func TestHumanGateWaitingNotifiesCardOnce(t *testing.T) {
	ctx := context.Background()
	graph := WorkflowDefinition{
		Nodes: []WorkflowNode{
			{ID: "gate", Kind: NodeKindHumanGate, Params: map[string]any{"prompt": "确认可以上线"}},
			{ID: "build", Kind: NodeKindAgent, Params: map[string]any{"prompt": "执行上线"}},
		},
		Edges: []WorkflowEdge{{From: "gate", To: "build"}},
	}
	store, svc, run, _ := newWorkflowCommentFixture(t, "wf-card-gate-task", "wf-card-gate-run", graph)
	notifier := &fakeCardNotifier{}
	svc.SetCardNotifier(notifier)
	svc.SetWorkflowNodeRunner(newRuntimeRunner(0))

	if err := svc.AdvanceWorkflowRun(ctx, store, run.TaskID); err != nil {
		t.Fatalf("AdvanceWorkflowRun: %v", err)
	}
	gate := notifier.byKind(CardKindGate)
	if len(gate) != 1 {
		t.Fatalf("gate notices = %+v", gate)
	}
	row, err := store.WorkflowNodeRunByKey(ctx, run.ID, "gate", "", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if want := "gate-" + row.ID; gate[0].ID != want {
		t.Fatalf("card id = %q, want %q", gate[0].ID, want)
	}
	if gate[0].TaskID != run.TaskID || gate[0].WorkflowNodeID != "gate" || gate[0].Prompt != "确认可以上线" {
		t.Fatalf("notice = %+v", gate[0])
	}
	if len(gate[0].Options) != 2 || gate[0].Options[0] != "批准" || gate[0].Options[1] != "驳回" {
		t.Fatalf("options = %v", gate[0].Options)
	}

	if err := svc.AdvanceWorkflowRun(ctx, store, run.TaskID); err != nil {
		t.Fatalf("second AdvanceWorkflowRun: %v", err)
	}
	if got := notifier.byKind(CardKindGate); len(got) != 1 {
		t.Fatalf("gate notified twice: %+v", got)
	}
}

// A Router question stops the task until someone answers; the card carries
// the question's activity id so a click can be written back to it.
func TestRouterAskUserNotifiesCard(t *testing.T) {
	f := newRouteFixture(t, RouterModeRoute)
	notifier := &fakeCardNotifier{}
	f.svc.SetCardNotifier(notifier)
	activity, err := f.svc.ActivityServiceForRoot(f.rootID)
	if err != nil {
		t.Fatal(err)
	}
	f.agent.calls = []scriptedRouterCall{{
		tool: "router_ask_user",
		args: map[string]any{"question": "要改哪个登录入口？", "options": []any{"Web", "App"}},
	}}
	ctx := context.Background()
	_, placeholder, err := activity.CreateUserActivity(ctx, "task-1", "改登录", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	f.waitDecision(t, RouterDecisionApplied)

	ask := notifier.byKind(CardKindAsk)
	if len(ask) != 1 {
		t.Fatalf("ask notices = %+v", ask)
	}
	if want := "ask-" + placeholder.ID; ask[0].ID != want {
		t.Fatalf("card id = %q, want %q", ask[0].ID, want)
	}
	if ask[0].QuestionID != placeholder.ID || !strings.Contains(ask[0].Prompt, "要改哪个登录入口") {
		t.Fatalf("notice = %+v", ask[0])
	}
	if len(ask[0].Options) != 2 || ask[0].Options[0] != "Web" {
		t.Fatalf("options = %v", ask[0].Options)
	}
}

// Only terminal states are worth a notification. waiting_user is the normal
// "your turn" state and already shows in the UI; notifying it would page
// someone on every single turn.
func TestTerminalTaskNotifiesNoticeButWaitingUserDoesNot(t *testing.T) {
	ctx := context.Background()
	_, svc, run, root := newWorkflowCommentFixture(t, "wf-card-notice-task", "wf-card-notice-run", workflowCommentTestGraph)
	notifier := &fakeCardNotifier{}
	svc.SetCardNotifier(notifier)

	if _, err := svc.setTaskStatus(ctx, root.ID, run.TaskID, StatusWaitingUser, "waiting", "等人回话", false); err != nil {
		t.Fatalf("setTaskStatus(waiting_user): %v", err)
	}
	if got := notifier.byKind(CardKindNotice); len(got) != 0 {
		t.Fatalf("waiting_user produced a notice: %+v", got)
	}

	if _, err := svc.setTaskStatus(ctx, root.ID, run.TaskID, StatusSuccess, "completed", "全部节点通过", true); err != nil {
		t.Fatalf("setTaskStatus(success): %v", err)
	}
	got := notifier.byKind(CardKindNotice)
	if len(got) != 1 {
		t.Fatalf("terminal notices = %+v", got)
	}
	if got[0].TaskID != run.TaskID || !strings.Contains(got[0].ID, run.TaskID) {
		t.Fatalf("notice = %+v", got[0])
	}
	if got[0].Status != StatusSuccess || got[0].Summary != "全部节点通过" {
		t.Fatalf("notice = %+v", got[0])
	}

	// A task already terminal that is written terminal again is not news.
	if _, err := svc.setTaskStatus(ctx, root.ID, run.TaskID, StatusSuccess, "completed", "全部节点通过", true); err != nil {
		t.Fatalf("second setTaskStatus: %v", err)
	}
	if got := notifier.byKind(CardKindNotice); len(got) != 1 {
		t.Fatalf("terminal notified twice: %+v", got)
	}
}

// A channel failure must never surface as a task error: Feishu being down is
// not a reason for a workflow to fail.
func TestCardNotifierFailureDoesNotFailTaskMove(t *testing.T) {
	ctx := context.Background()
	_, svc, run, root := newWorkflowCommentFixture(t, "wf-card-err-task", "wf-card-err-run", workflowCommentTestGraph)
	svc.SetCardNotifier(&fakeCardNotifier{err: context.DeadlineExceeded})
	if _, err := svc.setTaskStatus(ctx, root.ID, run.TaskID, StatusSuccess, "completed", "done", true); err != nil {
		t.Fatalf("notifier failure leaked into task move: %v", err)
	}
}
