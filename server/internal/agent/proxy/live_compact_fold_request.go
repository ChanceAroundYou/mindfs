package proxy

import (
	"time"
)

// The request-path entry point and the per-session calibrations it reads.
// Split out of live_compact_fold.go when that file crossed the 500-line hard
// limit; pure move, no behaviour change.

// foldRequest runs the fold on the request path and drops the ledger record.
// It exists for callers that only need the forwarded body; the handler uses
// foldRequestLedger so the fold's rewrite cost can be paired with the response
// usage and written to the ledger once the turn ends.
func (s *Server) foldRequest(token string, body []byte) ([]byte, foldResult, liveCompactGate) {
	out, result, _, gate := s.foldRequestLedger(token, body)
	return out, result, gate
}

// foldRequestLedger runs the fold on the request path. It returns the body to
// forward, the fold result, the ledger line still waiting for this turn's
// rewrite cost (nil when nothing was folded or no ledger is configured), and
// the rail decision (which the observation line logs so shadow can preview the
// same policy).
//
// Any error path returns the original body (I7).
func (s *Server) foldRequestLedger(token string, body []byte) ([]byte, foldResult, *foldLedgerPending, liveCompactGate) {
	cfg := s.liveCompact
	if !cfg.Observes() {
		return body, foldResult{}, nil, liveCompactGate{}
	}
	// Measured before folding: the rail compares like with like across turns,
	// and a post-fold estimate would shrink right after every event and mask
	// the next one.
	est := estimateByteTokens(body)
	growth := s.noteRequestEstimate(token, est)
	actual, forwardEst := s.sessionCalibration(token)
	fc := foldContext{est: est, growth: growth, actual: actual, forwardEst: forwardEst}
	measured := liveCompactGate{est: est, growth: growth}
	// The brake must stop the rewrite, not just the measurement. It is the only
	// way an operator can turn folding off without restarting the service, and
	// on this node the service restart is user-only.
	if s.liveCompactDisabled() {
		measured.decision = gateDisabled
		return body, foldResult{}, nil, measured
	}
	if !s.sessionFoldAllowed(token) {
		measured.decision = gateObserveOnly
		return body, foldResult{}, nil, measured
	}
	start := time.Now()
	req, err := scanWireRequest(body)
	if err != nil {
		logLiveCompactSkip(token, "scan")
		measured.decision = gateScanFailed
		return body, foldResult{}, nil, measured
	}
	// A rewrite is a client-side edit. On a preserved-thinking model that
	// invalidates every later thinking block, so the guard runs before anything
	// is planned — shadow included, since shadow must report the same policy
	// mode=on applies (design 2026-11-20 §4).
	if s.prefixGuardBlocks(token, req.model, cfg) {
		measured.decision = gatePrefixGuard
		s.noteForwardEstimate(token, est)
		return body, foldResult{}, nil, measured
	}
	// Peeked here, spent only once the planner reports it had candidates: the
	// request that follows a compaction often has nothing foldable, and the
	// signal describes the compaction, not this particular request (design
	// 2026-11-20 §5).
	fc.cold, fc.coldReason = s.peekColdFold(token, cfg, start)
	// Shadow (and on without an archive) rehearses the policy: it reports the
	// decision and the savings a fold would produce, but never rewrites the
	// body or writes an archive. This is what lets a 24h shadow rehearsal on a
	// live node gate mode=on admission (design §12.6 观测).
	if cfg.Mode != LiveCompactOn || s.archive == nil {
		_, gate := previewFold(body, req, cfg, s.sessionFoldCommits(token), fc)
		s.consumeColdFold(token, gate.candidates > 0)
		// Shadow forwards the body untouched: its estimate is the measured one.
		s.noteForwardEstimate(token, est)
		return body, foldResult{}, nil, gate
	}
	result, gate := planFoldCommit(body, req, cfg, s.archive, s.pointerCache,
		s.sessionFoldCommits(token), fc)
	s.consumeColdFold(token, gate.candidates > 0)
	gate.wouldFoldTokens = result.foldedTokens
	gate.wouldFoldBlocks = result.foldedBlocks
	if result.changed() {
		// Validate the rewrite before it is allowed onto the wire. This is the
		// one place where a scanner bug would reach the upstream, so the check
		// is cheap and structural: same message count, same tool pairing.
		if err := verifyFold(req, result.body); err != nil {
			logLiveCompactSkipDetails(token, "verify", err)
			s.noteForwardEstimate(token, est)
			return body, foldResult{}, nil, gate
		}
		// Sweep after committing, and protect what this node still points at.
		// Without the protection set, disk pressure would delete an archive
		// whose pointer is already inside the model's context; the block would
		// then silently unfold and the whole folded prefix would be resent.
		s.archive.SweepProtected(time.Now(), s.referencedArchives())
		elapsed := time.Since(start)
		logLiveCompactFold(token, result, gate.decision, elapsed)
		pending := s.newFoldLedgerPending(cfg.Mode, len(body), len(result.body), result, elapsed)
		forwarded := est - result.foldedTokens
		if forwarded < 0 {
			forwarded = 0
		}
		s.noteForwardEstimate(token, forwarded)
		return result.body, result, pending, gate
	}
	s.noteForwardEstimate(token, est)
	return body, result, nil, gate
}

