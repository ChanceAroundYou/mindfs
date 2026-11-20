package proxy

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Design §12.6: the rail gate and the per-session commitment set.

func gateTestConfig(dir string) LiveCompactConfig {
	cfg := foldTestConfig(dir)
	cfg.RailTokens = 1
	cfg.TargetTokens = 1
	cfg.MinFoldBytes = 1
	cfg.MaxFoldBytes = 0
	return cfg
}

// distinctFoldMessages is foldTestMessages with per-round content. The shared
// helper repeats the same filler, so every round hashes to the same
// pointerCacheKey — fine for rendering tests, wrong for a commitment set.
func distinctFoldMessages(rounds, size int) []any {
	messages := []any{userText("start")}
	for index := 0; index < rounds; index++ {
		id := fmt.Sprintf("toolu_%d", index)
		messages = append(messages, assistantToolUse(id, "Read"))
		messages = append(messages, userToolResult(id, strings.Repeat(string(rune('a'+index)), size), false))
	}
	return messages
}

func distinctFoldBody(t *testing.T, rounds, size int) []byte {
	t.Helper()
	return wireBody(t, distinctFoldMessages(rounds, size))
}

func foldWithCommits(t *testing.T, body []byte, cfg LiveCompactConfig, store *archiveStore, cache *pointerCache, commits *foldCommits, fc foldContext) (foldResult, liveCompactGate) {
	t.Helper()
	req, err := scanWireRequest(body)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	return planFoldCommit(body, req, cfg, store, cache, commits, fc)
}

// I8: a block committed on one request is folded again on the next, even when
// the rail gate is closed and no new block may be committed.
func TestFoldCommittedBlocksStayFoldedBelowRail(t *testing.T) {
	dir := t.TempDir()
	store := newArchiveStore(dir, time.Hour, archiveMaxBytes)
	cache := newPointerCache()
	commits := newFoldCommits()
	cfg := gateTestConfig(dir)

	first := distinctFoldBody(t, 4, 8001)
	result, gate := foldWithCommits(t, first, cfg, store, cache, commits, foldContext{est: 1 << 20})
	if gate.decision != gateFold || result.newBlocks != 3 || result.stickyBlocks != 0 {
		t.Fatalf("first fold = %+v gate=%s, want 3 new / 0 sticky / fold", result, gate.decision)
	}
	if n := bytes.Count(result.body, []byte("[MindFS live-compact]")); n != 3 {
		t.Fatalf("first fold wrote %d pointers, want 3", n)
	}

	// A new round closes, so a fourth block becomes eligible — but the gate is
	// closed now, so it must not be committed.
	cfg.RailTokens = 1 << 30
	second := distinctFoldBody(t, 5, 8001)
	result2, gate2 := foldWithCommits(t, second, cfg, store, cache, commits, foldContext{est: 8000})
	if gate2.decision != gateBelowRail {
		t.Fatalf("gate = %s, want %s", gate2.decision, gateBelowRail)
	}
	if result2.stickyBlocks != 3 || result2.newBlocks != 0 {
		t.Fatalf("second fold = %+v, want 3 sticky / 0 new", result2)
	}
	if n := bytes.Count(result2.body, []byte("[MindFS live-compact]")); n != 3 {
		t.Fatalf("second fold wrote %d pointers, want the same 3", n)
	}
}

