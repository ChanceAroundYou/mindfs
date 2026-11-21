package proxy

import (
	"time"

	"mindfs/server/internal/agent/fold"
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
// The policy itself lives in the fold kernel; this function owns only what the
// kernel cannot know: the emergency brake, the session's fold permission, the
// per-session calibration pair, and the proxy-side session state the engine
// reads through fold.Session.
//
// Any error path returns the original body (I7).
func (s *Server) foldRequestLedger(token string, body []byte) ([]byte, foldResult, *foldLedgerPending, liveCompactGate) {
	return s.foldRequestLedgerFor(token, adapterAnthropic, body)
}

// foldRequestLedgerFor is foldRequestLedger for one wire format: the policy is
// the same for every harness, only the adapter that parses and splices differs.
func (s *Server) foldRequestLedgerFor(token, adapter string, body []byte) ([]byte, foldResult, *foldLedgerPending, liveCompactGate) {
	harness := harnessForAdapter(adapter)
	rt := s.harnessRuntimeFor(harness)
	cfg := rt.cfg
	if !cfg.Observes() {
		return body, foldResult{}, nil, liveCompactGate{}
	}
	// Measured before folding: the rail compares like with like across turns,
	// and a post-fold estimate would shrink right after every event and mask
	// the next one.
	est := estimateByteTokens(body)
	previous := s.previousEstimate(token)
	growth := s.noteRequestEstimate(token, est)
	actual, forwardEst := s.sessionCalibration(token)
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
	// Only mode=on with an archive may rewrite. Everything else rehearses the
	// same policy: shadow reports the decision and the savings a fold would
	// produce without touching a byte (design §12.6 观测).
	allowed := cfg.Mode == LiveCompactOn && rt.archive != nil
	session := s.foldSession(token)
	out, err := rt.engine.Fold(fold.Request{
		Body:           body,
		Session:        session,
		Now:            start,
		FoldAllowed:    allowed,
		Preview:        !allowed,
		LastEstimate:   previous,
		LastActual:     actual,
		LastForwardEst: forwardEst,
	})
	// The engine spends the PostCompact signal and may flip the alias warning;
	// both live on the proxy's session state.
	s.applyFoldSession(token, session)
	if err != nil {
		if out.Verdict.Reason == fold.GateScanFailed {
			logLiveCompactSkip(token, "scan")
		} else {
			logLiveCompactSkipDetails(token, "fold", err)
		}
		s.noteForwardEstimate(token, est)
		measured.decision = out.Verdict.Reason
		return body, foldResult{}, nil, measured
	}
	if out.GuardUnknownModel {
		s.warnUnknownModelOnce(token, out.Model)
	}
	gate := gateFromOutcome(out, growth)
	if !allowed {
		// Shadow forwards the body untouched: its estimate is the measured one.
		s.noteForwardEstimate(token, est)
		return body, foldResult{}, nil, gate
	}
	result := foldResultFromOutcome(out)
	if out.Folded {
		logLiveCompactFold(token, result, gate.decision, time.Since(start))
		pending := s.newFoldLedgerPending(harness, cfg.Mode, len(body), len(out.Body), est, estimateByteTokens(out.Body), result, time.Since(start))
		// Sweep after committing, and protect what this node still points at.
		// Without the protection set, disk pressure would delete an archive
		// whose pointer is already inside the model's context; the block would
		// then silently unfold and the whole folded prefix would be resent.
		rt.archive.SweepProtected(time.Now(), s.referencedArchives())
		forwarded := est - result.foldedTokens
		if forwarded < 0 {
			forwarded = 0
		}
		s.noteForwardEstimate(token, forwarded)
		return out.Body, result, pending, gate
	}
	s.noteForwardEstimate(token, est)
	return body, result, nil, gate
}

// foldEngineFor returns one harness's engine. An engine holds its adapter and
// its configuration, and the adapter is the only protocol-aware part; the two
// transports reach it through different routes — Claude Code's
// `/p/{token}/...` (Messages) and Codex's `/c/{provider}/...` (Responses).
func (s *Server) foldEngineFor(harness string) *fold.Engine {
	return s.harnessRuntimeFor(harness).engine
}

// previousEstimate returns the previous request's pre-fold size. It must be
// read before noteRequestEstimate records this request, so the engine sees the
// same growth the observation line reports.
func (s *Server) previousEstimate(token string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.sessions[token]
	if !ok {
		return 0
	}
	return st.lastEstimate
}

// foldSession builds the kernel's view of this session's fold state. The
// commitment set is shared by pointer; the cold signals are copied in and the
// two the engine may change are written back by applyFoldSession.
func (s *Server) foldSession(token string) *fold.Session {
	commits := s.sessionFoldCommits(token)
	if commits == nil {
		commits = fold.NewCommits()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.sessions[token]
	if !ok {
		return &fold.Session{Commits: commits}
	}
	return &fold.Session{
		Commits:         commits,
		PendingColdFold: st.live.pendingColdFold,
		CompactedAt:     st.live.pendingColdFoldAt,
		LastUsageAt:     st.live.lastUsageAt,
		NoReadStreak:    st.live.noReadStreak,
		LastContext:     st.live.lastContext,
		PrefixWarned:    st.live.prefixWarned,
	}
}

// applyFoldSession writes back the state the engine owns decisions for.
func (s *Server) applyFoldSession(token string, session *fold.Session) {
	if session == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.sessions[token]
	if !ok {
		return
	}
	st.live.pendingColdFold = session.PendingColdFold
	st.live.prefixWarned = session.PrefixWarned
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
	harness := st.harness
	s.mu.Unlock()

	commits := newFoldCommitsFor(s.commitLogFor(harness, sessionKey))

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
func (s *Server) commitLogFor(harness, sessionKey string) *commitLog {
	rt := s.harnessRuntimeFor(harness)
	if rt.cfg.Mode != LiveCompactOn || rt.archive == nil {
		return nil
	}
	return newCommitLog(rt.archive.Dir(), sessionKey)
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
