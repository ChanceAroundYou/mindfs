package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// ── 回合失败：保留已记录的动作 + 超时可见告知（2026-11-19，#86） ──────────
//
// 线上事故：90s 死线打在回合后的 persistTurnBlocks 上，而模型早就把
// router_ask_user 调完、工具已回 recorded。整轮被判 fallback，动作被丢弃，
// 用户什么都没看到，旧路径还另起了一条 Run —— 这就是「做事做一半」。
//
// 这两条不变量必须钉住：
//  1. 失败前已经返回 recorded 的写动作，要么被应用，要么被明确拒绝；不能静默消失。
//  2. 超时对用户可见：活动流里要有一条系统行，而不是 90 秒后凭空出现一条旧回复。

// failingRouterAgent 先按脚本调用写工具（走真实收集链路），再返回失败。
// 真实场景里模型的动作早已记录，错误来自回合后置落库。
type failingRouterAgent struct {
	calls []scriptedRouterCall
	cause error
}

func (a *failingRouterAgent) RunRouterTurn(ctx context.Context, req RouterAgentRequest) (RouterAgentResult, error) {
	for _, call := range a.calls {
		var spec *RouterToolSpec
		for i := range req.Tools {
			if req.Tools[i].Name == call.tool {
				spec = &req.Tools[i]
				break
			}
		}
		if spec == nil {
			return RouterAgentResult{}, errors.New("tool not exposed: " + call.tool)
		}
		raw, err := json.Marshal(call.args)
		if err != nil {
			return RouterAgentResult{}, err
		}
		if _, err := spec.Handler(ctx, raw); err != nil {
			return RouterAgentResult{}, err
		}
	}
	return RouterAgentResult{}, a.cause
}

func (a *failingRouterAgent) CloseRouterSession(context.Context, string, string) error { return nil }

// wrappedDeadline 复刻线上错误链：SendMessage 用 %w 包装落库失败，所以超时判定
// 必须能穿过包装，不能只认裸 context.DeadlineExceeded。
func wrappedDeadline() error {
	return fmt.Errorf("persist turn blocks: %w", context.DeadlineExceeded)
}

func findTimeoutNotice(t *testing.T, f *routeFixture, ctx context.Context) *TaskActivity {
	t.Helper()
	all, err := NewActivityStore(f.store.db).ListIncludingHidden(ctx, "task-1")
	if err != nil {
		t.Fatalf("ListIncludingHidden: %v", err)
	}
	for _, act := range all {
		if strings.HasPrefix(act.ID, "router-turn-timeout:") {
			return act
		}
	}
	return nil
}

// waitTimeoutNotice 等系统行出现：决策状态先落库，告知行在回落分发之前写，
// 两者之间有一个很短的窗口，不能拿「决策已是 fallback」当同步点。
func waitTimeoutNotice(t *testing.T, f *routeFixture, ctx context.Context) *TaskActivity {
	t.Helper()
	var notice *TaskActivity
	waitForCondition(t, func() bool {
		notice = findTimeoutNotice(t, f, ctx)
		return notice != nil
	})
	return notice
}

