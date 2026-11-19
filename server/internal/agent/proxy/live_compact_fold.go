package proxy

import (
	"os"
	"sort"
	"time"
)

// The fold planner.
//
// Everything here works on byte spans recorded by scanWireRequest. The rewrite
// copies the body once and splices replacement string literals into the spans
// that were folded; every other byte — including the JSON key order, number
// formatting, and escape choices of blocks we do not touch — is carried over
// verbatim. That makes invariants I1/I2/I4/I6 true by construction instead of
// by careful re-encoding:
//
//	I1 message count/order/role unchanged  — one body, only literals spliced
//	I2 tool pairing unchanged              — only the text of a tool_result
//	                                          is replaced; ids never move
//	I3 non-text blocks untouched           — such results are skipped whole
//	I4 non-folded blocks identical         — their bytes are copied
//	I6 prefix before the first fold        — bytes before it are copied
//	I8 committed folds never revert        — see foldCommits
//	I9 below the rail, nothing changes     — see decideFoldBudget
//
// I5 (same input → same bytes) holds because the pointer is a pure function of
// tool name, input, folded text, and the content-addressed archive path.

// foldReplacement is one splice: replace body[start:end] with literal.
type foldReplacement struct {
	start   int
	end     int
	literal string
}

// foldMaxChunksPerEvent bounds how many max_fold_bytes chunks one fold event
// may drain. dsh drains up to 8 passes in one finishTurn (index.js:1938 loop);
// below 2 the per-event byte cap silently turns every event into a shallow fold,
// which design §12.7(b) measured as more expensive than not folding at all.
const foldMaxChunksPerEvent = 8

type foldResult struct {
	// body is the rewritten request, or nil when nothing was folded.
	body []byte
	// foldedBlocks, foldedTokens, foldedBytes describe what was replaced. They
	// count sticky and newly committed blocks together: they describe the
	// forwarded body, not this request's decisions.
	foldedBlocks int
	foldedTokens int
	foldedBytes  int
	// stickyBlocks were folded because the session had already committed to
	// them; newBlocks were committed by this request. Everything else is
	// unchanged. Design §12.6.
	stickyBlocks int
	newBlocks    int
	// skipped counts candidates that were eligible but not folded (archive
	// failure, multi-part content, decode failure, pointer not smaller).
	skipped int
	// archiveFailures counts blocks left alone because the archive write
	// failed. Non-zero means folding is degraded, not that the request is
	// wrong.
	archiveFailures int
}

func (r foldResult) changed() bool { return len(r.body) > 0 }

type scanToolRound struct {
	assistantIndex int
	names          map[string]string
	inputs         map[string]span
	resultIndexes  []int
	pending        int
	closed         bool
}

// scanFoldCandidate is one eligible tool_result, resolved down to the byte span
// that would be replaced. Building these is cheap (a hash and a byte count per
// block); decoding the text only happens for a block that is about to be
// folded.
type scanFoldCandidate struct {
	messageIndex int
	blockIndex   int
	part         span
	toolName     string
	inputSummary string
	key          pointerCacheKey
	approxTokens int
}

// renderedFold is the replacement for one candidate plus the numbers the log
// and the ledger report.
type renderedFold struct {
	replacement foldReplacement
	tokens      int
	textBytes   int
}

type foldSkip int

const (
	foldOK foldSkip = iota
	foldSkipDecode
	foldSkipArchive
	foldSkipTooBig
)

// pairScanRounds mirrors pairToolRounds for the scanned representation. The two
// exist separately because one works on decoded values and the other on spans;
// keeping them independent means a scanner bug cannot silently change what
// Phase A reports, and vice versa.
func pairScanRounds(messages []scanMessage) []*scanToolRound {
	rounds := make([]*scanToolRound, 0, 8)
	byToolID := make(map[string]*scanToolRound)
	for index, msg := range messages {
		if msg.role == "assistant" {
			var round *scanToolRound
			for _, block := range msg.blocks {
				if block.typ != "tool_use" || block.id == "" {
					continue
				}
				if round == nil {
					round = &scanToolRound{
						assistantIndex: index,
						names:          make(map[string]string),
						inputs:         make(map[string]span),
					}
				}
				round.names[block.id] = block.name
				round.inputs[block.id] = block.input
				round.pending++
				byToolID[block.id] = round
			}
			if round != nil {
				rounds = append(rounds, round)
			}
			continue
		}
		if msg.role != "user" {
			continue
		}
		seen := make(map[int]bool)
		for _, block := range msg.blocks {
			if block.typ != "tool_result" {
				continue
			}
			round, ok := byToolID[block.toolUseID]
			if !ok || round.closed {
				continue
			}
			if !seen[round.assistantIndex] {
				round.resultIndexes = append(round.resultIndexes, index)
				seen[round.assistantIndex] = true
			}
			round.pending--
			if round.pending <= 0 {
				round.closed = true
			}
		}
	}
	return rounds
}

