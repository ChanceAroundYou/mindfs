package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/server/internal/channels/feishu"
	"mindfs/server/internal/nodeinfo"
	"mindfs/server/internal/nodes"
)

func relayTestSetup(t *testing.T) (*HTTPHandler, *feishu.Service, string) {
	t.Helper()
	store := feishu.NewStore(filepath.Join(t.TempDir(), "cards.json"))
	svc := feishu.New(feishu.Config{}, store, nil)
	nodeStore, err := nodes.NewStoreAt(filepath.Join(t.TempDir(), "nodes.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := nodeStore.Replace([]nodes.NodeConnection{{ID: "wsl", Name: "wsl", URL: "https://wsl.example"}}); err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("k", 32)
	h := &HTTPHandler{
		AppContext:       &AppContext{Feishu: svc, Nodes: nodeStore},
		NodeRole:         nodeinfo.RoleControl,
		FeishuRelayToken: token,
	}
	return h, svc, token
}

func relayTestBody(t *testing.T, nodeID string) []byte {
	t.Helper()
	body, err := json.Marshal(feishu.RelayRequest{
		NodeID: nodeID,
		Notice: feishu.Notice{CardID: "relay-card", Kind: feishu.KindNotice, NodeID: nodeID, Text: "from worker"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func relayRequest(token string, body []byte) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/channels/feishu/relay", bytes.NewReader(body))
	if token != "" {
		req.Header.Set(feishu.RelayTokenHeader, token)
	}
	return req
}

func TestRelayRejectsMissingShortAndWrongToken(t *testing.T) {
	h, _, token := relayTestSetup(t)
	cases := []struct {
		name  string
		token string
	}{
		{"missing", ""},
		{"short", "too-short"},
		{"wrong", strings.Repeat("x", 32)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.handleFeishuRelay(rec, relayRequest(tc.token, relayTestBody(t, "wsl")))
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("token=%s got %d, want 401: %s", tc.name, rec.Code, rec.Body.String())
			}
		})
	}
	// 正确 token（即使带空白）必须放行。
	rec := httptest.NewRecorder()
	h.handleFeishuRelay(rec, relayRequest("  "+token+"\n", relayTestBody(t, "wsl")))
	if rec.Code != http.StatusOK {
		t.Fatalf("valid token got %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestRelayAcceptsWhitespacePaddedToken(t *testing.T) {
	h, _, token := relayTestSetup(t)
	rec := httptest.NewRecorder()
	h.handleFeishuRelay(rec, relayRequest("  "+token+"\n", relayTestBody(t, "wsl")))
	if rec.Code != http.StatusOK {
		t.Fatalf("padded token got %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestRelayRejectsUnconfiguredNode(t *testing.T) {
	h, _, token := relayTestSetup(t)
	for _, nodeID := range []string{"", "local", "attacker"} {
		rec := httptest.NewRecorder()
		h.handleFeishuRelay(rec, relayRequest(token, relayTestBody(t, nodeID)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("node=%q got %d, want 400: %s", nodeID, rec.Code, rec.Body.String())
		}
	}
}

func TestRelayIgnoresBodyURLAndChecksNoticeNode(t *testing.T) {
	h, _, token := relayTestSetup(t)
	body := []byte(`{"node_id":"wsl","url":"https://attacker.example","notice":{"CardID":"card-x","Kind":"notice","NodeID":"attacker"}}`)
	rec := httptest.NewRecorder()
	h.handleFeishuRelay(rec, relayRequest(token, body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("mismatched notice node got %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestRelayBodyLimit(t *testing.T) {
	h, _, token := relayTestSetup(t)
	body := append(relayTestBody(t, "wsl"), bytes.Repeat([]byte("x"), 64<<10)...)
	rec := httptest.NewRecorder()
	h.handleFeishuRelay(rec, relayRequest(token, body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized body got %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestRelayWithoutServiceReturns503(t *testing.T) {
	h, _, token := relayTestSetup(t)
	h.AppContext.Feishu = nil
	rec := httptest.NewRecorder()
	h.handleFeishuRelay(rec, relayRequest(token, relayTestBody(t, "wsl")))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("absent service got %d, want 503: %s", rec.Code, rec.Body.String())
	}
}

func TestRelaySendsNoticeToService(t *testing.T) {
	h, svc, token := relayTestSetup(t)
	rec := httptest.NewRecorder()
	h.handleFeishuRelay(rec, relayRequest(token, relayTestBody(t, "wsl")))
	if rec.Code != http.StatusOK {
		t.Fatalf("relay got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	card, ok := svc.Store().Get("relay-card")
	if !ok {
		t.Fatal("relayed notice did not reach the control node's card store")
	}
	if card.NodeID != "wsl" || card.Text != "from worker" {
		t.Fatalf("relayed card = %+v", card)
	}
}

// 行为测试无法从外部看见「比较是否常数时间」，这里钉住实现手段本身。
func TestFeishuRelayUsesConstantTimeTokenCompare(t *testing.T) {
	src, err := os.ReadFile("http_feishu_relay.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	if !strings.Contains(body, "subtle.ConstantTimeCompare") {
		t.Fatal("relay token must be compared with crypto/subtle.ConstantTimeCompare")
	}
	if strings.Contains(body, "presented != configured") {
		t.Fatal("relay token must not be compared with plain string equality")
	}
}

func TestRelayRouteRegisteredAndControlPlaneOnly(t *testing.T) {
	h, _, token := relayTestSetup(t)
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, relayRequest(token, relayTestBody(t, "wsl")))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/channels/feishu/relay via Routes got %d, want 200: %s", rec.Code, rec.Body.String())
	}

	worker := *h
	worker.NodeRole = nodeinfo.RoleWorker
	rec = httptest.NewRecorder()
	worker.Routes().ServeHTTP(rec, relayRequest(token, relayTestBody(t, "wsl")))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("worker POST relay got %d, want 403: %s", rec.Code, rec.Body.String())
	}

	// 数据面端点不能被控制面守卫顺手吃掉。
	guard := worker.rejectControlPlaneOnWorker(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for _, path := range []string{"/api/channels", "/api/channels/feishu/answer"} {
		rec = httptest.NewRecorder()
		guard.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("worker POST %s got %d, want 200", path, rec.Code)
		}
	}
}
