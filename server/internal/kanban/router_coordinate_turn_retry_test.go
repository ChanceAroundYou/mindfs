package kanban

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// flakyRouterAgent 前 len(failures) 次回合按给定错误失败，之后交给内层脚本
// agent。用来复现「模型回合超时 / 流中断」这类瞬时失败。
type flakyRouterAgent struct {
	mu       sync.Mutex
	failures []error
	calls    int
	inner    *scriptedRouterAgent
}

func (a *flakyRouterAgent) RunRouterTurn(ctx context.Context, req RouterAgentRequest) (RouterAgentResult, error) {
	a.mu.Lock()
	a.calls++
	var fail error
	if len(a.failures) > 0 {
		fail = a.failures[0]
		a.failures = a.failures[1:]
	}
	a.mu.Unlock()
	if fail != nil {
		return RouterAgentResult{}, fail
	}
	return a.inner.RunRouterTurn(ctx, req)
}

func (a *flakyRouterAgent) CloseRouterSession(context.Context, string, string) error { return nil }

func (a *flakyRouterAgent) callCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

// 行为：Router 自己的回合超时是可重试的瞬时失败，事件不能就此终结。生产上
// llmux task_260f5740cced704f 的 run_settled 汇报就是因为模型 TTFT 178s 撞上
// 180s 死线，整轮被吞掉、用户什么汇报都没收到。
func TestCoordinateEventTurnRetriesOnceAfterTimeout(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)
	f.agent.calls = []scriptedRouterCall{{tool: "router_say", args: map[string]any{"body": "流程已完成"}}}
	agent := &flakyRouterAgent{failures: []error{context.DeadlineExceeded}, inner: f.agent}
	f.svc.SetRouterAgent(agent)
	f.addUserActivity(t, "uact-retry-once")
	ev := f.addEvent(t, RouterEvent{Kind: RouterEventRunSettled, TurnID: "retry-once", UserActivityID: "uact-retry-once"})

	if _, err := f.router.ConsumeRouterEvents(ctx, now); err != nil {
		t.Fatalf("first consume: %v", err)
	}
	var requeued RouterEvent
	waitForCondition(t, func() bool {
		requeued = f.eventByID(t, "task-1", ev.ID)
		return requeued.Status == RouterEventPending && requeued.Attempt == 1
	})
	if !strings.Contains(requeued.LastError, "deadline exceeded") {
		t.Fatalf("last_error = %q, want the timeout reason", requeued.LastError)
	}

	if _, err := f.router.ConsumeRouterEvents(ctx, now); err != nil {
		t.Fatalf("second consume: %v", err)
	}
	f.waitEvent(t, ev.ID, RouterEventDone)
	if got := agent.callCount(); got != 2 {
		t.Fatalf("agent calls = %d, want 2 (initial + one retry)", got)
	}
	first, err := f.events.DecisionByTurn(ctx, ev.TaskID, "retry-once")
	if err != nil {
		t.Fatalf("first decision: %v", err)
	}
	if first.Status != RouterDecisionFailed || !strings.Contains(first.Error, "deadline exceeded") {
		t.Fatalf("first decision = %s %q, want failed with the timeout reason", first.Status, first.Error)
	}
	if _, err := f.events.DecisionByTurn(ctx, ev.TaskID, "retry-once:a2"); err != nil {
		t.Fatalf("retry decision missing: %v", err)
	}
}

// 行为：重试有上限。连续超时三次后事件落 failed，不无限重跑烧模型预算。
// 预算是 3 次回合（首轮 + 两次重试）——用户裁定「不要太吝啬次数」。
func TestCoordinateEventTurnTimeoutRetryIsBounded(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)
	agent := &flakyRouterAgent{
		failures: []error{context.DeadlineExceeded, context.DeadlineExceeded, context.DeadlineExceeded},
		inner:    f.agent,
	}
	f.svc.SetRouterAgent(agent)
	f.addUserActivity(t, "uact-retry-bound")
	ev := f.addEvent(t, RouterEvent{Kind: RouterEventRunSettled, TurnID: "retry-bound", UserActivityID: "uact-retry-bound"})

	for want := 1; want <= 2; want++ {
		if _, err := f.router.ConsumeRouterEvents(ctx, now); err != nil {
			t.Fatalf("consume %d: %v", want, err)
		}
		waitForCondition(t, func() bool {
			current := f.eventByID(t, "task-1", ev.ID)
			return current.Status == RouterEventPending && current.Attempt == want
		})
	}
	if _, err := f.router.ConsumeRouterEvents(ctx, now); err != nil {
		t.Fatalf("final consume: %v", err)
	}
	failed := f.waitEvent(t, ev.ID, RouterEventFailed)
	if failed.Attempt != 2 {
		t.Fatalf("attempt = %d, want 2 (two retries)", failed.Attempt)
	}
	if got := agent.callCount(); got != 3 {
		t.Fatalf("agent calls = %d, want 3 (no fourth attempt)", got)
	}
	if !strings.Contains(failed.LastError, "timed out") {
		t.Fatalf("last_error = %q, want a readable timeout reason", failed.LastError)
	}
}

// 行为：非瞬时失败不重试（参数/校验类错误重试只会再错一次）。
func TestCoordinateEventTurnDoesNotRetryPermanentFailure(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)
	agent := &flakyRouterAgent{
		failures: []error{errors.New("invalid api key")},
		inner:    f.agent,
	}
	f.svc.SetRouterAgent(agent)
	f.addUserActivity(t, "uact-retry-permanent")
	ev := f.addEvent(t, RouterEvent{Kind: RouterEventRunSettled, TurnID: "retry-permanent", UserActivityID: "uact-retry-permanent"})

	if _, err := f.router.ConsumeRouterEvents(ctx, now); err != nil {
		t.Fatalf("consume: %v", err)
	}
	f.waitEvent(t, ev.ID, RouterEventFailed)
	if got := agent.callCount(); got != 1 {
		t.Fatalf("agent calls = %d, want 1 (permanent failure must not retry)", got)
	}
}

func TestRouterTurnFailureReasonNamesTheDeadline(t *testing.T) {
	reason := routerTurnFailureReason(context.DeadlineExceeded, 3*time.Minute, context.DeadlineExceeded)
	if !strings.Contains(reason, "timed out after 3m0s") {
		t.Fatalf("reason = %q, want the timeout budget in it", reason)
	}
	plain := routerTurnFailureReason(errors.New("boom"), time.Minute, nil)
	if plain != "boom" {
		t.Fatalf("reason = %q, want the raw error for non-timeout failures", plain)
	}
}
