package proxy

import (
	"testing"
	"time"

	"mindfs/server/internal/agent/fold"
)

// Codex reports its own compaction on the event stream. The request right
// after it carries the whole transcript again — the upstream prefix cache is
// gone — so that is the one fold that costs nothing, and the signal must be
// spent exactly once.
func TestSignalCompactMarksNextDecisionCold(t *testing.T) {
	s, err := NewServerWithLiveCompact(codexFoldConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.RegisterThread("thread-1", "key-1")
	s.SignalCompact("thread-1")
	key := threadStateKey("thread-1")

	decide := func() fold.Outcome {
		t.Helper()
		session := s.foldSession(key)
		out, err := s.foldEngineFor("responses").Fold(fold.Request{
			Body:        codexTestBody(),
			Session:     session,
			Now:         time.Now(),
			FoldAllowed: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		s.applyFoldSession(key, session)
		return out
	}

	first := decide()
	if !first.Cold || first.ColdReason != fold.ColdReasonCompact {
		t.Fatalf("first decision cold=%v reason=%q, want compact", first.Cold, first.ColdReason)
	}
	second := decide()
	if second.Cold {
		t.Fatalf("second decision still cold (%q): the signal must be spent once", second.ColdReason)
	}
}

// A compaction can be signalled before the session's first request reaches the
// proxy, and a thread may never be registered at all. Neither may fail: there
// is simply nothing to mark.
func TestSignalCompactUnknownThreadIsIgnored(t *testing.T) {
	s, err := NewServerWithLiveCompact(codexFoldConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SignalCompact("thread-never-seen")
	s.SignalCompact("")
	if s.pendingColdFold(threadStateKey("thread-never-seen")) {
		t.Fatal("an unknown thread must not gain state")
	}
}

// pendingColdFold reports the flag for a state key, for tests.
func (s *Server) pendingColdFold(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.sessions[key]
	return ok && st.live.pendingColdFold
}