// sessionFoldAllowed is the request-path read of the same flag the observation
// worker uses.
func (s *Server) sessionFoldAllowed(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.sessions[token]
	return ok && st.foldAllowed
}

// noteRequestEstimate records this request's pre-fold size and returns how much
// it grew since the previous one. The first request of a session returns zero:
// its whole size would otherwise look like growth.
func (s *Server) noteRequestEstimate(token string, est int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.sessions[token]
	if !ok {
		return 0
	}
	growth := 0
	if st.lastEstimate > 0 && est > st.lastEstimate {
		growth = est - st.lastEstimate
	}
	st.lastEstimate = est
	return growth
}

// sessionCalibration returns the previous request's upstream/estimator pair.
// Missing values (first request, failed usage scan) leave the rail on its
// estimator-only fallback.
func (s *Server) sessionCalibration(token string) (actual, forwardEst int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.sessions[token]
	if !ok {
		return 0, 0
	}
	return st.lastActual, st.lastForwardEst
}

// noteForwardEstimate records the estimator size of the body this request
// forwarded. It is one half of the calibration pair; the other half arrives
// with the response usage.
func (s *Server) noteForwardEstimate(token string, est int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.sessions[token]
	if !ok {
		return
	}
	st.lastForwardEst = est
}

// noteActualContext records the upstream-reported context size for the request
// that just finished, plus the two cache observations the cold-cache detector
// reads (design 2026-11-20 §5). Both an unseen usage block and a seen-but-zero
// count mean "no sample", and neither may overwrite a good size.
func (s *Server) noteActualContext(token string, totals usageTotals) {
	if !totals.Seen {
		return
	}
	actual := totals.ContextTokens()
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.sessions[token]
	if !ok {
		return
	}
	st.live.lastUsageAt = time.Now()
	if actual > 0 {
		st.live.lastContext = actual
		st.lastActual = actual
	}
	// Neither read nor write means this response was served without touching
	// the cache at all. One such response is an ordinary cold start; a streak is
	// an upstream that does not cache this session.
	if totals.CacheReadInputTokens == 0 && totals.CacheCreationInputTokens == 0 {
		st.live.noReadStreak++
	} else {
		st.live.noReadStreak = 0
	}
}

// sessionFoldCommits returns the session's commitment set, creating it on first
// use. In mode=on the set restores the session's persisted commitments, so a
// proxy restart does not unfold the prefix.
//
// Loading the commit log happens outside the server lock: it is a small file
// read, but it is still disk I/O and must not block other sessions' requests.
func (s *Server) sessionFoldCommits(token string) *foldCommits {
	s.mu.Lock()
	st, ok := s.sessions[token]
	if !ok {
		s.mu.Unlock()
		return nil
	}
	if st.commits != nil {
		commits := st.commits
		s.mu.Unlock()
		return commits
	}
	sessionKey := st.sessionKey
	s.mu.Unlock()

	commits := newFoldCommitsFor(s.commitLogFor(sessionKey))

	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok = s.sessions[token]
	if !ok {
		return nil
	}
	if st.commits == nil {
		st.commits = commits
	}
	return st.commits
}

// commitLogFor returns where this session's commitments are persisted, or nil
// when they must not be. Shadow rehearses the policy without folding, so it
// must not write commitments; and without an archive there is nothing for a
// pointer to reference.
func (s *Server) commitLogFor(sessionKey string) *commitLog {
	if s.liveCompact.Mode != LiveCompactOn || s.archive == nil {
		return nil
	}
	return newCommitLog(s.archive.Dir(), sessionKey)
}

// referencedArchives is the sweep protection set: every archive named by a
// live session's commitments. Commit logs on disk are added by the sweep
// itself, which knows the retention horizon.
func (s *Server) referencedArchives() map[string]struct{} {
	s.mu.Lock()
	sets := make([]*foldCommits, 0, len(s.sessions))
	for _, st := range s.sessions {
		if st.commits != nil {
			sets = append(sets, st.commits)
		}
	}
	s.mu.Unlock()

	out := make(map[string]struct{})
	for _, commits := range sets {
		for _, name := range commits.ProtectedNames() {
			out[name] = struct{}{}
		}
	}
	return out
}
