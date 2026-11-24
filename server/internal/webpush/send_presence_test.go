package webpush

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	webpushgo "github.com/SherClockHolmes/webpush-go"
)

// 真实的 push 端点在本机是不可达的；用 httptest 冒充 WNS/FCM，只关心「有没有请求打进来」。
func newPushEndpoint(t *testing.T) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// 构造一条密钥合法的订阅（p256dh 为未压缩 P-256 公钥，auth 为 16 字节随机），
// 这样 webpush-go 能完成加密，只有「是否被过滤」会影响端点收到几次请求。
func newTestSubscription(t *testing.T, endpoint string, capable bool) Subscription {
	t.Helper()
	privateKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate subscription key: %v", err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatalf("generate auth secret: %v", err)
	}
	return Subscription{
		Endpoint: endpoint,
		Keys: SubscriptionKeys{
			Auth:   base64.RawURLEncoding.EncodeToString(auth),
			P256dh: base64.RawURLEncoding.EncodeToString(privateKey.PublicKey().Bytes()),
		},
		PresenceCapable: capable,
	}
}

func newPresenceTestService(t *testing.T, subs ...Subscription) *Service {
	t.Helper()
	privateKey, publicKey, err := webpushgo.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("generate vapid keys: %v", err)
	}
	store := NewStoreAt(t.TempDir())
	for _, sub := range subs {
		if _, err := store.Upsert(sub); err != nil {
			t.Fatalf("upsert subscription: %v", err)
		}
	}
	return NewService(Config{
		Enabled:    true,
		PublicKey:  publicKey,
		PrivateKey: privateKey,
		Subject:    "mindfs@example.com",
	}, store)
}

func testNotification() Payload {
	return Payload{Type: "session.done", Title: "完成", Body: "测试"}
}

// 可感知在场的订阅，页面关掉（无心跳）后一次都不许发。
func TestSendSkipsPresenceCapableSubscriptionWithoutHeartbeat(t *testing.T) {
	endpoint, hits := newPushEndpoint(t)
	svc := newPresenceTestService(t, newTestSubscription(t, endpoint.URL, true))
	svc.httpClient = endpoint.Client()

	if err := svc.send(context.Background(), testNotification()); err != nil {
		t.Fatalf("all subscriptions presence-skipped should be silent, got %v", err)
	}
	if got := atomic.LoadInt32(hits); got != 0 {
		t.Fatalf("push requests = %d, want 0（页面没心跳时不该投递）", got)
	}
}

// 页面心跳后投递，证明「过滤」不是因为密钥或客户端问题导致的假阴性。
func TestSendDeliversAfterPageHeartbeat(t *testing.T) {
	endpoint, hits := newPushEndpoint(t)
	svc := newPresenceTestService(t, newTestSubscription(t, endpoint.URL, true))
	svc.httpClient = endpoint.Client()
	if err := svc.ReportPresence(endpoint.URL, "tab-1", false); err != nil {
		t.Fatalf("report presence: %v", err)
	}

	if err := svc.send(context.Background(), testNotification()); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Fatalf("push requests = %d, want 1", got)
	}
}

// 关闭页面的 beacon 到达后立刻停止投递，不用等 TTL。
func TestSendStopsAfterPageLeave(t *testing.T) {
	endpoint, hits := newPushEndpoint(t)
	svc := newPresenceTestService(t, newTestSubscription(t, endpoint.URL, true))
	svc.httpClient = endpoint.Client()
	if err := svc.ReportPresence(endpoint.URL, "tab-1", false); err != nil {
		t.Fatalf("report presence: %v", err)
	}
	if err := svc.send(context.Background(), testNotification()); err != nil {
		t.Fatalf("send: %v", err)
	}
	if err := svc.ReportPresence(endpoint.URL, "tab-1", true); err != nil {
		t.Fatalf("report leave: %v", err)
	}
	if err := svc.send(context.Background(), testNotification()); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Fatalf("push requests = %d, want 1（leave 之后不能继续投递）", got)
	}
}

// 升级前落库的旧订阅没有 presence 能力，必须 fail-open，不能一升级就静音。
func TestSendKeepsLegacySubscriptionWithoutHeartbeat(t *testing.T) {
	endpoint, hits := newPushEndpoint(t)
	svc := newPresenceTestService(t, newTestSubscription(t, endpoint.URL, false))
	svc.httpClient = endpoint.Client()

	if err := svc.send(context.Background(), testNotification()); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Fatalf("push requests = %d, want 1（旧订阅保持原行为）", got)
	}
}

// 「发送测试」是用户当场点的，页面显然开着；即使心跳还没到也必须发出。
func TestSendTestToEndpointIgnoresPresence(t *testing.T) {
	endpoint, hits := newPushEndpoint(t)
	svc := newPresenceTestService(t, newTestSubscription(t, endpoint.URL, true))
	svc.httpClient = endpoint.Client()

	if err := svc.SendTestToEndpoint(context.Background(), endpoint.URL); err != nil {
		t.Fatalf("send test: %v", err)
	}
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Fatalf("push requests = %d, want 1", got)
	}
}

// TTL 到期后自动恢复静默（模拟浏览器崩溃、beacon 没送出去）。
func TestSendResumesSilenceAfterPresenceTTL(t *testing.T) {
	endpoint, hits := newPushEndpoint(t)
	svc := newPresenceTestService(t, newTestSubscription(t, endpoint.URL, true))
	svc.httpClient = endpoint.Client()
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	svc.presenceTracker().now = func() time.Time { return now }
	if err := svc.ReportPresence(endpoint.URL, "tab-1", false); err != nil {
		t.Fatalf("report presence: %v", err)
	}

	now = now.Add(defaultPresenceTTL + time.Minute)
	if err := svc.send(context.Background(), testNotification()); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got := atomic.LoadInt32(hits); got != 0 {
		t.Fatalf("push requests = %d, want 0（心跳过期后必须静默）", got)
	}
}
