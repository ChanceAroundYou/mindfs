package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	rootfs "mindfs/server/internal/fs"
)

func TestManagerNormalizesHistoricalForkParentLinks(t *testing.T) {
	ctx := context.Background()
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	dbFile := filepath.Join(root.MetaDir(), "sessions", "session-list.db")
	if err := os.MkdirAll(filepath.Dir(dbFile), 0o755); err != nil {
		t.Fatalf("make session db dir: %v", err)
	}
	db, err := openSQLiteDB(dbFile)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	if _, err := db.Exec(sessionTableSchema); err != nil {
		db.Close()
		t.Fatalf("create sessions table: %v", err)
	}
	const insert = `INSERT INTO sessions (
		key, type, parent_session_key, parent_tool_call_id, source, task_id, model, shell,
		plan_mode, name, related_files_json, related_worktree_json,
		last_context_window_total_tokens, last_context_window_model_context_window,
		created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, '', '', '', 0, ?, '[]', '', 0, 0, ?, ?)`
	now := time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	forkSource := `{"type":"fork","session_key":"source","seq":2,"agent":"claude","agent_session_id":"agent-source"}`
	if _, err := db.Exec(insert, "fork", TypeChat, "source", "fork-tool", forkSource, "Fork", now, now); err != nil {
		db.Close()
		t.Fatalf("insert historical fork: %v", err)
	}
	if _, err := db.Exec(insert, "subagent", TypeChat, "source", "spawn-tool", "", "Subagent", now, now); err != nil {
		db.Close()
		t.Fatalf("insert subagent: %v", err)
	}
	if _, err := db.Exec(insert, "unknown", TypeChat, "source", "unknown-tool", `not json`, "Unknown", now, now); err != nil {
		db.Close()
		t.Fatalf("insert unrecognized record: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close raw db: %v", err)
	}

	manager := NewManager(root)
	fork, err := manager.Get(ctx, "fork", 0)
	if err != nil {
		t.Fatalf("get normalized fork: %v", err)
	}
	if fork.ParentSessionKey != "" || fork.ParentToolCallID != "" {
		t.Fatalf("fork parents = (%q, %q), want empty", fork.ParentSessionKey, fork.ParentToolCallID)
	}
	if fork.Source != forkSource || fork.Name != "Fork" || !fork.UpdatedAt.Equal(time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("fork metadata changed: %#v", fork)
	}
	subagent, err := manager.Get(ctx, "subagent", 0)
	if err != nil {
		t.Fatalf("get subagent: %v", err)
	}
	if subagent.ParentSessionKey != "source" || subagent.ParentToolCallID != "spawn-tool" {
		t.Fatalf("subagent parents = (%q, %q), want (source, spawn-tool)", subagent.ParentSessionKey, subagent.ParentToolCallID)
	}
	unknown, err := manager.Get(ctx, "unknown", 0)
	if err != nil {
		t.Fatalf("get unrecognized record: %v", err)
	}
	if unknown.ParentSessionKey != "source" || unknown.ParentToolCallID != "unknown-tool" {
		t.Fatalf("unrecognized record parents = (%q, %q), want (source, unknown-tool)", unknown.ParentSessionKey, unknown.ParentToolCallID)
	}

	if err := manager.db.Close(); err != nil {
		t.Fatalf("close normalized db: %v", err)
	}
	manager.db = nil
	fork, err = manager.Get(ctx, "fork", 0)
	if err != nil {
		t.Fatalf("get normalized fork after reopen: %v", err)
	}
	if fork.ParentSessionKey != "" || fork.ParentToolCallID != "" {
		t.Fatalf("fork parents after reopen = (%q, %q), want empty", fork.ParentSessionKey, fork.ParentToolCallID)
	}
}

func TestManagerRestoresExternalSessionNameFromHistoricalBinding(t *testing.T) {
	ctx := context.Background()
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	dbFile := filepath.Join(root.MetaDir(), "sessions", "session-list.db")
	if err := os.MkdirAll(filepath.Dir(dbFile), 0o755); err != nil {
		t.Fatalf("make session db dir: %v", err)
	}
	db, err := openSQLiteDB(dbFile)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	if _, err := db.Exec(sessionTableSchema); err != nil {
		db.Close()
		t.Fatalf("create sessions table: %v", err)
	}
	if _, err := db.Exec(agentBindingTableSchema); err != nil {
		db.Close()
		t.Fatalf("create bindings table: %v", err)
	}
	const insert = `INSERT INTO sessions (
		key, type, parent_session_key, parent_tool_call_id, source, task_id, model, shell,
		plan_mode, name, related_files_json, related_worktree_json,
		last_context_window_total_tokens, last_context_window_model_context_window,
		created_at, updated_at
	) VALUES (?, ?, '', '', '', '', '', '', 0, ?, '[]', '', 0, 0, ?, ?)`
	updatedAt := time.Date(2026, 8, 23, 1, 2, 3, 0, time.UTC).Format(time.RFC3339Nano)
	if _, err := db.Exec(insert, "legacy-key", TypeChat, "MindFS import title", updatedAt, updatedAt); err != nil {
		db.Close()
		t.Fatalf("insert historical session: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO session_agent_bindings (session_key, agent, agent_session_id, agent_ctx_seq) VALUES (?, ?, ?, 0)`, "legacy-key", "claude", "external-1"); err != nil {
		db.Close()
		t.Fatalf("insert historical binding: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close raw db: %v", err)
	}

	manager := NewManager(root)
	if _, err := manager.Get(ctx, "legacy-key", 0); err != nil {
		t.Fatalf("open historical session db: %v", err)
	}
	if got, ok := manager.LookupAliasForAgent("claude", "external-1"); !ok || got != "MindFS import title" {
		t.Fatalf("external alias = (%q, %t), want (%q, true)", got, ok, "MindFS import title")
	}
	if err := manager.Delete(ctx, "legacy-key"); err != nil {
		t.Fatalf("delete historical session: %v", err)
	}
	if got, ok := manager.LookupAliasForAgent("claude", "external-1"); !ok || got != "MindFS import title" {
		t.Fatalf("external alias after delete = (%q, %t), want (%q, true)", got, ok, "MindFS import title")
	}
	if _, ok := manager.LookupAliasForAgent("codex", "external-1"); ok {
		t.Fatal("external alias matched a different agent")
	}
	if err := manager.db.Close(); err != nil {
		t.Fatalf("close normalized db: %v", err)
	}
	manager.db = nil
	if got, ok := manager.LookupAliasForAgent("claude", "external-1"); !ok || got != "MindFS import title" {
		t.Fatalf("external alias after reopen = (%q, %t), want (%q, true)", got, ok, "MindFS import title")
	}
}

func TestManagerRenameUpdatesExternalSessionName(t *testing.T) {
	ctx := context.Background()
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	created, err := manager.Create(ctx, CreateInput{Type: TypeChat, Agent: "claude", Name: "Initial"})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.UpdateAgentState(ctx, created, "claude", 0, "external-rename"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Rename(ctx, created.Key, "Renamed in MindFS"); err != nil {
		t.Fatal(err)
	}
	if got, ok := manager.LookupAliasForAgent("claude", "external-rename"); !ok || got != "Renamed in MindFS" {
		t.Fatalf("external alias = (%q, %t), want (%q, true)", got, ok, "Renamed in MindFS")
	}
}
