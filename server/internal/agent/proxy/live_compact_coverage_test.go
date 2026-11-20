package proxy

import (
	"testing"
	"time"
)

// Fold coverage (2026-11-20 production evidence).
//
// mode=on had been running with a 2000-token per-block floor. Real sessions
// hold mostly 1-7 KB tool results, so a rail-triggered event found nothing it
// was allowed to fold, reported no-backlog, and the context kept growing until
// the CLI compacted itself (token 07edc43f: three compactions in four minutes).
// These tests pin the three defects found on that evidence.

// A tool result well under the legacy token floor must still be folded when the
// pointer saves meaningful bytes.
func TestFoldCoversBlocksBelowLegacyTokenFloor(t *testing.T) {
	dir := t.TempDir()
	store := newArchiveStore(dir, time.Hour, archiveMaxBytes)
	cfg := gateTestConfig(dir)
	cfg.MinBlockTokens = 2000 // the production default

	body := distinctFoldBody(t, 3, 4096)
	result, gate := foldWithCommits(t, body, cfg, store, newPointerCache(),
		newFoldCommits(), foldContext{est: 1 << 20})
	if gate.decision != gateFold {
		t.Fatalf("decision = %s, want fold", gate.decision)
	}
	if result.newBlocks != 2 {
		t.Fatalf("newBlocks = %d, want the two closed rounds", result.newBlocks)
	}
}

// A pointer carries ~1.9 KB of header and excerpt, so a 2.4 KB result saves
// only a few hundred bytes. That is not worth an archive file and a pointer
// line, so the per-block decision is made on saved bytes.
func TestFoldSkipsBlockWhosePointerSavesTooLittle(t *testing.T) {
	dir := t.TempDir()
	store := newArchiveStore(dir, time.Hour, archiveMaxBytes)
	cfg := gateTestConfig(dir) // MinBlockTokens = 10: the legacy floor lets it through

	body := distinctFoldBody(t, 3, 2400)
	result, _ := foldWithCommits(t, body, cfg, store, newPointerCache(),
		newFoldCommits(), foldContext{est: 1 << 20})
	if result.newBlocks != 0 {
		t.Fatalf("newBlocks = %d, want 0: a 2.4 KB result saves too little", result.newBlocks)
	}
}

// The rail arithmetic must be logged even when there is nothing to fold: a 240k
// context reporting gate_projected=0 reads as a broken gate.
func TestGateProjectionReportedWithoutCandidates(t *testing.T) {
	dir := t.TempDir()
	cfg := gateTestConfig(dir)
	body := wireBody(t, []any{userText("hi")})

	_, gate := foldWithCommits(t, body, cfg, newArchiveStore(dir, time.Hour, archiveMaxBytes),
		newPointerCache(), newFoldCommits(), foldContext{est: 200000})
	if gate.projected != 200000 {
		t.Fatalf("projected = %d, want 200000", gate.projected)
	}
	if gate.decision != gateNoBacklog {
		t.Fatalf("decision = %s, want %s (rail fired, nothing to fold)", gate.decision, gateNoBacklog)
	}
}

// The PostCompact signal marks the request that follows the compaction, and
// that request often has nothing foldable. Consuming it there loses the one
// moment a fold is free.
func TestColdSignalNotConsumedByRequestWithoutCandidates(t *testing.T) {
	dir := t.TempDir()
	cfg := gateTestConfig(dir)
	cfg.RailTokens = 1 << 30 // only the cold rail can open the gate
	cfg.ColdRailTokens = 1
	s := &Server{
		sessions:     map[string]*sessionState{"tok": {foldAllowed: true}},
		liveCompact:  cfg,
		archive:      newArchiveStore(dir, time.Hour, archiveMaxBytes),
		pointerCache: newPointerCache(),
	}
	s.sessions["tok"].live.pendingColdFold = true
	s.sessions["tok"].live.pendingColdFoldAt = time.Now()

	s.foldRequestLedger("tok", wireBody(t, []any{userText("hi")}))
	if !s.sessions["tok"].live.pendingColdFold {
		t.Fatal("a request with no candidates must not consume the compact signal")
	}

	_, _, _, gate := s.foldRequestLedger("tok", distinctFoldBody(t, 3, 8001))
	if !gate.cold || gate.decision != gateFold {
		t.Fatalf("gate = %+v, want a cold fold on the next foldable request", gate)
	}
}
