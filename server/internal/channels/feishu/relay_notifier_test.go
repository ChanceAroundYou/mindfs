package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"mindfs/server/internal/kanban"
)

type relayCall struct {
	method string
	path   string
	token  string
	body   []byte
}

func relayNotifierServer(t *testing.T, statuses []int, calls *[]relayCall) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	next := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read relay body: %v", err)
		}
		mu.Lock()
		*calls = append(*calls, relayCall{method: r.Method, path: r.URL.Path, token: r.Header.Get(RelayTokenHeader), body: body})
		status := http.StatusOK
		if next < len(statuses) {
			status = statuses[next]
		}
		next++
		mu.Unlock()
		w.WriteHeader(status)
	}))
}

func TestRelayNotifierRetriesThenSucceeds(t *testing.T) {
	var calls []relayCall
	srv := relayNotifierServer(t, []int{500, 500, 200}, &calls)
	defer srv.Close()

	var sleeps []time.Duration
	n := &RelayNotifier{URL: srv.URL, Token: strings.Repeat("k", 32), NodeID: "wsl", Sleep: func(d time.Duration) { sleeps = append(sleeps, d) }}
	notice := kanban.CardNotice{ID: "gate-1", Kind: kanban.CardKindGate, TaskID: "task-1", Prompt: "approve?"}
	if err := n.NotifyCard(context.Background(), notice); err != nil {
		t.Fatalf("third attempt succeeded: %v", err)
	}
	if len(calls) != 3 {
		t.Fatalf("want 3 attempts, got %d", len(calls))
	}
	if want := []time.Duration{10 * time.Second, 30 * time.Second}; !reflect.DeepEqual(sleeps, want) {
		t.Fatalf("want backoff %v, got %v", want, sleeps)
	}
	for _, c := range calls {
		if c.method != http.MethodPost || c.path != "/" {
			t.Errorf("unexpected relay request %s %s", c.method, c.path)
		}
	}
}

func TestRelayNotifierGivesUpWithReadableError(t *testing.T) {
	var calls []relayCall
	srv := relayNotifierServer(t, []int{500, 500, 500}, &calls)
	defer srv.Close()

	var sleeps []time.Duration
	n := &RelayNotifier{URL: srv.URL, Token: strings.Repeat("k", 32), NodeID: "wsl", Sleep: func(d time.Duration) { sleeps = append(sleeps, d) }}
	notice := kanban.CardNotice{ID: "gate-2", Kind: kanban.CardKindGate}
	before := notice
	if err := n.NotifyCard(context.Background(), notice); err == nil {
		t.Fatal("want an error after three 5xx responses")
	} else if !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "3") {
		t.Fatalf("error must name the status and attempt count: %v", err)
	}
	if len(calls) != 3 || !reflect.DeepEqual(sleeps, []time.Duration{10 * time.Second, 30 * time.Second}) {
		t.Fatalf("attempts=%d sleeps=%v", len(calls), sleeps)
	}
	if !reflect.DeepEqual(notice, before) {
		t.Fatal("notifier must not remove or alter the caller's card")
	}
}

func TestRelayNotifierSendsOnlyConfiguredFields(t *testing.T) {
	var calls []relayCall
	srv := relayNotifierServer(t, nil, &calls)
	defer srv.Close()

	token := strings.Repeat("t", 40)
	n := &RelayNotifier{URL: srv.URL, Token: token, NodeID: "wsl", Sleep: func(time.Duration) {}}
	notice := kanban.CardNotice{
		ID: "ask-1", Kind: kanban.CardKindAsk, RootID: "root", TaskID: "task", TaskTitle: "标题",
		TaskNumber: 7, WorkflowNodeID: "n1", NodeName: "gate", QuestionID: "q1",
		Prompt: "请选择", Status: "waiting_user", Summary: "摘要", Options: []string{"批准", "驳回"},
	}
	if err := n.NotifyCard(context.Background(), notice); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("want one relay POST, got %d", len(calls))
	}
	call := calls[0]
	if call.token != token {
		t.Fatal("relay token header missing or wrong")
	}
	if len(call.body) == 0 || len(call.body) > 64<<10 {
		t.Fatalf("body size %d outside (0, 64KiB]", len(call.body))
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(call.body, &payload); err != nil {
		t.Fatalf("relay body is not JSON: %v", err)
	}
	for key := range payload {
		if key != "node_id" && key != "notice" {
			t.Errorf("relay body carries an unconfigured field %q", key)
		}
		if strings.Contains(strings.ToLower(key), "url") {
			t.Errorf("relay body must not carry a target URL: %q", key)
		}
	}
	var nested map[string]json.RawMessage
	if err := json.Unmarshal(payload["notice"], &nested); err != nil {
		t.Fatalf("relay notice is not a JSON object: %v", err)
	}
	want := make(map[string]bool)
	typ := reflect.TypeOf(Notice{})
	for i := 0; i < typ.NumField(); i++ {
		want[typ.Field(i).Name] = true
	}
	for key := range nested {
		if !want[key] {
			t.Errorf("relay notice carries an unconfigured field %q", key)
		}
		if strings.Contains(strings.ToLower(key), "url") {
			t.Errorf("relay notice must not carry a target URL: %q", key)
		}
	}
	for key := range want {
		if _, ok := nested[key]; !ok {
			t.Errorf("relay notice misses configured field %q", key)
		}
	}
	var req RelayRequest
	if err := json.Unmarshal(call.body, &req); err != nil {
		t.Fatal(err)
	}
	if req.NodeID != "wsl" || req.Notice.NodeID != "wsl" || req.Notice.CardID != "ask-1" {
		t.Fatalf("node/card identity drifted: %+v", req)
	}
}

func TestRelayNotifierFailsImmediatelyOnClientError(t *testing.T) {
	var calls []relayCall
	srv := relayNotifierServer(t, []int{400, 200}, &calls)
	defer srv.Close()

	var sleeps []time.Duration
	n := &RelayNotifier{URL: srv.URL, Token: strings.Repeat("k", 32), NodeID: "wsl", Sleep: func(d time.Duration) { sleeps = append(sleeps, d) }}
	if err := n.NotifyCard(context.Background(), kanban.CardNotice{ID: "x", Kind: kanban.CardKindNotice}); err == nil {
		t.Fatal("4xx must fail immediately")
	}
	if len(calls) != 1 || len(sleeps) != 0 {
		t.Fatalf("4xx must not be retried: calls=%d sleeps=%v", len(calls), sleeps)
	}
}

func TestRelayNotifierLogsNoTokenOrMessageBody(t *testing.T) {
	var calls []relayCall
	srv := relayNotifierServer(t, []int{503}, &calls)
	defer srv.Close()

	token := "tok_SUPER_SECRET_9f8e7d6c5b4a3210"
	secretBody := "BODY_MUST_NOT_APPEAR_IN_LOG_123"
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })

	n := &RelayNotifier{URL: srv.URL, Token: token, NodeID: "wsl", Sleep: func(time.Duration) {}}
	_ = n.NotifyCard(context.Background(), kanban.CardNotice{ID: "notice-1", Kind: kanban.CardKindNotice, Prompt: secretBody})
	if out := logs.String(); strings.Contains(out, token) || strings.Contains(out, secretBody) {
		t.Fatalf("relay log leaked token or body: %s", out)
	}
}