// collectFoldCandidates returns the eligible results in wire order: closed
// rounds outside the newest keepRounds rounds, never a failed result, only pure
// text, exactly one text part.
//
// One carve-out: rounds holding a result at or above large_block_tokens are
// eligible even inside the keep window. dsh does the same (index.js:650
// longSpan, plan.js:519 cutLongReadRound) — a single huge read is exactly the
// turn that would otherwise blow the context window, so protecting it because
// it is recent defeats the point.
func collectFoldCandidates(body []byte, req *scanRequest, cfg LiveCompactConfig) ([]scanFoldCandidate, int) {
	rounds := pairScanRounds(req.messages)
	closed := make([]*scanToolRound, 0, len(rounds))
	for _, round := range rounds {
		if round.closed {
			closed = append(closed, round)
		}
	}
	protectedFrom := len(closed) - cfg.KeepRounds
	if protectedFrom < 0 {
		protectedFrom = 0
	}
	// The protected tail is bounded by size as well as by round count (design
	// §12.7(d), dsh's keepRoundTokens). Rounds are dropped from the protected
	// set oldest-first until it fits; the newest round always stays verbatim, so
	// one huge read can still be cut by the oversized-block carve-out below.
	if cfg.KeepTailTokens > 0 {
		tailTokens := 0
		newest := len(closed) - 1
		for index := newest; index >= protectedFrom; index-- {
			tailTokens += roundResultTokens(body, req, closed[index])
			if tailTokens > cfg.KeepTailTokens && index < newest {
				protectedFrom = index + 1
				break
			}
		}
	}
	// Every oversized round is eligible, not just the newest one: once a block
	// is committed it must stay a candidate on every later request (I8), and a
	// round that was carved out below the keep window would otherwise slide
	// back into it when newer rounds arrive and its pointer would silently
	// unfold. dsh reaches the same end state because it rewrites the session
	// itself; we recompute from the original transcript every turn.
	eligible := make([]*scanToolRound, 0, len(closed))
	for index, round := range closed {
		if index < protectedFrom || roundHasOversizedResult(body, req, round, cfg) {
			eligible = append(eligible, round)
		}
	}
	if len(eligible) == 0 {
		return nil, 0
	}

	skippedMultiPart := 0
	var candidates []scanFoldCandidate
	for _, round := range eligible {
		for _, messageIndex := range round.resultIndexes {
			msg := req.messages[messageIndex]
			for blockIndex, block := range msg.blocks {
				if block.typ != "tool_result" {
					continue
				}
				// A result that carries an image/document/unknown block keeps
				// its bytes (I3), even if it also has text.
				if block.nonText || block.isError {
					continue
				}
				// Replacing several text blocks with one pointer would change
				// the block structure of the result. Folding only single-text
				// results keeps the wire shape identical; the remaining ones
				// stay readable at full size.
				if len(block.textParts) != 1 {
					if len(block.textParts) > 1 {
						skippedMultiPart++
					}
					continue
				}
				part := block.textParts[0]
				toolName := round.names[block.toolUseID]
				inputSummary := summarizeToolInput(body, round.inputs[block.toolUseID])
				candidates = append(candidates, scanFoldCandidate{
					messageIndex: messageIndex,
					blockIndex:   blockIndex,
					part:         part,
					toolName:     toolName,
					inputSummary: inputSummary,
					// Key on the raw (still escaped) span: the pointer is a pure
					// function of this content, and hashing the escaped form
					// avoids the decode this cache exists to skip.
					key: pointerCacheKeyFor(toolName, inputSummary, body[part.start:part.end]),
					// A byte-based estimate is close enough to decide whether a
					// block is oversized; the exact count is computed after the
					// decode that a fold needs anyway.
					approxTokens: estimateByteTokens(body[part.start:part.end]),
				})
			}
		}
	}
	return candidates, skippedMultiPart
}

