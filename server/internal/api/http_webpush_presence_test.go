package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"mindfs/server/internal/webpush"
)

func newWebPushPresenceHandler(t *testing.T) (*HTTPHandler, *webpush.Service, *webpush.Store) {
	t.Helper()
	store := webpush.NewStoreAt(t.TempDir())
	svc := webpush.NewService(webpush.Config{
		Enabled:    true,
		PublicKey:  "test-public-key",
		PrivateKey: "test-private-key",
	}, store)
	return &HTTPHandler{AppContext: &AppContext{WebPush: svc}}, svc, store
}

func postWebPushJSON(t *testing.T, path, body string, handler http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler(rr, req)
	return rr
}

// 页面心跳与页面关闭都要能通过 HTTP 改变在场状态。
func TestWebPushPresenceHandlerTracksTouchAndLeave(t *testing.T) {
	h, svc, _ := newWebPushPresenceHandler(t)
	endpoint := "https://push.example/endpoint-a"

	touch := postWebPushJSON(t, "/api/web-push/presence",
		`{"endpoint":"`+endpoint+`","client_id":"tab-1"}`, h.handleWebPushPresence)
	if touch.Code != http.StatusOK {
		t.Fatalf("touch status = %d, body = %s", touch.Code, touch.Body.String())
	}
	var touchBody struct {
		OK     bool `json:"ok"`
		Active bool `json:"active"`
	}
	if err := json.Unmarshal(touch.Body.Bytes(), &touchBody); err != nil {
		t.Fatalf("touch body: %v", err)
	}
	if !touchBody.OK || !touchBody.Active {
		t.Fatalf("touch body = %+v, want ok+active", touchBody)
	}
	if !svc.PresenceActive(endpoint) {
		t.Fatal("心跳后服务端必须认为页面开着")
	}

	leave := postWebPushJSON(t, "/api/web-push/presence",
		`{"endpoint":"`+endpoint+`","client_id":"tab-1","leave":true}`, h.handleWebPushPresence)
	if leave.Code != http.StatusOK {
		t.Fatalf("leave status = %d, body = %s", leave.Code, leave.Body.String())
	}
	var leaveBody struct {
		OK     bool `json:"ok"`
		Active bool `json:"active"`
	}
	if err := json.Unmarshal(leave.Body.Bytes(), &leaveBody); err != nil {
		t.Fatalf("leave body: %v", err)
	}
	if !leaveBody.OK || leaveBody.Active {
		t.Fatalf("leave body = %+v, want ok+inactive", leaveBody)
	}
	if svc.PresenceActive(endpoint) {
		t.Fatal("页面关闭后服务端必须立刻认为页面不在")
	}
}

// 没带 endpoint 的上报无处可记，必须是 400 而不是静默 200。
func TestWebPushPresenceHandlerRequiresEndpoint(t *testing.T) {
	h, _, _ := newWebPushPresenceHandler(t)
	rr := postWebPushJSON(t, "/api/web-push/presence", `{"client_id":"tab-1"}`, h.handleWebPushPresence)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rr.Code, rr.Body.String())
	}
}

// 上报订阅时声明 presence 的客户端从此受在场约束；旧客户端不声明，保持 fail-open。
func TestWebPushSubscriptionSaveMarksPresenceCapable(t *testing.T) {
	h, svc, store := newWebPushPresenceHandler(t)
	endpoint := "https://push.example/endpoint-b"

	rr := postWebPushJSON(t, "/api/web-push/subscriptions",
		`{"endpoint":"`+endpoint+`","presence":true,"client_id":"tab-1","keys":{"auth":"auth-key","p256dh":"p256dh-key"}}`,
		h.handleWebPushSubscriptionSave)
	if rr.Code != http.StatusOK {
		t.Fatalf("save status = %d, body = %s", rr.Code, rr.Body.String())
	}
	sub, ok, err := store.GetByEndpoint(endpoint)
	if err != nil || !ok {
		t.Fatalf("subscription missing: ok=%v err=%v", ok, err)
	}
	if !sub.PresenceCapable {
		t.Fatal("声明 presence 的订阅必须被记为 presence-capable")
	}
	if !svc.PresenceActive(endpoint) {
		t.Fatal("上报订阅本身就证明页面开着，应当立刻算作在场")
	}

	// 之后再上报（旧客户端不会带 presence）不许把能力抹掉：它描述的是客户端能力。
	rr = postWebPushJSON(t, "/api/web-push/subscriptions",
		`{"endpoint":"`+endpoint+`","keys":{"auth":"auth-key","p256dh":"p256dh-key"}}`,
		h.handleWebPushSubscriptionSave)
	if rr.Code != http.StatusOK {
		t.Fatalf("resave status = %d, body = %s", rr.Code, rr.Body.String())
	}
	sub, ok, err = store.GetByEndpoint(endpoint)
	if err != nil || !ok {
		t.Fatalf("subscription missing after resave: ok=%v err=%v", ok, err)
	}
	if !sub.PresenceCapable {
		t.Fatal("presence 能力一旦声明就不能被后续上报抹掉")
	}
}

