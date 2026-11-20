package proxy

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Shared helpers for the request-path tests. The policy-level fixtures moved to
// the fold package with the planner; what remains here is what a request-path
// test needs to build a body and a server.

// foldTestConfig is a live-compact configuration with archiving on and a
// permissive keep window; individual tests raise or lower the rail.
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

// distinctFoldMessages gives every round its own filler: a shared one would
// hash to the same pointer key, which is fine for rendering but wrong for a
// commitment set.
func distinctFoldMessages(rounds, size int) []any {
	messages := []any{userText("start")}
	for index := 0; index < rounds; index++ {
		id := fmt.Sprintf("toolu_%d", index)
		messages = append(messages, assistantToolUse(id, "Read"))
		messages = append(messages, userToolResult(id, strings.Repeat(string(rune('a'+index)), size), false))
	}
	return messages
}

func distinctFoldBody(t *testing.T, rounds, size int) []byte {
	t.Helper()
	return wireBody(t, distinctFoldMessages(rounds, size))
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

// newFoldServer builds the smallest server the fold request path needs: one
// session that may fold, a fresh archive, and a fresh render cache.
// Callers adjust s.liveCompact before the first request.
func newFoldServer(t *testing.T, cfg LiveCompactConfig) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	server := &Server{
		sessions:     map[string]*sessionState{"tok": {foldAllowed: true}},
		liveCompact:  cfg.Normalized(),
		archive:      newArchiveStore(dir, time.Hour, archiveMaxBytes),
		pointerCache: newPointerCache(),
	}
	return server, dir
}
