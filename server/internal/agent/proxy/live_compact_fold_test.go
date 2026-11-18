package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The fold invariants (design §5.2). Every one of these is a hard requirement:
// a fold that breaks any of them must not reach the wire, so each test asserts
// the property on the forwarded bytes themselves rather than on the planner's
// bookkeeping.

func foldTestConfig(dir string) LiveCompactConfig {
	return LiveCompactConfig{
		Mode:           LiveCompactOn,
		KeepRounds:     1,
		MinBlockTokens: 10,
		ArchiveDir:     dir,
		ArchiveEnabled: true,
	}.Normalized()
}

// bigText is comfortably above the token floor and has a recognizable head and
// tail so the excerpt can be checked.
func bigText(tag string) string {
	var builder strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&builder, "%s line %03d: some file content here\n", tag, i)
	}
	return builder.String()
}

func foldHarness(t *testing.T, messages []any) ([]byte, foldResult, *archiveStore) {
	t.Helper()
	dir := t.TempDir()
	body := wireBody(t, messages)
	store := newArchiveStore(dir, time.Hour, archiveMaxBytes)
	req, err := scanWireRequest(body)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	result := planFold(body, req, foldTestConfig(dir), store, nil)
	return body, result, store
}

func decodedMessages(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var payload struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode rewritten body: %v", err)
	}
	return payload.Messages
}

// I1: message count, order, and role are unchanged.

func TestFoldPreservesMessageCountOrderAndRole(t *testing.T) {
	original, result, _ := foldHarness(t, []any{
		userText("start"),
		assistantToolUse("toolu_1", "Read"),
		userToolResult("toolu_1", bigText("old"), false),
		assistantToolUse("toolu_2", "Read"),
		userToolResult("toolu_2", bigText("recent"), false),
	})
	if !result.changed() {
		t.Fatal("expected a fold with one round kept verbatim")
	}
	before := decodedMessages(t, original)
	after := decodedMessages(t, result.body)
	if len(before) != len(after) {
		t.Fatalf("message count %d -> %d", len(before), len(after))
	}
	for index := range before {
		if before[index]["role"] != after[index]["role"] {
			t.Fatalf("message %d role changed: %v -> %v", index, before[index]["role"], after[index]["role"])
		}
	}
}

// I2: tool_use ids and tool_result tool_use_id pairing are unchanged, and the
// ids themselves never move.
func TestFoldPreservesToolPairing(t *testing.T) {
	_, result, _ := foldHarness(t, []any{
		userText("start"),
		assistantToolUse("toolu_aaaaaaaa", "Read"),
		userToolResult("toolu_aaaaaaaa", bigText("old"), false),
		assistantToolUse("toolu_bbbbbbbb", "Bash"),
		userToolResult("toolu_bbbbbbbb", bigText("recent"), false),
	})
	if !result.changed() {
		t.Fatal("expected a fold")
	}
	after := decodedMessages(t, result.body)
	ids := map[string]string{} // tool_use id -> tool_result answer
	for _, message := range after {
		blocks, _ := message["content"].([]any)
		for _, raw := range blocks {
			block, _ := raw.(map[string]any)
			switch block["type"] {
			case "tool_use":
				ids[fmt.Sprint(block["id"])] = ""
			case "tool_result":
				answer := fmt.Sprint(block["tool_use_id"])
				if _, ok := ids[answer]; !ok {
					t.Fatalf("tool_result %q has no matching tool_use", answer)
				}
				ids[answer] = answer
			}
		}
	}
	for id, answer := range ids {
		if answer != id {
			t.Fatalf("tool call %q lost its result", id)
		}
	}
}

// I3: a tool_result carrying an image/document/unknown block is skipped whole,
// even when it also contains foldable-looking text.
func TestFoldSkipsToolResultsWithNonTextBlocks(t *testing.T) {
	_, result, store := foldHarness(t, []any{
		userText("start"),
		assistantToolUse("toolu_img", "Read"),
		userToolResultBlocks("toolu_img", []any{
			map[string]any{"type": "text", "text": bigText("mixed")},
			map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": "AAAA"}},
		}, false),
		assistantToolUse("toolu_keep", "Read"),
		userToolResult("toolu_keep", bigText("recent"), false),
	})
	if result.changed() {
		t.Fatalf("a mixed text/image result must not be folded, got %d blocks", result.foldedBlocks)
	}
	assertArchiveEmpty(t, store)
}

// I4: every block that was not folded decodes to the same value it had before.
func TestFoldLeavesOtherBlocksDecodeIdentical(t *testing.T) {
	original, result, _ := foldHarness(t, []any{
		userText("start"),
		assistantToolUse("toolu_old", "Read"),
		userToolResult("toolu_old", bigText("old"), false),
		map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "thinking", "thinking": "internal reasoning", "signature": "sig"},
			map[string]any{"type": "text", "text": "assistant note"},
		}},
		assistantToolUse("toolu_new", "Read"),
		userToolResult("toolu_new", bigText("recent"), false),
	})
	if !result.changed() {
		t.Fatal("expected a fold")
	}
	before := decodedMessages(t, original)
	after := decodedMessages(t, result.body)
	for index := range before {
		if index == 2 {
			continue // the folded tool_result
		}
		beforeJSON, _ := json.Marshal(before[index])
		afterJSON, _ := json.Marshal(after[index])
		if !bytes.Equal(beforeJSON, afterJSON) {
			t.Fatalf("message %d changed:\n before=%s\n after=%s", index, beforeJSON, afterJSON)
		}
	}
}

// I7: malformed input and unreadable archives leave the body untouched. Fold
// returns the original slice, not a copy, so the handler's forward path is
// exactly what it was before this feature existed.
func TestFoldFailsOpenOnMalformedBody(t *testing.T) {
	dir := t.TempDir()
	cfg := foldTestConfig(dir)
	store := newArchiveStore(dir, time.Hour, archiveMaxBytes)

	for _, body := range [][]byte{
		[]byte(`{"messages": not-json`),
		[]byte(`{}`),
		[]byte(`not json at all`),
		[]byte(``),
	} {
		req, err := scanWireRequest(body)
		if err == nil {
			result := planFold(body, req, cfg, store, nil)
			if result.changed() {
				t.Fatalf("body %q was rewritten by a malformed-input fold", body)
			}
		}
	}
}

// An archive write failure must leave the block unfolded: a pointer to a file
// that does not exist is worse than a full-size result.
func TestFoldSkipsBlockWhenArchiveWriteFails(t *testing.T) {
	// A path under a regular file cannot be created, so MkdirAll fails.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newArchiveStore(filepath.Join(blocker, "context"), time.Hour, archiveMaxBytes)
	cfg := foldTestConfig(dir)

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
	result := planFold(body, req, cfg, store, nil)
	if result.changed() {
		t.Fatal("a block with an unwritable archive must not be folded")
	}
	if result.archiveFailures == 0 {
		t.Fatal("the archive failure must be counted")
	}
	if result.foldedBlocks != 0 {
		t.Fatalf("folded=%d with no writable archive", result.foldedBlocks)
	}
}

func assertArchiveEmpty(t *testing.T, store *archiveStore) {
	t.Helper()
	entries, err := os.ReadDir(store.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatalf("read archive dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("archive should be empty, has %d entries", len(entries))
	}
}
