package proxy

import (
	"fmt"
	"log"
	"time"

	"mindfs/server/internal/agent/fold"
)

// Log lines for the fold path. Kept apart from the Phase A observation logging
// so the two can be read (and grep-ed) independently.

func logLiveCompactSkip(token, reason string) {
	log.Printf("[live-compact] skip token=%s reason=%s", ShortToken(token), reason)
}

func logLiveCompactSkipDetails(token, reason string, err error) {
	log.Printf("[live-compact] skip token=%s reason=%s detail=%v", ShortToken(token), reason, err)
}

// logLiveCompactFold records an applied fold. The saving is measured by the
// proxy itself (folded bytes and their estimate) because the upstream's usage
// counters go through the llmux relay and are not trustworthy in absolute terms
// (design §5.6).
func logLiveCompactFold(token string, result foldResult, gate string, elapsed time.Duration) {
	log.Printf("%s", liveCompactFoldLine(token, result, gate, elapsed))
}

func liveCompactFoldLine(token string, result foldResult, gate string, elapsed time.Duration) string {
	return fmt.Sprintf("[live-compact] fold token=%s blocks=%d new_blocks=%d new_tokens=%d sticky_blocks=%d tokens_est=%d bytes=%d skipped=%d archive_errors=%d gate=%s fold_ms=%d",
		ShortToken(token), result.foldedBlocks, result.newBlocks, result.newTokens, result.stickyBlocks,
		result.foldedTokens, result.foldedBytes, result.skipped, result.archiveFailures,
		gate, elapsed.Milliseconds())
}

// logLiveCompactProvider writes the per-request observation line for a provider
// route (Codex). The Messages path gets a richer one from its response parser,
// which sees the upstream usage; here the proxy only knows the request, but
// some line is mandatory: without it a shadow — or an `enabled=false` harness —
// is invisible in the log, and "why does Codex never fold" can only be answered
// by capturing a request body.
//
// The line is written after the response so `actual` sits next to `est`: the
// estimator reads 1.4-2.6x high on Responses bodies, and a reader who sees only
// `est` cannot tell a rail that fired on a real 40k context from one that fired
// at 100k.
func logLiveCompactProvider(token string, gate liveCompactGate, result foldResult, usage fold.Usage) {
	log.Printf("%s", liveCompactProviderLine(token, gate, result, usage))
}

func liveCompactProviderLine(token string, gate liveCompactGate, result foldResult, usage fold.Usage) string {
	actual, cached := -1, 0
	// A missing sample prints why it is missing: "actual=-1" alone cannot tell
	// a compressed body from a stream that ended before its usage block, and
	// those need different fixes.
	missing := "-"
	if usage.Source == fold.UsageSourceResponse {
		actual, cached = usage.ContextTokens, usage.CacheRead
	} else {
		missing = usage.MissingReason
		if missing == "" {
			missing = "unknown"
		}
	}
	return fmt.Sprintf("[live-compact] provider=responses token=%s gate=%s est=%d actual=%d cached=%d usage_missing=%s growth=%d projected=%d candidates=%d cold=%t cold_reason=%s folded=%t blocks=%d new_blocks=%d new_tokens=%d sticky_blocks=%d saved_bytes=%d skipped=%d archive_errors=%d %s",
		ShortToken(token), gate.decision, gate.est, actual, cached, missing, gate.growth, gate.projected, gate.candidates,
		gate.cold, gate.coldReason, result.changed(), result.foldedBlocks, result.newBlocks, result.newTokens,
		result.stickyBlocks, result.foldedBytes, result.skipped, result.archiveFailures,
		planStatsFields(gate.plan))
}

// planStatsFields renders the kernel planner's composition of a request body.
// Both provider paths print it: for Codex this line is the only trace of what a
// request was made of, and "candidates=0" alone cannot say whether the
// protected tail or the content itself was the reason.
func planStatsFields(stats fold.PlanStats) string {
	return fmt.Sprintf("plan_body_bytes=%d plan_result_bytes=%d plan_unaccounted_bytes=%d plan_protected_bytes=%d plan_nontext_bytes=%d plan_multipart_kept_bytes=%d plan_failed_small_bytes=%d plan_candidate_bytes=%d",
		stats.BodyBytes, stats.ResultBytes, stats.UnaccountedBytes, stats.ProtectedBytes, stats.NonTextBytes,
		stats.MultiPartKeptBytes, stats.FailedSmallBytes, stats.CandidateBytes)
}
