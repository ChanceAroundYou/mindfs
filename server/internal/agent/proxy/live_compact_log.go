package proxy

import (
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
	log.Printf("[live-compact] fold token=%s blocks=%d new_blocks=%d sticky_blocks=%d tokens_est=%d bytes=%d skipped=%d archive_errors=%d gate=%s fold_ms=%d",
		ShortToken(token), result.foldedBlocks, result.newBlocks, result.stickyBlocks,
		result.foldedTokens, result.foldedBytes, result.skipped, result.archiveFailures,
		gate, elapsed.Milliseconds())
}
