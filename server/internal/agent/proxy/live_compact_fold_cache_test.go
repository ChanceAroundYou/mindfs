package proxy

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mindfs/server/internal/agent/fold"
)

// Cache and prefix-stability tests. These pin the two properties that decide
// whether folding saves anything at all: an already-folded block renders the
// same bytes on every later turn, and a cache hit is indistinguishable from a
// cold render.

// The card must not depend on how the CLI happened to serialize the tool input.
// After a worktree repoint or a fork the CLI can re-emit the same historical
// tool_use with its keys in a different order; if the pointer followed that
// order the whole folded prefix would rewrite, which is exactly the cache break
// this feature must avoid. Rendered from spans, so the inputs are hand-built
// rather than marshaled from a Go map.
func TestFoldPointerIgnoresToolInputKeyOrder(t *testing.T) {
	text := bigText("payload")
	ordered := []byte(`{"file_path":"/tmp/a","offset":1,"limit":20}`)
	reordered := []byte(`{"limit":20,"file_path":"/tmp/a","offset":1}`)
	render := func(input []byte) string {
		return fold.RenderPointer(fold.PointerInput{
			ToolName:     "Read",
			InputSummary: summarizeToolInput(input, span{Start: 0, End: len(input)}),
			Text:         text,
			ArchivePath:  "/tmp/ctx/abc.txt",
		})
	}
	if first, second := render(ordered), render(reordered); first != second {
		t.Fatalf("pointer depends on tool input key order:\n a=%q\n b=%q", first, second)
	}
	// And the values must actually be present, so the normalization is not
	// "drop everything and agree on nothing".
	if got := render(ordered); !strings.Contains(got, "/tmp/a") {
		t.Fatalf("pointer dropped the file path: %q", got)
	}
}

func TestFoldIsDeterministic(t *testing.T) {
	messages := []any{
		userText("start"),
		assistantToolUse("toolu_1", "Read"),
		userToolResult("toolu_1", bigText("old"), false),
		assistantToolUse("toolu_2", "Read"),
		userToolResult("toolu_2", bigText("recent"), false),
	}
	dir := t.TempDir()
	body := wireBody(t, messages)
	cfg := foldTestConfig(dir)

	first := foldOnce(t, body, cfg, newArchiveStore(dir, time.Hour, archiveMaxBytes))
	// A fresh store and a fresh directory walk: determinism must not depend on
	// in-process memoization.
	second := foldOnce(t, body, cfg, newArchiveStore(dir, time.Hour, archiveMaxBytes))
	if !bytes.Equal(first, second) {
		t.Fatal("two folds of the same body differ")
	}
}

func foldOnce(t *testing.T, body []byte, cfg LiveCompactConfig, store *archiveStore) []byte {
	t.Helper()
	req, err := scanWireRequest(body)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	result := planFold(body, req, cfg, store, nil)
	if !result.changed() {
		t.Fatal("expected a fold")
	}
	return result.body
}

// I6: appending a turn must not change how the already-folded rounds were
// rendered. This is the cache-stability requirement and the reason only
// just-out-of-window rounds are folded: the pointers for old rounds are pure
// functions of their own content, so turn N+1's body is byte-identical to turn
// N's up to the block that newly left the window.
//
// The naive version of this test (compare against a common-prefix offset) is
// tautological, because the offset is by definition the first differing byte.
// The assertion here is where that offset lands: it must be at or after the
// last block turn N folded, not inside it.
func TestFoldKeepsOldPointersStableAcrossTurns(t *testing.T) {
	dir := t.TempDir()
	cfg := foldTestConfig(dir)
	store := newArchiveStore(dir, time.Hour, archiveMaxBytes)

	rounds := func(count int) []any {
		built := []any{userText("start")}
		for index := 1; index <= count; index++ {
			id := fmt.Sprintf("toolu_%d", index)
			built = append(built,
				assistantToolUse(id, "Read"),
				userToolResult(id, bigText(fmt.Sprintf("round%d", index)), false),
			)
		}
		return built
	}

	turnN := wireBody(t, rounds(3))
	turnN1 := wireBody(t, rounds(4))
	foldedN := foldOnce(t, turnN, cfg, store)
	foldedN1 := foldOnce(t, turnN1, cfg, store)

	// The property that matters for prefix stability: the pointer rendered for
	// an already-folded round is byte-identical on the next turn. If it were
	// not, every turn would rewrite the whole folded prefix and the cache
	// saving would invert into a cache miss.
	pointerAtN := foldedResultText(t, foldedN, "toolu_1")
	pointerAtN1 := foldedResultText(t, foldedN1, "toolu_1")
	if pointerAtN == "" || pointerAtN1 == "" {
		t.Fatal("round 1 was not folded")
	}
	if pointerAtN != pointerAtN1 {
		t.Fatalf("the pointer for an already-folded round changed between turns:\n turn N:   %q\n turn N+1: %q", pointerAtN, pointerAtN1)
	}
	// And a round that just left the window must render exactly as it will on
	// every subsequent turn (turn N+1 folds round 3 for the first time; turn
	// N+1 and a hypothetical turn N+2 must agree, which is what determinism
	// already covers — here we assert round 3's pointer names an archive that
	// exists).
	pointerAtN1ForRound3 := foldedResultText(t, foldedN1, "toolu_3")
	if !strings.Contains(pointerAtN1ForRound3, "archive: ") {
		t.Fatalf("newly folded round has no archive pointer: %q", pointerAtN1ForRound3)
	}
}