// roundResultTokens sums the estimated tokens of a round's foldable results.
// It decides how much of the protected tail this round costs (design §12.7(d)),
// so it counts exactly what the keep window would otherwise be holding hostage.
func roundResultTokens(body []byte, req *scanRequest, round *scanToolRound) int {
	total := 0
	for _, messageIndex := range round.resultIndexes {
		for _, block := range req.messages[messageIndex].blocks {
			if block.typ != "tool_result" || block.nonText || block.isError || len(block.textParts) != 1 {
				continue
			}
			part := block.textParts[0]
			total += estimateByteTokens(body[part.start:part.end])
		}
	}
	return total
}

// roundHasOversizedResult reports whether a closed round holds a foldable
// result at or above large_block_tokens. Only results this planner could
// actually fold count: a multi-part or error result is never a candidate, so
// exempting its round would drop protection for nothing.
func roundHasOversizedResult(body []byte, req *scanRequest, round *scanToolRound, cfg LiveCompactConfig) bool {
	// An unset threshold must not exempt everything: a caller that skips
	// Normalized would otherwise fold the round the model just read.
	if cfg.LargeBlockTokens <= 0 {
		return false
	}
	for _, messageIndex := range round.resultIndexes {
		for _, block := range req.messages[messageIndex].blocks {
			if block.typ != "tool_result" || block.nonText || block.isError || len(block.textParts) != 1 {
				continue
			}
			part := block.textParts[0]
			if estimateByteTokens(body[part.start:part.end]) >= cfg.LargeBlockTokens {
				return true
			}
		}
	}
	return false
}

// renderPointer produces the pointer for one candidate, reusing the archive and
// the rendered literal when they are still valid.
func renderPointer(body []byte, candidate scanFoldCandidate, store *archiveStore, cache *pointerCache) (renderedFold, foldSkip) {
	spanLen := candidate.part.end - candidate.part.start
	if entry, hit := cache.get(candidate.key); hit {
		// The sweep can have removed the archive since the entry was written. A
		// pointer to a missing file is worse than a full-size result, so verify
		// before reusing it.
		if info, err := os.Stat(entry.path); err == nil && info.Size() == int64(entry.bytes) && len(entry.literal) < spanLen {
			return renderedFold{
				replacement: foldReplacement{start: candidate.part.start, end: candidate.part.end, literal: entry.literal},
				tokens:      entry.tokens,
				textBytes:   entry.bytes,
			}, foldOK
		}
	}
	text, err := jsonStringAt(body, candidate.part.start, candidate.part.end)
	if err != nil || text == "" {
		return renderedFold{}, foldSkipDecode
	}
	tokens := estimateTokens(text)
	archivePath, err := store.write(text)
	if err != nil {
		return renderedFold{}, foldSkipArchive
	}
	literal := encodeJSONStringLiteral(renderFoldPointer(pointerInput{
		toolName:     candidate.toolName,
		inputSummary: candidate.inputSummary,
		text:         text,
		tokens:       tokens,
		archivePath:  archivePath,
		errorText:    looksLikeErrorText(text),
	}))
	// A fold that is not smaller is not a fold. This is the backstop for the
	// excerpt bound above: whatever the reason, a pointer that costs more bytes
	// than the result it replaces would make every subsequent turn bigger,
	// permanently.
	if len(literal) >= spanLen {
		return renderedFold{}, foldSkipTooBig
	}
	cache.put(candidate.key, cachedPointer{
		literal: literal,
		path:    archivePath,
		tokens:  tokens,
		bytes:   len(text),
	})
	return renderedFold{
		replacement: foldReplacement{start: candidate.part.start, end: candidate.part.end, literal: literal},
		tokens:      tokens,
		textBytes:   len(text),
	}, foldOK
}

