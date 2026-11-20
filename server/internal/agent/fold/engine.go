package fold

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// The fold engine.
//
// One pass over a request: scan (adapter) → policy (rail, keep window, chunk
// budget) → render pointers → archive originals → splice (adapter) → verify
// the rewritten body still describes the same conversation → commit.
//
// Every failure returns the original body. A fold is an optimisation; a
// request that cannot be folded must still be forwarded exactly as the harness
// sent it (I7).

// ErrVerifyFailed means the rewritten body no longer describes the same
// conversation — a scanner or splice bug. The caller must forward the original.
var ErrVerifyFailed = errors.New("live-compact: fold verification failed")

// Outcome is what one Fold call did.
type Outcome struct {
	// Body is the rewritten request, or the original body when nothing was
	// folded or anything failed.
	Body   []byte
	Folded bool
	// FoldedBlocks, FoldedTokens, FoldedBytes describe what the forwarded body
	// replaces: sticky and newly committed together. They describe the body,
	// not this request's decisions.
	FoldedBlocks int
	FoldedTokens int
	FoldedBytes  int
	// StickyBlocks were folded because the session had already committed to
	// them; NewBlocks were committed by this request.
	StickyBlocks int
	NewBlocks    int
	// Skipped counts candidates that were eligible but not folded (decode
	// failure, multi-part content, archive failure, pointer not smaller).
	Skipped int
	// Candidates is how many blocks the planner was willing to fold. Zero means
	// the request had nothing to act on — which is what keeps a PostCompact cold
	// signal alive for the next request.
	Candidates int
	// ArchiveFailures counts blocks left alone because the archive write
	// failed. Non-zero means folding is degraded, not that the request is
	// wrong.
	ArchiveFailures int
	// Verdict is the gate decision, for the log. Block is true whenever Body is
	// the original.
	Verdict Verdict
	// Estimated is the pre-fold size of the request; Projected is the rail
	// input in upstream tokens when calibration is available.
	Estimated  int
	Projected  int
	Cold       bool
	ColdReason string
	// Model is the model id the adapter read from the body, for log lines that
	// need to name it (the unknown-alias warning).
	Model string
	// GuardUnknownModel is set when the adapter could not classify the request's
	// model. The caller owns the per-session dedupe of the log line.
	GuardUnknownModel bool
}

// Engine folds one harness's requests. It is safe for concurrent use as long as
// each request passes its own Session and the caller serialises Session access.
type Engine struct {
	cfg     Config
	adapter Adapter
	archive *ArchiveStore
	cache   *PointerCache
}

// NewEngine builds an engine. archive may be nil only if the engine is never
// asked to fold (it then reports observe-only); cache may be nil, in which case
// nothing is cached.
func NewEngine(cfg Config, a Adapter, archive *ArchiveStore, cache *PointerCache) *Engine {
	return &Engine{cfg: cfg, adapter: a, archive: archive, cache: cache}
}

