package proxy

// The rail gate.
//
// Design §12.6: folding invalidates the upstream prefix cache from the fold
// boundary onward, so a fold that happens every turn converts a 0.1x cache read
// into a full-price rewrite of the whole history. The measured cost of that was
// +32.5% against the same workload without folding (spec §12.5). The only
// policy that can pay for itself is therefore "fold rarely, and only when the
// context is about to become a problem": below the rail, commit nothing.
//
// Design §12.7 sharpened this: what pays is fold *depth*, not frequency. An
// event's payoff is roughly (folded out) / (folded left) rounds, so the default
// is to trigger late and cut deep rather than trim a little every few turns.
//
// This mirrors dsh-live-compact's cloud engine, which refuses to cut unless it
// is draining (`plan.js:900-901`).

// Gate decisions, logged and never used for control flow outside the planner.
const (
	// gateUnmeasured: no usable size estimate (unit tests and the compatibility
	// wrapper), so the gate is disabled and every eligible block is committed.
	gateUnmeasured = "unmeasured"
	// gateBelowRail: measured, and the projected size is under rail_tokens.
	gateBelowRail = "below-rail"
	// gateFold: the rail (or a single oversized block) fired.
	gateFold = "fold"
	// gateNoBacklog: the rail fired but there was not enough new material to
	// justify a cache break.
	gateNoBacklog = "no-backlog"
	// gateDisabled: the emergency brake file is present.
	gateDisabled = "disabled"
	// gateObserveOnly: an isolated-ConfigDir session, which is never folded.
	gateObserveOnly = "observe-only"
	// gateScanFailed: the request body could not be scanned.
	gateScanFailed = "scan-failed"
	// gatePrefixGuard: the request targets a model that runs Anthropic's
	// preserved-thinking prefix check, where a proxy-side rewrite invalidates
	// every later thinking block (design 2026-11-20 §4).
	gatePrefixGuard = "prefix-guard"
)

// foldContext is what the request path measured before folding.
type foldContext struct {
	// est is estimateByteTokens(body) on the original (unfolded) body.
	est int
	// growth is est minus the previous request's est, clamped at zero. The
	// projection uses it so one oversized turn does not push us past the rail
	// before the next fold can happen.
	growth int
	// actual / forwardEst are the calibration pair from the previous request in
	// this session: the upstream-reported context size (input + cache creation +
	// cache read) and the proxy's own estimate for the body that request
	// forwarded. Byte estimates run ~1.33x below real token counts on live
	// traffic, so a rail expressed in est tokens fires around 174k upstream
	// tokens — past the CLI's ~167k auto-compact threshold. Dividing the two
	// turns the rail into an upstream-token threshold; zero means "not measured
	// yet" and the gate keeps its estimator-only behaviour.
	actual     int
	forwardEst int
	// cold reports that the upstream prompt cache is believed to be already
	// gone (just compacted, idle past the TTL, or the previous response read
	// nothing from cache). A fold then costs no cache rewrite, so the rail is
	// not the right trigger; coldReason names which signal fired.
	cold       bool
	coldReason string
}

// actualScale converts estimator tokens into upstream tokens. A missing or
// nonsensical pair (first request, failed usage scan) degrades to 1:1, which is
// the old estimator-only behaviour. The clamp keeps one bad sample from moving
// the rail by more than 3x in either direction.
func (fc foldContext) actualScale() (num, den int) {
	if fc.actual <= 0 || fc.forwardEst <= 0 {
		return 1, 1
	}
	num, den = fc.actual, fc.forwardEst
	if num > 3*den {
		num = 3 * den
	}
	if num*2 < den {
		num = (den + 1) / 2
	}
	return num, den
}

