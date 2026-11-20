package proxy

import (
	"strings"
	"testing"
	"time"
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
	gate := liveCompactGate{decision: "fold", est: 87056, growth: 4055, projected: 90000, candidates: 6}
	result := foldResult{body: []byte("x"), foldedBlocks: 6, newBlocks: 6, foldedBytes: 180257}
	line := liveCompactProviderLine("thread:01a1f8ad-925f-72a2-963a-d9f854b95cf3", gate, result)
	for _, want := range []string{
		"provider=responses", "token=thread:54b95cf3", "gate=fold", "est=87056",
		"candidates=6", "folded=true", "blocks=6", "new_blocks=6", "saved_bytes=180257",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("line %q missing %q", line, want)
		}
	}
	empty := liveCompactProviderLine("thread:x", liveCompactGate{}, foldResult{})
	if !strings.Contains(empty, "folded=false") {
		t.Fatalf("unfolded line = %q", empty)
	}
}

func TestLiveCompactFoldLineKeepsFormat(t *testing.T) {
	line := liveCompactFoldLine("1795174185", foldResult{foldedBlocks: 1, newBlocks: 1, foldedTokens: 1985, foldedBytes: 6818}, "fold", 2*time.Millisecond)
	if !strings.HasPrefix(line, "[live-compact] fold token=17951741 blocks=1 new_blocks=1") || !strings.HasSuffix(line, "gate=fold fold_ms=2") {
		t.Fatalf("fold line = %q", line)
	}
}