// Fold runs the policy for one request.
func (e *Engine) Fold(req Request) (Outcome, error) {
	if req.Now.IsZero() {
		req.Now = time.Now()
	}
	out := Outcome{Body: req.Body}
	if e.adapter == nil {
		out.Verdict = Verdict{Block: true, Reason: GateObserveOnly}
		return out, nil
	}
	conv, err := e.adapter.Scan(req.Body)
	if err != nil {
		out.Estimated = EstimateByteTokens(req.Body)
		out.Verdict = Verdict{Block: true, Reason: GateScanFailed}
		return out, fmt.Errorf("live-compact: scan: %w", err)
	}
	out.Estimated = conv.Estimated
	out.Model = conv.Model
	if out.Estimated <= 0 {
		out.Estimated = EstimateByteTokens(req.Body)
	}
	if conv.Refuse != "" {
		// The adapter knows something about this body that makes a rewrite
		// wrong regardless of size — a stored conversation, for example.
		out.Verdict = Verdict{Block: true, Reason: conv.Refuse}
		return out, nil
	}
	if req.Session == nil || (!req.FoldAllowed && !req.Preview) {
		out.Verdict = Verdict{Block: true, Reason: GateObserveOnly}
		return out, nil
	}
	if e.archive == nil && !req.Preview {
		out.Verdict = Verdict{Block: true, Reason: GateObserveOnly}
		return out, nil
	}

	// A rewrite is a client-side edit. On a preserved-thinking model it
	// invalidates every later thinking block, so the guard runs before
	// anything is planned.
	guard := e.adapter.Guard(conv.Model, e.cfg)
	out.GuardUnknownModel = guard.UnknownModel
	if guard.Block {
		reason := guard.Reason
		if reason == "" {
			reason = GatePrefixGuard
		}
		out.Verdict = Verdict{Block: true, Reason: reason}
		return out, nil
	}

	candidates, skipped := PlanCandidates(req.Body, conv, e.cfg, req.Session.Commits)
	out.Candidates = len(candidates)
	cold, coldReason := e.coldDecision(req)
	out.Cold, out.ColdReason = cold, coldReason
	if len(candidates) > 0 {
		// The signal describes the compaction, not this request: spending it on
		// a request with nothing foldable would lose the one moment a fold is
		// free.
		req.Session.PendingColdFold = false
	}

	growth := 0
	if req.LastEstimate > 0 && out.Estimated > req.LastEstimate {
		growth = out.Estimated - req.LastEstimate
	}
	estimate := Estimate{Est: out.Estimated, Growth: growth, Actual: req.LastActual, ForwardEst: req.LastForwardEst}

	if req.Preview {
		return e.preview(req, candidates, skipped, estimate, cold, coldReason, out), nil
	}

	// Pass 1: committed blocks are folded unconditionally. This is what makes
	// the fold boundary monotone across turns (I8).
	//
	// Committed in an earlier process are not in the key set yet: their keys are
	// maphash values and cannot be persisted, so they are matched here by
	// archive name, which costs one render (cached from then on).
	restoring := req.Session.Commits.RestoredCount() > 0
	var replacements []Replacement
	used := make([]bool, len(candidates))
	stickyTokens := 0
	for index, candidate := range candidates {
		if !candidate.Committed && !restoring {
			continue
		}
		rendered, skip := e.render(req.Body, candidate)
		if skip != renderOK {
			skipped++
			if skip == renderSkipArchive {
				out.ArchiveFailures++
			}
			continue
		}
		if !candidate.Committed && !req.Session.Commits.Adopt(candidate.Key, filepath.Base(rendered.path)) {
			// A restored name that does not match: leave it to pass 2 like any
			// other uncommitted candidate.
			continue
		}
		used[index] = true
		replacements = append(replacements, rendered.replacement)
		out.StickyBlocks++
		out.FoldedBlocks++
		out.FoldedTokens += rendered.tokens
		out.FoldedBytes += rendered.textBytes
		stickyTokens += rendered.tokens
	}
	if restoring {
		// Drop whatever could not be matched: those blocks are no longer in
		// this session's transcript. Keeping them would make every later
		// request pay for renders that can never match.
		req.Session.Commits.DropUnrestored()
	}

	// Pass 2: decide whether this request may commit new blocks. Upstream
	// calibration already reflects the sticky blocks (the previous request was
	// folded before it was sent), so the estimator subtraction only applies on
	// the fallback path.
	current := out.Estimated - stickyTokens
	if estimate.Actual > 0 {
		current = estimate.Actual
	}
	scaleNum, scaleDen := estimate.Scale()
	largeBlock := false
	for index, candidate := range candidates {
		if !used[index] && candidate.Result.Tokens >= e.cfg.LargeBlockTokens {
			largeBlock = true
			break
		}
	}
	budget, decision := DecideBudget(e.cfg, estimate, cold, coldReason, current, largeBlock)
	decision.Candidates = len(candidates)
	out.Projected = decision.Projected

	var newKeys []PointerCacheKey
	var newPaths []string
	var newSizes []int
	if budget.CommitNew {
		var newReplacements []Replacement
		newBytes, newTokens := 0, 0
		// Chunked drain (design §12.7(d)): max_fold_bytes bounds one chunk, and
		// one event may drain up to MaxChunksPerEvent of them. A single global
		// cap would end the event after ~2 blocks and leave the context
		// shallow-folded — the shape §12.7(b) measured as strictly worse than
		// not folding. The chunk cap still bounds the work and the latency of
		// one request; the chunk count bounds the whole event.
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
			rendered, skip := e.render(req.Body, candidate)
			if skip != renderOK {
				skipped++
				if skip == renderSkipArchive {
					out.ArchiveFailures++
				}
				continue
			}
			if rendered.savedBytes < FoldMinBlockSavings {
				continue
			}
			newReplacements = append(newReplacements, rendered.replacement)
			newKeys = append(newKeys, candidate.Key)
			newPaths = append(newPaths, filepath.Base(rendered.path))
			newSizes = append(newSizes, rendered.textBytes)
			chunkBytes += width
			newBytes += rendered.textBytes
			newTokens += rendered.tokens
			if estimate.Est > 0 && current+estimate.Growth*scaleNum/scaleDen-newTokens*scaleNum/scaleDen <= e.cfg.TargetTokens {
				break
			}
		}
		// A fold event that is too small does not justify a cache break. Drop
		// the whole new set; never go back to accumulate more.
		if budget.MinNewBytes > 0 && newBytes < budget.MinNewBytes {
			decision.Decision = GateNoBacklog
			newKeys, newPaths, newSizes = nil, nil, nil
		} else {
			replacements = append(replacements, newReplacements...)
			out.NewBlocks += len(newReplacements)
			out.FoldedBlocks += len(newReplacements)
			out.FoldedTokens += newTokens
			out.FoldedBytes += newBytes
		}
	}
	out.Skipped = skipped
	decision.WouldFoldTokens = out.FoldedTokens
	decision.WouldFoldBlocks = out.FoldedBlocks

	if len(replacements) == 0 {
		out.Verdict = Verdict{Block: true, Reason: decision.Decision}
		return out, nil
	}
	sort.Slice(replacements, func(i, j int) bool { return replacements[i].Span.Start < replacements[j].Span.Start })
	if err := checkSpliceBounds(len(req.Body), replacements); err != nil {
		return e.abandon(&out, req.Body, decision.Decision), err
	}
	folded, err := e.adapter.Splice(req.Body, replacements)
	if err != nil {
		return e.abandon(&out, req.Body, decision.Decision), fmt.Errorf("live-compact: splice: %w", err)
	}
	// Validate the rewrite before it is allowed onto the wire. This is the one
	// place where a scanner bug would reach the upstream, so the check is cheap
	// and structural: same rounds, same call pairing.
	if err := verifyConversation(conv, folded, e.adapter); err != nil {
		return e.abandon(&out, req.Body, decision.Decision), err
	}
	// Only a verified rewrite may commit: otherwise a restart would treat
	// blocks as folded that the upstream never saw folded.
	req.Session.Commits.CommitAll(newKeys, newPaths, newSizes)
	out.Body = folded
	out.Folded = true
	out.Verdict = Verdict{Reason: decision.Decision}
	return out, nil
}