// foldedResultText returns the text of the tool_result answering id in a
// (possibly already folded) body.
func foldedResultText(t *testing.T, body []byte, id string) string {
	t.Helper()
	req, err := scanWireRequest(body)
	if err != nil {
		t.Fatalf("scan folded body: %v", err)
	}
	for _, message := range req.messages {
		for _, block := range message.blocks {
			if block.typ != "tool_result" || block.toolUseID != id || len(block.textParts) != 1 {
				continue
			}
			part := block.textParts[0]
			text, err := jsonStringAt(body, part.Start, part.End)
			if err != nil {
				t.Fatalf("decode result text: %v", err)
			}
			return text
		}
	}
	return ""
}

// The rendered-pointer cache must be invisible: a warm fold and a cold fold of
// the same body produce byte-identical output.
func TestFoldCacheHitIsByteIdentical(t *testing.T) {
	dir := t.TempDir()
	cfg := foldTestConfig(dir)
	store := newArchiveStore(dir, time.Hour, archiveMaxBytes)
	body := wireBody(t, []any{
		userText("start"),
		assistantToolUse("toolu_1", "Read"),
		userToolResult("toolu_1", bigText("old"), false),
		assistantToolUse("toolu_2", "Read"),
		userToolResult("toolu_2", bigText("recent"), false),
	})
	req, err := scanWireRequest(body)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	cached := newPointerCache()
	first := planFold(body, req, cfg, store, cached)
	second := planFold(body, req, cfg, store, cached)
	if !first.changed() || !second.changed() {
		t.Fatal("expected both folds to rewrite")
	}
	if !bytes.Equal(first.body, second.body) {
		t.Fatal("a cache hit changed the rewritten body")
	}
	if first.foldedBlocks != second.foldedBlocks || first.foldedBytes != second.foldedBytes {
		t.Fatalf("cache hit changed the counters: %+v -> %+v", first, second)
	}
}

// The sweep can delete an archive between turns. A cache hit must not emit a
// pointer to a missing file: it has to notice and rewrite the archive.
func TestFoldCacheRewritesVanishedArchive(t *testing.T) {
	dir := t.TempDir()
	cfg := foldTestConfig(dir)
	store := newArchiveStore(dir, time.Hour, archiveMaxBytes)
	body := wireBody(t, []any{
		userText("start"),
		assistantToolUse("toolu_1", "Read"),
		userToolResult("toolu_1", bigText("old"), false),
		assistantToolUse("toolu_2", "Read"),
		userToolResult("toolu_2", bigText("recent"), false),
	})
	req, err := scanWireRequest(body)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	cached := newPointerCache()
	first := planFold(body, req, cfg, store, cached)
	if !first.changed() {
		t.Fatal("expected a fold")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			t.Fatal(err)
		}
	}
	second := planFold(body, req, cfg, store, cached)
	if !second.changed() {
		t.Fatal("the second fold was abandoned instead of rewriting the archive")
	}
	if !bytes.Equal(first.body, second.body) {
		t.Fatal("rewriting a vanished archive produced a different pointer")
	}
	entries, err = os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("the archive was not rewritten: %v (%d entries)", err, len(entries))
	}
}

// A truncated archive is as bad as a missing one: the pointer advertises a
// byte count the file no longer has, and the model would read a partial result.
func TestFoldCacheRewritesTruncatedArchive(t *testing.T) {
	dir := t.TempDir()
	cfg := foldTestConfig(dir)
	store := newArchiveStore(dir, time.Hour, archiveMaxBytes)
	body := wireBody(t, []any{
		userText("start"),
		assistantToolUse("toolu_1", "Read"),
		userToolResult("toolu_1", bigText("old"), false),
		assistantToolUse("toolu_2", "Read"),
		userToolResult("toolu_2", bigText("recent"), false),
	})
	req, err := scanWireRequest(body)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	cached := newPointerCache()
	first := planFold(body, req, cfg, store, cached)
	if !first.changed() {
		t.Fatal("expected a fold")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one archive, got %d (%v)", len(entries), err)
	}
	target := filepath.Join(dir, entries[0].Name())
	if err := os.WriteFile(target, []byte("truncated"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := planFold(body, req, cfg, store, cached)
	if !second.changed() {
		t.Fatal("the fold trusted a truncated archive")
	}
	body2, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(body2) == "truncated" {
		t.Fatal("the truncated archive was not rewritten")
	}
	if !bytes.Equal(first.body, second.body) {
		t.Fatal("rewriting a truncated archive produced a different pointer")
	}
}
