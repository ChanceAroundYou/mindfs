package conformance

import (
	"strings"
	"testing"
)

// 采集层的用例：session.stream 的两种形状、字段折叠、以及「哪些事件算输出」。
// 这三处错了，真机探针会把「没收到」当成「一致的」。

func TestStreamEventsReadsSingleAndBatch(t *testing.T) {
	single := map[string]any{"event": map[string]any{"type": "message_chunk", "data": map[string]any{"text": "OK"}}}
	if events := streamEvents(single); len(events) != 1 || events[0].Type != "message_chunk" {
		t.Fatalf("单条 event 形状没读出来：%#v", events)
	}
	batch := map[string]any{"events": []any{
		map[string]any{"type": "message_chunk"},
		map[string]any{"type": "message_done", "data": map[string]any{"tokenUsage": map[string]any{"inputTokens": 1}}},
	}}
	events := streamEvents(batch)
	if len(events) != 2 || events[1].Type != "message_done" {
		t.Fatalf("批量 events 形状没读出来：%#v", events)
	}
}

func TestFlattenDataStopsAtThreeLevels(t *testing.T) {
	fields := flattenData(map[string]any{
		"contextWindow": map[string]any{"totalTokens": 1},
		"tokenUsage":    map[string]any{"inputTokens": 2, "nested": map[string]any{"deep": map[string]any{"deeper": 3}}},
	})
	joined := strings.Join(fields, ",")
	for _, want := range []string{"data.contextWindow", "data.contextWindow.totalTokens", "data.tokenUsage.inputTokens"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("缺少字段路径 %q：%s", want, joined)
		}
	}
	if strings.Contains(joined, "deeper") {
		t.Fatalf("超过三层的字段不应记入：%s", joined)
	}
}

func TestIsOutputEvent(t *testing.T) {
	for _, eventType := range []string{"message_chunk", "thought_chunk", "tool_call"} {
		if !isOutputEvent(eventType) {
			t.Fatalf("%s 必须算输出（取消要等它出现才发）", eventType)
		}
	}
	for _, eventType := range []string{"message_done", "todo_update", ""} {
		if isOutputEvent(eventType) {
			t.Fatalf("%s 不该算输出", eventType)
		}
	}
}

// 「harness 不可用」只认实例自己这么说。会话没建起来（create_failed: root not found）
// 是探针或实例配置的问题 —— 把它当跳过，一个坏掉的 harness 就会伪装成「没跑」。
func TestHarnessUnavailableOnlyOnExplicitSignal(t *testing.T) {
	for _, message := range []string{
		"session.create_failed: agent pool unavailable",
		"codex not installed",
		"harness 未安装",
	} {
		if !harnessUnavailable(message) {
			t.Fatalf("%q 应当被判定为 harness 不可用", message)
		}
	}
	for _, message := range []string{
		"session.create_failed: root not found",
		"session.create_failed: invalid request",
		"",
	} {
		if harnessUnavailable(message) {
			t.Fatalf("%q 不该被判定为 harness 不可用（那是失败，不是跳过）", message)
		}
	}
}

func TestErrorFrameVerdict(t *testing.T) {
	cases := []struct {
		name      string
		code      string
		message   string
		sawOutput bool
		want      errorVerdict
	}{
		{"unavailable", "session.create_failed", "agent pool unavailable", false, verdictSkip},
		{"create failed", "session.create_failed", "root not found", false, verdictFailure},
		{"cancel 之后的错误帧", "", "turn cancelled", true, verdictTerminal},
		{"取消回合一开始就报错", "session.error", "boom", false, verdictFailure},
	}
	for _, tc := range cases {
		if got, _ := errorFrameVerdict(tc.code, tc.message, tc.sawOutput); got != tc.want {
			t.Fatalf("%s: verdict = %v, want %v", tc.name, got, tc.want)
		}
	}
	if _, detail := errorFrameVerdict("session.create_failed", "root not found", false); !strings.Contains(detail, "root not found") {
		t.Fatalf("失败明细必须带上实例给的原因，实际 %q", detail)
	}
}
