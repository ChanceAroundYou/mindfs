package proxy

import (
	"strings"
	"testing"
	"time"

	"mindfs/server/internal/agent/fold"
)

// A provider-route token is "thread:<uuid>": the first eight bytes are the same
// for every Codex session, so a prefix-only shortening made every fold log line
// read "token=thread:0" and sessions indistinguishable.
func TestShortTokenKeepsProviderTail(t *testing.T) {
	if got := ShortToken("thread:01a1f8ad-925f-72a2-963a-d9f854b95cf3"); got != "thread:54b95cf3" {
		t.Fatalf("provider token = %q", got)
	}
	if got := ShortToken("1795174185-992e5137eb18"); got != "17951741" {
		t.Fatalf("session key = %q", got)
	}
	if got := ShortToken("short"); got != "short" {
		t.Fatalf("short token = %q", got)
	}
}

// The provider route has no response parser to borrow the Messages observation
// line from, so this line is the only trace a Codex session leaves.
func TestLiveCompactProviderLine(t *testing.T) {
	gate := liveCompactGate{
		decision: "fold", est: 87056, growth: 4055, projected: 90000, candidates: 6,
		plan: fold.PlanStats{BodyBytes: 97000, ResultBytes: 88000, UnaccountedBytes: 9000, ProtectedBytes: 20000, CandidateBytes: 64000},
	}
	result := foldResult{body: []byte("x"), foldedBlocks: 6, newBlocks: 6, newTokens: 12000, foldedBytes: 180257}
	usage := fold.Usage{Source: fold.UsageSourceResponse, ContextTokens: 128072, CacheRead: 53248, RewriteTokens: 74824}
	line := liveCompactProviderLine("thread:01a1f8ad-925f-72a2-963a-d9f854b95cf3", gate, result, usage)
	for _, want := range []string{
		"provider=responses", "token=thread:54b95cf3", "gate=fold", "est=87056",
		// est and actual side by side: the estimate is 1.4-2.6x high on a
		// Responses body, so a rail line without the real count cannot be read.
		"actual=128072", "cached=53248",
		"candidates=6", "folded=true", "blocks=6", "new_blocks=6", "new_tokens=12000",
		"saved_bytes=180257", "plan_body_bytes=97000", "plan_unaccounted_bytes=9000", "plan_protected_bytes=20000",
		"plan_candidate_bytes=64000",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("line %q missing %q", line, want)
		}
	}
	missing := liveCompactProviderLine("thread:x", liveCompactGate{}, foldResult{}, fold.Usage{Source: fold.UsageSourceMissing, RewriteTokens: -1})
	if !strings.Contains(missing, "folded=false") || !strings.Contains(missing, "actual=-1") {
		t.Fatalf("unfolded line = %q", missing)
	}
	if !strings.Contains(missing, "usage_missing=unknown") {
		t.Fatalf("missing sample without a reason = %q", missing)
	}
	// The reason is what separates a compressed body from a stream that ended
	// early; both look like actual=-1 without it.
	compressed := liveCompactProviderLine("thread:x", liveCompactGate{}, foldResult{}, fold.Usage{
		Source: fold.UsageSourceMissing, RewriteTokens: -1, MissingReason: fold.UsageMissingContentEncoding,
	})
	if !strings.Contains(compressed, "usage_missing=content_encoding") {
		t.Fatalf("compressed line = %q", compressed)
	}
	if !strings.Contains(line, "usage_missing=-") {
		t.Fatalf("sampled line must not carry a missing reason: %q", line)
	}
}

func TestLiveCompactFoldLineKeepsFormat(t *testing.T) {
	line := liveCompactFoldLine("1795174185", foldResult{foldedBlocks: 1, newBlocks: 1, newTokens: 3400, foldedTokens: 1985, foldedBytes: 6818}, "fold", 2*time.Millisecond)
	if !strings.HasPrefix(line, "[live-compact] fold token=17951741 blocks=1 new_blocks=1 new_tokens=3400") || !strings.HasSuffix(line, "gate=fold fold_ms=2") {
		t.Fatalf("fold line = %q", line)
	}
}

// The observation line says how big the request was but not what it was made
// of. Without the composition, a rail hit with nothing foldable and a fold that
// only had a small share to work with produce the same numbers.
func TestLiveCompactObservationLineCarriesPlanComposition(t *testing.T) {
	obs := liveCompactObservation{
		mode: "on", messages: 40, rounds: 12, closedRounds: 11, keepRounds: 3,
		candidates: 2, candidateBytes: 5000,
		gateDecision: "fold", gateEst: 130000,
		plan: fold.PlanStats{
			BodyBytes: 131072, ResultBytes: 96000, UnaccountedBytes: 35072, ProtectedBytes: 20000,
			NonTextBytes: 900, MultiPartKeptBytes: 1000, FailedSmallBytes: 700,
			CandidateBytes: 73400,
		},
	}
	line := liveCompactObservationLine("1795174185", obs, usageTotals{}, 129000)
	for _, want := range []string{
		"on token=17951741", "cand_bytes=5000",
		"plan_body_bytes=131072", "plan_result_bytes=96000", "plan_unaccounted_bytes=35072", "plan_protected_bytes=20000",
		"plan_nontext_bytes=900", "plan_multipart_kept_bytes=1000", "plan_failed_small_bytes=700",
		"plan_candidate_bytes=73400",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("line %q missing %q", line, want)
		}
	}
}
