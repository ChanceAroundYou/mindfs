package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mindfs/server/internal/api"
	"mindfs/server/internal/channels/feishu"
	"mindfs/server/internal/kanban"
	"mindfs/server/internal/nodes"
)

type feishuITRelayEnv struct {
	svc     *feishu.Service
	control *httptest.Server
	decoy   *httptest.Server
	worker  *httptest.Server
	hits    *atomic.Int32
	answers chan feishuITTaskCall
	ready   map[string]string
	answer  func(context.Context, feishu.Answer) error
}

// feishuITRelayHarness mounts the production HTTP routes on both ends and uses
// a real card store, a real RelayNotifier and a real feishuAnswerRelay. Only
// lark-cli is faked; no production handler is swapped out in package app.
func feishuITRelayHarness(t *testing.T) *feishuITRelayEnv {
	t.Helper()
	dir := t.TempDir()
	lark, _, ready := feishuITLark(t, dir, map[string]string{
		"card.action.trigger": feishuITClickEv,
	})
	nodesStore := feishuAnswerNodeStore(t,
		nodes.NodeConnection{ID: "home", Name: "home", URL: "http://127.0.0.1:1"},
	)
	hits := &atomic.Int32{}
	decoy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(decoy.Close)

	answers := make(chan feishuITTaskCall, 8)
	workerHandler := &api.HTTPHandler{FeishuRelayToken: feishuAnswerTestToken, FeishuNodeID: "fn", AppContext: &api.AppContext{}}
	worker := httptest.NewServer(feishuITCapture(workerHandler.Routes(), "/api/channels/feishu/answer", func(r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			t.Errorf("read answer body: %v", err)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		answers <- feishuITTaskCall{
			method: r.Method,
			path:   r.URL.Path,
			body:   body,
			token:  r.Header.Get(feishu.RelayTokenHeader),
		}
	}))
	t.Cleanup(worker.Close)
	if _, err := nodesStore.Upsert(nodes.NodeConnection{ID: "fn", Name: "fn", URL: worker.URL}); err != nil {
		t.Fatal(err)
	}

	svc := feishuITService(t, dir, lark)
	controlHandler := &api.HTTPHandler{FeishuRelayToken: feishuAnswerTestToken, FeishuNodeID: "home", AppContext: &api.AppContext{
		Feishu: svc,
		Nodes:  nodesStore,
	}}
	control := httptest.NewServer(controlHandler.Routes())
	t.Cleanup(control.Close)

	relay := &feishuAnswerRelay{
		Nodes:       nodesStore,
		LocalNodeID: "home",
		Token:       feishuAnswerTestToken,
		Client:      worker.Client(),
		Apply:       func(context.Context, feishu.Answer) error { return nil },
		Sleep: func(context.Context, time.Duration) error {
			t.Error("first-try answer delivery must not sleep")
			return nil
		},
	}
	svc.SetAnswerHandler(relay.Relay)
	return &feishuITRelayEnv{svc: svc, control: control, decoy: decoy, worker: worker,
		hits: hits, answers: answers, ready: ready, answer: relay.Relay}
}

type feishuITCaptureHandler struct {
	inner http.Handler
	path  string
	fn    func(*http.Request)
}

func (h feishuITCaptureHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == h.path {
		h.fn(r)
	}
	h.inner.ServeHTTP(w, r)
}

func feishuITCapture(inner http.Handler, path string, fn func(*http.Request)) http.Handler {
	return feishuITCaptureHandler{inner: inner, path: path, fn: fn}
}