// planFold is the Phase B1 planner: commit every eligible block. It is kept
// for tests and for callers that have no request-size measurement; foldRequest
// uses planFoldCommit with the rail gate.
func planFold(body []byte, req *scanRequest, cfg LiveCompactConfig, store *archiveStore, cache *pointerCache) foldResult {
	result, _ := planFoldCommit(body, req, cfg, store, cache, nil, foldContext{})
	return result
}

// planFoldCommit plans a fold under the §12.6 policy.
//
// Two passes: first the session's committed blocks (always folded, invariant
// I8), then — only when the rail fired — new blocks, oldest first, until the
// projection is under target_tokens or the event budget is spent.
func planFoldCommit(body []byte, req *scanRequest, cfg LiveCompactConfig, store *archiveStore, cache *pointerCache, commits *foldCommits, fc foldContext) (foldResult, liveCompactGate) {
	var result foldResult
	candidates, multiPart := collectFoldCandidates(body, req, cfg)
	result.skipped += multiPart
	gate := liveCompactGate{decision: gateBelowRail, est: fc.est, growth: fc.growth}
	if fc.est <= 0 {
		gate.decision = gateUnmeasured
	}
	if len(candidates) == 0 {
		return result, gate
	}

	var replacements []foldReplacement
	used := make([]bool, len(candidates))
	stickyTokens := 0

	// Pass 1: committed blocks are folded unconditionally. This is what makes
	// the fold boundary monotone across turns.
	for index, candidate := range candidates {
		if !commits.has(candidate.key) {
			continue
		}
		rendered, outcome := renderPointer(body, candidate, store, cache)
		if outcome != foldOK {
			// Cannot render a pointer to a missing or broken archive. Leaving
			// the block unfolded is the only safe choice here; it is a disk
			// failure, not a policy decision.
			result.skipped++
			if outcome == foldSkipArchive {
				result.archiveFailures++
			}
			continue
		}
		used[index] = true
		replacements = append(replacements, rendered.replacement)
		result.stickyBlocks++
		result.foldedBlocks++
		result.foldedTokens += rendered.tokens
		result.foldedBytes += rendered.textBytes
		stickyTokens += rendered.tokens
	}

	// Pass 2: decide whether this request may commit new blocks. Upstream
	// calibration already reflects the sticky blocks (the previous request was
	// folded before it was sent), so the estimator subtraction only applies on
	// the fallback path.
	current := fc.est - stickyTokens
	if fc.actual > 0 {
		current = fc.actual
	}
	scaleNum, scaleDen := fc.actualScale()
	largeBlock := false
	for index, candidate := range candidates {
		if used[index] {
			continue
		}
		if candidate.approxTokens >= cfg.LargeBlockTokens {
			largeBlock = true
			break
		}
	}
	budget, decided := decideFoldBudget(cfg, fc, current, largeBlock)
	gate = decided

	if budget.commitNew {
		var newReplacements []foldReplacement
		var newKeys []pointerCacheKey
		newBytes, newTokens := 0, 0
		// Chunked drain (design §12.7(d)): max_fold_bytes bounds one chunk, and
		// one event may drain up to foldMaxChunksPerEvent of them. A single
		// global cap would end the event after ~2 blocks and leave the context
		// shallow-folded — the shape §12.7(b) measured as strictly worse than
		// not folding. The chunk cap still bounds the work and the latency of
		// one request; the chunk count bounds the whole event.
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
			rendered, outcome := renderPointer(body, candidate, store, cache)
			if outcome != foldOK {
				result.skipped++
				if outcome == foldSkipArchive {
					result.archiveFailures++
				}
				continue
			}
			if rendered.tokens < cfg.MinBlockTokens {
				continue
			}
			newReplacements = append(newReplacements, rendered.replacement)
			newKeys = append(newKeys, candidate.key)
			chunkBytes += width
			newBytes += rendered.textBytes
			newTokens += rendered.tokens
			if fc.est > 0 && current+fc.growth*scaleNum/scaleDen-newTokens*scaleNum/scaleDen <= cfg.TargetTokens {
				break
			}
		}
		// A fold event that is too small does not justify a cache break. Drop
		// the whole new set; never go back to accumulate more.
		if budget.minNewBytes > 0 && newBytes < budget.minNewBytes {
			gate.decision = gateNoBacklog
		} else {
			for index := range newReplacements {
				commits.add(newKeys[index])
				replacements = append(replacements, newReplacements[index])
			}
			result.newBlocks += len(newReplacements)
			result.foldedBlocks += len(newReplacements)
			result.foldedTokens += newTokens
			result.foldedBytes += newBytes
		}
	}

	if len(replacements) == 0 {
		return result, gate
	}
	sort.Slice(replacements, func(i, j int) bool { return replacements[i].start < replacements[j].start })
	rewritten, ok := applyFold(body, replacements)
	if !ok {
		// Overlapping or out-of-range spans mean the scan disagreed with
		// itself. Fail open: forward the original body (I7). Commits already
		// recorded in this call stay recorded; the next request will render
		// them again, so the only cost is one unfolded turn.
		result.skipped += result.foldedBlocks
		result.foldedBlocks, result.foldedTokens, result.foldedBytes = 0, 0, 0
		result.stickyBlocks, result.newBlocks = 0, 0
		return result, gate
	}
	result.body = rewritten
	return result, gate
}

