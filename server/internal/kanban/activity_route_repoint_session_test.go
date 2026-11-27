package kanban

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestApplyRouteRepointSessionPassesTaskRootAndTarget(t *testing.T) {
	ctx := context.Background()
	port := &fakeSessionRepointer{}
	svc := &ActivityService{sessionRepointer: port}
	action := RouterAction{Kind: RouterActionRepointSession, SessionKey: "sess-1", TargetPath: "/tmp/target"}
	if err := svc.applyRouteRepointSession(ctx, Task{RootID: "root-1"}, action); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if port.calls != 1 || port.in.RootID != "root-1" || port.in.SessionKey != "sess-1" || port.in.TargetPath != "/tmp/target" {
		t.Fatalf("port call = %d %+v", port.calls, port.in)
	}

	// 空 target_path 原样透传，由 usecase 决定主 checkout。
	port.in = SessionRepointInput{}
	if err := svc.applyRouteRepointSession(ctx, Task{RootID: "root-1"}, RouterAction{Kind: RouterActionRepointSession, SessionKey: "sess-1"}); err != nil {
		t.Fatalf("apply empty target: %v", err)
	}
	if port.in.TargetPath != "" {
		t.Fatalf("empty target became %q", port.in.TargetPath)
	}
}

func TestApplyRouteRepointSessionRejectsMissingConfigAndKey(t *testing.T) {
	ctx := context.Background()
	if err := (&ActivityService{}).applyRouteRepointSession(ctx, Task{RootID: "root-1"}, RouterAction{SessionKey: "sess"}); err == nil || !strings.Contains(err.Error(), "端口未配置") {
		t.Fatalf("missing port err = %v", err)
	}
	port := &fakeSessionRepointer{}
	svc := &ActivityService{sessionRepointer: port}
	if err := svc.applyRouteRepointSession(ctx, Task{RootID: "root-1"}, RouterAction{}); err == nil || !strings.Contains(err.Error(), "session_key") {
		t.Fatalf("missing key err = %v", err)
	}
	if err := svc.applyRouteRepointSession(ctx, Task{}, RouterAction{SessionKey: "sess"}); err == nil || !strings.Contains(err.Error(), "task root") {
		t.Fatalf("missing root err = %v", err)
	}
	if port.calls != 0 {
		t.Fatalf("invalid action still reached port %d times", port.calls)
	}
}

func TestApplyRouteRepointSessionPortErrorPropagates(t *testing.T) {
	boom := errors.New("会话正在回复")
	svc := &ActivityService{sessionRepointer: &fakeSessionRepointer{err: boom}}
	err := svc.applyRouteRepointSession(context.Background(), Task{RootID: "root-1"}, RouterAction{SessionKey: "sess"})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the port error", err)
	}
}

func TestApplyRouteActionsRepointIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store, svc, _, root := newWorkflowCommentFixture(t, "task-repoint-apply", "run-repoint-apply", workflowCommentTestGraph)
	activity, err := svc.ActivityServiceForRoot(root.ID)
	if err != nil {
		t.Fatalf("activity service: %v", err)
	}
	user, err := activity.QueueUserActivity(ctx, "task-repoint-apply", "把这个会话搬回主目录", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("QueueUserActivity: %v", err)
	}
	port := &fakeSessionRepointer{}
	activity.SetSessionRepointer(port)
	decision := &RouterDecision{
		ID: "decision-repoint-apply", TaskID: "task-repoint-apply", RootID: root.ID,
		TurnID: "msg:" + user.ID, UserActivityID: user.ID, Status: RouterDecisionApplying,
		TriggerKind: RouterTriggerUserMessage,
		Actions: []RouterAction{{
			Seq: 0, Kind: RouterActionRepointSession, SessionKey: "sess-1", TargetPath: "/tmp/target",
		}},
	}
	routerStore := NewRouterStore(store.db)
	if err := routerStore.InsertDecision(ctx, decision); err != nil {
		t.Fatalf("InsertDecision: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := activity.ApplyRouteActions(ctx, decision); err != nil {
			t.Fatalf("ApplyRouteActions #%d: %v", i+1, err)
		}
	}
	if port.calls != 1 {
		t.Fatalf("repoint called %d times, want 1", port.calls)
	}
	if port.in.RootID != root.ID || port.in.SessionKey != "sess-1" || port.in.TargetPath != "/tmp/target" {
		t.Fatalf("port input = %+v", port.in)
	}
}

func TestApplyRouteActionsRepointFailureMarksDecisionFailed(t *testing.T) {
	ctx := context.Background()
	store, svc, _, root := newWorkflowCommentFixture(t, "task-repoint-fail", "run-repoint-fail", workflowCommentTestGraph)
	activity, err := svc.ActivityServiceForRoot(root.ID)
	if err != nil {
		t.Fatalf("activity service: %v", err)
	}
	user, err := activity.QueueUserActivity(ctx, "task-repoint-fail", "改目录", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("QueueUserActivity: %v", err)
	}
	svc.SetSessionRepointer(&fakeSessionRepointer{err: errors.New("会话正在回复")})
	decision := &RouterDecision{
		ID: "decision-repoint-fail", TaskID: "task-repoint-fail", RootID: root.ID,
		TurnID: "msg:" + user.ID, UserActivityID: user.ID, Status: RouterDecisionApplying,
		TriggerKind: RouterTriggerUserMessage,
		Actions: []RouterAction{{
			Seq: 0, Kind: RouterActionRepointSession, SessionKey: "sess-1",
		}},
	}
	routerStore := NewRouterStore(store.db)
	if err := routerStore.InsertDecision(ctx, decision); err != nil {
		t.Fatalf("InsertDecision: %v", err)
	}
	// 走 coordinate 的 applyRoute（取 root 的 ActivityService）并复现它的失败落库。
	rs := &RouterService{service: svc, rootID: root.ID, store: routerStore}
	applyErr := rs.applyRoute(ctx, decision)
	if applyErr == nil || !strings.Contains(applyErr.Error(), "apply repoint_session(seq=0)") {
		t.Fatalf("apply err = %v, want wrapped repoint_session error", applyErr)
	}
	if err := routerStore.MarkDecisionStatus(ctx, decision.ID, RouterDecisionFailed, applyErr.Error()); err != nil {
		t.Fatalf("MarkDecisionStatus: %v", err)
	}
	got, err := routerStore.GetDecision(ctx, decision.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if got.Status != RouterDecisionFailed || !strings.Contains(got.Error, "会话正在回复") {
		t.Fatalf("decision = %#v, want failed with the real reason", got)
	}
}

type fakeSessionRepointer struct {
	calls int
	in    SessionRepointInput
	err   error
}

func (f *fakeSessionRepointer) RepointSessionForRouter(_ context.Context, in SessionRepointInput) error {
	f.calls++
	f.in = in
	return f.err
}
