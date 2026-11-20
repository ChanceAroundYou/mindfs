package fold

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
// The strings are the ones the proxy has always logged; changing them would
// break existing log tooling for no gain.
const (
	// GateUnmeasured: no usable size estimate, so the gate is disabled and
	// every eligible block is committed. Unit tests and callers with no
	// request measurement land here.
	GateUnmeasured = "unmeasured"
	// GateBelowRail: measured, and the projected size is under the rail.
	GateBelowRail = "below-rail"
	// GateFold: the rail (or a single oversized block) fired.
	GateFold = "fold"
	// GateNoBacklog: the rail fired but there was not enough new material to
	// justify a cache break.
	GateNoBacklog = "no-backlog"
	// GateDisabled: the emergency brake file is present.
	GateDisabled = "disabled"
	// GateObserveOnly: an isolated-ConfigDir session, which is never folded.
	GateObserveOnly = "observe-only"
	// GateScanFailed: the request body could not be scanned.
	GateScanFailed = "scan-failed"
	// GatePrefixGuard: the request targets a model where a proxy-side rewrite
	// invalidates every later thinking block (design 2026-11-20 §4).
	GatePrefixGuard = "prefix-guard"
)

// Estimate is what the request path measured before folding.
type Estimate struct {
	// Est is EstimateByteTokens over the original (unfolded) body.
	Est int
	// Growth is Est minus the previous request's Est, clamped at zero. The
	// projection uses it so one oversized turn does not push us past the rail
	// before the next fold can happen.
	Growth int
	// Actual and ForwardEst are the calibration pair from the previous request
	// in this session. Byte estimates run ~1.33x below real token counts on
	// live traffic, so a rail expressed in est tokens fires around 174k
	// upstream tokens — past the CLI's ~167k auto-compact threshold. Dividing
	// the two turns the rail into an upstream-token threshold; zero means "not
	// measured yet" and the gate keeps its estimator-only behaviour.
	Actual     int
	ForwardEst int
}

// Scale converts estimator tokens into upstream tokens.
//
// A missing or nonsensical pair (first request, failed usage scan) degrades to
// 1:1, which is the old estimator-only behaviour. The clamp keeps one bad
// sample from moving the rail by more than 3x in either direction.
func (e Estimate) Scale() (num, den int) {
	if e.Actual <= 0 || e.ForwardEst <= 0 {
		return 1, 1
	}
	num, den = e.Actual, e.ForwardEst
	if num > 3*den {
		num = 3 * den
	}
	if num*2 < den {
		num = (den + 1) / 2
	}
	return num, den
}

// Decision is the logged outcome of the rail decision for one request.
type Decision struct {
	Decision string
	Est      int
	Growth   int
	// Projected is the rail input in upstream tokens when calibration is
	// available (estimator tokens otherwise). Logged so a rehearsal can be
	// checked against actual_in without redoing the arithmetic.
	Projected int
	// WouldFoldTokens / WouldFoldBlocks describe what this request's fold
	// replaces (sticky plus newly committed).
	WouldFoldTokens int
	WouldFoldBlocks int
	// Cold / ColdReason describe the cache state the decision was taken on, so
	// a later log reader can tell a rail fold from a free cold fold.
	Cold       bool
	ColdReason string
	// Candidates is how many foldable blocks this request had. Zero means the
	// decision had nothing to act on, which is also what keeps a PostCompact
	// cold signal alive for the next request instead of spending it here.
	Candidates int
}

// Budget bounds what this request may newly commit.
type Budget struct {
	// CommitNew is false below the rail: previously committed blocks are still
	// folded, but no new block is added to the commitment set.
	CommitNew bool
	// MinNewBytes rejects a fold event that would not be worth a cache break.
	// Zero in the unmeasured case, where the old "fold everything" behaviour
	// must be preserved.
	MinNewBytes int
	// MaxNewBytes caps one chunk of an event. Zero means unlimited.
	MaxNewBytes int
}

// DecideBudget applies the rail rule.
//
// current is the request's size with the session's committed blocks already
// folded away; largeBlock reports that at least one uncommitted candidate is
// big enough to be worth folding even below the rail (absorbed from dsh's
// cutLongReadRound, plan.js:519-525).
func DecideBudget(cfg Config, est Estimate, cold bool, coldReason string, current int, largeBlock bool) (Budget, Decision) {
	num, den := est.Scale()
	decision := Decision{Est: est.Est, Growth: est.Growth, Cold: cold, ColdReason: coldReason}
	if est.Est <= 0 || cfg.RailTokens <= 0 {
		decision.Decision = GateUnmeasured
		return Budget{CommitNew: true}, decision
	}
	if current < 0 {
		current = 0
	}
	// current is the forwarded size of the previous request (upstream tokens
	// when calibrated), growth is this request's increment in estimator tokens.
	projected := current + est.Growth*num/den
	decision.Projected = projected
	if largeBlock || projected > cfg.RailTokens {
		decision.Decision = GateFold
		return Budget{
			CommitNew:   true,
			MinNewBytes: cfg.MinFoldBytes,
			MaxNewBytes: cfg.MaxFoldBytes,
		}, decision
	}
	// A cold cache makes the fold free, so the rail — which exists only to make
	// the rewrite pay for itself — is the wrong trigger. The cold rail still
	// requires enough context for folding to buy anything at all, and every
	// other constraint (protected tail, block floor, chunk budget) is unchanged.
	if cfg.ColdFoldOn() && cold && projected >= cfg.ColdRailTokens {
		decision.Decision = GateFold
		return Budget{
			CommitNew:   true,
			MinNewBytes: cfg.MinFoldBytes,
			MaxNewBytes: cfg.MaxFoldBytes,
		}, decision
	}
	decision.Decision = GateBelowRail
	return Budget{}, decision
}