// I8 must not depend on pointerCache: it resets wholesale at 4096 entries, and
// losing it may cost a re-render but must never unfold the prefix.
func TestFoldCommittedBlocksSurvivePointerCacheReset(t *testing.T) {
	dir := t.TempDir()
	store := newArchiveStore(dir, time.Hour, archiveMaxBytes)
	cfg := gateTestConfig(dir)
	commits := newFoldCommits()

	first := distinctFoldBody(t, 4, 8001)
	firstResult, _ := foldWithCommits(t, first, cfg, store, newPointerCache(), commits, foldContext{est: 1 << 20})

	cfg.RailTokens = 1 << 30
	second := distinctFoldBody(t, 5, 8001)
	secondResult, gate := foldWithCommits(t, second, cfg, store, newPointerCache(), commits, foldContext{est: 8000})
	if gate.decision != gateBelowRail || secondResult.stickyBlocks != 3 {
		t.Fatalf("after cache reset: gate=%s sticky=%d, want below-rail / 3", gate.decision, secondResult.stickyBlocks)
	}
	// Same content, same archive path, same pointer bytes.
	if n := bytes.Count(secondResult.body, []byte("[MindFS live-compact]")); n != 3 {
		t.Fatalf("pointers after cache reset = %d, want 3", n)
	}
	if firstResult.foldedTokens != secondResult.foldedTokens {
		t.Fatalf("folded tokens changed across cache reset: %d -> %d", firstResult.foldedTokens, secondResult.foldedTokens)
	}
}

// I9: below the rail, two requests with the same committed set produce the same
// folded bytes — the prefix the upstream cached stays valid.
func TestFoldBelowRailIsByteStable(t *testing.T) {
	dir := t.TempDir()
	store := newArchiveStore(dir, time.Hour, archiveMaxBytes)
	cfg := gateTestConfig(dir)
	commits := newFoldCommits()
	cache := newPointerCache()

	body := distinctFoldBody(t, 4, 8001)
	if _, gate := foldWithCommits(t, body, cfg, store, cache, commits, foldContext{est: 1 << 20}); gate.decision != gateFold {
		t.Fatalf("setup fold did not fire: %s", gate.decision)
	}

	cfg.RailTokens = 1 << 30
	closed := foldContext{est: 8000, growth: 0}
	a, _ := foldWithCommits(t, body, cfg, store, cache, commits, closed)
	b, _ := foldWithCommits(t, body, cfg, store, cache, commits, closed)
	if !bytes.Equal(a.body, b.body) {
		t.Fatal("below-rail folds are not byte-stable")
	}
}

// A fold event that would save too little is dropped whole; the policy never
// goes back to accumulate more.
func TestFoldEventBelowMinBytesIsDropped(t *testing.T) {
	dir := t.TempDir()
	store := newArchiveStore(dir, time.Hour, archiveMaxBytes)
	cfg := gateTestConfig(dir)
	cfg.MinFoldBytes = 1 << 20
	commits := newFoldCommits()

	body := distinctFoldBody(t, 4, 8001)
	result, gate := foldWithCommits(t, body, cfg, store, newPointerCache(), commits, foldContext{est: 1 << 20})
	if gate.decision != gateNoBacklog {
		t.Fatalf("gate = %s, want %s", gate.decision, gateNoBacklog)
	}
	if result.newBlocks != 0 || result.changed() {
		t.Fatalf("small event was applied: %+v", result)
	}
	if commits.Len() != 0 {
		t.Fatalf("small event recorded %d commitments", commits.Len())
	}
}

// The protected tail is bounded by size, not only by round count (design
// §12.7(d), dsh's keepRoundTokens). Three rounds of 90 KB reads is a ~75k-token
// tail; a rail that fires at 160k would then have exactly one round to cut,
// producing the shallow fold §12.7(b) measured as more expensive than no fold.
func TestKeepTailTokensShrinksProtectedWindow(t *testing.T) {
	dir := t.TempDir()
	store := newArchiveStore(dir, time.Hour, archiveMaxBytes)
	cfg := gateTestConfig(dir)
	cfg.KeepRounds = 3
	cfg.KeepTailTokens = 1 << 30 // effectively disabled: round count wins
	cfg.MaxFoldBytes = 0         // fold everything eligible

	body := distinctFoldBody(t, 4, 40000) // 4 closed rounds, ~10k tokens each
	byRounds, _ := foldWithCommits(t, body, cfg, store, newPointerCache(), newFoldCommits(), foldContext{est: 1 << 20})
	if byRounds.newBlocks != 1 {
		t.Fatalf("keep_rounds only: new blocks = %d, want 1 (three rounds protected)", byRounds.newBlocks)
	}

	// 25k tail: the two newest rounds fit, the third does not, so it becomes
	// eligible even though keep_rounds would protect it.
	cfg.KeepTailTokens = 25000
	byTail, _ := foldWithCommits(t, body, cfg, store, newPointerCache(), newFoldCommits(), foldContext{est: 1 << 20})
	if byTail.newBlocks != 2 {
		t.Fatalf("keep_tail_tokens: new blocks = %d, want 2 (a third round no longer fits the tail)", byTail.newBlocks)
	}

	// The newest round is never cut, no matter how small the tail budget.
	cfg.KeepTailTokens = 1
	tiny, _ := foldWithCommits(t, body, cfg, store, newPointerCache(), newFoldCommits(), foldContext{est: 1 << 20})
	if tiny.newBlocks != 3 {
		t.Fatalf("tiny tail: new blocks = %d, want 3 (all but the newest)", tiny.newBlocks)
	}
}

