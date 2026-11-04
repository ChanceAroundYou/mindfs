// 请求去重表的第二职责：记住首次受理时解析出的会话键，供重复请求回放。
//
// 客户端把「还没拿到真键的草稿」用原 request_id 在重连后重发一次；服务端只回键、
// 不重复跑回合（见 ws_session_message.go 的重复分支）。键记不下来的话，重放只能
// 回到空键 —— 客户端仍然停在 pending-*，下一次发送继续新开会话。
package api

import (
	"testing"
	"time"
)

func TestReplaySessionKeyReturnsFirstResolvedKey(t *testing.T) {
	handler := &WSHandler{}

	if _, reserved := handler.reserveClientRequest("req-1"); !reserved {
		t.Fatal("first request should be reserved")
	}
	if got := handler.replaySessionKey("req-1"); got != "" {
		t.Fatalf("key before recording = %q, want empty", got)
	}
	handler.recordClientRequestSession("req-1", "sess-a")

	if _, reserved := handler.reserveClientRequest("req-1"); reserved {
		t.Fatal("duplicate request must not be reserved again")
	}
	if got := handler.replaySessionKey("req-1"); got != "sess-a" {
		t.Fatalf("replayed key = %q, want sess-a", got)
	}
}

func TestReplaySessionKeyIsPerRequest(t *testing.T) {
	handler := &WSHandler{}
	handler.reserveClientRequest("req-1")
	handler.recordClientRequestSession("req-1", "sess-a")

	if got := handler.replaySessionKey("req-2"); got != "" {
		t.Fatalf("unseen request key = %q, want empty", got)
	}
	if got := handler.replaySessionKey(""); got != "" {
		t.Fatalf("empty request key = %q, want empty", got)
	}
}

func TestRecordClientRequestSessionIgnoresUnreservedRequest(t *testing.T) {
	handler := &WSHandler{}
	handler.recordClientRequestSession("req-1", "sess-a")
	if got := handler.replaySessionKey("req-1"); got != "" {
		t.Fatalf("key without reservation = %q, want empty", got)
	}
}

// 24h 清理照旧按 seenAt 丢记录；记录里多出来的会话键不改变清理判据。
func TestReserveClientRequestStillExpiresOldRecords(t *testing.T) {
	handler := &WSHandler{}
	handler.reserveClientRequest("stale")
	handler.recordClientRequestSession("stale", "sess-old")
	handler.reserveClientRequest("fresh")

	handler.requestMu.Lock()
	handler.requests["stale"] = clientRequestRecord{
		seenAt:     time.Now().Add(-25 * time.Hour),
		sessionKey: "sess-old",
	}
	handler.requestMu.Unlock()

	handler.reserveClientRequest("new")

	handler.requestMu.Lock()
	_, staleExists := handler.requests["stale"]
	_, freshExists := handler.requests["fresh"]
	handler.requestMu.Unlock()
	if staleExists {
		t.Fatal("expired request should be dropped")
	}
	if !freshExists {
		t.Fatal("non-expired request should survive cleanup")
	}
}
