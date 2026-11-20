package codex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
)

// 复现 2026-11-20 的现场：MindFS 侧 codex 助手行比 rollout 里的 turn 多
// （实测 12 行 vs 9 turn），按行号取序号会越界；按目标行的落盘时间取
// 「最后一条不晚于它的助手 turn」可以稳定命中。
func TestCodexResolveForkPointPrefersTargetTimestamp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	rootPath := filepath.Join(home, "proj")
	if err := os.MkdirAll(rootPath, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionsDir := filepath.Join(home, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `{"timestamp":"2026-07-28T01:00:00Z","type":"session_meta","payload":{"id":"thread-1","cwd":"` + rootPath + `"}}
{"timestamp":"2026-07-28T01:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"u1"}]}}
{"timestamp":"2026-07-28T01:00:02Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"a1"}]}}
{"timestamp":"2026-07-28T01:00:03Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"u2"}]}}
{"timestamp":"2026-07-28T01:00:04Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"a2"}]}}
`
	if err := os.WriteFile(filepath.Join(sessionsDir, "rollout-thread-1.jsonl"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	imp := NewImporter(ImporterOptions{AgentName: "codex"})
	ctx := context.Background()

	// 旧路径：MindFS 多出来的第 3 行在 rollout 里没有对应 turn → 越界。
	if _, err := imp.ResolveForkPointByAgentTurnIndex(ctx, agenttypes.ResolveForkPointInput{
		RootPath:       rootPath,
		AgentSessionID: "thread-1",
		AgentTurnIndex: 3,
	}); err == nil {
		t.Fatal("index=3 应当复现 agent turn index out of range")
	}

	out, err := imp.ResolveForkPointByAgentTurnIndex(ctx, agenttypes.ResolveForkPointInput{
		RootPath:        rootPath,
		AgentSessionID:  "thread-1",
		AgentTurnIndex:  3,
		TargetTimestamp: time.Date(2026, 7, 28, 1, 0, 5, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("timestamp resolve failed: %v", err)
	}
	if out.Kind != agenttypes.ForkPointCodexUserOrdinal || out.CodexUserOrdinal != 2 {
		t.Fatalf("fork point = %#v, want ordinal 2", out)
	}

	first, err := imp.ResolveForkPointByAgentTurnIndex(ctx, agenttypes.ResolveForkPointInput{
		RootPath:        rootPath,
		AgentSessionID:  "thread-1",
		TargetTimestamp: time.Date(2026, 7, 28, 1, 0, 3, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("first turn resolve failed: %v", err)
	}
	if first.CodexUserOrdinal != 1 {
		t.Fatalf("first turn ordinal = %d, want 1", first.CodexUserOrdinal)
	}
}
