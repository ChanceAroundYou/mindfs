package proxy

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// Defaults and the proxy-level I8 check. Split from
// live_compact_fold_gate_test.go when that file crossed the 500-line hard
// limit; pure move, no behaviour change.

func TestLiveCompactNormalizesRailDefaults(t *testing.T) {
	cfg := LiveCompactConfig{}.Normalized()
	// "Fold deep, fold rarely" (design §12.7(d)): cut all the way down behind
	// the protected tail, and keep the resident request around 100k.
	if cfg.RailTokens != 100000 || cfg.TargetTokens != 20000 {
		t.Fatalf("rail/target = %d/%d, want 100000/20000", cfg.RailTokens, cfg.TargetTokens)
	}
	if cfg.MinFoldBytes != 4096 || cfg.MaxFoldBytes != 262144 || cfg.LargeBlockTokens != 32000 {
		t.Fatalf("fold budget = %d/%d/%d, want 4096/262144/32000", cfg.MinFoldBytes, cfg.MaxFoldBytes, cfg.LargeBlockTokens)
	}
	// A target at or above the rail is pulled below it.
	cfg = LiveCompactConfig{RailTokens: 1000, TargetTokens: 2000}.Normalized()
	if cfg.TargetTokens >= cfg.RailTokens {
		t.Fatalf("target %d not pulled below rail %d", cfg.TargetTokens, cfg.RailTokens)
	}
	// A negative rail disables the gate.
	if cfg := (LiveCompactConfig{RailTokens: -1}).Normalized(); cfg.RailTokens != -1 {
		t.Fatalf("negative rail normalized to %d", cfg.RailTokens)
	}
}

// postMessagesKeepSession posts through the proxy without touching the session
// registration, so the commitment set survives across turns.
func postMessagesKeepSession(t *testing.T, s *Server, upstreamURL, token string, body []byte) []byte {
	t.Helper()
	var captured []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":100}}}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()
	// Registration is the caller's job: re-registering would replace the
	// session state and wipe the commitment set this test exists to check.
	s.mu.Lock()
	if st, ok := s.sessions[token]; ok {
		st.realBaseURL = upstream.URL
	} else {
		s.sessions[token] = &sessionState{realBaseURL: upstream.URL, sessionKey: "session-" + token, foldAllowed: true}
	}
	s.mu.Unlock()

	resp, err := http.Post(
		fmt.Sprintf("http://127.0.0.1:%d/p/%s/v1/messages", s.Port(), token),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return captured
}

// End-to-end I8: the second turn, below the rail, forwards the same folded
// prefix the first turn produced.
//
// The rail is derived from the measured first-request estimate rather than
// hardcoded, so the test does not encode the byte-per-token formula: turn 1
// travels alone and must exceed it; by turn 2 the three committed blocks are
// subtracted from the projection, which keeps the projection below the rail
// even after the new round's growth is added back.
func TestProxyFoldKeepsCommittedPrefixAcrossTurns(t *testing.T) {
	first := distinctFoldBody(t, 4, 8001)
	second := distinctFoldBody(t, 5, 8001)
	rail := estimateByteTokens(first) * 85 / 100

	archiveDir := filepath.Join(t.TempDir(), "context")
	s, err := NewServerWithLiveCompact(LiveCompactConfig{
		Mode: LiveCompactOn, KeepRounds: 1, MinBlockTokens: 100,
		MinBodyBytes: 1, ArchiveDir: archiveDir, ArchiveEnabled: true,
		RailTokens: rail, TargetTokens: 1, MinFoldBytes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	forwarded := postMessagesKeepSession(t, s, "", "sticky1", first)
	if n := bytes.Count(forwarded, []byte("[MindFS live-compact]")); n != 3 {
		t.Fatalf("first turn pointers = %d, want 3", n)
	}

	forwarded2 := postMessagesKeepSession(t, s, "", "sticky1", second)
	if n := bytes.Count(forwarded2, []byte("[MindFS live-compact]")); n != 3 {
		t.Fatalf("second turn pointers = %d, want the same 3", n)
	}
}
