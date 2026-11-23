package claude

import (
	"testing"

	claudeagent "github.com/roasbeef/claude-agent-sdk-go"

	agenttypes "mindfs/server/internal/agent/types"
)

func TestResultErrCarriesTerminalReason(t *testing.T) {
	reason := claudeagent.TerminalReasonBlockingLimit
	err := resultErr(claudeagent.ResultMessage{
		IsError:        true,
		Result:         "Prompt is too long",
		TerminalReason: &reason,
	})
	if got := agenttypes.TurnTerminalReason(err); got != "blocking_limit" {
		t.Fatalf("TurnTerminalReason = %q, want blocking_limit (err=%v)", got, err)
	}
	if err == nil || err.Error() != "Prompt is too long" {
		t.Fatalf("error text changed: %v", err)
	}
}

func TestResultErrSuccessIgnoresTerminalReason(t *testing.T) {
	reason := claudeagent.TerminalReasonCompleted
	if err := resultErr(claudeagent.ResultMessage{
		Status:         "success",
		Subtype:        "success",
		TerminalReason: &reason,
	}); err != nil {
		t.Fatalf("success result returned error %v", err)
	}
}

// 失败 result 的正文是 harness 错误文案，不能当助手输出发出去 —— 这正是
// `Prompt is too longPrompt is too long` 的第一份来源。
func TestHandleResultMessageDoesNotEmitErrorResultAsBody(t *testing.T) {
	s, events := newResultTestSession()
	waiter := make(chan error, 1)
	s.enqueueTurn(waiter)
	reason := claudeagent.TerminalReasonBlockingLimit

	s.handleResultMessage(claudeagent.ResultMessage{
		IsError:        true,
		Result:         "Prompt is too long",
		TerminalReason: &reason,
	}, nil)

	for _, event := range *events {
		if event.Type == agenttypes.EventTypeMessageChunk {
			t.Fatalf("error result was emitted as body chunk: %#v", event)
		}
	}
	var turnErr error
	select {
	case turnErr = <-waiter:
	default:
		t.Fatal("turn did not complete")
	}
	if turnErr == nil {
		t.Fatal("error result completed the turn without error")
	}
	if got := agenttypes.TurnTerminalReason(turnErr); got != "blocking_limit" {
		t.Fatalf("turn error reason = %q, want blocking_limit (err=%v)", got, turnErr)
	}
}

// 线上观测到的重复正文是「同一轮失败 → 补发 continue → 再失败」，两条错误
// result 的 Result 都被当成正文，MessageChunk 直接拼成
// `Prompt is too longPrompt is too long`。这里把两条 result 依次喂进来，钉住
// 无论哪一条都不再变成正文。
func TestRepeatedOverflowResultsNeverBecomeBodyText(t *testing.T) {
	s, events := newResultTestSession()
	waiter := make(chan error, 1)
	s.enqueueTurn(waiter)
	reason := claudeagent.TerminalReasonBlockingLimit
	result := claudeagent.ResultMessage{
		IsError:        true,
		Result:         "Prompt is too long",
		TerminalReason: &reason,
	}

	s.handleResultMessage(result, nil)
	s.handleResultMessage(result, nil)

	for _, event := range *events {
		if event.Type == agenttypes.EventTypeMessageChunk {
			t.Fatalf("overflow result leaked into body: %#v", event)
		}
	}
	if err := <-waiter; agenttypes.TurnTerminalReason(err) != "blocking_limit" {
		t.Fatalf("first turn error = %v, want blocking_limit", err)
	}
}

func TestHandleResultMessageStillEmitsSuccessfulResultBody(t *testing.T) {
	s, events := newResultTestSession()
	waiter := make(chan error, 1)
	s.enqueueTurn(waiter)

	s.handleResultMessage(claudeagent.ResultMessage{
		Status:  "success",
		Subtype: "success",
		Result:  "final text",
	}, nil)

	found := false
	for _, event := range *events {
		if event.Type != agenttypes.EventTypeMessageChunk {
			continue
		}
		chunk, ok := event.Data.(agenttypes.MessageChunk)
		if !ok {
			t.Fatalf("message chunk payload = %#v", event.Data)
		}
		if chunk.Content == "final text" {
			found = true
		}
	}
	if !found {
		t.Fatalf("successful result body was not emitted: %#v", *events)
	}
	if err := <-waiter; err != nil {
		t.Fatalf("successful turn returned %v", err)
	}
}
