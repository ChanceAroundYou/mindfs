package proxy

import (
	"fmt"
	"log"
	"time"
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
	return fmt.Sprintf("[live-compact] fold token=%s blocks=%d new_blocks=%d sticky_blocks=%d tokens_est=%d bytes=%d skipped=%d archive_errors=%d gate=%s fold_ms=%d",
		ShortToken(token), result.foldedBlocks, result.newBlocks, result.stickyBlocks,
		result.foldedTokens, result.foldedBytes, result.skipped, result.archiveFailures,
		gate, elapsed.Milliseconds())
}

// logLiveCompactProvider writes the per-request observation line for a provider
// route (Codex). The Messages path gets a richer one from its response parser,
// which sees the upstream usage; here the proxy only knows the request, but
// some line is mandatory: without it a shadow — or an `enabled=false` harness —
// is invisible in the log, and "why does Codex never fold" can only be answered
// by capturing a request body.
func logLiveCompactProvider(token string, gate liveCompactGate, result foldResult) {
	log.Printf("%s", liveCompactProviderLine(token, gate, result))
}

func liveCompactProviderLine(token string, gate liveCompactGate, result foldResult) string {
	return fmt.Sprintf("[live-compact] provider=responses token=%s gate=%s est=%d growth=%d projected=%d candidates=%d cold=%t cold_reason=%s folded=%t blocks=%d new_blocks=%d sticky_blocks=%d saved_bytes=%d skipped=%d archive_errors=%d",
		ShortToken(token), gate.decision, gate.est, gate.growth, gate.projected, gate.candidates,
		gate.cold, gate.coldReason, result.changed(), result.foldedBlocks, result.newBlocks,
		result.stickyBlocks, result.foldedBytes, result.skipped, result.archiveFailures)
}