func feishuITPost(t *testing.T, client *http.Client, url, token string, body any) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set(feishu.RelayTokenHeader, token)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// TestFeishuRelayEndToEndCardAndAnswer covers the four real hops: worker card
// -> RelayNotifier -> control relay endpoint -> control card store/lark, and a
// click's Answer -> feishuAnswerRelay -> worker answer endpoint -> local apply.
func TestFeishuRelayEndToEndCardAndAnswer(t *testing.T) {
	h := feishuITRelayHarness(t)
	notifier := &feishu.RelayNotifier{
		URL:    h.control.URL + "/api/channels/feishu/relay",
		Token:  feishuAnswerTestToken,
		NodeID: "fn",
		Client: h.control.Client(),
	}
	cardID := "card-relay-1"
	if err := notifier.NotifyCard(context.Background(), kanban.CardNotice{
		ID: cardID, Kind: string(feishu.KindNotice), RootID: "root-docs",
		TaskID: "task-1", TaskTitle: "处理说明",
	}); err != nil {
		t.Fatalf("relay notice: %v", err)
	}
	stored, ok := h.svc.Store().Get(cardID)
	if !ok || stored.NodeID != "fn" || stored.Kind != feishu.KindNotice {
		t.Fatalf("control card = %+v, want fn notice (found=%v)", stored, ok)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); h.svc.Wait() }()
	h.svc.Start(ctx)
	if err := h.svc.DeliverOnce(ctx); err != nil {
		t.Fatal(err)
	}
	feishuITWaitFor(t, "控制节点发出 worker 卡片", func() bool {
		c, ok := h.svc.Store().Get(cardID)
		return ok && c.MessageID != ""
	})

	answer := feishu.Answer{
		CardID: cardID, Kind: feishu.KindNotice, NodeID: "fn",
		AccountID: "u_1", RootID: "root-docs", TaskID: "task-1",
		Text: "已处理", Note: h.decoy.URL + "/api/channels/feishu/answer",
	}
	if err := h.answer(ctx, answer); err != nil {
		t.Fatalf("answer relay: %v", err)
	}
	select {
	case call := <-h.answers:
		if call.method != http.MethodPost || call.path != "/api/channels/feishu/answer" {
			t.Fatalf("worker answer request = %s %s", call.method, call.path)
		}
		if call.token != feishuAnswerTestToken {
			t.Fatalf("worker answer token header = %q", call.token)
		}
		var got feishu.Answer
		if err := json.Unmarshal(call.body, &got); err != nil {
			t.Fatalf("answer body is not JSON: %v", err)
		}
		if got != answer {
			t.Fatalf("worker answer = %+v, want %+v", got, answer)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("answer 没有被回传到 fn 节点")
	}
	if got := h.hits.Load(); got != 0 {
		t.Fatalf("载荷里的伪造 URL 被访问了 %d 次", got)
	}

	// A notice click settles locally; the answer wire path above is the relay.
	if err := os.WriteFile(h.ready["card.action.trigger"], []byte("go"), 0o600); err != nil {
		t.Fatal(err)
	}
	feishuITWaitFor(t, "控制节点卡片点击后结案", func() bool {
		c, ok := h.svc.Store().Get(cardID)
		return ok && c.State == feishu.StateDone
	})
}

// TestFeishuRelayEndToEndRejectsWrongToken proves the shared secret is checked
// on both relay endpoints and that a rejected relay creates no card.
func TestFeishuRelayEndToEndRejectsWrongToken(t *testing.T) {
	h := feishuITRelayHarness(t)
	bad := "ffffffffffffffffffffffffffffffff"
	resp := feishuITPost(t, h.control.Client(), h.control.URL+"/api/channels/feishu/relay", bad, map[string]any{
		"node_id": "fn",
		"notice":  map[string]any{"card_id": "card-bad-token", "kind": "notice", "node_id": "fn", "text": "secret"},
	})
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("relay wrong token = %d (%s)", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if _, ok := h.svc.Store().Get("card-bad-token"); ok {
		t.Fatal("wrong token still created a card")
	}

	resp = feishuITPost(t, h.worker.Client(), h.worker.URL+"/api/channels/feishu/answer", bad,
		feishu.Answer{CardID: "card-bad-token", Kind: feishu.KindNotice, NodeID: "fn"})
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("answer wrong token = %d (%s)", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	for len(h.answers) > 0 {
		call := <-h.answers
		if call.token == feishuAnswerTestToken {
			t.Fatalf("wrong token still reached the worker apply handler: %+v", call)
		}
	}
}