// liveCompactGate is the logged outcome of the rail decision for one request.
type liveCompactGate struct {
	decision string
	est      int
	growth   int
	// projected is the rail input in upstream tokens when calibration is
	// available (estimator tokens otherwise). Logged as gate_projected so a
	// rehearsal can be checked against actual_in without redoing the arithmetic.
	projected int
	// wouldFoldTokens / wouldFoldBlocks describe what this request's fold
	// replaces (sticky plus newly committed). Shadow reports them so a
	// rehearsal can measure the savings mode=on would have produced; on
	// reports what was actually folded.
	wouldFoldTokens int
	wouldFoldBlocks int
	// cold / coldReason describe the cache state the decision was taken on, so
	// a later log reader can tell a rail fold from a free cold fold.
	cold       bool
	coldReason string
	// candidates is how many foldable blocks this request had. Zero means the
	// decision had nothing to act on, which is also what keeps a PostCompact
	// cold signal alive for the next request instead of spending it here.
	candidates int
}

// foldPreview is what shadow's rehearsal of the rail policy would do.
type foldPreview struct {
	tokens    int // sticky + new tokens a fold would remove
	blocks    int
	newBlocks int
}

// previewFold applies the same rail rules as planFoldCommit without rendering
// pointers, touching the render cache, or writing archives. Shadow mode reports
// the decision and the savings so a rehearsal on a live node can gate
// admission (design §12.6 观测).
//
// It commits the keys it would fold into the session set. Without that every
// request would look unfolded and the gate would fire on every turn, which is
// not the policy mode=on applies (I8). Sizes come from the byte-span estimate
// rather than a decode, so the min/max fold-budget edges can differ from the
// real planner by the JSON escaping overhead; the gate decision itself depends
// only on estimated tokens and eligibility.
func previewFold(body []byte, req *scanRequest, cfg LiveCompactConfig, commits *foldCommits, fc foldContext) (foldPreview, liveCompactGate) {
	gate := liveCompactGate{decision: gateBelowRail, est: fc.est, growth: fc.growth, cold: fc.cold, coldReason: fc.coldReason}
	if fc.est <= 0 {
		gate.decision = gateUnmeasured
	}
	candidates, _ := collectFoldCandidates(body, req, cfg)
	gate.candidates = len(candidates)
	if len(candidates) == 0 {
		// Mirror planFoldCommit: report the rail arithmetic even with nothing to
		// fold, so a rehearsal's gate_projected is the real projection.
		current := fc.est
		if fc.actual > 0 {
			current = fc.actual
		}
		_, decided := decideFoldBudget(cfg, fc, current, false)
		if decided.decision == gateFold {
			decided.decision = gateNoBacklog
		}
		decided.candidates = 0
		return foldPreview{}, decided
	}

	used := make([]bool, len(candidates))
	preview := foldPreview{}
	stickyTokens := 0
	for index, candidate := range candidates {
		if !commits.has(candidate.key) {
			continue
		}
		used[index] = true
		preview.blocks++
		preview.tokens += candidate.approxTokens
		stickyTokens += candidate.approxTokens
	}

	// Upstream-calibrated: shadow forwards the unfolded body, so its measured
	// context still contains the blocks mode=on would have folded. Subtract them
	// (scaled to upstream tokens) or the preview would fire earlier than the
	// real planner and over-report fold events.
	current := fc.est - stickyTokens
	if fc.actual > 0 {
		num, den := fc.actualScale()
		current = fc.actual - stickyTokens*num/den
	}
	scaleNum, scaleDen := fc.actualScale()
	largeBlock := false
	for index, candidate := range candidates {
		if !used[index] && candidate.approxTokens >= cfg.LargeBlockTokens {
			largeBlock = true
			break
		}
	}
	budget, decided := decideFoldBudget(cfg, fc, current, largeBlock)
	gate = decided
	gate.candidates = len(candidates)

	if budget.commitNew {
		newBytes, newTokens, newBlocks := 0, 0, 0
		committed := make([]pointerCacheKey, 0, len(candidates))
		// Mirror planFoldCommit's chunked drain so a shadow rehearsal reports the
		// same fold depth mode=on would produce.
		chunkBytes, chunks := 0, 1
		for index, candidate := range candidates {
			if used[index] {
				continue
			}
			width := candidate.part.end - candidate.part.start
			if budget.maxNewBytes > 0 && chunkBytes > 0 && chunkBytes+width > budget.maxNewBytes {
				if chunks >= foldMaxChunksPerEvent {
					break
				}
				chunks++
				chunkBytes = 0
			}
			// Mirror planFoldCommit's per-block savings floor. The span width is
			// the un-decoded upper bound on what a pointer can save, so this is
			// the same decision made before rendering.
			if width < foldMinBlockSavings {
				continue
			}
			chunkBytes += width
			newBytes += width
			newTokens += candidate.approxTokens
			newBlocks++
			committed = append(committed, candidate.key)
			if fc.est > 0 && current+fc.growth*scaleNum/scaleDen-newTokens*scaleNum/scaleDen <= cfg.TargetTokens {
				break
			}
		}
		// Mirror planFoldCommit: a fold event too small to pay for a cache
		// break commits nothing, and the keys are not recorded.
		if budget.minNewBytes > 0 && newBytes < budget.minNewBytes {
			gate.decision = gateNoBacklog
		} else {
			for _, key := range committed {
				commits.add(key)
			}
			preview.tokens += newTokens
			preview.blocks += newBlocks
			preview.newBlocks = newBlocks
		}
	}
	gate.wouldFoldTokens = preview.tokens
	gate.wouldFoldBlocks = preview.blocks
	return preview, gate
}