// abandon resets the counters of a fold that will not be forwarded and hands
// back the original body. Commits already adopted in pass 1 stay adopted; they
// cost one render next request and never change the body.
func (e *Engine) abandon(out *Outcome, body []byte, reason string) Outcome {
	out.Body = body
	out.Folded = false
	out.Skipped += out.FoldedBlocks
	out.FoldedBlocks, out.FoldedTokens, out.FoldedBytes = 0, 0, 0
	out.StickyBlocks, out.NewBlocks = 0, 0
	out.Verdict = Verdict{Block: true, Reason: reason}
	return *out
}

// coldDecision assembles the session's cold signals and applies the rule. It
// drops a PostCompact flag older than the cache TTL in the same call that reads
// it, so a stale flag cannot make a later request look cold.
func (e *Engine) coldDecision(req Request) (bool, string) {
	snapshot := ColdSnapshot{
		PendingCompact: req.Session.PendingColdFold,
		CompactedAt:    req.Session.CompactedAt,
		LastUsageAt:    req.Session.LastUsageAt,
		NoReadStreak:   req.Session.NoReadStreak,
		LastContext:    req.Session.LastContext,
	}
	if snapshot.PendingCompact {
		ttl := time.Duration(e.cfg.CacheTTLSeconds) * time.Second
		if !snapshot.CompactedAt.IsZero() && req.Now.Sub(snapshot.CompactedAt) >= ttl {
			req.Session.PendingColdFold = false
			snapshot.PendingCompact = false
		}
	}
	return ColdDecision(snapshot, e.cfg, req.Now)
}

// checkSpliceBounds rejects a replacement set that is out of order, out of
// range, or would more than double the body. A rewrite that grows the request
// by its own size is a bug, not a fold.
func checkSpliceBounds(bodyLen int, reps []Replacement) error {
	delta := 0
	cursor := 0
	for _, rep := range reps {
		if rep.Span.Start < cursor || rep.Span.End > bodyLen || rep.Span.Start >= rep.Span.End {
			return fmt.Errorf("%w: replacement span out of order or range", ErrVerifyFailed)
		}
		delta += len(rep.Literal) - (rep.Span.End - rep.Span.Start)
		cursor = rep.Span.End
	}
	if delta >= 0 && delta > bodyLen {
		return fmt.Errorf("%w: rewrite more than doubles the body", ErrVerifyFailed)
	}
	return nil
}

