package proxy

import (
	"path/filepath"
	"sort"
)

// The fold planner: which candidates become replacements this request.
// Split out of live_compact_fold.go when that file crossed the 500-line hard
// limit; pure move, no behaviour change.

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
	//
	// Committed in an earlier process are not in the key set yet: their keys are
	// maphash values and cannot be persisted, so they are matched here by
	// archive name, which costs one render (cached from then on). Only the first
	// fold after a restart pays for it.
	restoring := commits.restoredCount() > 0
	for index, candidate := range candidates {
		committed := commits.has(candidate.key)
		if !committed && !restoring {
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
		if !committed && !commits.adopt(candidate.key, filepath.Base(rendered.path)) {
			// A restored name that does not match: leave it to pass 2 like any
			// other uncommitted candidate.
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
	if restoring {
		// Drop whatever could not be matched: those blocks are no longer in this
		// session's transcript. Keeping them would make every later request pay
		// for renders that can never match.
		commits.dropUnrestored()
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
		var newPaths []string
		var newSizes []int
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
			newPaths = append(newPaths, filepath.Base(rendered.path))
			newSizes = append(newSizes, rendered.textBytes)
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
			commits.commitAll(newKeys, newPaths, newSizes)
			replacements = append(replacements, newReplacements...)
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
