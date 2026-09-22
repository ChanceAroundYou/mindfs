package api

import (
	"testing"
	"time"
)

// 用户消息的持久化 seq 必须随 session.user_message 下发：客户端据此把本地乐观条目
// （无 seq）收敛为已持久化条目，否则它会以瞬时项身份与窗口取回的同一条消息重复渲染。
func TestBuildSessionUserMessageResponseCarriesUserExchangeSeq(t *testing.T) {
	ts := time.Date(2026, 9, 10, 3, 14, 11, 0, time.UTC)

	withSeq := buildSessionUserMessageResponse(
		"root", "sess", "chat", "", "claude", "m", "", "", "", "", false,
		"hello", ts, false, 42,
	)
	if withSeq.Type != "session.user_message" {
		t.Fatalf("type = %q", withSeq.Type)
	}
	exchange, ok := withSeq.Payload["exchange"].(map[string]any)
	if !ok {
		t.Fatalf("exchange payload type = %T", withSeq.Payload["exchange"])
	}
	if got := exchange["seq"]; got != 42 {
		t.Fatalf("exchange.seq = %v, want 42", got)
	}
	if got := exchange["content"]; got != "hello" {
		t.Fatalf("exchange.content = %v", got)
	}

	// seq 未知（0）时不得下发假 seq——客户端需保持旧的瞬时项行为。
	withoutSeq := buildSessionUserMessageResponse(
		"root", "sess", "chat", "", "claude", "m", "", "", "", "", false,
		"hello", ts, false, 0,
	)
	exchangeNoSeq, ok := withoutSeq.Payload["exchange"].(map[string]any)
	if !ok {
		t.Fatalf("exchange payload type = %T", withoutSeq.Payload["exchange"])
	}
	if _, present := exchangeNoSeq["seq"]; present {
		t.Fatalf("exchange.seq must be omitted when unknown, got %v", exchangeNoSeq["seq"])
	}
}
