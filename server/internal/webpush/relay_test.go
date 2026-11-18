package webpush

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// relayRecorder 收 worker 中继过来的请求；带缓冲，避免 handler 被自身的同步语义卡住。
type relayRecorder struct {
	requests chan RelayRequest
}

func newRelayRecorder(t *testing.T) (*relayRecorder, *httptest.Server) {
	t.Helper()
	rec := &relayRecorder{requests: make(chan RelayRequest, 8)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req RelayRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		rec.requests <- req
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return rec, srv
}

func (r *relayRecorder) next(t *testing.T) RelayRequest {
	t.Helper()
	select {
	case req := <-r.requests:
		return req
	case <-time.After(5 * time.Second):
		t.Fatal("relay did not receive a request")
		return RelayRequest{}
	}
}

func (r *relayRecorder) expectNone(t *testing.T) {
	t.Helper()
	select {
	case req := <-r.requests:
		t.Fatalf("unexpected relay request: %#v", req)
	case <-time.After(300 * time.Millisecond):
	}
}

// worker 上没有 VAPID 密钥、没有订阅表，只有 relayURL —— 通知必须仍然被转发。
func TestRelayForwardsPayloadWithoutLocalKeys(t *testing.T) {
	rec, srv := newRelayRecorder(t)
	service := NewService(Config{}, nil)
	if err := service.SetRelayURL(srv.URL); err != nil {
		t.Fatalf("SetRelayURL: %v", err)
	}
	if service.Enabled() {
		t.Fatal("service without VAPID keys must not report local sending enabled")
	}

	service.NotifyPayload(context.Background(), "event-1", Payload{
		Type:  "session.done",
		Title: "MindFS · 会话 · 完成",
		Tag:   "mindfs:session.done:root:sess",
		URL:   "./?root=root&session=sess",
	})

	req := rec.next(t)
	if req.EventID != "event-1" {
		t.Fatalf("event id = %q", req.EventID)
	}
	if req.Payload.Type != "session.done" || req.Payload.URL != "./?root=root&session=sess" {
		t.Fatalf("payload = %#v", req.Payload)
	}
}

// 同一事件重复通知只中继一次（事件账本去重是跨节点的：控制节点也有一份 recent）。
func TestRelayDeduplicatesEventID(t *testing.T) {
	rec, srv := newRelayRecorder(t)
	service := NewService(Config{}, nil)
	if err := service.SetRelayURL(srv.URL); err != nil {
		t.Fatalf("SetRelayURL: %v", err)
	}

	payload := Payload{Type: "session.ask_user", Title: "需要输入", URL: "./"}
	service.NotifyPayload(context.Background(), "event-dup", payload)
	rec.next(t)
	service.NotifyPayload(context.Background(), "event-dup", payload)
	rec.expectNone(t)
}

// 没有 relayURL 时不发任何外部请求（控制节点行为不变）。
func TestRelayDisabledDoesNothing(t *testing.T) {
	rec, _ := newRelayRecorder(t)
	service := NewService(Config{}, nil)
	service.NotifyPayload(context.Background(), "event-2", Payload{Type: "session.done", URL: "./"})
	rec.expectNone(t)
}

func TestSetRelayURLRejectsNonHTTPSchemes(t *testing.T) {
	service := NewService(Config{}, nil)
	for _, raw := range []string{"file:///etc/passwd", "ftp://example.com/relay", "not a url"} {
		if err := service.SetRelayURL(raw); err == nil {
			t.Fatalf("SetRelayURL(%q) should fail", raw)
		}
	}
	if service.RelayEnabled() {
		t.Fatal("rejected relay url must leave relaying disabled")
	}
	// 宿主机名缺失同样拒绝。
	if err := service.SetRelayURL("http:///api/web-push/relay"); err == nil {
		t.Fatal("relay url without host should fail")
	}
}

func TestValidateRelayedPayloadRejectsForeignURLs(t *testing.T) {
	if err := ValidateRelayedPayload(Payload{Type: "session.done", URL: "https://evil.example/login"}); err == nil {
		t.Fatal("absolute url must be rejected: notification click would open it")
	}
	if err := ValidateRelayedPayload(Payload{Type: "session.done", URL: "./?root=r"}); err != nil {
		t.Fatalf("relative url rejected: %v", err)
	}
	if err := ValidateRelayedPayload(Payload{Type: "session.done", URL: "/mindfs/?root=r"}); err != nil {
		t.Fatalf("root-relative url rejected: %v", err)
	}
	if err := ValidateRelayedPayload(Payload{Type: "unknown.kind"}); err == nil {
		t.Fatal("unknown notification type must be rejected")
	}
}
