package claude

import (
	"testing"
	"time"

	claudeagent "github.com/roasbeef/claude-agent-sdk-go"

	"mindfs/server/internal/agent/types"
)

// 回归：只有「人类发起的那一轮」的 ResultMessage 才是回合边界。
//
// 现象（2026-10-27 WSL 实测，会话 1793005653-4478d98a7bf8）：会话只在输出正文时亮灯，
// 一进 tool 调用/等上游就灭灯报完成，结果回来又亮；且任何阶段按停止都无效。
//
// 根因：SDK 的 Stream.Messages() 是长驻迭代器，CLI 会把后台任务完成通知作为
// origin.kind="task-notification" 的 ResultMessage 插进同一条流（实测插在**下一轮开头**）。
// 旧实现无条件把它当回合边界：emit(MessageDone) + completeTurn(resultErr(m))，
// 而 resultErr 对 subtype:"success" 返回 nil ⇒ SendMessage 成功返回 ⇒ EndSessionTurn
// ⇒ 广播 session.done 并清 pending，而 agent 还在跑。回合出列之后
// ws_upgrade 取到的 target_turn=0 会被 staleCancelTarget 恒丢弃 —— 停止随之全阶段失效。
//
// 这组测试钉住判定口径本身，以及它必须真的能失败（见 docs 里的变异验证记录）。

// newResultTestSession 返回一个只用于 handleResultMessage 的会话：
// onUpdate 收集事件，waiter 由 enqueueTurn 挂进 FIFO，与 SendMessage 的真实路径一致。
func newResultTestSession() (*session, *[]types.Event) {
	events := make([]types.Event, 0, 4)
	s := &session{
		sessionKey: "claude-test",
		sessionID:  "claude-session",
		onUpdate: func(event types.Event) {
			events = append(events, event)
		},
	}
	return s, &events
}

// assertTurnStillRunning 断言这一轮仍挂着：waiter 在短超时内没有收到值。
func assertTurnStillRunning(t *testing.T, waiter chan error) {
	t.Helper()
	select {
	case err := <-waiter:
		t.Fatalf("turn completed unexpectedly with %v, want still running", err)
	case <-time.After(50 * time.Millisecond):
	}
}

// assertTurnCompleted 断言这一轮已结束，并返回它拿到的错误。
func assertTurnCompleted(t *testing.T, waiter chan error) error {
	t.Helper()
	select {
	case err := <-waiter:
		return err
	case <-time.After(time.Second):
		t.Fatalf("turn still running, want completed")
		return nil
	}
}

func hasMessageDone(events []types.Event) bool {
	for _, event := range events {
		if event.Type == types.EventTypeMessageDone {
			return true
		}
	}
	return false
}

func hasMessageChunk(events []types.Event) bool {
	for _, event := range events {
		if event.Type == types.EventTypeMessageChunk {
			return true
		}
	}
	return false
}

// 非人类来源的 result 一律不是边界 —— 覆盖 SDK 已知的 5 个非人类 kind，
// 以及一个未来可能出现的新 kind（白名单的代价就是这个方向，必须钉住）。
func TestResultOriginNonBoundaryIsIgnored(t *testing.T) {
	cases := []struct {
		name string
		kind claudeagent.MessageOriginKind
	}{
		{name: "task-notification", kind: claudeagent.MessageOriginKindTaskNotification},
		{name: "coordinator", kind: claudeagent.MessageOriginKindCoordinator},
		{name: "peer", kind: claudeagent.MessageOriginKindPeer},
		{name: "channel", kind: claudeagent.MessageOriginKindChannel},
		{name: "auto-continuation", kind: claudeagent.MessageOriginKindAutoContinuation},
		{name: "future-unknown", kind: claudeagent.MessageOriginKind("something-new")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, events := newResultTestSession()
			waiter := make(chan error, 1)
			s.enqueueTurn(waiter)

			// 真实形态：subtype=success、usage 全 0、stop_reason 为 null，
			// 有时还带一句别的回合的正文（实测 "完成。" / "No goal set"）。
			s.handleResultMessage(claudeagent.ResultMessage{
				Subtype: "success",
				Result:  "完成。",
				Origin:  &claudeagent.MessageOrigin{Kind: tc.kind},
			}, nil)

			if hasMessageDone(*events) {
				t.Fatalf("emitted message_done for non-boundary origin %q", tc.kind)
			}
			if hasMessageChunk(*events) {
				t.Fatalf("emitted message_chunk for non-boundary origin %q", tc.kind)
			}
			assertTurnStillRunning(t, waiter)
		})
	}
}

// 人类轮（Origin 缺省，或显式 human）才是边界。
func TestResultOriginHumanOrAbsentIsBoundary(t *testing.T) {
	cases := []struct {
		name   string
		origin *claudeagent.MessageOrigin
	}{
		{name: "absent", origin: nil},
		{name: "human", origin: &claudeagent.MessageOrigin{Kind: claudeagent.MessageOriginKindHuman}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, events := newResultTestSession()
			s.sawDelta = true
			s.sawMessageText = true
			waiter := make(chan error, 1)
			s.enqueueTurn(waiter)

			s.handleResultMessage(claudeagent.ResultMessage{
				Subtype: "success",
				Origin:  tc.origin,
			}, nil)

			if !hasMessageDone(*events) {
				t.Fatalf("no message_done for boundary origin %v", tc.name)
			}
			if err := assertTurnCompleted(t, waiter); err != nil {
				t.Fatalf("turn err = %v, want nil", err)
			}
			if s.sawDelta || s.sawMessageText {
				t.Fatalf("sawDelta=%v sawMessageText=%v, want both reset", s.sawDelta, s.sawMessageText)
			}
		})
	}
}

// 一条 result 只弹一个 waiter：通知那条不许吃掉真回合的 waiter。
func TestResultOriginNotificationThenRealCompletesOnce(t *testing.T) {
	s, events := newResultTestSession()
	first := make(chan error, 1)
	second := make(chan error, 1)
	s.enqueueTurn(first)
	s.enqueueTurn(second)

	s.handleResultMessage(claudeagent.ResultMessage{
		Subtype: "success",
		Origin:  &claudeagent.MessageOrigin{Kind: claudeagent.MessageOriginKindTaskNotification},
	}, nil)
	assertTurnStillRunning(t, first)
	assertTurnStillRunning(t, second)

	s.handleResultMessage(claudeagent.ResultMessage{Subtype: "success"}, nil)
	if err := assertTurnCompleted(t, first); err != nil {
		t.Fatalf("first turn err = %v, want nil", err)
	}
	assertTurnStillRunning(t, second)
	if !hasMessageDone(*events) {
		t.Fatalf("no message_done for the real turn")
	}
}

// 非人类来源的**错误** result 也不得把错误塞给本轮：它不是这一轮的终点。
func TestResultOriginErrorResultStillFailsTurn(t *testing.T) {
	s, events := newResultTestSession()
	waiter := make(chan error, 1)
	s.enqueueTurn(waiter)

	s.handleResultMessage(claudeagent.ResultMessage{
		Subtype: "error_during_execution",
		IsError: true,
		Errors:  []string{"background task failed"},
		Origin:  &claudeagent.MessageOrigin{Kind: claudeagent.MessageOriginKindTaskNotification},
	}, nil)

	if hasMessageDone(*events) {
		t.Fatalf("emitted message_done for non-boundary error result")
	}
	assertTurnStillRunning(t, waiter)
}
