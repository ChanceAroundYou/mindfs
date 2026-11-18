package kanban

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// Interrupt a real Router turn after its brief was built, before returning the scripted decision.
type changingTaskRouterAgent struct {
	scriptedRouterAgent
	beforeAction func(context.Context) error
}

func (a *changingTaskRouterAgent) RunRouterTurn(ctx context.Context, req RouterAgentRequest) (RouterAgentResult, error) {
	if err := a.beforeAction(ctx); err != nil {
		return RouterAgentResult{}, err
	}
	return a.scriptedRouterAgent.RunRouterTurn(ctx, req)
}

func TestRouterApplyRechecksStateAfterModel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tool   string
		status string
		cause  error
	}{
		{"paused_before_delivery", "router_deliver_to_active_run", StatusPaused, ErrTaskPaused},
		{"completed_before_say", "router_say", StatusSuccess, ErrTaskClosed},
		{"human_gate_before_direct", "router_message_agent", StatusWaitingUser, ErrWorkflowWaitingApproval},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			graph := workflowCommentTestGraph
			if tc.status == StatusWaitingUser {
				graph = humanGateGraph()
			}
			store, svc, run, root := newWorkflowCommentFixture(t, "task-apply-guard", "run-apply-guard", graph)
			const summary = "操作员验收的结果"
			switch tc.status {
			case StatusPaused:
				startWorkflowAgentNode(t, store, run.ID, "build", "sess-build")
				if err := store.UpdateTaskStatus(ctx, run.TaskID, StatusRunning, false); err != nil {
					t.Fatal(err)
				}
			case StatusSuccess:
				if err := store.UpdateTaskResultSummary(ctx, run.TaskID, summary); err != nil {
					t.Fatal(err)
				}
			}
			beforeAction := func(ctx context.Context) error {
				switch tc.status {
				case StatusPaused:
					return svc.PauseWorkflowRun(ctx, root.ID, run.TaskID, "人工暂停")
				case StatusSuccess:
					_, err := svc.Complete(ctx, MoveInput{RootID: root.ID, TaskID: run.TaskID})
					return err
				default:
					return svc.AdvanceWorkflowRun(ctx, store, run.TaskID)
				}
			}
			agent := &changingTaskRouterAgent{beforeAction: beforeAction}
			agent.calls = []scriptedRouterCall{{tool: tc.tool, args: map[string]any{"body": "迟到的 Router 回复"}}}
			direct := &fakeDirectExecutor{done: make(chan struct{}), reply: "直连执行"}
			svc.SetDirectExecutor(direct)
			svc.SetRouterConfig(RouterConfig{Mode: RouterModeRoute, IdleClose: time.Hour})
			svc.SetRouterAgent(agent)
			router := svc.RouterServiceForRoot(root.ID)
			t.Cleanup(router.Close)
			t.Cleanup(func() { waitRouterLaneIdle(t, router, run.TaskID, false) })
			activity, err := svc.ActivityServiceForRoot(root.ID)
			if err != nil {
				t.Fatal(err)
			}
			user, reply, err := activity.CreateUserActivity(ctx, run.TaskID, "补充", CommentWorkflowChoiceEmpty, nil)
			if err != nil {
				t.Fatal(err)
			}
			decision := waitRouteDecisionTerminal(t, store, run.TaskID)
			waitRouterLaneIdle(t, router, run.TaskID, true)
			if err := svc.WaitActivityTurnsIdle(ctx); err != nil {
				t.Fatal(err)
			}
			if decision.Status != RouterDecisionFailed || !strings.Contains(decision.Error, tc.cause.Error()) {
				t.Errorf("decision=%s error=%q, want failed with %q", decision.Status, decision.Error, tc.cause)
			}
			got := reloadTestActivity(t, store, reply.ID)
			if got.Status != ActivityStatusFailed || got.Hidden || !strings.Contains(got.Summary, tc.cause.Error()) {
				t.Errorf("reply status=%s hidden=%t summary=%q", got.Status, got.Hidden, got.Summary)
			}
			task, err := store.GetTask(ctx, run.TaskID)
			if err != nil || task.Status != tc.status {
				t.Fatalf("task status=%s err=%v", task.Status, err)
			}
			runs, err := store.ListWorkflowRunsForTask(ctx, run.TaskID)
			if err != nil || len(runs) != 1 || runs[0].ID != run.ID {
				t.Fatalf("runs=%#v err=%v", runs, err)
			}
			switch tc.status {
			case StatusPaused:
				requests, err := store.ListOpenWorkflowComments(ctx, run.TaskID)
				if err != nil || len(requests) != 0 || reloadTestActivity(t, store, user.ID).NodeRunID != "" {
					t.Errorf("paused task accepted a binding: requests=%#v err=%v", requests, err)
				}
				// Exercise the transaction directly even if the service guard already refused.
				if _, err := store.DeliverActivityToActiveNodeRun(ctx, run.TaskID, user.ID, ""); !errors.Is(err, ErrTaskPaused) {
					t.Errorf("binding transaction error=%v, want ErrTaskPaused", err)
				}
			case StatusSuccess:
				if task.ResultSummary != summary {
					t.Errorf("terminal result changed to %q", task.ResultSummary)
				}
			case StatusWaitingUser:
				gate, err := store.WorkflowNodeRunByKey(ctx, run.ID, "gate", "", 0, 1)
				if err != nil || gate.Status != NodeRunWaitingUser {
					t.Fatalf("gate=%#v err=%v", gate, err)
				}
				if direct.got != "" {
					t.Fatalf("Router started direct execution at gate: %q", direct.got)
				}
				_, _, err = activity.CreateUserActivity(ctx, task.ID, "显式纯对话", CommentWorkflowChoiceDisabled, nil)
				if err != nil {
					t.Fatal(err)
				}
				if err := svc.WaitActivityTurnsIdle(ctx); err != nil || direct.got != "显式纯对话" {
					t.Fatalf("explicit direct got=%q err=%v", direct.got, err)
				}
			}
		})
	}
}

