package proxy

import (
	"bytes"
	"strings"
	"testing"
)

// Failed (is_error) tool results used to be unfolded forever. A large one is
// exactly the kind of block that blows up the context window, and the pointer
// already keeps a 20/20-line excerpt plus the archive path, so it can be folded
// like any other result — as long as the card says the result failed.

func errorFoldMessages(tag string, size int) []any {
	return []any{
		userText("start"),
		assistantToolUse("toolu_err", "Bash"),
		userToolResult("toolu_err", bigText(tag), true),
		assistantToolUse("toolu_keep", "Read"),
		userToolResult("toolu_keep", strings.Repeat("k", size), false),
	}
}

func TestFoldFoldsLargeErrorResultsWithFailedLabel(t *testing.T) {
	_, result, _ := foldHarness(t, errorFoldMessages("err", 8001))
	if !result.changed() {
		t.Fatal("a large failed result was not folded")
	}
	card := string(result.body)
	if !strings.Contains(card, "该结果标记为失败") {
		t.Fatalf("pointer does not say the result failed:\n%s", card)
	}
	if !strings.Contains(card, "首尾各 20 行") {
		t.Fatalf("failed pointer does not use the wide excerpt:\n%s", card)
	}
	if result.newBlocks != 1 {
		t.Fatalf("new blocks = %d, want 1", result.newBlocks)
	}
}

// "Large enough" is the existing per-block token floor: a small failed result
// stays verbatim, both because folding it saves nothing and because its text is
// usually the whole diagnostic.
func TestFoldKeepsSmallErrorResults(t *testing.T) {
	dir := t.TempDir()
	cfg := foldTestConfig(dir)
	cfg.MinBlockTokens = 1000
	body := wireBody(t, []any{
		userText("start"),
		assistantToolUse("toolu_err", "Bash"),
		userToolResult("toolu_err", "exit status 1\nno such file", true),
		assistantToolUse("toolu_keep", "Read"),
		userToolResult("toolu_keep", bigText("keep"), false),
	})
	req, err := scanWireRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	result := planFold(body, req, cfg, newArchiveStore(dir, 0, 0), nil)
	if result.changed() {
		t.Fatalf("a small failed result was folded: %s", result.body)
	}
}

// The same text can arrive once as a failure and once as success. The render
// cache is keyed by content, so without a variant in the key the second
// rendering would be handed the first one's card — and one of them would say
// the wrong thing about failure.
func TestErrorPointerUsesDistinctCacheKey(t *testing.T) {
	dir := t.TempDir()
	store := newArchiveStore(dir, 0, 0)
	cache := newPointerCache()
	body := wireBody(t, []any{
		userText("start"),
		assistantToolUse("toolu_1", "Read"),
		userToolResult("toolu_1", bigText("same"), false),
		assistantToolUse("toolu_2", "Read"),
		userToolResult("toolu_2", bigText("same"), true),
	})
	req, err := scanWireRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	candidates, _ := collectFoldCandidates(body, req, LiveCompactConfig{MinBlockTokens: 10})
	if len(candidates) != 2 {
		t.Fatalf("candidates = %d, want 2", len(candidates))
	}
	ok, failed := candidates[0], candidates[1]
	if !failed.failed || ok.failed {
		t.Fatalf("failed flags = %v/%v", ok.failed, failed.failed)
	}
	normalCard, outcome := renderPointer(body, ok, store, cache)
	if outcome != foldOK {
		t.Fatalf("normal render outcome = %v", outcome)
	}
	failedCard, outcome := renderPointer(body, failed, store, cache)
	if outcome != foldOK {
		t.Fatalf("failed render outcome = %v", outcome)
	}
	if strings.Contains(normalCard.replacement.literal, "该结果标记为失败") {
		t.Fatal("a successful result was rendered as failed")
	}
	if !strings.Contains(failedCard.replacement.literal, "该结果标记为失败") {
		t.Fatal("a failed result reused the successful card")
	}
}

// Structural markers are how Bash and the CLI report failure, sometimes with
// is_error=false. They must widen the excerpt like other error-looking text.
func TestLooksLikeErrorTextStructuralMarkers(t *testing.T) {
	for _, text := range []string{
		"[stderr] something broke\nrest of output",
		"exit status 1",
		"Exit code 128",
	} {
		if !looksLikeErrorText(text) {
			t.Fatalf("structural failure marker not recognised: %q", text)
		}
	}
	if looksLikeErrorText("exit strategy: none\nthis file documents the exit status field") {
		t.Fatal("ordinary prose about exit status was flagged")
	}
}

// The observation line must say how many bytes the fold could not take and why;
// otherwise the rail looks satisfiable while un-foldable blocks keep it full.
func TestAnalyzeWireCountsUnfoldableBytesByReason(t *testing.T) {
	large := strings.Repeat("a", 8001)
	small := "exit status 1"
	messages := []any{
		userText("start"),
		assistantToolUse("big_err", "Bash"),
		userToolResult("big_err", large, true),
		assistantToolUse("small_err", "Bash"),
		userToolResult("small_err", small, true),
		assistantToolUse("multi", "Read"),
		userToolResultBlocks("multi", []any{
			map[string]any{"type": "text", "text": large},
			map[string]any{"type": "text", "text": large},
		}, false),
		assistantToolUse("img", "Read"),
		userToolResultBlocks("img", []any{
			map[string]any{"type": "text", "text": large},
			map[string]any{"type": "image", "source": map[string]any{"type": "base64", "data": "AAAA"}},
		}, false),
		assistantToolUse("keep", "Read"),
		userToolResult("keep", large, false),
	}
	analysis := analyzeWire(mustParse(t, wireBody(t, messages)), 1, 2000)
	if len(analysis.Candidates) != 1 || analysis.Candidates[0].ToolUseID != "big_err" {
		t.Fatalf("candidates = %+v, want only the large failed result", analysis.Candidates)
	}
	if analysis.FailedCandidates != 1 {
		t.Fatalf("failed candidates = %d, want 1", analysis.FailedCandidates)
	}
	if analysis.SkippedErrorBytes <= 0 {
		t.Fatal("small failed result bytes were not counted")
	}
	if analysis.SkippedMultiPartBytes <= 0 {
		t.Fatal("multi-part result bytes were not counted")
	}
	if analysis.SkippedNonTextBytes <= 0 {
		t.Fatal("non-text result bytes were not counted")
	}
}

// is_error changes the card, never the block structure: message count, roles,
// and tool pairing survive the fold.
func TestFoldErrorResultKeepsWireShape(t *testing.T) {
	original, result, _ := foldHarness(t, errorFoldMessages("err", 8001))
	if !result.changed() {
		t.Fatal("expected a fold")
	}
	before := decodedMessages(t, original)
	after := decodedMessages(t, result.body)
	if len(before) != len(after) {
		t.Fatalf("message count %d -> %d", len(before), len(after))
	}
	for index := range before {
		if before[index]["role"] != after[index]["role"] {
			t.Fatalf("message %d role changed", index)
		}
	}
	if !bytes.Contains(result.body, []byte(`"is_error":true`)) {
		t.Fatal("the folded block lost its is_error flag")
	}
}
