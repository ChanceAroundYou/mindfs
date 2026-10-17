package claude

import (
	"context"
	"strings"
	"testing"
	"time"

	types "mindfs/server/internal/agent/types"
)

func newAskUserTestSession() *session {
	return &session{
		sessionKey:    "test",
		questionWaits: map[string]chan askUserAnswerResult{},
	}
}

func shrinkAskUserWaiterGrace(t *testing.T, grace time.Duration) {
	t.Helper()
	previous := askUserWaiterGrace
	askUserWaiterGrace = grace
	t.Cleanup(func() { askUserWaiterGrace = previous })
}

// 真机上，同一次 AskUserQuestion 的两半由不同路径、不同时刻送达：卡片（用户看得见、
// 能点）来自 finalized AssistantMessage 的 tool_use 块，而**应答通道**来自随后约 74ms
// 才到达的 can_use_tool 权限回调。这里按实测次序复现：先提交答案，再登记 waiter。
//
// 修之前 AnswerQuestion 当场返回 "question is not pending" —— 前端已把卡片翻成
// 「已回答」，CLI 仍在等权限应答，该轮永久卡死。所以这个测试钉的是「答案不许被拒」。
func TestAnswerQuestionAcceptedBeforeWaiterRegistered(t *testing.T) {
	shrinkAskUserWaiterGrace(t, time.Second)
	s := newAskUserTestSession()

	answered := make(chan error, 1)
	go func() {
		answered <- s.AnswerQuestion(context.Background(), types.AskUserAnswer{
			ToolUseID: "call_late",
			Answers:   map[string]string{"Which color do you prefer?": "crimson"},
		})
	}()

	// can_use_tool 路径随后才登记 waiter（awaitAskUserQuestion 的形状）。
	waiter := make(chan askUserAnswerResult, 1)
	time.Sleep(30 * time.Millisecond)
	s.questionMu.Lock()
	s.questionWaits["call_late"] = waiter
	s.questionMu.Unlock()

	select {
	case err := <-answered:
		if err != nil {
			t.Fatalf("答案在窗口内被拒（真机上就是永久卡死）：%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("AnswerQuestion 迟迟不返回")
	}

	select {
	case result := <-waiter:
		if result.err != nil {
			t.Fatalf("waiter 收到错误：%v", result.err)
		}
		if result.answers["Which color do you prefer?"] != "crimson" {
			t.Fatalf("答案没送到 CLI：%#v", result.answers)
		}
	case <-time.After(time.Second):
		t.Fatal("答案没有送达 waiter —— CLI 永远等不到权限应答")
	}
}

// 窗口容忍有上限：确实不是待答提问的 callID 仍要报错，不能被静默吞掉。
func TestAnswerQuestionRejectsUnknownCallID(t *testing.T) {
	shrinkAskUserWaiterGrace(t, 30*time.Millisecond)
	s := newAskUserTestSession()

	start := time.Now()
	err := s.AnswerQuestion(context.Background(), types.AskUserAnswer{
		ToolUseID: "call_unknown",
		Answers:   map[string]string{"q": "a"},
	})
	if err == nil || !strings.Contains(err.Error(), "question is not pending") {
		t.Fatalf("未知 callID 应报 not pending，得到 %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("判死用了 %v，窗口容忍不应超出 grace", elapsed)
	}
}

// 等待期间调用方上下文结束（WS 连接断了）要立刻返回，不能一直占着窗口。
func TestAnswerQuestionReturnsWhenContextDone(t *testing.T) {
	shrinkAskUserWaiterGrace(t, 5*time.Second)
	s := newAskUserTestSession()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- s.AnswerQuestion(ctx, types.AskUserAnswer{
			ToolUseID: "call_never",
			Answers:   map[string]string{"q": "a"},
		})
	}()
	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("上下文已取消，AnswerQuestion 应报错")
		}
	case <-time.After(time.Second):
		t.Fatal("上下文取消后 AnswerQuestion 仍未返回")
	}
}
