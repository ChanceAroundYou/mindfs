package kanban

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"
	"time"
)

// 线上排查靠 journalctl：24 小时里 17 条决策、其中 4 条是 90s 超时回落，
// `[router]` 一行都没有——因为只有错误路径才打日志。回落是「用户看到的东西
// 其实是旧路径跑的」这件事的唯一线索，必须在成功路径上也留一行。
func TestFinishFallbackLogsOutcome(t *testing.T) {
	store := newRouterEventStore(t)
	rs := &RouterService{store: store}
	decision := &RouterDecision{
		ID:               "dec-fallback-log",
		RootID:           "root-a",
		TaskID:           "task-1",
		UserActivityID:   "act-user",
		TriggerKind:      RouterTriggerUserMessage,
		TurnID:           "turn-1",
		RouterSessionKey: "router-task-1",
	}

	out := captureRouterLog(t, func() {
		rs.finishFallback(context.Background(), decision,
			time.Now().Add(-90*time.Second),
			errors.New("persist turn blocks: context deadline exceeded"), false)
	})

	for _, want := range []string{"[router] fallback", "task=task-1", "latency_ms=", "context deadline exceeded"} {
		if !strings.Contains(out, want) {
			t.Fatalf("fallback log %q missing %q", out, want)
		}
	}
}

// route 模式的应用结果同理：只看到「模型回了话」不够，还要知道这一轮到底
// 落成了 applied 还是 fallback、用了几个动作、花了多久。
func TestRouterRouteAppliedLogsOutcome(t *testing.T) {
	f := newRouteFixture(t, RouterModeRoute)
	ctx := context.Background()

	out := captureRouterLog(t, func() {
		if _, _, err := f.activity.CreateUserActivity(ctx, "task-1", "修复登录", CommentWorkflowChoiceEmpty, nil); err != nil {
			t.Errorf("CreateUserActivity: %v", err)
		}
		f.waitDecision(t, RouterDecisionApplied)
	})
	for _, want := range []string{"[router] applied", "task=task-1", "actions=1", "kinds=say"} {
		if !strings.Contains(out, want) {
			t.Fatalf("applied log %q missing %q", out, want)
		}
	}
}

func captureRouterLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prevOut := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})
	fn()
	return buf.String()
}