// foldBudget bounds what this request may newly commit.
type foldBudget struct {
	// commitNew is false below the rail: previously committed blocks are still
	// folded, but no new block is added to the commitment set.
	commitNew bool
	// minNewBytes rejects a fold event that would not be worth a cache break.
	// Zero in the unmeasured case, where the old "fold everything" behaviour
	// must be preserved.
	minNewBytes int
	// maxNewBytes caps one event. Zero means unlimited.
	maxNewBytes int
}

// decideFoldBudget applies the rail rule.
//
// current is the request's estimated size with the session's committed blocks
// already folded away; largeBlock reports that at least one uncommitted
// candidate is big enough to be worth folding even below the rail (absorbed
// from dsh's cutLongReadRound, plan.js:519-525).
func decideFoldBudget(cfg LiveCompactConfig, fc foldContext, current int, largeBlock bool) (foldBudget, liveCompactGate) {
	num, den := fc.actualScale()
	gate := liveCompactGate{est: fc.est, growth: fc.growth, cold: fc.cold, coldReason: fc.coldReason}
	if fc.est <= 0 || cfg.RailTokens <= 0 {
		gate.decision = gateUnmeasured
		return foldBudget{commitNew: true}, gate
	}
	if current < 0 {
		current = 0
	}
	// current is the forwarded size of the previous request (upstream tokens
	// when calibrated), growth is this request's increment in estimator tokens.
	projected := current + fc.growth*num/den
	gate.projected = projected
	if largeBlock || projected > cfg.RailTokens {
		gate.decision = gateFold
		return foldBudget{
			commitNew:   true,
			minNewBytes: cfg.MinFoldBytes,
			maxNewBytes: cfg.MaxFoldBytes,
		}, gate
	}
	// A cold cache makes the fold free, so the rail — which exists only to make
	// the rewrite pay for itself — is the wrong trigger. The cold rail still
	// requires enough context for folding to buy anything at all, and every
	// other constraint (protected tail, block floor, chunk budget) is unchanged.
	if cfg.ColdFoldOn() && fc.cold && projected >= cfg.ColdRailTokens {
		gate.decision = gateFold
		return foldBudget{
			commitNew:   true,
			minNewBytes: cfg.MinFoldBytes,
			maxNewBytes: cfg.MaxFoldBytes,
		}, gate
	}
	gate.decision = gateBelowRail
	return foldBudget{}, gate
}
