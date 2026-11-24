package fold

// The rehearsal path.
//
// Shadow mode has to answer one question on a live node: what would mode=on do
// with this request, and how much would it save? Answering it must not render a
// pointer, write an archive, or touch the body — a rehearsal that leaves side
// effects behind is not a rehearsal, and an archive written by shadow would make
// a later mode=on turn treat a block it never folded as already folded.
//
// It does commit the keys it would fold, though. Without that every request
// would look unfolded, the rail would fire on every turn, and the rehearsal
// would describe a policy nobody is going to run (I8).
//
// The arithmetic mirrors Engine.Fold's real path exactly, with one difference:
// sizes come from the scanned byte spans rather than the rendered pointers, so
// the min/max fold-budget edges can differ by the JSON escaping overhead. The
// decision itself depends only on estimated tokens and eligibility.

// preview applies the rail rules and reports what a fold would have done.
func (e *Engine) preview(req Request, candidates []Candidate, estimate Estimate, cold bool, coldReason string, out Outcome) Outcome {
	if len(candidates) == 0 {
		// Mirror the real path: report the rail arithmetic even with nothing to
		// fold, so a rehearsal's projection is the real projection.
		current := out.Estimated
		if estimate.Actual > 0 {
			current = estimate.Actual
		}
		_, decided := DecideBudget(e.cfg, estimate, cold, coldReason, current, false)
		if decided.Decision == GateFold {
			decided.Decision = GateNoBacklog
		}
		decided.Candidates = 0
		out.Projected = decided.Projected
		out.Verdict = Verdict{Block: true, Reason: decided.Decision}
		return out
	}

	scaleNum, scaleDen := estimate.Scale()
	used := make([]bool, len(candidates))
	stickyTokens := 0
	for index, candidate := range candidates {
		if !candidate.Committed {
			continue
		}
		used[index] = true
		out.StickyBlocks++
		out.FoldedBlocks++
		out.FoldedTokens += candidate.Result.Tokens
		stickyTokens += candidate.Result.Tokens
	}

	// Shadow forwards the unfolded body, so the upstream-reported context still
	// contains the blocks mode=on would have folded away. Subtract them, scaled
	// to upstream tokens, or the rehearsal would fire earlier than the real
	// planner and over-report fold events.
	current := out.Estimated - stickyTokens
	if estimate.Actual > 0 {
		current = estimate.Actual - stickyTokens*scaleNum/scaleDen
	}
	largeBlock := false
	for index, candidate := range candidates {
		if !used[index] && candidate.Result.Tokens >= e.cfg.LargeBlockTokens {
			largeBlock = true
			break
		}
	}
	budget, decided := DecideBudget(e.cfg, estimate, cold, coldReason, current, largeBlock)
	decided.Candidates = len(candidates)
	out.Projected = decided.Projected

	if budget.CommitNew {
		newBytes, newTokens, newBlocks := 0, 0, 0
		committed := make([]PointerCacheKey, 0, len(candidates))
		// Mirror the real path's chunked drain so a rehearsal reports the same
		// fold depth mode=on would produce.
		chunkBytes, chunks := 0, 1
		for index, candidate := range candidates {
			if used[index] {
				continue
			}
			width := candidate.Result.Parts[0].End - candidate.Result.Parts[0].Start
			if budget.MaxNewBytes > 0 && chunkBytes > 0 && chunkBytes+width > budget.MaxNewBytes {
				if chunks >= MaxChunksPerEvent {
					break
				}
				chunks++
				chunkBytes = 0
			}
			// The span width is the un-decoded upper bound on what a pointer can
			// save, so this is the real per-block floor decided before rendering.
			if width < FoldMinBlockSavings {
				continue
			}
			chunkBytes += width
			newBytes += width
			newTokens += candidate.Result.Tokens
			newBlocks++
			committed = append(committed, candidate.Key)
		}
		// A fold event too small to pay for a cache break commits nothing, and
		// its keys are not recorded either.
		if budget.MinNewBytes > 0 && newBytes < budget.MinNewBytes {
			decided.Decision = GateNoBacklog
		} else {
			for _, key := range committed {
				// Add, not CommitAll: a rehearsal must not persist a commitment,
				// or a later mode=on turn would skip the blocks it never folded.
				req.Session.Commits.Add(key)
			}
			out.FoldedTokens += newTokens
			out.FoldedBlocks += newBlocks
			out.NewBlocks = newBlocks
		}
	}

	decided.WouldFoldTokens = out.FoldedTokens
	decided.WouldFoldBlocks = out.FoldedBlocks
	out.Verdict = Verdict{Block: true, Reason: decided.Decision}
	return out
}
