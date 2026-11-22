package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"mindfs/server/internal/channels/feishu"
	"mindfs/server/internal/nodes"
)

const feishuAnswerTestToken = "0123456789abcdef0123456789abcdef"

type feishuAnswerCall struct {
	method string
	path   string
	token  string
	body   []byte
}

type feishuAnswerRoundTrip func(*http.Request) (*http.Response, error)

func (fn feishuAnswerRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}

func feishuAnswerNodeStore(t *testing.T, entries ...nodes.NodeConnection) *nodes.Store {
	t.Helper()
	store, err := nodes.NewStoreAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if _, err := store.Upsert(entry); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

func feishuAnswerResponse(status int) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
	}
}

func TestFeishuAnswerRelayRoutesByCardNode(t *testing.T) {
	var mu sync.Mutex
	var calls []feishuAnswerCall
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			t.Errorf("read answer body: %v", err)
		}
		mu.Lock()
		calls = append(calls, feishuAnswerCall{r.Method, r.URL.Path, r.Header.Get(feishu.RelayTokenHeader), body})
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer remote.Close()

	store := feishuAnswerNodeStore(t,
		nodes.NodeConnection{ID: "fn", Name: "fn", URL: remote.URL + "/"},
		nodes.NodeConnection{ID: "home", Name: "home", URL: remote.URL},
	)
	localCalls := 0
	localAnswer := feishu.Answer{}
	relay := &feishuAnswerRelay{
		Nodes:       store,
		LocalNodeID: "home",
		Token:       feishuAnswerTestToken,
		Apply: func(_ context.Context, answer feishu.Answer) error {
			localCalls++
			localAnswer = answer
			return nil
		},
		Client: remote.Client(),
		Sleep: func(context.Context, time.Duration) error {
			t.Error("local and first-try remote routing must not sleep")
			return nil
		},
	}

	answer := feishu.Answer{CardID: "om_1", NodeID: "fn", AccountID: "u_1", Text: "选项目", Note: "note"}
	if err := relay.Relay(context.Background(), answer); err != nil {
		t.Fatalf("relay to fn: %v", err)
	}
	mu.Lock()
	if len(calls) != 1 {
		t.Fatalf("remote calls = %d, want 1", len(calls))
	}
	call := calls[0]
	mu.Unlock()
	if call.method != http.MethodPost || call.path != "/api/channels/feishu/answer" {
		t.Fatalf("remote request = %s %s", call.method, call.path)
	}
	if call.token != feishuAnswerTestToken {
		t.Fatalf("relay token header = %q, want configured token", call.token)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(call.body, &wire); err != nil {
		t.Fatalf("answer body is not JSON: %v", err)
	}
	for key := range wire {
		if strings.Contains(strings.ToLower(key), "url") {
			t.Fatalf("answer body carries a URL field %q; destination must come only from the node table", key)
		}
	}
	var got feishu.Answer
	if err := json.Unmarshal(call.body, &got); err != nil {
		t.Fatal(err)
	}
	if got != answer {
		t.Fatalf("remote answer = %+v, want %+v", got, answer)
	}
	if localCalls != 0 {
		t.Fatalf("remote card applied locally %d times", localCalls)
	}

	for _, nodeID := range []string{"home", ""} {
		localCalls = 0
		localAnswer = feishu.Answer{}
		local := answer
		local.NodeID = nodeID
		if err := relay.Relay(context.Background(), local); err != nil {
			t.Fatalf("relay to local node %q: %v", nodeID, err)
		}
		mu.Lock()
		remoteCalls := len(calls)
		mu.Unlock()
		if remoteCalls != 1 {
			t.Fatalf("local node %q made %d remote calls, want none", nodeID, remoteCalls-1)
		}
		if localCalls != 1 || localAnswer != local {
			t.Fatalf("local node %q applied %d times with %+v", nodeID, localCalls, localAnswer)
		}
	}

	unknown := answer
	unknown.NodeID = "not-in-table"
	err := relay.Relay(context.Background(), unknown)
	if err == nil || !strings.Contains(err.Error(), "not-in-table") || !strings.Contains(err.Error(), "未知节点") {
		t.Fatalf("unknown node error = %v, want a clear error naming the node", err)
	}
	if strings.Contains(err.Error(), feishuAnswerTestToken) || strings.Contains(err.Error(), answer.Text) {
		t.Fatalf("unknown node error leaks secret or answer: %v", err)
	}
	mu.Lock()
	remoteCalls := len(calls)
	mu.Unlock()
	if remoteCalls != 1 || localCalls != 1 {
		t.Fatalf("unknown node sent %d remote requests and %d local applies", remoteCalls-1, localCalls)
	}
}