// 旧客户端（从不声明 presence）不能被在场约束静音。
func TestWebPushSubscriptionSaveLeavesLegacyFailOpen(t *testing.T) {
	h, _, store := newWebPushPresenceHandler(t)
	endpoint := "https://push.example/endpoint-legacy"

	rr := postWebPushJSON(t, "/api/web-push/subscriptions",
		`{"endpoint":"`+endpoint+`","keys":{"auth":"auth-key","p256dh":"p256dh-key"}}`,
		h.handleWebPushSubscriptionSave)
	if rr.Code != http.StatusOK {
		t.Fatalf("save status = %d, body = %s", rr.Code, rr.Body.String())
	}
	sub, ok, err := store.GetByEndpoint(endpoint)
	if err != nil || !ok {
		t.Fatalf("subscription missing: ok=%v err=%v", ok, err)
	}
	if sub.PresenceCapable {
		t.Fatal("不声明 presence 的旧客户端必须保持 fail-open")
	}
}

// 旧订阅收到心跳/关页上报即证明页面已升级：必须立刻补上能力声明，不需要等
// 浏览器重新保存订阅（否则「关页也弹」会一直持续到下一次页面加载）。
func TestWebPushPresenceReportMarksLegacySubscriptionCapable(t *testing.T) {
	h, svc, store := newWebPushPresenceHandler(t)
	endpoint := "https://push.example/endpoint-legacy-heartbeat"

	rr := postWebPushJSON(t, "/api/web-push/subscriptions",
		`{"endpoint":"`+endpoint+`","keys":{"auth":"auth-key","p256dh":"p256dh-key"}}`,
		h.handleWebPushSubscriptionSave)
	if rr.Code != http.StatusOK {
		t.Fatalf("save status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if sub, ok, err := store.GetByEndpoint(endpoint); err != nil || !ok || sub.PresenceCapable {
		t.Fatalf("precondition: ok=%v err=%v capable=%v", ok, err, sub.PresenceCapable)
	}

	rr = postWebPushJSON(t, "/api/web-push/presence",
		`{"endpoint":"`+endpoint+`","client_id":"tab-1"}`, h.handleWebPushPresence)
	if rr.Code != http.StatusOK {
		t.Fatalf("presence status = %d, body = %s", rr.Code, rr.Body.String())
	}
	sub, ok, err := store.GetByEndpoint(endpoint)
	if err != nil || !ok {
		t.Fatalf("subscription missing after presence: ok=%v err=%v", ok, err)
	}
	if !sub.PresenceCapable {
		t.Fatal("presence 上报必须把旧订阅标记为 capable")
	}
	if !svc.PresenceActive(endpoint) {
		t.Fatal("心跳本身也应继续算作在场")
	}
}

// 订阅表被清空后页面还在心跳：上报不能凭空造一条没有密钥的记录。
func TestWebPushPresenceReportDoesNotCreateSubscription(t *testing.T) {
	h, _, store := newWebPushPresenceHandler(t)
	rr := postWebPushJSON(t, "/api/web-push/presence",
		`{"endpoint":"https://push.example/unknown","client_id":"tab-1"}`, h.handleWebPushPresence)
	if rr.Code != http.StatusOK {
		t.Fatalf("presence status = %d, body = %s", rr.Code, rr.Body.String())
	}
	items, err := store.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("subscriptions = %d, want 0（presence 上报不能创建订阅）", len(items))
	}
}
