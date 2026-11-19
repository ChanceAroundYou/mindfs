package proxy

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Design §12.6 观测: shadow must rehearse the rail policy, not just parse the
// request. A 24h shadow rehearsal is the admission gate for mode=on
// (`gate=fold` occurrences, `est_after/est_before`), so the decision and the
// savings a fold would produce have to be reported without rewriting a byte or
// writing an archive.

func shadowPreviewServer(t *testing.T, cfg LiveCompactConfig) *Server {
	t.Helper()
	cfg.Mode = LiveCompactShadow
	if cfg.KeepRounds == 0 {
		cfg.KeepRounds = 1
	}
	if cfg.MinBlockTokens == 0 {
		cfg.MinBlockTokens = 10
	}
	if cfg.MinBodyBytes == 0 {
		cfg.MinBodyBytes = 1
	}
	s, err := NewServerWithLiveCompact(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// A shadow request above the rail reports gate=fold and the tokens a fold would
// remove, while the forwarded body stays byte-identical and no archive is
// written.
func TestShadowPreviewReportsFoldWithoutRewriting(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "context")
	s := shadowPreviewServer(t, LiveCompactConfig{
		RailTokens:     1000,
		TargetTokens:   500,
		MinFoldBytes:   1,
		ArchiveDir:     dir,
		ArchiveEnabled: true,
	})
	s.RegisterSessionWithKey("shadow1", "http://127.0.0.1:0", "shadow-1")

	body := distinctFoldBody(t, 4, 8001)
	forwarded, result, gate := s.foldRequest("shadow1", body)
	if !bytes.Equal(forwarded, body) {
		t.Fatal("shadow changed the forwarded body")
	}
	if result.changed() {
		t.Fatalf("shadow produced a fold result: %+v", result)
	}
	if gate.decision != gateFold {
		t.Fatalf("gate = %s, want %s", gate.decision, gateFold)
	}
	if gate.wouldFoldTokens <= 0 {
		t.Fatalf("would_fold_tokens = %d, want > 0", gate.wouldFoldTokens)
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		t.Fatalf("shadow wrote %d archive files", len(entries))
	}
}

// Once shadow has rehearsed a fold event, the committed blocks stay counted on
// later requests even below the rail — otherwise every turn would look
// unfolded and the gate would fire every time, which is not the policy mode=on
// applies (I8/I9).
func TestShadowPreviewKeepsCommittedBlocksBelowRail(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "context")
	s := shadowPreviewServer(t, LiveCompactConfig{
		RailTokens:     1000,
		TargetTokens:   500,
		MinFoldBytes:   1,
		ArchiveDir:     dir,
		ArchiveEnabled: true,
	})
	s.RegisterSessionWithKey("shadow2", "http://127.0.0.1:0", "shadow-2")

	first := distinctFoldBody(t, 4, 8001)
	if _, _, gate := s.foldRequest("shadow2", first); gate.decision != gateFold {
		t.Fatalf("first gate = %s, want %s", gate.decision, gateFold)
	}
	if got := s.sessionFoldCommits("shadow2").len(); got == 0 {
		t.Fatal("shadow rehearsal committed no blocks")
	}

	// Raise the rail: the new round is closed, but nothing new may be committed.
	s.liveCompact.RailTokens = 1 << 30
	second := distinctFoldBody(t, 5, 8001)
	forwarded, _, gate := s.foldRequest("shadow2", second)
	if gate.decision != gateBelowRail {
		t.Fatalf("second gate = %s, want %s", gate.decision, gateBelowRail)
	}
	if gate.wouldFoldTokens <= 0 {
		t.Fatalf("below rail with committed blocks: would_fold_tokens = %d, want > 0", gate.wouldFoldTokens)
	}
	if !bytes.Equal(forwarded, second) {
		t.Fatal("shadow below the rail changed the forwarded body")
	}
}

// A single oversized result fires the gate even below the rail (design §12.6,
// absorbed from dsh's cutLongReadRound).
func TestShadowPreviewFiresOnOversizedBlockBelowRail(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "context")
	s := shadowPreviewServer(t, LiveCompactConfig{
		RailTokens:       1 << 30,
		TargetTokens:     500,
		MinFoldBytes:     1,
		LargeBlockTokens: 100,
		ArchiveDir:       dir,
		ArchiveEnabled:   true,
	})
	s.RegisterSessionWithKey("shadow3", "http://127.0.0.1:0", "shadow-3")

	body := distinctFoldBody(t, 4, 8001)
	_, _, gate := s.foldRequest("shadow3", body)
	if gate.decision != gateFold {
		t.Fatalf("gate = %s, want %s for an oversized block", gate.decision, gateFold)
	}
	if gate.wouldFoldTokens <= 0 {
		t.Fatalf("would_fold_tokens = %d, want > 0", gate.wouldFoldTokens)
	}
}

