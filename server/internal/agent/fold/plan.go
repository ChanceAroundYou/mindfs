package fold

// Candidate eligibility.
//
// The policy answers one question: which already-answered tool results may be
// replaced by a pointer in this request? The wire format is the adapter's
// problem; this file only knows rounds and results.

// FoldMinBlockSavings is how many bytes a pointer must save for its block to be
// worth committing. The event-level floor (min_fold_bytes) cannot do this job:
// it guards the cache break, while this guards the per-block decision to write
// an archive file and permanently replace a result with a pointer. A token
// floor used to sit here instead (min_block_tokens, 2000 ≈ 8 KB), and on real
// traffic it rejected most 1-7 KB tool results, so rail-triggered events found
// nothing to fold and the context kept growing into the CLI's own compaction.
const FoldMinBlockSavings = 1024

// MaxChunksPerEvent bounds how many max_fold_bytes chunks one fold event may
// drain. dsh drains up to 8 passes in one finishTurn (index.js:1938 loop);
// below 2 the per-event byte cap silently turns every event into a shallow
// fold, which design §12.7(b) measured as more expensive than not folding.
const MaxChunksPerEvent = 8

// Candidate is one result the planner is willing to fold.
type Candidate struct {
	Result Result
	// Key identifies the rendered pointer by content; Commits uses it to make
	// the fold boundary monotone.
	Key PointerCacheKey
	// Committed means the session already folded this block in an earlier
	// request. It is folded again unconditionally (I8).
	Committed bool
}

// PlanCandidates returns the eligible results in wire order.
//
// Rules, in order:
//
//   - Only closed rounds (a call with at least one answer) are considered; an
//     open round is still being awaited.
//   - Rounds inside the newest keep window are protected, bounded both by
//     round count and by token size (design §12.7(d)).
//   - A round holding a result at or above large_block_tokens is eligible even
//     inside the keep window: a single huge read is exactly the turn that
//     would otherwise blow the context window, and protecting it because it is
//     recent defeats the point. Every such round is carved out, not just the
//     newest — a committed block must stay a candidate on every later request
//     or its pointer would silently unfold.
//   - A result with anything but exactly one text part is skipped: replacing
//     several literals with one pointer would change the structure the model
//     sees. Non-text results are skipped whole (I3).
//   - A failed result below min_block_tokens is left verbatim: folding it saves
//     nothing and its text is often the whole diagnostic.
//
// The second return counts multi-part results that were skipped, which the
// caller reports as `skipped`.
func PlanCandidates(body []byte, conv *Conversation, cfg Config, commits *Commits) ([]Candidate, int) {
	if conv == nil {
		return nil, 0
	}
	closed := make([]Round, 0, len(conv.Rounds))
	for _, round := range conv.Rounds {
		if len(round.Results) > 0 {
			closed = append(closed, round)
		}
	}
	protectedFrom := len(closed) - cfg.KeepRounds
	if protectedFrom < 0 {
		protectedFrom = 0
	}
	// The protected tail is bounded by size as well as by round count (dsh's
	// keepRoundTokens). Rounds are dropped from the protected set oldest-first
	// until it fits; the newest round always stays verbatim, so one huge read
	// can still be cut by the oversized-block carve-out below.
	if cfg.KeepTailTokens > 0 {
		tailTokens := 0
		newest := len(closed) - 1
		for index := newest; index >= protectedFrom; index-- {
			tailTokens += roundTokens(closed[index])
			if tailTokens > cfg.KeepTailTokens && index < newest {
				protectedFrom = index + 1
				break
			}
		}
	}
	eligible := make([]Round, 0, len(closed))
	for index, round := range closed {
		if index < protectedFrom || roundOversized(round, cfg) {
			eligible = append(eligible, round)
		}
	}
	if len(eligible) == 0 {
		return nil, 0
	}

	skippedMultiPart := 0
	var candidates []Candidate
	for _, round := range eligible {
		for _, result := range round.Results {
			// A result that carries an image/document/unknown block keeps its
			// bytes (I3), even if it also has text.
			if result.NonText {
				continue
			}
			if len(result.Parts) != 1 {
				if len(result.Parts) > 1 {
					skippedMultiPart++
				}
				continue
			}
			if result.Failed && result.Tokens < cfg.MinBlockTokens {
				continue
			}
			variant := ""
			if result.Failed {
				variant = PointerVariantFailed
			}
			part := result.Parts[0]
			key := NewPointerCacheKeyVariant(result.ToolName, result.InputSummary, variant, body[part.Start:part.End])
			candidates = append(candidates, Candidate{
				Result:    result,
				Key:       key,
				Committed: commits.Has(key),
			})
		}
	}
	return candidates, skippedMultiPart
}

// roundTokens sums the estimated tokens of a round's foldable results. It
// decides how much of the protected tail this round costs, so it counts
// exactly what the keep window would otherwise be holding hostage.
func roundTokens(round Round) int {
	total := 0
	for _, result := range round.Results {
		if result.NonText || len(result.Parts) != 1 {
			continue
		}
		total += result.Tokens
	}
	return total
}

// roundOversized reports whether a round holds a foldable result at or above
// large_block_tokens. Only results the planner could actually fold count: a
// multi-part or non-text result is never a candidate, so exempting its round
// would drop protection for nothing. (A small failed result is measured here
// even though the candidate set would skip it; this mirrors the proxy's
// existing behaviour, and the cost is only that the round is exempted.)
func roundOversized(round Round, cfg Config) bool {
	// An unset threshold must not exempt everything: a caller that skips
	// Normalized would otherwise fold the round the model just read.
	if cfg.LargeBlockTokens <= 0 {
		return false
	}
	for _, result := range round.Results {
		if result.NonText || len(result.Parts) != 1 {
			continue
		}
		if result.Tokens >= cfg.LargeBlockTokens {
			return true
		}
	}
	return false
}
