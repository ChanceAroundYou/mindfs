package claude

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	types "mindfs/server/internal/agent/types"

	claudeagent "github.com/roasbeef/claude-agent-sdk-go"
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

// 提问参数读不出来或没有问题时要**拒绝**，不能放行。
//
// 放行意味着 CLI 自行执行这次提问、拿不到任何答案，模型收到的是「用户没有回答」这个
// 凭空捏造的结果；拒绝则把真实原因作为 tool_result 回给模型，它可以换个问法或直接继续。
func TestHandleCanUseToolDeniesUnreadableAskUserQuestion(t *testing.T) {
	s := newAskUserTestSession()

	for name, arguments := range map[string]string{
		"arguments 不是 JSON": `{`,
		"questions 为空":      `{"questions":[]}`,
		"没有 questions 字段":  `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			result := s.handleCanUseTool(context.Background(), claudeagent.ToolPermissionRequest{
				ToolName:  "AskUserQuestion",
				Arguments: json.RawMessage(arguments),
				Context:   claudeagent.PermissionContext{ToolUseID: "call_bad", SessionID: "test"},
			})
			deny, ok := result.(claudeagent.PermissionDeny)
			if !ok {
				t.Fatalf("result = %#v, want PermissionDeny（放行会让模型看到捏造的「用户没有回答」）", result)
			}
			if strings.TrimSpace(deny.Reason) == "" {
				t.Fatal("PermissionDeny 必须带原因，否则模型不知道这次提问为什么没送达")
			}
		})
	}
}

// 正常提问仍走放行路径，且必须把答案塞回 updatedInput（Deny 只针对读不出来的那种）。
func TestHandleCanUseToolAllowsAskUserQuestionWithAnswers(t *testing.T) {
	s := newAskUserTestSession()
	arguments := json.RawMessage(`{"questions":[{"question":"Which color do you prefer?","header":"Color","options":[{"label":"crimson"}]}]}`)

	done := make(chan claudeagent.PermissionResult, 1)
	go func() {
		done <- s.handleCanUseTool(context.Background(), claudeagent.ToolPermissionRequest{
			ToolName:  "AskUserQuestion",
			Arguments: arguments,
			Context:   claudeagent.PermissionContext{ToolUseID: "call_ok", SessionID: "test"},
		})
	}()

	// handleCanUseTool 自己登记应答通道（awaitAskUserQuestion），这里等它登记好再答。
	var waiter chan askUserAnswerResult
	for deadline := time.Now().Add(time.Second); waiter == nil; {
		s.questionMu.Lock()
		waiter = s.questionWaits["call_ok"]
		s.questionMu.Unlock()
		if waiter == nil {
			if !time.Now().Before(deadline) {
				t.Fatal("handleCanUseTool 没有登记应答通道")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waiter <- askUserAnswerResult{answers: claudeagent.Answers{"Which color do you prefer?": "crimson"}}

	select {
	case result := <-done:
		allow, ok := result.(claudeagent.PermissionAllow)
		if !ok {
			t.Fatalf("result = %#v, want PermissionAllow", result)
		}
		answers, _ := allow.UpdatedInput["answers"].(map[string]string)
		if answers["Which color do you prefer?"] != "crimson" {
			t.Fatalf("答案没塞回 updatedInput：%#v", allow.UpdatedInput)
		}
	case <-time.After(time.Second):
		t.Fatal("handleCanUseTool 没有返回")
	}
}