// With a calibration sample, shadow's measured context still contains the
// blocks mode=on would have folded, so their upstream-scaled tokens must be
// subtracted before the rail comparison. Skipping that makes the rehearsal fire
// earlier than the real planner and over-report fold events on a 24h run.
func TestShadowPreviewSubtractsStickyInUpstreamUnits(t *testing.T) {
	cfg := foldTestConfig(t.TempDir())
	cfg.RailTokens = 130000
	cfg.TargetTokens = 100000
	cfg.MaxFoldBytes = 1 << 30     // three blocks must fit in one event for this case
	cfg.LargeBlockTokens = 1 << 30 // isolate the rail path from the single-block bypass
	commits := newFoldCommits()

	body := distinctFoldBody(t, 4, 200000) // three eligible blocks ≈ 50k tokens each
	est := estimateByteTokens(body)
	req, err := scanWireRequest(body)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if _, gate := previewFold(body, req, cfg, commits, foldContext{est: est}); gate.decision != gateFold {
		t.Fatalf("setup rehearsal did not fire: %s", gate.decision)
	}
	stickyEst := 0
	for _, candidate := range mustCandidates(t, body, req, cfg) {
		if commits.has(candidate.key) {
			stickyEst += candidate.approxTokens
		}
	}
	if stickyEst == 0 {
		t.Fatal("setup committed no blocks")
	}

	// Shadow forwards the unfolded body, so the upstream count still contains
	// the committed blocks. 1.5x calibration for readability.
	actual, forwardEst := est*3/2, est
	fc := foldContext{est: est, growth: 1000, actual: actual, forwardEst: forwardEst}
	preview, gate := previewFold(body, req, cfg, commits, fc)
	withoutSubtraction := actual + fc.growth*actual/forwardEst
	if withoutSubtraction <= cfg.RailTokens {
		t.Fatalf("case invalid: %d does not exceed the rail without subtraction", withoutSubtraction)
	}
	if gate.decision != gateBelowRail {
		t.Fatalf("gate = %s (projected %d), want %s: sticky tokens were not subtracted",
			gate.decision, gate.projected, gateBelowRail)
	}
	if preview.newBlocks != 0 {
		t.Fatalf("preview committed %d new blocks below the rail", preview.newBlocks)
	}
	if gate.wouldFoldTokens <= 0 {
		t.Fatalf("would_fold_tokens = %d, want the sticky blocks reported", gate.wouldFoldTokens)
	}
}

// mustCandidates is collectFoldCandidates with a test-fatal on scan failure.
func mustCandidates(t *testing.T, body []byte, req *scanRequest, cfg LiveCompactConfig) []scanFoldCandidate {
	t.Helper()
	candidates, _ := collectFoldCandidates(body, req, cfg)
	return candidates
}

// The rehearsal only matters if it reaches the log an operator parses during a
// 24h shadow run: the decision and the savings must both be on the line.
func TestShadowObservationLogsGateAndWouldFoldTokens(t *testing.T) {
	var buf bytes.Buffer
	oldWriter, oldFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(oldWriter)
		log.SetFlags(oldFlags)
	}()

	s := &Server{liveCompact: LiveCompactConfig{Mode: LiveCompactShadow}.Normalized()}
	s.logLiveCompact("shadow-log", liveCompactObservation{
		mode:            LiveCompactShadow,
		foldAllowed:     true,
		gateDecision:    gateFold,
		gateEst:         200000,
		wouldFoldTokens: 1234,
	}, usageTotals{})
	line := buf.String()
	if !strings.Contains(line, " gate=fold ") || !strings.Contains(line, " would_fold_tokens=1234") {
		t.Fatalf("shadow log line missing gate/savings: %q", line)
	}

	buf.Reset()
	logLiveCompactFold("shadow-log", foldResult{newBlocks: 2, stickyBlocks: 1}, gateFold, 0)
	if line := buf.String(); !strings.Contains(line, " gate=fold ") {
		t.Fatalf("fold log line missing gate: %q", line)
	}
}
