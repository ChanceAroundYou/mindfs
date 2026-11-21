package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"mindfs/server/internal/agent/fold"
)

// codexStreamWithUsage is one Responses SSE stream carrying the upstream's own
// token counts. That block is the only place a Codex turn reports the real
// context size: the body is largely base64 `encrypted_content` (10-20 bytes per
// token against the estimator's 3.9), so the estimator reads 1.4-2.6x high and
// cannot be used as a rail input on its own.
func codexStreamWithUsage(input, cached, output int) string {
	return "event: response.completed\n" +
		fmt.Sprintf(`data: {"type":"response.completed","response":{"usage":{"input_tokens":%d,"input_tokens_details":{"cached_tokens":%d},"output_tokens":%d}}}`,
			input, cached, output) + "\n\n"
}

// newCodexRoute starts a proxy pointed at a fake Responses upstream and
// registers one thread, so a request through /c/llmux/v1/responses is folded as
// a real Codex session would be. forwarded is filled once the request lands.
func newCodexRoute(t *testing.T, cfg LiveCompactConfig, stream string) (*Server, *[]byte) {
	t.Helper()
	forwarded := new([]byte)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*forwarded, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(stream))
	}))
	t.Cleanup(upstream.Close)
	s, err := NewServerWithLiveCompact(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	s.RegisterProvider("llmux", upstream.URL+"/v1")
	s.RegisterThread("thread-1", "codex-mindfs-session-key")
	return s, forwarded
}

// waitCalibration polls until the response stream has been fully proxied: the
// ledger and the calibration pair are written after the body copy ends, which
// the client can observe first.
func waitCalibration(t *testing.T, s *Server, key string) (int, int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		actual, forwardEst := s.sessionCalibration(key)
		if actual > 0 || time.Now().After(deadline) {
			return actual, forwardEst
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A folded Codex turn must record the upstream's context count as the session's
// calibration sample. Before this, only the Messages path called
// noteActualContext, so every Codex session ran with lastActual=0 — the rail
// compared a byte estimate (2.6x high) against a token threshold, and the
// cold-cache rule never got a sample at all.
func TestCodexFoldedTurnCalibratesTheNextRequest(t *testing.T) {
	s, forwarded := newCodexRoute(t, codexFoldConfig(t), codexStreamWithUsage(120000, 40000, 10))
	postCodexThread(t, s, "session-id", "thread-1", codexTestBody())
	if len(*forwarded) == 0 {
		t.Fatal("upstream saw no request")
	}
	actual, forwardEst := waitCalibration(t, s, threadStateKey("thread-1"))
	if actual != 120000 {
		t.Fatalf("calibrated actual = %d, want the upstream input_tokens 120000", actual)
	}
	// The pair is only usable when the forwarded estimate was recorded too: the
	// scale between the two is what turns the rail into an upstream threshold.
	if forwardEst <= 0 {
		t.Fatalf("forward estimate = %d, want the forwarded body's estimate", forwardEst)
	}
}

// A turn that folds nothing still reports usage, and the calibration pair needs
// it just as much: a session that spends several turns below the rail would
// otherwise arrive at its first fold with no idea how large it really is.
func TestCodexUnfoldedTurnStillRecordsCalibration(t *testing.T) {
	cfg := codexFoldConfig(t)
	cfg.RailTokens = 10_000_000 // never fires on this fixture
	s, forwarded := newCodexRoute(t, cfg, codexStreamWithUsage(90000, 30000, 10))
	postCodexThread(t, s, "session-id", "thread-1", codexTestBody())
	body := codexTestBody()
	if string(*forwarded) != string(body) {
		t.Fatalf("request must be forwarded verbatim when below the rail: %d -> %d bytes", len(body), len(*forwarded))
	}
	actual, _ := waitCalibration(t, s, threadStateKey("thread-1"))
	if actual != 90000 {
		t.Fatalf("calibrated actual = %d, want the upstream input_tokens 90000", actual)
	}
}

// The rail must read the calibrated number, not the estimator. This is the
// defect that made a 100k rail fire at a real ~40k context on the Codex worker.
func TestCodexRailUsesTheUpstreamTokenCount(t *testing.T) {
	s, _ := newCodexRoute(t, codexFoldConfig(t), codexStreamWithUsage(120000, 40000, 10))
	body := codexTestBody()
	postCodexThread(t, s, "session-id", "thread-1", body)
	if actual, _ := waitCalibration(t, s, threadStateKey("thread-1")); actual != 120000 {
		t.Fatalf("calibrated actual = %d", actual)
	}
	// Same body again: no growth, so the projection is the previous request's
	// upstream context size.
	_, _, _, gate := s.foldRequestLedgerFor(threadStateKey("thread-1"), "responses", body)
	if gate.projected != 120000 {
		t.Fatalf("projected = %d (est=%d), want the calibrated 120000", gate.projected, gate.est)
	}
	// This fixture has no reasoning blobs, so the estimator reads *below* the
	// reported count. Falling back to it would land under that number; the rail
	// must follow the upstream count instead.
	if gate.projected <= gate.est {
		t.Fatalf("the rail fell back to the byte estimate: est=%d projected=%d", gate.est, gate.projected)
	}
}

// Responses reports one input total that already includes the cache; the
// calibration pair stores three disjoint counters. Splitting the total back out
// must leave ContextTokens() — the number the rail and the cold rule read —
// exactly equal to the upstream's input_tokens.
func TestFoldUsageTotalsSplitsTheResponsesTotal(t *testing.T) {
	totals := foldUsageTotals(fold.Usage{
		Source: fold.UsageSourceResponse, ContextTokens: 120000,
		CacheRead: 40000, CacheWrite: 5000, Output: 10, RewriteTokens: 80000,
	})
	if !totals.Seen {
		t.Fatal("a response usage block must mark the sample as seen")
	}
	if totals.ContextTokens() != 120000 {
		t.Fatalf("context tokens = %d, want input_tokens 120000", totals.ContextTokens())
	}
	if totals.InputTokens != 75000 || totals.CacheReadInputTokens != 40000 || totals.CacheCreationInputTokens != 5000 {
		t.Fatalf("split = in:%d read:%d write:%d", totals.InputTokens, totals.CacheReadInputTokens, totals.CacheCreationInputTokens)
	}
	if totals.OutputTokens != 10 {
		t.Fatalf("output = %d", totals.OutputTokens)
	}
	if foldUsageTotals(fold.Usage{Source: fold.UsageSourceMissing, RewriteTokens: -1}).Seen {
		t.Fatal("a missing sample must not look like a real zero")
	}
	if got := foldUsageTotals(fold.Usage{Source: fold.UsageSourceResponse, ContextTokens: 100, CacheRead: 400}); got.InputTokens != 0 {
		t.Fatalf("cached above total must clamp fresh input to zero, got %d", got.InputTokens)
	}
}