// foldRequest runs the fold on the request path. It returns the body to
// forward, the fold result, and the rail decision (which the observation line
// logs so shadow can preview the same policy).
//
// Any error path returns the original body (I7).
func (s *Server) foldRequest(token string, body []byte) ([]byte, foldResult, liveCompactGate) {
	cfg := s.liveCompact
	if !cfg.Observes() {
		return body, foldResult{}, liveCompactGate{}
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
		return body, foldResult{}, measured
	}
	if !s.sessionFoldAllowed(token) {
		measured.decision = gateObserveOnly
		return body, foldResult{}, measured
	}
	start := time.Now()
	req, err := scanWireRequest(body)
	if err != nil {
		logLiveCompactSkip(token, "scan")
		measured.decision = gateScanFailed
		return body, foldResult{}, measured
	}
	// Shadow (and on without an archive) rehearses the policy: it reports the
	// decision and the savings a fold would produce, but never rewrites the
	// body or writes an archive. This is what lets a 24h shadow rehearsal on a
	// live node gate mode=on admission (design §12.6 观测).
	if cfg.Mode != LiveCompactOn || s.archive == nil {
		_, gate := previewFold(body, req, cfg, s.sessionFoldCommits(token), fc)
		// Shadow forwards the body untouched: its estimate is the measured one.
		s.noteForwardEstimate(token, est)
		return body, foldResult{}, gate
	}
	result, gate := planFoldCommit(body, req, cfg, s.archive, s.pointerCache,
		s.sessionFoldCommits(token), fc)
	gate.wouldFoldTokens = result.foldedTokens
	gate.wouldFoldBlocks = result.foldedBlocks
	if result.changed() {
		// Validate the rewrite before it is allowed onto the wire. This is the
		// one place where a scanner bug would reach the upstream, so the check
		// is cheap and structural: same message count, same tool pairing.
		if err := verifyFold(req, result.body); err != nil {
			logLiveCompactSkipDetails(token, "verify", err)
			s.noteForwardEstimate(token, est)
			return body, foldResult{}, gate
		}
		s.archive.sweep(time.Now())
		elapsed := time.Since(start)
		logLiveCompactFold(token, result, gate.decision, elapsed)
		s.recordFoldLedger(token, cfg.Mode, len(body), len(result.body), result, elapsed)
		forwarded := est - result.foldedTokens
		if forwarded < 0 {
			forwarded = 0
		}
		s.noteForwardEstimate(token, forwarded)
		return result.body, result, gate
	}
	s.noteForwardEstimate(token, est)
	return body, result, gate
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
// that just finished. Both an unseen usage block and a seen-but-zero count mean
// "no sample", and neither may overwrite a good one.
func (s *Server) noteActualContext(token string, totals usageTotals) {
	actual := totals.ContextTokens()
	if !totals.Seen || actual <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.sessions[token]; ok {
		st.lastActual = actual
	}
}

// sessionFoldCommits returns the session's commitment set, creating it on first
// use.
func (s *Server) sessionFoldCommits(token string) *foldCommits {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.sessions[token]
	if !ok {
		return nil
	}
	if st.commits == nil {
		st.commits = newFoldCommits()
	}
	return st.commits
}
