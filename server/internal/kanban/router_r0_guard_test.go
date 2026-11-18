package kanban

import (
	"context"
	"testing"
	"time"
)

func TestRouterPausedAndClosedGuards(t *testing.T) {
	for _, tc := range []struct {
		status string
		reason error
	}{
		{StatusPaused, ErrTaskPaused},
		{StatusSuccess, ErrTaskClosed},
		{StatusCancelled, ErrTaskClosed},
		{StatusFail, ErrTaskClosed},
	} {
		for _, timing := range []string{"before_send", "while_queued"} {
			t.Run(tc.status+"/"+timing, func(t *testing.T) {
				ctx := context.Background()
				store, svc, run, root := newWorkflowCommentFixture(t, "task-guard", "run-guard", workflowCommentTestGraph)
				agent := &promptRecordingRouterAgent{}
				svc.SetRouterConfig(RouterConfig{Mode: RouterModeRoute, IdleClose: time.Hour})
				svc.SetRouterAgent(agent)
				router := svc.RouterServiceForRoot(root.ID)
				t.Cleanup(router.Close)
				activity, err := svc.ActivityServiceForRoot(root.ID)
				if err != nil {
					t.Fatal(err)
				}

				// Hold this lane so the operator can close/pause after enqueue but before consumption.
				lane := router.lane(run.TaskID)
				lane.running = true
				changeStatus := func() {
					t.Helper()
					if err := store.UpdateTaskStatus(ctx, run.TaskID, tc.status, false); err != nil {
						t.Fatal(err)
					}
				}
				if timing == "before_send" {
					changeStatus()
				}
				_, reply, err := activity.CreateUserActivity(ctx, run.TaskID, "补充说明", CommentWorkflowChoiceEmpty, nil)
				if err != nil || reply == nil {
					t.Fatalf("CreateUserActivity reply=%#v err=%v", reply, err)
				}
				if timing == "while_queued" {
					if len(lane.queue) != 1 || reply.Status != ActivityStatusPending {
						t.Fatalf("expected a pending turn in lane: reply=%#v queue=%d", reply, len(lane.queue))
					}
					changeStatus()
				}
				router.drain(run.TaskID, lane)
				if _, called := agent.lastPrompt(); called {
					t.Error("Router called model after task was paused/closed")
				}
				got := reloadTestActivity(t, store, reply.ID)
				if got.Status != ActivityStatusFailed || got.Hidden || got.Summary != tc.reason.Error() {
					t.Errorf("reply=%#v, want visible failed reply with %q", got, tc.reason)
				}
				runs, err := store.ListWorkflowRunsForTask(ctx, run.TaskID)
				if err != nil || len(runs) != 1 || runs[0].ID != run.ID {
					t.Fatalf("runs=%#v err=%v", runs, err)
				}
			})
		}
	}
}

func TestRouterBriefTargetsRespectAgentSessionAndUniqueness(t *testing.T) {
	ctx := context.Background()
	store, _, run, _ := newWorkflowCommentFixture(t, "task-target", "run-target", workflowCommentTestGraph)
	task := Task{ID: run.TaskID, Status: StatusRunning}
	build := startWorkflowAgentNode(t, store, run.ID, "build", "")
	check := func(want int) {
		t.Helper()
		execution := briefActiveExecution(ctx, store, run, task)
		if execution.RunID != run.ID || len(execution.CurrentNodes) != want {
			t.Fatalf("execution=%#v, want original run with %d candidates", execution, want)
		}
		for _, node := range execution.CurrentNodes {
			if !node.SessionPresent {
				t.Fatalf("advertised node without a session: %#v", node)
			}
		}
	}
	check(0)
	startWorkflowAgentNode(t, store, run.ID, "review", "sess-review")
	// Store requires exactly one active Agent before routing implicitly.
	// Dropping only the unbound Agent would incorrectly single out "review".
	check(0)
	if err := store.BindWorkflowNodeSession(ctx, build, "sess-build"); err != nil {
		t.Fatal(err)
	}
	check(2)
}

func TestRouterHumanGateBriefRetainsExecutionWithoutDeliveryTarget(t *testing.T) {
	ctx := context.Background()
	store, svc, run, _ := newWorkflowCommentFixture(t, "task-gate-brief", "run-gate-brief", humanGateGraph())
	if err := svc.AdvanceWorkflowRun(ctx, store, run.TaskID); err != nil {
		t.Fatal(err)
	}
	task, err := store.GetTask(ctx, run.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	brief, err := buildSituationBrief(ctx, store, nil, RouterConfig{}.Normalized(), task, RouterTriggerUserMessage, "")
	if err != nil {
		t.Fatal(err)
	}
	execution := brief.ActiveExecution
	if execution == nil || execution.RunID != run.ID || execution.Status != StatusWaitingUser || len(execution.CurrentNodes) != 0 {
		t.Fatalf("gate execution = %#v, want waiting run without a delivery target", execution)
	}
	if brief.LatestRun == nil || len(brief.LatestRun.WaitingUserNodes) != 1 || brief.LatestRun.WaitingUserNodes[0] != "gate" {
		t.Fatalf("missing approval evidence: %#v", brief.LatestRun)
	}
}
