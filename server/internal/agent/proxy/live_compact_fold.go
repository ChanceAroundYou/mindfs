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
//
// I5 (same input → same bytes) holds because the pointer is a pure function of
// tool name, input, folded text, and the content-addressed archive path.

// foldReplacement is one splice: replace body[start:end] with literal.
type foldReplacement struct {
	start   int
	end     int
	literal string
}

type foldResult struct {
	// body is the rewritten request, or nil when nothing was folded.
	body []byte
	// foldedBlocks, foldedTokens, foldedBytes describe what was replaced.
	foldedBlocks int
	foldedTokens int
	foldedBytes  int
	// skipped counts candidates that were eligible but not folded (archive
	// failure, multi-part content, decode failure).
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

// planFold selects the blocks to fold and renders their pointers. It writes
// archives as a side effect; a block whose archive cannot be written is left
// unfolded rather than pointed at a missing file.
//
// The selection rules are design §5.3: only closed rounds outside the newest
// keepRounds rounds, never a failed result, only pure text, only results at or
// above minBlockTokens.
func planFold(body []byte, req *scanRequest, cfg LiveCompactConfig, store *archiveStore, cache *pointerCache) foldResult {
	rounds := pairScanRounds(req.messages)
	closed := make([]*scanToolRound, 0, len(rounds))
	for _, round := range rounds {
		if round.closed {
			closed = append(closed, round)
		}
	}
	if len(closed) <= cfg.KeepRounds {
		return foldResult{}
	}
	eligible := closed[:len(closed)-cfg.KeepRounds]

	var result foldResult
	var replacements []foldReplacement
	for _, round := range eligible {
		for _, messageIndex := range round.resultIndexes {
			msg := req.messages[messageIndex]
			for _, block := range msg.blocks {
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
						result.skipped++
					}
					continue
				}
				part := block.textParts[0]
				toolName := round.names[block.toolUseID]
				inputSummary := summarizeToolInput(body, round.inputs[block.toolUseID])
				// Key on the raw (still escaped) span: the pointer is a pure
				// function of this content, and hashing the escaped form avoids
				// the decode this cache exists to skip.
				key := pointerCacheKeyFor(toolName, inputSummary, body[part.start:part.end])
				entry, hit := cache.get(key)
				if hit {
					// The sweep can have removed the archive since the entry was
					// written. A pointer to a missing file is worse than a
					// full-size result, so verify before reusing it.
					if info, err := os.Stat(entry.path); err == nil && info.Size() == int64(entry.bytes) && len(entry.literal) < part.end-part.start {
						replacements = append(replacements, foldReplacement{
							start: part.start, end: part.end, literal: entry.literal,
						})
						result.foldedBlocks++
						result.foldedTokens += entry.tokens
						result.foldedBytes += entry.bytes
						continue
					}
				}
				text, err := jsonStringAt(body, part.start, part.end)
				if err != nil || text == "" {
					result.skipped++
					continue
				}
				tokens := estimateTokens(text)
				if tokens < cfg.MinBlockTokens {
					continue
				}
				archivePath, err := store.write(text)
				if err != nil {
					result.skipped++
					result.archiveFailures++
					continue
				}
				literal := encodeJSONStringLiteral(renderFoldPointer(pointerInput{
					toolName:     toolName,
					inputSummary: inputSummary,
					text:         text,
					tokens:       tokens,
					archivePath:  archivePath,
					errorText:    looksLikeErrorText(text),
				}))
				// A fold that is not smaller is not a fold. This is the
				// backstop for the excerpt bound above: whatever the reason,
				// a pointer that costs more bytes than the result it replaces
				// would make every subsequent turn bigger, permanently.
				if len(literal) >= part.end-part.start {
					result.skipped++
					continue
				}
				cache.put(key, cachedPointer{
					literal: literal,
					path:    archivePath,
					tokens:  tokens,
					bytes:   len(text),
				})
				replacements = append(replacements, foldReplacement{
					start:   part.start,
					end:     part.end,
					literal: literal,
				})
				result.foldedBlocks++
				result.foldedTokens += tokens
				result.foldedBytes += len(text)
			}
		}
	}
	if len(replacements) == 0 {
		return result
	}
	sort.Slice(replacements, func(i, j int) bool { return replacements[i].start < replacements[j].start })
	rewritten, ok := applyFold(body, replacements)
	if !ok {
		// Overlapping or out-of-range spans mean the scan disagreed with
		// itself. Fail open: forward the original body (I7).
		result.skipped += result.foldedBlocks
		result.foldedBlocks, result.foldedTokens, result.foldedBytes = 0, 0, 0
		return result
	}
	result.body = rewritten
	return result
}

// foldRequest runs the fold on the request path for mode=on sessions. It
// returns the body to forward: the rewritten one when something was folded,
// otherwise the original slice. Any error path returns the original body (I7).
func (s *Server) foldRequest(token string, body []byte) ([]byte, foldResult) {
	cfg := s.liveCompact
	if cfg.Mode != LiveCompactOn || s.archive == nil {
		return body, foldResult{}
	}
	// The brake must stop the rewrite, not just the measurement. It is the only
	// way an operator can turn folding off without restarting the service, and
	// on this node the service restart is user-only.
	if s.liveCompactDisabled() {
		return body, foldResult{}
	}
	if !s.sessionFoldAllowed(token) {
		return body, foldResult{}
	}
	start := time.Now()
	req, err := scanWireRequest(body)
	if err != nil {
		logLiveCompactSkip(token, "scan")
		return body, foldResult{}
	}
	result := planFold(body, req, cfg, s.archive, s.pointerCache)
	if result.changed() {
		// Validate the rewrite before it is allowed onto the wire. This is the
		// one place where a scanner bug would reach the upstream, so the check
		// is cheap and structural: same message count, same tool pairing.
		if err := verifyFold(req, result.body); err != nil {
			logLiveCompactSkipDetails(token, "verify", err)
			return body, foldResult{}
		}
		s.archive.sweep(time.Now())
		elapsed := time.Since(start)
		logLiveCompactFold(token, result, elapsed)
		s.recordFoldLedger(token, cfg.Mode, len(body), len(result.body), result, elapsed)
		return result.body, result
	}
	return body, result
}

// sessionFoldAllowed is the request-path read of the same flag the observation
// worker uses.
func (s *Server) sessionFoldAllowed(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.sessions[token]
	return ok && st.foldAllowed
}
