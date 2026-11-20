package kanban

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// Router 主动监管 S3：放开 resume_run / escalate，并把直连回合纳入监管。
// 这些用例先于实现写（TDD），每一条都钉住一个「机器该不该动手」的判据。

// fakeRunResumer 记录唤醒请求，避免用例真去启动 driver 改库。
type fakeRunResumer struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (f *fakeRunResumer) WakeWorkflowRun(_ context.Context, rootID, taskID, cause string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, rootID+"/"+taskID+"/"+cause)
	return f.err
}

func (f *fakeRunResumer) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// stalledRunFixture 造一个「还有 open 节点、但会话没在回复」的 Run：resume_run
// 的合法场景。节点带会话键，探针由用例按需实现。
func stalledRunFixture(t *testing.T, mode RouterSupervisionMode) *coordinateFixture {
	t.Helper()
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.svc.SetRouterConfig(RouterConfig{
		Mode:        RouterModeCoordinate,
		IdleClose:   time.Hour,
		Supervision: RouterSupervisionConfig{Mode: mode},
	})
	f.stopLoop(t)
	f.addRun(t, "task-1", "run-1", WorkflowTriggeredByUser, map[string]string{"agent-1": NodeRunRunning})
	f.markOwned(t, "task-1", "run-1", "decision-1")
	f.setTaskStatus(t, "task-1", StatusRunning)
	f.addNodeRun(t, NodeRun{ID: "nr-1", RunID: "run-1", NodeID: "agent-1", Status: NodeRunRunning,
		Attempt: 1, SessionKey: "sess-idle", StartedAt: time.Now().Add(-10 * time.Minute)})
	return f
}

func (f *coordinateFixture) runStalledEvent(t *testing.T, turnID string) RouterEvent {
	t.Helper()
	return f.addEvent(t, RouterEvent{
		Kind: RouterEventRunStalled, RunID: "run-1", TurnID: turnID,
		UserActivityID: "uact-run-1", InstanceKey: turnID,
		PayloadJSON: `{"run_id":"run-1","task_id":"task-1","node_run_id":"nr-1"}`,
	})
}

// 行为：on 下 resume_run 真的把唤醒请求交给 Run 的 single-flight 入口，决策 applied。
func TestSupervisionResumeRunWakesStalledRun(t *testing.T) {
	f := stalledRunFixture(t, RouterSupervisionOn)
	resumer := &fakeRunResumer{}
	f.svc.SetActivityRunResumer(resumer)
	f.agent.calls = []scriptedRouterCall{
		{tool: "router_resume_run", args: map[string]any{"reason": "节点没有推进"}},
	}
	ev := f.runStalledEvent(t, "resume-turn")
	if _, err := f.router.ConsumeRouterEvents(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("ConsumeRouterEvents: %v", err)
	}
	f.waitEvent(t, ev.ID, RouterEventDone)
	calls := resumer.snapshot()
	if len(calls) != 1 || calls[0] != f.root.ID+"/task-1/"+supervisionResumeReason {
		t.Fatalf("wake calls = %v", calls)
	}
	decision, err := NewRouterStore(f.store.db).DecisionByTurn(context.Background(), "task-1", "resume-turn")
	must(t, err, "DecisionByTurn")
	if decision.Status != RouterDecisionApplied {
		t.Fatalf("decision status = %q, want applied", decision.Status)
	}
}

// 行为：还有节点在回复时不许「唤醒」——那会把真的在跑的回合和新 driver 撞在一起。
func TestSupervisionResumeRunRefusesWhileNodeReplying(t *testing.T) {
	f := stalledRunFixture(t, RouterSupervisionOn)
	f.svc.SetSessionRunningProbe(func(key string) bool { return key == "sess-idle" })
	resumer := &fakeRunResumer{}
	f.svc.SetActivityRunResumer(resumer)
	f.agent.calls = []scriptedRouterCall{
		{tool: "router_resume_run", args: map[string]any{"reason": "看起来卡了"}},
	}
	ev := f.runStalledEvent(t, "resume-busy-turn")
	if _, err := f.router.ConsumeRouterEvents(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("ConsumeRouterEvents: %v", err)
	}
	got := f.waitEvent(t, ev.ID, RouterEventFailed)
	if !strings.Contains(got.LastError, "replying") {
		t.Fatalf("event reason = %q, want mention of replying", got.LastError)
	}
	if calls := resumer.snapshot(); len(calls) != 0 {
		t.Fatalf("wake calls = %v, want none", calls)
	}
}

