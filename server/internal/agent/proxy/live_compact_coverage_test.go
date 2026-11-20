package proxy

import (
	"bytes"
	"testing"
	"time"
)

// Fold coverage (2026-11-20 production evidence).
//
// mode=on had been running with a 2000-token per-block floor. Real sessions
// hold mostly 1-7 KB tool results, so a rail-triggered event found nothing it
// was allowed to fold, reported no-backlog, and the context kept growing until
// the CLI compacted itself (token 07edc43f: three compactions in four minutes).
// These tests pin the three defects found on that evidence, through the request
// path rather than the planner, because the planner is now the kernel's and its
// own tests live with it.

// A tool result well under the legacy token floor must still be folded when the
// pointer saves meaningful bytes.
func TestFoldCoversBlocksBelowLegacyTokenFloor(t *testing.T) {
	cfg := foldTestConfig(t.TempDir())
	cfg.MinBlockTokens = 2000 // the production default
	cfg.RailTokens = 1
	cfg.TargetTokens = 1
	cfg.MinFoldBytes = 1
	server, _ := newFoldServer(t, cfg)

	body := distinctFoldBody(t, 3, 4096)
	forwarded, result, _, gate := server.foldRequestLedger("tok", body)
	if gate.decision != gateFold {
		t.Fatalf("decision = %s, want fold", gate.decision)
	}
	if !result.changed() {
		t.Fatal("nothing was folded")
	}
	if !bytes.Contains(body, []byte("aaaa")) || bytes.Contains(forwarded, bytes.Repeat([]byte("a"), 4096)) {
		t.Fatal("the eligible round is still present in full")
	}
}

// A pointer carries ~1.9 KB of header and excerpt, so a 2.4 KB result saves
// only a few hundred bytes. That is not worth an archive file and a pointer
// line, so the per-block decision is made on saved bytes.
func TestFoldSkipsBlockWhosePointerSavesTooLittle(t *testing.T) {
	cfg := foldTestConfig(t.TempDir())
	cfg.RailTokens = 1
	cfg.TargetTokens = 1
	cfg.MinFoldBytes = 1
	server, _ := newFoldServer(t, cfg)

	body := distinctFoldBody(t, 3, 2400)
	_, result, _, gate := server.foldRequestLedger("tok", body)
	if result.changed() {
		t.Fatalf("folded %d blocks, want 0: a 2.4 KB result saves too little", result.newBlocks)
	}
	if gate.decision != gateNoBacklog {
		t.Fatalf("decision = %s, want no-backlog", gate.decision)
	}
}

// The rail arithmetic must be logged even when there is nothing to fold: a 240k
// context reporting gate_projected=0 reads as a broken gate.
func TestGateProjectionReportedWithoutCandidates(t *testing.T) {
	cfg := foldTestConfig(t.TempDir())
	server, _ := newFoldServer(t, cfg)
	server.sessions["tok"].lastActual = 200000

	_, _, _, gate := server.foldRequestLedger("tok", wireBody(t, []any{userText("hi")}))
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
	cfg := foldTestConfig(t.TempDir())
	cfg.RailTokens = 1 << 30 // only the cold rail can open the gate
	cfg.ColdRailTokens = 1
	server, _ := newFoldServer(t, cfg)
	server.sessions["tok"].live.pendingColdFold = true
	server.sessions["tok"].live.pendingColdFoldAt = time.Now()

	server.foldRequestLedger("tok", wireBody(t, []any{userText("hi")}))
	if !server.sessions["tok"].live.pendingColdFold {
		t.Fatal("a request with no candidates must not consume the compact signal")
	}

	_, _, _, gate := server.foldRequestLedger("tok", distinctFoldBody(t, 3, 8001))
	if !gate.cold || gate.decision != gateFold {
		t.Fatalf("gate = %+v, want a cold fold on the next foldable request", gate)
	}
}