// max_fold_bytes caps one chunk, not the whole event (design §12.7(d), dsh's
// chunked drain): an event that stopped at one chunk would leave the context
// shallow-folded, which costs more than not folding. One event may drain up to
// foldMaxChunksPerEvent chunks, so the rest still waits for a later event.
func TestFoldEventRespectsMaxBytes(t *testing.T) {
	dir := t.TempDir()
	store := newArchiveStore(dir, time.Hour, archiveMaxBytes)
	cfg := gateTestConfig(dir)
	cfg.MaxFoldBytes = 8100 // room for exactly one 8001-byte block per chunk
	commits := newFoldCommits()

	// Three blocks, three chunks: all of them drain in this event.
	body := distinctFoldBody(t, 4, 8001)
	result, gate := foldWithCommits(t, body, cfg, store, newPointerCache(), commits, foldContext{est: 1 << 20})
	if gate.decision != gateFold {
		t.Fatalf("gate = %s, want %s", gate.decision, gateFold)
	}
	if result.newBlocks != 3 {
		t.Fatalf("new blocks = %d, want 3 (one per chunk)", result.newBlocks)
	}

	// Twelve blocks need twelve chunks; the event stops at the cap.
	dir2 := t.TempDir()
	store2 := newArchiveStore(dir2, time.Hour, archiveMaxBytes)
	body = distinctFoldBody(t, 13, 8001)
	result, _ = foldWithCommits(t, body, cfg, store2, newPointerCache(), newFoldCommits(), foldContext{est: 1 << 20})
	if result.newBlocks != foldMaxChunksPerEvent {
		t.Fatalf("new blocks = %d, want %d (chunks per event)", result.newBlocks, foldMaxChunksPerEvent)
	}
}

// A single oversized result is worth folding even below the rail: it dominates
// the context on its own (absorbed from dsh's cutLongReadRound).
func TestLargeBlockFoldsBelowRail(t *testing.T) {
	dir := t.TempDir()
	store := newArchiveStore(dir, time.Hour, archiveMaxBytes)
	cfg := gateTestConfig(dir)
	cfg.RailTokens = 1 << 30 // never fires on size
	cfg.MinFoldBytes = 1
	commits := newFoldCommits()

	messages := []any{
		userText("start"),
		assistantToolUse("small", "Read"),
		userToolResult("small", strings.Repeat("s", 9000), false),
		assistantToolUse("huge", "Read"),
		userToolResult("huge", strings.Repeat("h", 200000), false),
		assistantToolUse("newest", "Read"),
		userToolResult("newest", strings.Repeat("n", 9000), false),
	}
	body := wireBody(t, messages)
	result, gate := foldWithCommits(t, body, cfg, store, newPointerCache(), commits, foldContext{est: 60000})
	if gate.decision != gateFold {
		t.Fatalf("gate = %s, want %s for an oversized block", gate.decision, gateFold)
	}
	if result.newBlocks < 1 {
		t.Fatalf("new blocks = %d, want at least the oversized one", result.newBlocks)
	}
}