// 行为：证据不确定（unknown）时 resume 不碰 Run —— 与 retry_node 同一条边界。
func TestSupervisionResumeRunRefusesUncertainFailure(t *testing.T) {
	f := supervisionFixture(t, RouterSupervisionOn)
	resumer := &fakeRunResumer{}
	f.svc.SetActivityRunResumer(resumer)
	f.saveStates(t, "run-1", map[string]string{"agent-1": NodeRunUnknown, "agent-2": NodeRunRunning})
	f.addNodeRun(t, NodeRun{ID: "nr-unknown", RunID: "run-1", NodeID: "agent-1", Status: NodeRunUnknown,
		Attempt: 2, PrevAttemptID: "nr-1", Output: map[string]any{"worktree_error": "merge conflict while closing"}})
	f.addNodeRun(t, NodeRun{ID: "nr-open", RunID: "run-1", NodeID: "agent-2", Status: NodeRunRunning,
		Attempt: 1, SessionKey: "sess-idle", StartedAt: time.Now().Add(-10 * time.Minute)})
	f.agent.calls = []scriptedRouterCall{
		{tool: "router_resume_run", args: map[string]any{"reason": "继续"}},
	}
	ev := f.addEvent(t, RouterEvent{
		Kind: RouterEventRunStalled, RunID: "run-1", TurnID: "resume-uncertain-turn",
		UserActivityID: "uact-run-1", InstanceKey: "resume-uncertain-turn",
		PayloadJSON: `{"run_id":"run-1","task_id":"task-1","node_run_id":"nr-open"}`,
	})
	if _, err := f.router.ConsumeRouterEvents(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("ConsumeRouterEvents: %v", err)
	}
	got := f.waitEvent(t, ev.ID, RouterEventFailed)
	if !strings.Contains(got.LastError, "uncertain") {
		t.Fatalf("event reason = %q, want mention of uncertain", got.LastError)
	}
	if calls := resumer.snapshot(); len(calls) != 0 {
		t.Fatalf("wake calls = %v, want none", calls)
	}
}

// 行为：escalate 写一条可见系统说明并把任务放回 waiting_user；不取消 Run。
func TestSupervisionEscalateHandsOffToUser(t *testing.T) {
	f := supervisionFixture(t, RouterSupervisionOn)
	f.agent.calls = []scriptedRouterCall{
		{tool: "router_escalate", args: map[string]any{"summary": "两次重试都失败，需要你决定下一步"}},
	}
	ev := f.runFailedEvent(t, "escalate-turn")
	if _, err := f.router.ConsumeRouterEvents(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("ConsumeRouterEvents: %v", err)
	}
	f.waitEvent(t, ev.ID, RouterEventDone)
	decision, err := NewRouterStore(f.store.db).DecisionByTurn(context.Background(), "task-1", "escalate-turn")
	must(t, err, "DecisionByTurn")
	if decision.Status != RouterDecisionApplied {
		t.Fatalf("decision status = %q, want applied", decision.Status)
	}
	act, err := NewActivityStore(f.store.db).Get(context.Background(), "route-escalate:"+decision.ID+":0")
	must(t, err, "escalate activity")
	if act.Role != "system" || !strings.Contains(act.Body, "两次重试都失败") {
		t.Fatalf("escalate activity = %+v", act)
	}
	if task := f.task(t, "task-1"); task.Status != StatusWaitingUser {
		t.Fatalf("task status = %q, want waiting_user", task.Status)
	}
}

// 行为：shadow 下 escalate 只落决策，不写系统行、不改任务状态。
func TestSupervisionEscalateShadowHasNoSideEffects(t *testing.T) {
	f := supervisionFixture(t, RouterSupervisionShadow)
	f.agent.calls = []scriptedRouterCall{
		{tool: "router_escalate", args: map[string]any{"summary": "需要你决定"}},
	}
	ev := f.runFailedEvent(t, "escalate-shadow-turn")
	if _, err := f.router.ConsumeRouterEvents(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("ConsumeRouterEvents: %v", err)
	}
	got := f.waitEvent(t, ev.ID, RouterEventDone)
	if !strings.Contains(got.LastError, "supervision_shadow") {
		t.Fatalf("event reason = %q, want supervision_shadow", got.LastError)
	}
	if task := f.task(t, "task-1"); task.Status != StatusFail {
		t.Fatalf("task status = %q, want fail (shadow must not touch it)", task.Status)
	}
}

// 校验层的独立守卫：工具会先挡一次，但重放/手工构造的动作仍然必须过这里。
func TestSupervisionResumeValidationGuards(t *testing.T) {
	cfg := RouterConfig{Supervision: RouterSupervisionConfig{Mode: RouterSupervisionOn}}
	cases := []struct {
		name  string
		brief *SituationBrief
		act   RouterAction
		want  string
	}{
		{
			name:  "no supervision block",
			brief: &SituationBrief{},
			act:   RouterAction{Kind: RouterActionResumeRun},
			want:  "requires a supervision block",
		},
		{
			name: "all open nodes replying",
			brief: &SituationBrief{Supervision: &SupervisionObservation{
				OpenNodes: []SupervisionNodeState{{NodeID: "a", Replying: true}},
			}},
			act:  RouterAction{Kind: RouterActionResumeRun},
			want: "still replying",
		},
		{
			name: "uncertain failure",
			brief: &SituationBrief{Supervision: &SupervisionObservation{
				OpenNodes:   []SupervisionNodeState{{NodeID: "a"}},
				FailedNodes: []SupervisionNodeState{{NodeID: "b", FailureKind: SupervisionFailureUncertain}},
			}},
			act:  RouterAction{Kind: RouterActionResumeRun},
			want: "uncertain",
		},
		{
			name:  "escalate needs a summary",
			brief: &SituationBrief{Supervision: &SupervisionObservation{}},
			act:   RouterAction{Kind: RouterActionEscalate},
			want:  "summary required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSupervisionActions(tc.brief, []RouterAction{tc.act}, cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want mention of %q", err, tc.want)
			}
		})
	}
}
