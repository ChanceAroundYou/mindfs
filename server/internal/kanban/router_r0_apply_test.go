package kanban

import (
	"context"
	"strings"
	"testing"
)

func TestRouterApplyFailedDeliveryFinishesUnusedPlaceholder(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sayFirst bool
	}{
		{"unused", false},
		{"visible_reply_already_applied", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store, svc, run, root := newWorkflowCommentFixture(t, "task-apply", "run-apply", workflowCommentTestGraph)
			activity, err := svc.ActivityServiceForRoot(root.ID)
			if err != nil {
				t.Fatal(err)
			}
			user, err := activity.QueueUserActivity(ctx, run.TaskID, "补充要求", CommentWorkflowChoiceEmpty, nil)
			if err != nil {
				t.Fatal(err)
			}
			task, err := store.GetTask(ctx, run.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			reply := activity.newAgentActivity(task, CommentWorkflowChoiceEmpty)
			if err := activity.activities.Insert(ctx, reply); err != nil {
				t.Fatal(err)
			}
			decision := &RouterDecision{
				ID: "decision-apply", TaskID: task.ID, RootID: root.ID, TurnID: "msg:" + user.ID,
				UserActivityID: user.ID, PlaceholderActivityID: reply.ID, Status: RouterDecisionApplying,
			}
			const say = "正在核实节点状态"
			if tc.sayFirst {
				decision.Actions = append(decision.Actions, RouterAction{Kind: RouterActionSay, Body: say})
			}
			decision.Actions = append(decision.Actions, RouterAction{Seq: len(decision.Actions), Kind: RouterActionDeliver, Body: user.Body})
			if err := NewRouterStore(store.db).InsertDecision(ctx, decision); err != nil {
				t.Fatal(err)
			}
			err = activity.ApplyRouteActions(ctx, decision)
			const reason = "no single active agent node run to deliver to"
			if err == nil || !strings.Contains(err.Error(), reason) {
				t.Fatalf("ApplyRouteActions error = %v, want store rejection", err)
			}
			got := reloadTestActivity(t, store, reply.ID)
			if tc.sayFirst {
				if got.Status != ActivityStatusCompleted || got.Body != say || got.Hidden {
					t.Errorf("previously applied reply changed: %#v", got)
				}
			} else if got.Status != ActivityStatusFailed || !strings.Contains(got.Summary, reason) || got.Hidden {
				t.Errorf("unused reply=%#v, want visible failed reply explaining the rejection", got)
			}
			runs, err := store.ListWorkflowRunsForTask(ctx, task.ID)
			if err != nil || len(runs) != 1 || runs[0].ID != run.ID {
				t.Fatalf("runs=%#v err=%v", runs, err)
			}
			nodes, err := store.ListWorkflowNodeRuns(ctx, run.ID)
			if err != nil || len(nodes) != 0 {
				t.Fatalf("unexpected execution: nodes=%#v err=%v", nodes, err)
			}
			requests, err := store.ListOpenWorkflowComments(ctx, task.ID)
			if err != nil || len(requests) != 0 {
				t.Fatalf("unexpected delivery: requests=%#v err=%v", requests, err)
			}
		})
	}
}
