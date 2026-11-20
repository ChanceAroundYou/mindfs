package proxy

import (
	"bytes"
	"mindfs/server/internal/agent/fold"
	"path/filepath"
	"testing"
	"time"
)

// waitForLedgerRequests polls until the session's ledger shows want entries.
//
// The ledger line is written when the turn ends — it carries the turn's own
// rewrite cost, which only exists after the response stream — so it lands after
// the client may already have read the whole body (an upstream with a known
// Content-Length lets the client finish first). Tests must wait for it instead
// of assuming the handler is done.
func waitForLedgerRequests(t *testing.T, s *Server, sessionKey string, want int) fold.ContextSaving {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		summary := s.SessionContextSaving(sessionKey)
		if summary.Requests >= want {
			return summary
		}
		if time.Now().After(deadline) {
			t.Fatalf("ledger requests = %d, want %d", summary.Requests, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The response path writes one line per fold, attributed to the session key the
// proxy recorded — through the real handler, not by calling the store.
func TestProxyFoldWritesLedger(t *testing.T) {
	archiveDir := filepath.Join(t.TempDir(), "context")
	s, err := NewServerWithLiveCompact(LiveCompactConfig{
		Mode: LiveCompactOn, KeepRounds: 1, MinBlockTokens: 100, RailTokens: -1,
		MinBodyBytes: 1, ArchiveDir: archiveDir, ArchiveEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	body := foldBody(t, 4, 8001)
	forwarded, _ := postMessages(t, s, "ledger-token", body)
	if bytes.Equal(forwarded, body) {
		t.Fatal("expected a fold")
	}

	summary := waitForLedgerRequests(t, s, "session-ledger-token", 1)
	if summary.Latest.ReqBytes != len(body) || summary.Latest.FoldedBytes != len(forwarded) {
		t.Fatalf("ledger sizes = %+v, want %d -> %d", summary.Latest, len(body), len(forwarded))
	}
	if summary.Latest.SavedBytes <= 0 {
		t.Fatalf("saved bytes = %d", summary.Latest.SavedBytes)
	}
	if summary.Latest.Blocks == 0 {
		t.Fatal("ledger recorded no folded blocks")
	}
}

// A session that never folded has an empty summary rather than an error.
func TestSessionContextSavingUnknownSession(t *testing.T) {
	archiveDir := filepath.Join(t.TempDir(), "context")
	s, err := NewServerWithLiveCompact(LiveCompactConfig{
		Mode: LiveCompactOn, ArchiveDir: archiveDir, ArchiveEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := s.SessionContextSaving("never-seen"); got.Requests != 0 {
		t.Fatalf("unknown session summary = %+v", got)
	}
}

// Observe-only sessions never fold, so they must not appear in the ledger
// either: the saving the chip reports is the user's, not the router's.
func TestObserveOnlySessionDoesNotWriteLedger(t *testing.T) {
	archiveDir := filepath.Join(t.TempDir(), "context")
	s, err := NewServerWithLiveCompact(LiveCompactConfig{
		Mode: LiveCompactOn, KeepRounds: 1, MinBlockTokens: 100, RailTokens: -1,
		MinBodyBytes: 1, ArchiveDir: archiveDir, ArchiveEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	upstream := httptestUpstream(t)
	s.RegisterSessionObserveOnly("isolated", upstream, "router-session")
	postToProxy(t, s, "isolated", foldBody(t, 4, 8001))
	if got := s.SessionContextSaving("router-session"); got.Requests != 0 {
		t.Fatalf("observe-only session wrote %d ledger lines", got.Requests)
	}
}
