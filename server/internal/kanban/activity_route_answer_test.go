package kanban

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeNodeAnswerer struct {
	calls      int
	rootID     string
	taskID     string
	nodeRunID  string
	body       string
	activityID string
	err        error
}

func (f *fakeNodeAnswerer) AnswerWaitingWorkflowNode(_ context.Context, rootID, taskID, nodeRunID, body, activityID string) (NodeRun, error) {
	f.calls++
	f.rootID, f.taskID, f.nodeRunID, f.body, f.activityID = rootID, taskID, nodeRunID, body, activityID
	if f.err != nil {
		return NodeRun{}, f.err
	}
	return NodeRun{ID: nodeRunID, NodeID: "build", RunID: "run-route-answer"}, nil
}

func answerDecisionFixture(t *testing.T, taskID, runID, decisionID string) (*TaskStore, *ActivityService, *RouterDecision, *TaskActivity) {
	t.Helper()
	ctx := context.Background()
	store, svc, run, root := newWorkflowCommentFixture(t, taskID, runID, workflowCommentTestGraph)
	activity, err := svc.ActivityServiceForRoot(root.ID)
	if err != nil {
		t.Fatalf("activity service: %v", err)
	}
	user, err := activity.QueueUserActivity(ctx, taskID, "我希望是A", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("QueueUserActivity: %v", err)
	}
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	reply := activity.newAgentActivity(task, CommentWorkflowChoiceEmpty)
	if err := activity.activities.Insert(ctx, reply); err != nil {
		t.Fatalf("insert placeholder: %v", err)
	}
	decision := &RouterDecision{
		ID: decisionID, TaskID: taskID, RootID: root.ID, TurnID: "msg:" + user.ID,
		UserActivityID: user.ID, PlaceholderActivityID: reply.ID, Status: RouterDecisionApplying,
		TriggerKind: RouterTriggerUserMessage, Actions: []RouterAction{},
	}
	_ = run
	return store, activity, decision, reply
}

// 应用答复动作时，必须调到端口并把用户看得见的占位改成事实回执 —— 线上用户
// 抱怨的正是「转交之后什么都没发生」。
func TestApplyRouteAnswerWaitingNodeRendersConfirmation(t *testing.T) {
	ctx := context.Background()
	store, activity, decision, reply := answerDecisionFixture(t, "task-route-answer", "run-route-answer", "decision-route-answer")
	answerer := &fakeNodeAnswerer{}
	activity.SetNodeAnswerer(answerer)
	decision.Actions = append(decision.Actions, RouterAction{
		Seq: 0, Kind: RouterActionAnswerWaiting, Body: "我希望是A", NodeRunID: "run-route-answer:build::0:1",
	})
	if err := NewRouterStore(store.db).InsertDecision(ctx, decision); err != nil {
		t.Fatalf("InsertDecision: %v", err)
	}
	if err := activity.ApplyRouteActions(ctx, decision); err != nil {
		t.Fatalf("ApplyRouteActions: %v", err)
	}
	if answerer.calls != 1 || answerer.body != "我希望是A" || answerer.nodeRunID != "run-route-answer:build::0:1" || answerer.activityID != decision.UserActivityID {
		t.Fatalf("port call = %+v, want the answer handed to the exact node run", answerer)
	}
	got := reloadTestActivity(t, store, reply.ID)
	if got.Status != ActivityStatusCompleted || got.Hidden || got.RouterDecisionID != decision.ID || got.ReplyToID != decision.UserActivityID {
		t.Fatalf("confirmation activity = %#v, want a visible completed reply", got)
	}
	if !strings.Contains(got.Body, "答复") || !strings.Contains(got.Body, "build") {
		t.Fatalf("confirmation body = %q, want the node name and what happened", got.Body)
	}
}

// 端口缺失/失败时必须落 failed 并让占位显示原因，不能假装成功。
func TestApplyRouteAnswerWaitingNodeFailureIsVisible(t *testing.T) {
	ctx := context.Background()

	t.Run("port_not_configured", func(t *testing.T) {
		store, activity, decision, reply := answerDecisionFixture(t, "task-route-no-port", "run-route-no-port", "decision-no-port")
		// ActivityServiceForRoot 在生产装配里挂好了端口；这条用例要的是「没挂」
		// 时明确失败，所以主动清掉。
		activity.SetNodeAnswerer(nil)
		decision.Actions = append(decision.Actions, RouterAction{
			Seq: 0, Kind: RouterActionAnswerWaiting, Body: "我希望是A", NodeRunID: "run-route-no-port:build::0:1",
		})
		if err := NewRouterStore(store.db).InsertDecision(ctx, decision); err != nil {
			t.Fatalf("InsertDecision: %v", err)
		}
		err := activity.ApplyRouteActions(ctx, decision)
		if err == nil || !strings.Contains(err.Error(), "not configured") {
			t.Fatalf("ApplyRouteActions err = %v, want the missing-port failure", err)
		}
		got := reloadTestActivity(t, store, reply.ID)
		if got.Status != ActivityStatusFailed || !strings.Contains(got.Summary, "not configured") {
			t.Fatalf("placeholder = %#v, want a visible failure", got)
		}
	})

	t.Run("node_already_moved_on", func(t *testing.T) {
		store, activity, decision, reply := answerDecisionFixture(t, "task-route-moved", "run-route-moved", "decision-moved")
		answerer := &fakeNodeAnswerer{err: errors.New("node run is success")}
		activity.SetNodeAnswerer(answerer)
		decision.Actions = append(decision.Actions, RouterAction{
			Seq: 0, Kind: RouterActionAnswerWaiting, Body: "我希望是A", NodeRunID: "run-route-moved:build::0:1",
		})
		if err := NewRouterStore(store.db).InsertDecision(ctx, decision); err != nil {
			t.Fatalf("InsertDecision: %v", err)
		}
		if err := activity.ApplyRouteActions(ctx, decision); err == nil {
			t.Fatal("apply succeeded although the node could not be answered")
		}
		got := reloadTestActivity(t, store, reply.ID)
		if got.Status != ActivityStatusFailed || !strings.Contains(got.Summary, "node run is success") {
			t.Fatalf("placeholder = %#v, want the real reason", got)
		}
	})
}