// dsh's longSpan: the newest round containing an oversized result is cuttable
// even though it sits inside the keep window (index.js:649-668, plan.js:519).
// Without this the turn that needs folding most — one huge read — can never be
// folded, so the request goes straight into the context window.
func TestNewestRoundWithOversizedResultIsNotProtected(t *testing.T) {
	dir := t.TempDir()
	store := newArchiveStore(dir, time.Hour, archiveMaxBytes)
	cfg := gateTestConfig(dir)
	cfg.RailTokens = 1 << 30 // the rail never fires on size alone
	huge := strings.Repeat("h", 400000)

	body := wireBody(t, []any{
		userText("start"),
		assistantToolUse("small", "Read"),
		userToolResult("small", strings.Repeat("s", 9000), false),
		assistantToolUse("huge", "Read"),
		userToolResult("huge", huge, false),
	})
	result, gate := foldWithCommits(t, body, cfg, store, newPointerCache(), newFoldCommits(), foldContext{est: 110000})
	if gate.decision != gateFold {
		t.Fatalf("gate = %s, want %s for an oversized newest round", gate.decision, gateFold)
	}
	forwarded := result.body
	if forwarded == nil {
		forwarded = body // nothing folded: foldResult.body is nil, not the original
	}
	if bytes.Contains(forwarded, []byte(huge)) {
		t.Fatal("the oversized result in the newest round is still verbatim")
	}
	if !bytes.Contains(forwarded, []byte("[MindFS live-compact]")) {
		t.Fatal("no pointer was written")
	}
}

// The carve-out is for oversized results only: an ordinary newest round is
// still protected by keep_rounds.
func TestNewestNormalRoundStaysProtected(t *testing.T) {
	dir := t.TempDir()
	store := newArchiveStore(dir, time.Hour, archiveMaxBytes)
	cfg := gateTestConfig(dir)
	cfg.RailTokens = 1 << 30
	normal := strings.Repeat("n", 9000)

	body := wireBody(t, []any{
		userText("start"),
		assistantToolUse("old", "Read"),
		userToolResult("old", strings.Repeat("o", 9000), false),
		assistantToolUse("newest", "Read"),
		userToolResult("newest", normal, false),
	})
	// Twice: once with the gate closed and once with it firing. keep_rounds
	// protects the newest round unconditionally — the model needs the result it
	// just read, and the rail firing is not a reason to take it away.
	for _, rail := range []int{1 << 30, 1} {
		cfg.RailTokens = rail
		result, _ := foldWithCommits(t, body, cfg, store, newPointerCache(), newFoldCommits(), foldContext{est: 1 << 20})
		forwarded := result.body
		if forwarded == nil {
			forwarded = body
		}
		if !bytes.Contains(forwarded, []byte(normal)) {
			t.Fatalf("the newest ordinary round was folded (rail=%d)", rail)
		}
	}
	// A zero large_block threshold (a caller that skipped Normalized) must not
	// turn every round into an exemption.
	cfg.RailTokens, cfg.LargeBlockTokens = 1, 0
	result, _ := foldWithCommits(t, body, cfg, store, newPointerCache(), newFoldCommits(), foldContext{est: 1 << 20})
	forwarded := result.body
	if forwarded == nil {
		forwarded = body
	}
	if !bytes.Contains(forwarded, []byte(normal)) {
		t.Fatal("large_block_tokens=0 exempted the newest ordinary round")
	}

	cfg.RailTokens, cfg.LargeBlockTokens = 1<<30, 1<<20
	if result, gate := foldWithCommits(t, body, cfg, store, newPointerCache(), newFoldCommits(), foldContext{est: 6000}); gate.decision != gateBelowRail || result.newBlocks != 0 {
		t.Fatalf("below rail: gate=%s new=%d", gate.decision, result.newBlocks)
	}
}