func TestRouterRouteTimeoutKeepsRecordedActions(t *testing.T) {
	f := newRouteFixture(t, RouterModeRoute)
	f.svc.SetRouterAgent(&failingRouterAgent{
		calls: []scriptedRouterCall{{
			tool: "router_say",
			args: map[string]any{"body": "超时前已经决定的回复"},
		}},
		cause: wrappedDeadline(),
	})
	ctx := context.Background()

	_, placeholder, err := f.activity.CreateUserActivity(ctx, "task-1", "帮我看下这个报错", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	decision := f.waitDecision(t, RouterDecisionApplied)
	if len(decision.Actions) != 1 || decision.Actions[0].Kind != RouterActionSay {
		t.Fatalf("salvaged actions = %#v, want the recorded say", decision.Actions)
	}
	if !strings.Contains(decision.Error, "did not finish") {
		t.Fatalf("decision error = %q, want the truncation note", decision.Error)
	}
	act := f.reloadActivity(t, placeholder.ID)
	if act.Status != ActivityStatusCompleted || act.Body != "超时前已经决定的回复" {
		t.Fatalf("placeholder = status %q body %q, want the salvaged say", act.Status, act.Body)
	}
	// 救回动作就不能再回落：回落会另起一条 Run，把同一句话执行两遍。
	if runs := f.runs(t); len(runs) != 0 {
		t.Fatalf("salvaged decision fell back to a legacy run: %#v", runs)
	}
	notice := waitTimeoutNotice(t, f, ctx)
	if notice.Type != ActivityTypeAttention || notice.Role != "system" {
		t.Fatalf("notice = type %q role %q, want a system attention row", notice.Type, notice.Role)
	}
	if !strings.Contains(notice.Body, "已保留并执行") {
		t.Fatalf("notice body = %q, want to say the recorded actions were kept", notice.Body)
	}
}

func TestRouterRouteTimeoutWithoutActionsNotifiesAndFallsBack(t *testing.T) {
	f := newRouteFixture(t, RouterModeRoute)
	f.svc.SetRouterAgent(&failingRouterAgent{cause: wrappedDeadline()})
	ctx := context.Background()

	_, placeholder, err := f.activity.CreateUserActivity(ctx, "task-1", "帮我看下这个报错", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	f.waitDecision(t, RouterDecisionFallback)
	var runs []*WorkflowRun
	waitForCondition(t, func() bool {
		runs = f.runs(t)
		return len(runs) == 1
	})
	act := f.reloadActivity(t, placeholder.ID)
	if act.RunID != runs[0].ID {
		t.Fatalf("fallback must reuse the placeholder: run id %q want %q", act.RunID, runs[0].ID)
	}
	notice := waitTimeoutNotice(t, f, ctx)
	if !strings.Contains(notice.Body, "已改由原执行路径") {
		t.Fatalf("notice body = %q, want to say the legacy path took over", notice.Body)
	}
	// 回落分发是异步的；不等它收尾，TempDir 清理会和驱动写库赛跑成偶发失败。
	if err := f.svc.WaitWorkflowRunIdle(ctx, f.rootID, "task-1"); err != nil {
		t.Fatalf("WaitWorkflowRunIdle: %v", err)
	}
}

// 非超时失败也要救回动作，但不能谎称「超过 90 秒」：告知行只对超时写。
// 「救回」与「超时告知」是两件事，救回分支最容易顺手把两者焊死。
func TestRouterRouteNonTimeoutSalvageKeepsActionsWithoutTimeoutNotice(t *testing.T) {
	f := newRouteFixture(t, RouterModeRoute)
	f.svc.SetRouterAgent(&failingRouterAgent{
		calls: []scriptedRouterCall{{
			tool: "router_say",
			args: map[string]any{"body": "崩溃前已经决定的回复"},
		}},
		cause: errors.New("router cli crashed"),
	})
	ctx := context.Background()

	_, placeholder, err := f.activity.CreateUserActivity(ctx, "task-1", "帮我看下这个报错", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	decision := f.waitDecision(t, RouterDecisionApplied)
	if len(decision.Actions) != 1 || decision.Actions[0].Kind != RouterActionSay {
		t.Fatalf("salvaged actions = %#v, want the recorded say", decision.Actions)
	}
	act := f.reloadActivity(t, placeholder.ID)
	if act.Status != ActivityStatusCompleted || act.Body != "崩溃前已经决定的回复" {
		t.Fatalf("placeholder = status %q body %q, want the salvaged say", act.Status, act.Body)
	}
	if runs := f.runs(t); len(runs) != 0 {
		t.Fatalf("salvaged decision fell back to a legacy run: %#v", runs)
	}
	// 告知行在决策落 applied 之后才写，给一个短窗口再看：非超时不该有任何这样的行。
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if notice := findTimeoutNotice(t, f, ctx); notice != nil {
			t.Fatalf("non-timeout salvage wrote a timeout notice: %#v", notice)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// 非超时失败（例如 CLI 崩溃）不写系统行：回落本身就会给出一条旧路径回复，
// 再插一行只是噪声。这条钉住「只对超时可见告知」的范围，防止下一个人顺手放宽。
func TestRouterRouteNonTimeoutFailureWritesNoNotice(t *testing.T) {
	f := newRouteFixture(t, RouterModeRoute)
	f.svc.SetRouterAgent(&failingRouterAgent{cause: errors.New("router cli crashed")})
	ctx := context.Background()

	if _, _, err := f.activity.CreateUserActivity(ctx, "task-1", "修复登录", CommentWorkflowChoiceEmpty, nil); err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	f.waitDecision(t, RouterDecisionFallback)
	if err := f.svc.WaitWorkflowRunIdle(ctx, f.rootID, "task-1"); err != nil {
		t.Fatalf("WaitWorkflowRunIdle: %v", err)
	}
	if notice := findTimeoutNotice(t, f, ctx); notice != nil {
		t.Fatalf("non-timeout failure wrote a timeout notice: %#v", notice)
	}
}

// ── brief 陈旧性 ──────────────────────────────────────────────────────────

func briefTestRun(nodeStates map[string]string) *WorkflowRun {
	return &WorkflowRun{
		ID:     "run-1",
		TaskID: "task-1",
		Graph: WorkflowDefinition{Nodes: []WorkflowNode{
			{ID: "input", Name: "问题描述", Kind: NodeKindAgent},
			{ID: "locate", Name: "定位根因", Kind: NodeKindAgent},
		}},
		NodeStates: nodeStates,
		CreatedAt:  time.Date(2026, 11, 19, 11, 35, 0, 0, time.UTC),
	}
}

// TestBriefTaskStatusFollowsRunProjection pin 线上现场：任务行写着 waiting_user，
// Run 里却有一个节点在 running。brief 必须以 Run 为准，否则 Router 被告知「没有
// 活动执行」，一条本该转交的留言会被判成新开一轮。
func TestBriefTaskStatusFollowsRunProjection(t *testing.T) {
	run := briefTestRun(map[string]string{"input": NodeRunSuccess, "locate": NodeRunRunning})
	stale := Task{ID: "task-1", Status: StatusWaitingUser}
	if got := briefTaskStatus(run, stale); got != StatusRunning {
		t.Fatalf("briefTaskStatus = %q, want %q", got, StatusRunning)
	}
	if got := briefRun(context.Background(), nil, run, stale); got.Status != StatusRunning {
		t.Fatalf("briefRun.Status = %q, want %q", got.Status, StatusRunning)
	}
	if got := briefActiveExecution(context.Background(), nil, run, stale); got.Status != StatusRunning {
		t.Fatalf("briefActiveExecution.Status = %q, want %q", got.Status, StatusRunning)
	}

	// 操作员的暂停必须压过投影：暂停期间节点可能还在收尾。
	paused := Task{ID: "task-1", Status: StatusPaused}
	if got := briefTaskStatus(run, paused); got != StatusPaused {
		t.Fatalf("paused task derived %q, want %q", got, StatusPaused)
	}

	// 任务已终态时投影不能把它拉回来。
	done := briefTestRun(map[string]string{"input": NodeRunSuccess, "locate": NodeRunSuccess})
	cancelled := Task{ID: "task-1", Status: StatusCancelled}
	if got := briefTaskStatus(done, cancelled); got != StatusCancelled {
		t.Fatalf("terminal task derived %q, want %q", got, StatusCancelled)
	}

	// 没有 Run 时保持任务行原值。
	if got := briefTaskStatus(nil, stale); got != StatusWaitingUser {
		t.Fatalf("task without run derived %q, want %q", got, StatusWaitingUser)
	}
}
