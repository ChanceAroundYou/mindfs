package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/server/internal/nodeinfo"
	"mindfs/server/internal/webpush"
)

func newRelayTestHandler(t *testing.T) *HTTPHandler {
	t.Helper()
	svc := webpush.NewService(webpush.Config{}, webpush.NewStoreAt(filepath.Join(t.TempDir(), "push")))
	return &HTTPHandler{AppContext: &AppContext{WebPush: svc}}
}

func postRelay(t *testing.T, h *HTTPHandler, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/web-push/relay", strings.NewReader(body))
	h.handleWebPushRelay(rec, req)
	return rec
}

func TestWebPushRelayAcceptsRelativePayload(t *testing.T) {
	h := newRelayTestHandler(t)
	body, err := json.Marshal(webpush.RelayRequest{
		EventID: "event-1",
		Payload: webpush.Payload{
			Type:  "session.done",
			Title: "MindFS · 会话 · 完成",
			URL:   "./?root=root&session=sess",
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rec := postRelay(t, h, string(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

// 通知点击会 openWindow(url)：绝对 URL 必须挡在中继入口，不能让它变成钓鱼通道。
func TestWebPushRelayRejectsAbsoluteURL(t *testing.T) {
	h := newRelayTestHandler(t)
	body := `{"event_id":"e1","payload":{"type":"session.done","title":"x","url":"https://evil.example/login"}}`
	rec := postRelay(t, h, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestWebPushRelayRejectsUnknownTypeAndBadJSON(t *testing.T) {
	h := newRelayTestHandler(t)
	if rec := postRelay(t, h, `{"event_id":"e1","payload":{"type":"drop.tables","url":"./"}}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown type status = %d", rec.Code)
	}
	if rec := postRelay(t, h, `{`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json status = %d", rec.Code)
	}
}

// 中继只在控制面提供：worker 上一律 403（订阅表只在主节点）。
func TestWebPushRelayIsControlPlaneOnly(t *testing.T) {
	if !nodeinfo.IsControlPlane("/api/web-push/relay") {
		t.Fatal("/api/web-push/relay must stay on the control plane")
	}
}
