package proxy

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
	// failed marks a tool_result with is_error=true. It is foldable once it
	// clears min_block_tokens, and its card says the result failed.
	failed bool
}

// renderedFold is the replacement for one candidate plus the numbers the log
// and the ledger report.
type renderedFold struct {
	replacement foldReplacement
	tokens      int
	textBytes   int
	// path is the archive file backing this pointer. Its base name is the
	// block's stable identity across restarts (see foldCommitRecord).
	path string
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
// rounds outside the newest keepRounds rounds, only pure text, exactly one text
// part. A failed (is_error) result is foldable once it clears min_block_tokens:
// a huge error dump is exactly what blows the context window, and the pointer
// keeps a 20/20-line excerpt plus the archive path.
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
				if block.nonText {
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
				approxTokens := estimateByteTokens(body[part.start:part.end])
				// A small failed result stays verbatim: folding it saves
				// nothing, and its text is often the whole diagnostic.
				if block.isError && approxTokens < cfg.MinBlockTokens {
					continue
				}
				toolName := round.names[block.toolUseID]
				inputSummary := summarizeToolInput(body, round.inputs[block.toolUseID])
				variant := ""
				if block.isError {
					variant = pointerVariantFailed
				}
				candidates = append(candidates, scanFoldCandidate{
					messageIndex: messageIndex,
					blockIndex:   blockIndex,
					part:         part,
					toolName:     toolName,
					inputSummary: inputSummary,
					// Key on the raw (still escaped) span: the pointer is a pure
					// function of this content, and hashing the escaped form
					// avoids the decode this cache exists to skip.
					key: pointerCacheKeyForVariant(toolName, inputSummary, variant, body[part.start:part.end]),
					// A byte-based estimate is close enough to decide whether a
					// block is oversized; the exact count is computed after the
					// decode that a fold needs anyway.
					approxTokens: approxTokens,
					failed:       block.isError,
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
			if block.typ != "tool_result" || block.nonText || len(block.textParts) != 1 {
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
// actually fold count: a multi-part or non-text result is never a candidate, so
// exempting its round would drop protection for nothing. A failed result counts
// once it clears min_block_tokens, the same bar the candidate set uses.
func roundHasOversizedResult(body []byte, req *scanRequest, round *scanToolRound, cfg LiveCompactConfig) bool {
	// An unset threshold must not exempt everything: a caller that skips
	// Normalized would otherwise fold the round the model just read.
	if cfg.LargeBlockTokens <= 0 {
		return false
	}
	for _, messageIndex := range round.resultIndexes {
		for _, block := range req.messages[messageIndex].blocks {
			if block.typ != "tool_result" || block.nonText || len(block.textParts) != 1 {
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
