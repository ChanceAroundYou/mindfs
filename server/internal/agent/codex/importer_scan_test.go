package codex

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
)

func TestExternalSessionFileCursorDetectsUnchangedAndChangedFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cursor, unchanged, err := externalSessionFileCursor(path, agenttypes.ExternalSessionCursor{})
	if err != nil || unchanged {
		t.Fatalf("first cursor: unchanged=%v err=%v", unchanged, err)
	}
	if _, unchanged, err = externalSessionFileCursor(path, cursor); err != nil || !unchanged {
		t.Fatalf("second cursor: unchanged=%v err=%v", unchanged, err)
	}
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, unchanged, err = externalSessionFileCursor(path, cursor); err != nil || unchanged {
		t.Fatalf("changed cursor: unchanged=%v err=%v", unchanged, err)
	}
}

func TestReadCodexImportedSubagentsLinksSpawnCall(t *testing.T) {
	dir := t.TempDir()
	parentPath := filepath.Join(dir, "parent.jsonl")
	childPath := filepath.Join(dir, "child.jsonl")
	parent := `{"timestamp":"2026-07-28T01:00:00Z","type":"session_meta","payload":{"id":"parent","cwd":"/project"}}
{"timestamp":"2026-07-28T01:00:01Z","type":"response_item","payload":{"type":"function_call","name":"spawn_agent","call_id":"spawn-1","arguments":"{\"agent_type\":\"explorer\",\"message\":\"inspect importer\"}"}}
{"timestamp":"2026-07-28T01:00:02Z","type":"response_item","payload":{"type":"function_call_output","call_id":"spawn-1","output":"{\"agent_id\":\"child\",\"nickname\":\"Darwin\"}"}}
`
	child := `{"timestamp":"2026-07-28T01:00:03Z","type":"session_meta","payload":{"id":"child","cwd":"/project","source":{"subagent":{"thread_spawn":{"parent_thread_id":"parent","depth":1,"agent_nickname":"Darwin","agent_role":"default"}}}}}
{"timestamp":"2026-07-28T01:00:04Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"inspect importer"}]}}
{"timestamp":"2026-07-28T01:00:05Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"found it"}]}}
`
	if err := os.WriteFile(parentPath, []byte(parent), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(childPath, []byte(child), 0o600); err != nil {
		t.Fatal(err)
	}
	importer := NewImporter(ImporterOptions{AgentName: "codex"})
	importer.baseDir = dir
	root, ok, err := inspectCodexSessionFile(parentPath)
	if err != nil || !ok {
		t.Fatalf("inspect parent: ok=%v err=%v", ok, err)
	}
	items, err := importer.readImportedSubagents(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("len(subagents) = %d, want 1: %#v", len(items), items)
	}
	item := items[0]
	if item.AgentSessionID != "child" || item.ParentAgentSessionID != "" ||
		item.ParentToolCallID != "spawn-1" || item.Title != "Darwin" {
		t.Fatalf("subagent = %#v", item)
	}
	if len(item.Exchanges) != 2 {
		t.Fatalf("len(exchanges) = %d, want 2", len(item.Exchanges))
	}
}

// worktree 会话的 cwd 是工作树路径：索引快路径与 fork 定位必须接受 .worktree/* 归属的 cwd，
// 否则 fork/同步报 external session not found。导入回退本身按 ID 全局扫描，仅需校验 cwd 归属。
func TestResolveForkPointByAgentTurnIndexAcceptsWorktreeCwd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	imp := NewImporter(ImporterOptions{AgentName: "codex"})
	mainPath := filepath.Join(home, "proj")
	wtPath := filepath.Join(mainPath, ".worktree", "task-5")
	sessionsDir := filepath.Join(home, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `{"timestamp":"2026-07-28T01:00:00Z","type":"session_meta","payload":{"id":"wt-id","cwd":"` + wtPath + `"}}
{"timestamp":"2026-07-28T01:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"u1"}]}}
{"timestamp":"2026-07-28T01:00:02Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"a1"}]}}
{"timestamp":"2026-07-28T01:00:03Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"u2"}]}}
{"timestamp":"2026-07-28T01:00:04Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"a2"}]}}
`
	if err := os.WriteFile(filepath.Join(sessionsDir, "rollout-wt.jsonl"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := imp.ResolveForkPointByAgentTurnIndex(context.Background(), agenttypes.ResolveForkPointInput{
		RootPath:       mainPath,
		AgentSessionID: "wt-id",
		AgentTurnIndex: 1,
	})
	if err != nil {
		t.Fatalf("ResolveForkPointByAgentTurnIndex(worktree cwd) failed: %v", err)
	}
	if out.AgentSessionID != "wt-id" || out.CodexUserOrdinal != 1 {
		t.Fatalf("fork point = %#v", out)
	}
}

func TestCodexCwdMatchesRoot(t *testing.T) {
	root := "/data/proj"
	cases := map[string]bool{
		root:                       true,
		root + "/.worktree/task-5": true,
		root + "/.worktree-evil/x": false,
		"/elsewhere/proj":          false,
	}
	for cwd, want := range cases {
		if got := cwdMatchesRoot(cwd, root); got != want {
			t.Fatalf("cwdMatchesRoot(%q, %q) = %v, want %v", cwd, root, got, want)
		}
	}
}