func TestFeishuAnswerRelayRetriesTransientFailuresOnly(t *testing.T) {
	store := feishuAnswerNodeStore(t, nodes.NodeConnection{ID: "fn", Name: "fn", URL: "http://fn.invalid"})
	answer := feishu.Answer{CardID: "om_2", NodeID: "fn", Text: "secret-answer"}

	tests := []struct {
		name       string
		statuses   []int
		networkErr bool
		wantTries  int
		wantSleeps []time.Duration
		wantErr    bool
	}{
		{name: "5xx then success", statuses: []int{500, 503, 502, 200}, wantTries: 4, wantSleeps: []time.Duration{10 * time.Second, 30 * time.Second, 2 * time.Minute}},
		{name: "exhausted 5xx", statuses: []int{500, 500, 500, 500}, wantTries: 4, wantSleeps: []time.Duration{10 * time.Second, 30 * time.Second, 2 * time.Minute}, wantErr: true},
		{name: "network error retries", networkErr: true, wantTries: 4, wantSleeps: []time.Duration{10 * time.Second, 30 * time.Second, 2 * time.Minute}, wantErr: true},
		{name: "4xx is final", statuses: []int{403, 200}, wantTries: 1, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tries := 0
			var sleeps []time.Duration
			client := &http.Client{Transport: feishuAnswerRoundTrip(func(r *http.Request) (*http.Response, error) {
				tries++
				if tc.networkErr {
					return nil, errors.New("dial failed")
				}
				status := tc.statuses[tries-1]
				return feishuAnswerResponse(status), nil
			})}
			relay := &feishuAnswerRelay{
				Nodes:       store,
				LocalNodeID: "home",
				Token:       feishuAnswerTestToken,
				Client:      client,
				Sleep: func(_ context.Context, d time.Duration) error {
					sleeps = append(sleeps, d)
					return nil
				},
			}
			err := relay.Relay(context.Background(), answer)
			if tries != tc.wantTries {
				t.Fatalf("attempts = %d, want %d (err=%v)", tries, tc.wantTries, err)
			}
			if len(sleeps) != len(tc.wantSleeps) {
				t.Fatalf("sleeps = %v, want %v", sleeps, tc.wantSleeps)
			}
			for i, want := range tc.wantSleeps {
				if sleeps[i] != want {
					t.Fatalf("sleep[%d] = %v, want %v", i, sleeps[i], want)
				}
			}
			if tc.wantErr && err == nil {
				t.Fatal("expected a relay error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected relay error: %v", err)
			}
			if err != nil && (strings.Contains(err.Error(), feishuAnswerTestToken) || strings.Contains(err.Error(), answer.Text)) {
				t.Fatalf("error leaks secret or answer body: %v", err)
			}
		})
	}
}

func assertRetryableFeishuAnswerError(t *testing.T, err error, want bool) {
	t.Helper()
	var marker interface{ RetryableAnswer() bool }
	got := errors.As(err, &marker) && marker.RetryableAnswer()
	if got != want {
		t.Fatalf("retryable answer error = %v, want %v (err=%v)", got, want, err)
	}
}

func TestFeishuAnswerRelayShortDeadlineCancelsBackoff(t *testing.T) {
	store := feishuAnswerNodeStore(t, nodes.NodeConnection{ID: "fn", Name: "fn", URL: "http://fn.invalid"})
	answer := feishu.Answer{CardID: "om_3", NodeID: "fn", Text: "secret-answer"}
	tries := 0
	client := &http.Client{Transport: feishuAnswerRoundTrip(func(*http.Request) (*http.Response, error) {
		tries++
		return feishuAnswerResponse(http.StatusInternalServerError), nil
	})}
	relay := &feishuAnswerRelay{
		Nodes:       store,
		LocalNodeID: "home",
		Token:       feishuAnswerTestToken,
		Client:      client,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := relay.Relay(ctx, answer)
	elapsed := time.Since(start)
	if tries != 1 {
		t.Fatalf("attempts = %d, want 1 (err=%v)", tries, err)
	}
	if elapsed > time.Second {
		t.Fatalf("cancellation took %s; backoff must wait on the context, not sleep 10s", elapsed)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline exceeded", err)
	}
	assertRetryableFeishuAnswerError(t, err, true)
}

func TestFeishuAnswerRelayDeadlinePermitsFourSends(t *testing.T) {
	answer := feishu.Answer{CardID: "om_4", NodeID: "fn", Text: "secret-answer"}
	// The approved backoffs total 160s. With the old 2m handler deadline the
	// fourth send was unreachable; with 3m it must really happen.
	for _, tc := range []struct {
		name        string
		deadline    time.Duration
		wantTries   int
		wantErr     bool
		wantElapsed time.Duration
	}{
		{name: "old 2m deadline", deadline: 2 * time.Minute, wantTries: 3, wantErr: true, wantElapsed: 160 * time.Second},
		{name: "approved 3m deadline", deadline: 3 * time.Minute, wantTries: 4, wantElapsed: 160 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := feishuAnswerNodeStore(t, nodes.NodeConnection{ID: "fn", Name: "fn", URL: "http://fn.invalid"})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var elapsed time.Duration
			tries := 0
			client := &http.Client{Transport: feishuAnswerRoundTrip(func(*http.Request) (*http.Response, error) {
				tries++
				if tries < 4 {
					return feishuAnswerResponse(http.StatusInternalServerError), nil
				}
				return feishuAnswerResponse(http.StatusOK), nil
			})}
			relay := &feishuAnswerRelay{
				Nodes:       store,
				LocalNodeID: "home",
				Token:       feishuAnswerTestToken,
				Client:      client,
				Sleep: func(ctx context.Context, d time.Duration) error {
					elapsed += d
					if elapsed >= tc.deadline {
						cancel()
					}
					return ctx.Err()
				},
			}
			err := relay.Relay(ctx, answer)
			if tries != tc.wantTries {
				t.Fatalf("attempts = %d, want %d (err=%v)", tries, tc.wantTries, err)
			}
			if elapsed != tc.wantElapsed {
				t.Fatalf("simulated backoff = %s, want %s", elapsed, tc.wantElapsed)
			}
			if tc.wantErr {
				assertRetryableFeishuAnswerError(t, err, true)
			} else if err != nil {
				t.Fatalf("fourth send under 3m deadline failed: %v", err)
			}
		})
	}
}

func TestFeishuAnswerRelayUsesRedirectSafeClient(t *testing.T) {
	src, err := os.ReadFile("feishu_answer_relay.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "feishu.NewRelayClient(feishuAnswerTimeout)") {
		t.Fatal("回答中继的默认 client 必须拒绝跨主机跳转，防止 token 被带去恶意主机")
	}
}