// I8 across the carve-out: a round committed while it sits inside the keep
// window must stay a candidate after newer rounds arrive. Otherwise a newer
// oversized round takes the carve-out, the older one falls back under
// keep_rounds, and the committed pointer silently unfolds — the prefix the
// upstream cached gets rewritten even though nothing about it changed.
func TestCarvedOutCommitSurvivesNewerOversizedRound(t *testing.T) {
	dir := t.TempDir()
	store := newArchiveStore(dir, time.Hour, archiveMaxBytes)
	cache := newPointerCache()
	commits := newFoldCommits()
	cfg := gateTestConfig(dir)
	cfg.KeepRounds = 2
	// Pin the tail by round count: this case is about the keep window sliding,
	// and a size-bounded tail would keep every small round eligible regardless.
	cfg.KeepTailTokens = 1 << 30
	huge, newer := strings.Repeat("h", 400000), strings.Repeat("H", 400000)

	round := func(id, name string, body string) []any {
		return []any{assistantToolUse(id, name), userToolResult(id, body, false)}
	}
	first := []any{userText("start")}
	first = append(first, round("r0", "Read", strings.Repeat("a", 9000))...)
	first = append(first, round("r1", "Read", strings.Repeat("b", 9000))...)
	first = append(first, round("r2", "Read", strings.Repeat("c", 9000))...)
	first = append(first, round("r3", "Read", huge)...)

	result, gate := foldWithCommits(t, wireBody(t, first), cfg, store, cache, commits, foldContext{est: 1 << 20})
	if gate.decision != gateFold {
		t.Fatalf("first fold gate = %s, want %s", gate.decision, gateFold)
	}
	if !bytes.Contains(result.body, []byte("[MindFS live-compact]")) || bytes.Contains(result.body, []byte(huge)) {
		t.Fatal("the oversized round inside the keep window was not folded")
	}

	// One more round closes and it is oversized too. Both oversized rounds now
	// sit inside the keep window (r3 at len-2, r4 at len-1), so a carve-out
	// that only exempts the newest one drops r3's committed block.
	second := append(append([]any{}, first...), round("r4", "Read", newer)...)
	result2, _ := foldWithCommits(t, wireBody(t, second), cfg, store, cache, commits, foldContext{est: 1 << 20})
	// Two new commits: r2 slid out of the keep window, and r4 is carved out.
	if result2.newBlocks != 2 {
		t.Fatalf("second fold committed %d new blocks, want 2", result2.newBlocks)
	}
	forwarded := result2.body
	if forwarded == nil {
		forwarded = wireBody(t, second)
	}
	if bytes.Contains(forwarded, []byte(huge)) {
		t.Fatal("the earlier carved-out commit unfolded once a newer oversized round arrived")
	}
	if bytes.Contains(forwarded, []byte(newer)) {
		t.Fatal("the newest oversized round is still verbatim")
	}
}

// The rail is an upstream-token threshold, not an estimator threshold. Byte
// estimates run ~1.33x below real usage on live traffic (bench, 2026-11-19), so
// a 130k rail in est tokens fires around 174k upstream — past the CLI's ~167k
// auto-compact threshold. With a calibration pair the same request has to fire
// earlier; the estimator alone would keep it below the rail.
func TestRailUsesUpstreamTokenScale(t *testing.T) {
	cfg := gateTestConfig(t.TempDir())
	cfg.RailTokens = 130000
	cfg.TargetTokens = 100000

	// 120k upstream tokens for a 90k estimator body: 9k est of growth is 12k
	// upstream. The estimator alone would read 129k and stay closed; the
	// calibration pushes it to 132k, over the rail.
	fc := foldContext{est: 99000, growth: 9000, actual: 120000, forwardEst: 90000}
	if _, gate := decideFoldBudget(cfg, fc, fc.actual, false); gate.decision != gateFold {
		t.Fatalf("projected %d did not fire: %s", gate.projected, gate.decision)
	} else if gate.projected != 132000 {
		t.Fatalf("projected = %d, want 132000 (estimator units would be 129000)", gate.projected)
	}
	// Less growth: 128k upstream, still under the rail.
	fc.growth = 6000
	if _, gate := decideFoldBudget(cfg, fc, fc.actual, false); gate.decision != gateBelowRail {
		t.Fatalf("projected %d fired: %s", gate.projected, gate.decision)
	} else if gate.projected != 128000 {
		t.Fatalf("projected = %d, want 128000", gate.projected)
	}
}