// verifyConversation asserts the invariants a fold must never break (I1/I2):
// the same rounds in the same order, each with the same calls and the same
// number of results, and no result changing its text shape or failure flag.
func verifyConversation(before *Conversation, folded []byte, adapter Adapter) error {
	after, err := adapter.Scan(folded)
	if err != nil {
		return fmt.Errorf("%w: rewritten body does not scan", ErrVerifyFailed)
	}
	if len(before.Rounds) != len(after.Rounds) {
		return fmt.Errorf("%w: round count changed", ErrVerifyFailed)
	}
	for index := range before.Rounds {
		oldRound := before.Rounds[index]
		newRound := after.Rounds[index]
		if len(oldRound.Results) != len(newRound.Results) {
			return fmt.Errorf("%w: result count changed in round %d", ErrVerifyFailed, index)
		}
		for resultIndex := range oldRound.Results {
			oldResult := oldRound.Results[resultIndex]
			newResult := newRound.Results[resultIndex]
			if oldResult.CallID != newResult.CallID {
				return fmt.Errorf("%w: call pairing changed", ErrVerifyFailed)
			}
			if oldResult.Failed != newResult.Failed || oldResult.NonText != newResult.NonText {
				return fmt.Errorf("%w: result shape changed", ErrVerifyFailed)
			}
		}
	}
	return nil
}

// Render outcome kinds.
type renderSkip int

const (
	renderOK renderSkip = iota
	renderSkipDecode
	renderSkipArchive
	renderSkipTooBig
)

// rendered is the replacement for one candidate plus the numbers the log and
// the ledger report.
type rendered struct {
	replacement Replacement
	tokens      int
	textBytes   int
	// savedBytes is how much smaller the pointer is than the wire span it
	// replaces. A block that saves a trivial amount is not worth an archive
	// file and a pointer line, so this — not a token floor — decides whether a
	// candidate is committed.
	savedBytes int
	// path is the archive file backing this pointer. Its base name is the
	// block's stable identity across restarts.
	path string
}

// render produces the pointer for one candidate, reusing the archive and the
// encoded literal when they are still valid.
func (e *Engine) render(body []byte, candidate Candidate) (rendered, renderSkip) {
	span := candidate.Result.Parts[0]
	spanLen := span.End - span.Start
	if entry, hit := e.cache.Get(candidate.Key); hit {
		// The sweep can have removed the archive since the entry was written. A
		// pointer to a missing file is worse than a full-size result, so verify
		// before reusing it.
		if info, err := os.Stat(entry.Path); err == nil && info.Size() == int64(entry.Bytes) && len(entry.Literal) < spanLen {
			return rendered{
				replacement: Replacement{Span: span, Literal: entry.Literal},
				tokens:      entry.Tokens,
				textBytes:   entry.Bytes,
				savedBytes:  spanLen - len(entry.Literal),
				path:        entry.Path,
			}, renderOK
		}
	}
	text, ok := e.adapter.Text(body, candidate.Result)
	if !ok || text == "" {
		return rendered{}, renderSkipDecode
	}
	archivePath, err := e.archive.Write(text)
	if err != nil {
		return rendered{}, renderSkipArchive
	}
	tokens := EstimateTokens(text)
	literal := e.adapter.Encode(RenderPointer(PointerInput{
		ToolName:     candidate.Result.ToolName,
		InputSummary: candidate.Result.InputSummary,
		Text:         text,
		ArchivePath:  archivePath,
		ErrorText:    candidate.Result.ErrorText || LooksLikeErrorText(text),
		Failed:       candidate.Result.Failed,
	}))
	// A fold that is not smaller is not a fold. This is the backstop for the
	// excerpt bound: whatever the reason, a pointer that costs more bytes than
	// the result it replaces would make every subsequent turn bigger,
	// permanently.
	if len(literal) >= spanLen {
		return rendered{}, renderSkipTooBig
	}
	e.cache.Put(candidate.Key, CachedPointer{
		Literal: literal,
		Path:    archivePath,
		Tokens:  tokens,
		Bytes:   len(text),
	})
	return rendered{
		replacement: Replacement{Span: span, Literal: literal},
		tokens:      tokens,
		textBytes:   len(text),
		savedBytes:  spanLen - len(literal),
		path:        archivePath,
	}, renderOK
}
