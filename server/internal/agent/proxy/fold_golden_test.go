package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Golden output of the fold request path.
//
// The planner moved from this package into the fold kernel, and an equivalence
// refactor is only worth its risk if the forwarded bytes are provably identical.
// These five bodies cover the shapes the planner treats differently — one big
// result, several rounds, a failed result, a result that cannot be folded, and a
// call still waiting for its answer — and the test pins the sha256 of what
// foldRequestLedger would forward for each, plus the decision it reported.
//
// The archive directory is fixed rather than per-test: a pointer embeds the
// archive path, so a random directory would make the hash meaningless. Run with
// UPDATE_GOLDEN=1 to regenerate after an intentional behaviour change.

const foldGoldenDir = "/tmp/mindfs-fold-golden"

type foldGoldenCase struct {
	name string
	body func(t *testing.T) []byte
}

func foldGoldenCases() []foldGoldenCase {
	return []foldGoldenCase{
		{
			name: "single_result",
			body: func(t *testing.T) []byte {
				return wireBody(t, []any{
					userText("read the file"),
					assistantToolUse("toolu_1", "Read"),
					userToolResult("toolu_1", bigText("single"), false),
				})
			},
		},
		{
			name: "multi_round",
			body: func(t *testing.T) []byte {
				return wireBody(t, []any{
					userText("read both"),
					assistantToolUse("toolu_1", "Read"),
					userToolResult("toolu_1", bigText("first"), false),
					assistantToolUse("toolu_2", "Grep"),
					userToolResult("toolu_2", bigText("second"), false),
				})
			},
		},
		{
			name: "is_error",
			body: func(t *testing.T) []byte {
				return wireBody(t, []any{
					userText("run it"),
					assistantToolUse("toolu_1", "Bash"),
					userToolResult("toolu_1", bigText("failure"), true),
				})
			},
		},
		{
			name: "image_result",
			body: func(t *testing.T) []byte {
				return wireBody(t, []any{
					userText("look at this"),
					assistantToolUse("toolu_1", "Read"),
					userToolResultBlocks("toolu_1", []any{
						map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": "aGVsbG8="}},
					}, false),
					assistantToolUse("toolu_2", "Read"),
					userToolResult("toolu_2", bigText("textual"), false),
				})
			},
		},
		{
			name: "open_round",
			body: func(t *testing.T) []byte {
				return wireBody(t, []any{
					userText("read it"),
					assistantToolUse("toolu_1", "Read"),
					userToolResult("toolu_1", bigText("done"), false),
					assistantToolUse("toolu_2", "Read"),
				})
			},
		},
	}
}

// newGoldenServer builds the smallest server foldRequestLedger needs: one
// foldable session, the gate wide open, a fresh archive and render cache.
func newGoldenServer(t *testing.T, dir string) *Server {
	t.Helper()
	cfg := foldTestConfig(dir)
	cfg.RailTokens = 1
	cfg.MinFoldBytes = 1
	cfg.MaxFoldBytes = 0
	// No keep window: every closed round is eligible, so each fixture exercises
	// the render/splice path instead of being protected as "recent".
	cfg.KeepRounds = 0
	cfg.KeepTailTokens = 1 << 30
	return &Server{
		sessions:     map[string]*sessionState{"tok": {foldAllowed: true}},
		liveCompact:  cfg,
		archive:      newArchiveStore(dir, time.Hour, archiveMaxBytes),
		pointerCache: newPointerCache(),
	}
}

func TestFoldGolden(t *testing.T) {
	update := os.Getenv("UPDATE_GOLDEN") == "1"
	path := filepath.Join("testdata", "fold_golden.txt")

	var lines []string
	for _, tc := range foldGoldenCases() {
		if err := os.RemoveAll(foldGoldenDir); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(foldGoldenDir, 0o700); err != nil {
			t.Fatal(err)
		}
		server := newGoldenServer(t, foldGoldenDir)
		body, result, pending, gate := server.foldRequestLedger("tok", tc.body(t))
		sum := sha256.Sum256(body)
		lines = append(lines, fmt.Sprintf(
			"%s sha256=%s folded=%t new=%d sticky=%d total=%d tokens=%d skipped=%d decision=%s pending=%t",
			tc.name, hex.EncodeToString(sum[:]), result.changed(), result.newBlocks, result.stickyBlocks,
			result.foldedBlocks, result.foldedTokens, result.skipped, gate.decision, pending != nil,
		))
	}
	got := strings.Join(lines, "\n") + "\n"

	if update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("recorded first golden at %s", path)
		return
	}
	if string(want) != got {
		t.Fatalf("fold output changed\n--- want ---\n%s--- got ---\n%s", want, got)
	}
}
