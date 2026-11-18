package proxy

import (
	"testing"
	"time"
)

func analyzeObservation(estimated int) liveCompactObservation {
	return liveCompactObservation{mode: LiveCompactShadow, estimatedInput: estimated, messages: 5}
}

// The bug this type exists to prevent: with analysis on a worker and usage only
// available after the stream ends, reading session-level "last usage" pairs turn
// N's estimate with turn N-1's usage. Each turn must carry its own half.
func TestLiveCompactTurnPairsOneRequestEstimateWithItsOwnUsage(t *testing.T) {
	turn := &liveCompactTurn{}
	if _, _, ready := turn.completeAnalysis(analyzeObservation(1000)); ready {
		t.Fatal("analysis alone must not emit")
	}
	final, totals, ready := turn.completeTurn(usageTotals{InputTokens: 1100, Seen: true})
	if !ready {
		t.Fatal("usage completing the pair must emit")
	}
	if final.estimatedInput != 1000 {
		t.Fatalf("estimate = %d, want 1000", final.estimatedInput)
	}
	if totals.ContextTokens() != 1100 {
		t.Fatalf("usage = %d, want 1100", totals.ContextTokens())
	}
	if _, _, again := turn.completeTurn(usageTotals{InputTokens: 9999, Seen: true}); again {
		t.Fatal("a turn must emit exactly once")
	}
}

func TestLiveCompactTurnEmitsWhenUsageArrivesFirst(t *testing.T) {
	turn := &liveCompactTurn{}
	if _, _, ready := turn.completeTurn(usageTotals{InputTokens: 500, Seen: true}); ready {
		t.Fatal("usage alone must not emit")
	}
	final, totals, ready := turn.completeAnalysis(analyzeObservation(480))
	if !ready {
		t.Fatal("analysis completing the pair must emit")
	}
	if final.estimatedInput != 480 || totals.ContextTokens() != 500 {
		t.Fatalf("pairing wrong: est=%d actual=%d", final.estimatedInput, totals.ContextTokens())
	}
}

// A compressed stream or a failed upstream call produces no usage. The turn
// must still close, with actual_in unknown, instead of holding its analysis
// forever or leaving a hole in the per-turn series.
func TestLiveCompactTurnClosesWithoutUsage(t *testing.T) {
	turn := &liveCompactTurn{}
	if _, _, ready := turn.completeTurn(usageTotals{}); ready {
		t.Fatal("an early response half must not emit")
	}
	final, totals, ready := turn.completeAnalysis(analyzeObservation(700))
	if !ready {
		t.Fatal("analysis after an unknown-usage close must emit")
	}
	if final.estimatedInput != 700 {
		t.Fatalf("estimate = %d, want 700", final.estimatedInput)
	}
	if totals.Seen {
		t.Fatal("turn without usage must report actual_in as unknown")
	}
}

// Two concurrent turns of the same session must not swap halves.
func TestLiveCompactTurnsDoNotCrossPair(t *testing.T) {
	first := &liveCompactTurn{}
	second := &liveCompactTurn{}
	first.completeAnalysis(analyzeObservation(100))
	second.completeAnalysis(analyzeObservation(200))
	_, totals, ready := first.completeTurn(usageTotals{InputTokens: 111, Seen: true})
	if !ready || totals.ContextTokens() != 111 {
		t.Fatalf("first turn paired wrong: %+v", totals)
	}
	_, totals, ready = second.completeTurn(usageTotals{InputTokens: 222, Seen: true})
	if !ready || totals.ContextTokens() != 222 {
		t.Fatalf("second turn paired wrong: %+v", totals)
	}
}

func TestLiveCompactQueueRejectsBodiesOverRetainBudget(t *testing.T) {
	s, err := NewServerWithLiveCompact(LiveCompactConfig{Mode: LiveCompactShadow, MinBodyBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Pretend the budget is already nearly exhausted, then try to enqueue one
	// more body. Per-session depth is fine here; only the byte budget blocks.
	s.workMu.Lock()
	s.workRetained = liveCompactRetainBudget
	s.workMu.Unlock()
	if s.enqueueLiveCompactWork("tok", 1024, func() {}) {
		t.Fatal("enqueue must fail once the retain budget is spent")
	}
	s.workMu.Lock()
	retained := s.workRetained
	dropped := s.workDropped
	s.workMu.Unlock()
	if retained != liveCompactRetainBudget {
		t.Fatalf("retained = %d, want unchanged budget", retained)
	}
	if dropped != 1 {
		t.Fatalf("dropped = %d, want 1", dropped)
	}
}

// The byte accounting must be returned when a job runs, or the budget leaks
// and observation stops for the process lifetime.
func TestLiveCompactQueueReleasesRetainedBytes(t *testing.T) {
	s, err := NewServerWithLiveCompact(LiveCompactConfig{Mode: LiveCompactShadow, MinBodyBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	done := make(chan struct{})
	if !s.enqueueLiveCompactWork("tok", 12345, func() { close(done) }) {
		t.Fatal("enqueue failed")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("job did not run")
	}
	if !s.flushLiveCompactWork(2 * time.Second) {
		t.Fatal("queue did not drain")
	}
	s.workMu.Lock()
	retained := s.workRetained
	s.workMu.Unlock()
	if retained != 0 {
		t.Fatalf("retained = %d after drain, want 0", retained)
	}
}