func TestActualScaleClampsOutliers(t *testing.T) {
	cases := []struct {
		name               string
		actual, forwardEst int
		num, den           int
	}{
		{"no sample", 0, 90000, 1, 1},
		{"plain", 120000, 90000, 120000, 90000},
		{"absurd high clamps to 3x", 50000, 5000, 15000, 5000},
		{"absurd low clamps to 0.5x", 15000, 150000, 75000, 150000},
	}
	for _, tc := range cases {
		num, den := foldContext{actual: tc.actual, forwardEst: tc.forwardEst}.actualScale()
		if num != tc.num || den != tc.den {
			t.Fatalf("%s: scale = %d/%d, want %d/%d", tc.name, num, den, tc.num, tc.den)
		}
	}
}

// Without a calibration sample (first request, failed usage scan) the gate
// keeps its old estimator behaviour.
func TestRailFallsBackToEstimator(t *testing.T) {
	cfg := gateTestConfig(t.TempDir())
	cfg.RailTokens = 130000
	cfg.TargetTokens = 100000

	if _, gate := decideFoldBudget(cfg, foldContext{est: 120000}, 120000, false); gate.decision != gateBelowRail {
		t.Fatalf("no sample: %s", gate.decision)
	}
	if _, gate := decideFoldBudget(cfg, foldContext{est: 140000}, 140000, false); gate.decision != gateFold {
		t.Fatalf("no sample over rail: %s", gate.decision)
	}
}

// target_tokens shares the rail's unit: at 1.5x calibration, 150k upstream
// tokens and a 100k target need two 30k-token blocks (45k upstream each), not
// one. The estimator-only arithmetic would have stopped after the first.
func TestFoldStopsAtTargetInUpstreamUnits(t *testing.T) {
	dir := t.TempDir()
	store := newArchiveStore(dir, time.Hour, archiveMaxBytes)
	cfg := gateTestConfig(dir)
	cfg.RailTokens = 130000
	cfg.TargetTokens = 100000
	cfg.KeepRounds = 1

	body := distinctFoldBody(t, 4, 120000) // 3 eligible blocks ≈ 30k tokens each
	fc := foldContext{est: estimateByteTokens(body), actual: 150000, forwardEst: 100000}
	result, gate := foldWithCommits(t, body, cfg, store, newPointerCache(), newFoldCommits(), fc)
	if gate.decision != gateFold {
		t.Fatalf("gate = %s, want %s", gate.decision, gateFold)
	}
	if result.newBlocks != 2 {
		t.Fatalf("new blocks = %d, want 2 (one block leaves the projection above target)", result.newBlocks)
	}
}

// The calibration pair is written by two different halves of a turn (request
// path and response usage). Unseen usage must not clobber a good sample.
func TestSessionCalibrationRoundTrip(t *testing.T) {
	s := &Server{sessions: map[string]*sessionState{"token": {}}}
	s.noteForwardEstimate("token", 88000)
	s.noteActualContext("token", usageTotals{
		InputTokens: 100000, CacheReadInputTokens: 20000, Seen: true,
	})
	if actual, forwardEst := s.sessionCalibration("token"); actual != 120000 || forwardEst != 88000 {
		t.Fatalf("calibration = %d/%d, want 120000/88000", actual, forwardEst)
	}
	s.noteActualContext("token", usageTotals{})
	if actual, _ := s.sessionCalibration("token"); actual != 120000 {
		t.Fatalf("unseen usage overwrote the sample: %d", actual)
	}
	// A seen-but-zero block is also no sample: some upstream error paths report
	// a message_start with empty usage.
	s.noteActualContext("token", usageTotals{Seen: true})
	if actual, _ := s.sessionCalibration("token"); actual != 120000 {
		t.Fatalf("zero usage overwrote the sample: %d", actual)
	}
}
