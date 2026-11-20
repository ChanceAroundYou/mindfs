package claude

import (
	"encoding/json"
	"testing"
)

// message_delta 是每段回合都会出现的正常帧（携带 stop_reason / usage）。它过去
// 不在已知帧白名单里，于是每个回合都打一条 output.unhandled.partial ——
// 2026-11-20 实测两小时 1,672 条，真正的未知帧警告被彻底埋掉。
func TestKnownNonDisplayPartialEventCoversMessageDelta(t *testing.T) {
	raw := json.RawMessage(`{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"cache_read_input_tokens":100224,"input_tokens":1926,"output_tokens":690}}`)
	if !isKnownNonDisplayPartialEvent(raw) {
		t.Fatal("message_delta 是正常帧，必须算已知事件")
	}
}

// 白名单不能退化成「什么都放行」：没见过的 partial 仍要报出来，否则适配层
// 升级引入的新帧类型就永远不会有人发现。
func TestKnownNonDisplayPartialEventStillReportsUnknownTypes(t *testing.T) {
	raw := json.RawMessage(`{"type":"brand_new_partial","delta":{"type":"mystery"}}`)
	if isKnownNonDisplayPartialEvent(raw) {
		t.Fatal("未知帧类型不能被白名单吞掉")
	}
}