// Complete the task when the first reply becomes visible, before its summary
// projection or the second action can write.
type completeAfterReply struct {
	ActivityBroadcaster
	complete func()
}

func (b completeAfterReply) ActivityUpdated(a *TaskActivity, _ map[string]any) {
	if a.Status == ActivityStatusCompleted && a.RouterDecisionID != "" && a.Ordinal == 0 {
		b.complete()
	}
}

func TestRouterCompletionBetweenAppliedRepliesKeepsFirstEffect(t *testing.T) {
	ctx := context.Background()
	f := newRouteFixture(t, RouterModeRoute)
	const summary = "已验收"
	if err := f.store.UpdateTaskResultSummary(ctx, "task-1", summary); err != nil {
		t.Fatal(err)
	}
	f.agent.calls = []scriptedRouterCall{
		{tool: "router_say", args: map[string]any{"body": "第一条"}},
		{tool: "router_say", args: map[string]any{"body": "第二条"}},
	}
	var completeErr error
	f.svc.SetActivityBroadcaster(completeAfterReply{
		ActivityBroadcaster: &recordingActivityBroadcaster{},
		complete: func() {
			_, completeErr = f.svc.Complete(ctx, MoveInput{RootID: f.rootID, TaskID: "task-1"})
		},
	})
	_, reply, err := f.activity.CreateUserActivity(ctx, "task-1", "汇报", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatal(err)
	}
	decision := waitRouteDecisionTerminal(t, f.store, "task-1")
	waitRouterLaneIdle(t, f.activity.router, "task-1", true)
	if completeErr != nil {
		t.Fatal(completeErr)
	}
	if decision.Status != RouterDecisionFailed || !strings.Contains(decision.Error, ErrTaskClosed.Error()) {
		t.Errorf("decision=%s error=%q", decision.Status, decision.Error)
	}
	task, err := f.store.GetTask(ctx, "task-1")
	if err != nil || task.Status != StatusSuccess || task.ResultSummary != summary {
		t.Errorf("status=%q summary=%q err=%v", task.Status, task.ResultSummary, err)
	}
	rows, err := f.activity.activities.List(ctx, "task-1")
	if err != nil || len(rows) != 2 {
		t.Fatalf("visible activities=%d err=%v, want user and original reply", len(rows), err)
	}
	got := reloadTestActivity(t, f.store, reply.ID)
	if got.Status != ActivityStatusCompleted || got.Body != "第一条" {
		t.Errorf("first effect status=%q body=%q", got.Status, got.Body)
	}
}
