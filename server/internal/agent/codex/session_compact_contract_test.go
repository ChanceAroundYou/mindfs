package codex

import (
	"encoding/json"
	"testing"

	codextypes "github.com/fanwenlin/codex-go-sdk/types"

	"mindfs/server/internal/agent/conformance"
	"mindfs/server/internal/agent/types"
)

// compact 事件的跨 harness 共同面是**归一化之后**的事件：同一个
// types.EventTypeCompact、同一份 types.CompactNotice 载荷。Codex 的三种 wire 形态
// （compacted item / unknown 与 typed contextCompaction）是适配器实现细节，只由本包
// 用例钉住，不写进跨 harness 契约。这里拿 conformance 的契约常量来判 —— 常量漂移
// 或适配器漂移都会让本用例变红。
func TestCodexCompactEventSatisfiesCrossHarnessContract(t *testing.T) {
	s := &session{threadID: "thread-contract"}
	var got types.Event
	s.OnUpdate(func(event types.Event) { got = event })

	if !s.handleNonToolItem(&codextypes.UnknownItem{
		Type: "contextCompaction",
		Raw:  json.RawMessage(`{"id":"compact-contract","summary":"Compacted old context"}`),
	}, false) {
		t.Fatal("contextCompaction item was not handled")
	}

	if _, ok := got.Data.(types.CompactNotice); !ok {
		t.Fatalf("compact payload = %T, want types.CompactNotice", got.Data)
	}
	if err := conformance.CompactContract.Matches(string(got.Type), got.Data); err != nil {
		t.Fatalf("compact event violates cross-harness contract: %v", err)
	}
}
