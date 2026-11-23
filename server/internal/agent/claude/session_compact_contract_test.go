package claude

import (
	"testing"

	claudeagent "github.com/roasbeef/claude-agent-sdk-go"

	"mindfs/server/internal/agent/conformance"
	"mindfs/server/internal/agent/types"
)

// compact 事件的跨 harness 共同面是**归一化之后**的事件：同一个
// types.EventTypeCompact、同一份 types.CompactNotice 载荷。wire 形态（Claude 的
// compact boundary）是适配器自己的实现细节，只由本包用例钉住，不写进跨 harness
// 契约。这里拿 conformance 的契约常量来判 —— 常量漂移或适配器漂移都会让本用例变红。
func TestClaudeCompactEventSatisfiesCrossHarnessContract(t *testing.T) {
	var got types.Event
	s := &session{sessionID: "claude-contract", onUpdate: func(event types.Event) { got = event }}

	s.handleCompactBoundaryMessage(claudeagent.CompactBoundaryMessage{
		UUID:            "compact-contract",
		SessionID:       "claude-contract",
		CompactMetadata: claudeagent.CompactMetadata{Trigger: "auto", PreTokens: 100},
	})

	if _, ok := got.Data.(types.CompactNotice); !ok {
		t.Fatalf("compact payload = %T, want types.CompactNotice", got.Data)
	}
	if err := conformance.CompactContract.Matches(string(got.Type), got.Data); err != nil {
		t.Fatalf("compact event violates cross-harness contract: %v", err)
	}
}
