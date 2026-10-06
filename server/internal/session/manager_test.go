package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	rootfs "mindfs/server/internal/fs"
)

func TestManagerUsesSessionDBLink(t *testing.T) {
	rootDir := t.TempDir()
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	manager := NewManager(root)

	linkedDB := filepath.Join(t.TempDir(), "session-list.db")
	linkFile := filepath.Join(root.MetaDir(), "sessions", "session-list.db.link")
	if err := writeSessionDBLink(linkFile, linkedDB); err != nil {
		t.Fatalf("write link: %v", err)
	}

	if _, err := manager.Create(context.Background(), CreateInput{Type: TypeChat, Name: "Linked"}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root.MetaDir(), "sessions", "session-list.db")); err == nil {
		t.Fatalf("legacy session-list.db should not be created when link exists")
	}
	if _, err := os.Stat(linkedDB); err != nil {
		t.Fatalf("stat linked db: %v", err)
	}
}

func TestManagerExternalCursorSurvivesAgentStateUpdate(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	created, err := manager.Create(context.Background(), CreateInput{Type: TypeChat, Agent: "codex", Name: "Cursor"})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.UpsertAgentBinding(context.Background(), AgentBinding{SessionKey: created.Key, Agent: "codex", AgentSessionID: "external-1"}); err != nil {
		t.Fatal(err)
	}
	cursor := agenttypes.ExternalSessionCursor{SourcePath: "/tmp/session.jsonl", Offset: 1234, ModTimeUnixNano: 5678}
	if err := manager.UpdateExternalSessionCursor(context.Background(), created.Key, "codex", cursor); err != nil {
		t.Fatal(err)
	}
	if err := manager.UpdateAgentState(context.Background(), created, "codex", 3, "external-1"); err != nil {
		t.Fatal(err)
	}
	binding, err := manager.GetAgentBinding(context.Background(), created.Key, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if binding.ExternalCursor() != cursor {
		t.Fatalf("cursor = %#v, want %#v", binding.ExternalCursor(), cursor)
	}
}

func TestManagerRecordRelatedWorktreeDoesNotOverwriteExisting(t *testing.T) {
	rootDir := t.TempDir()
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	manager := NewManager(root)

	created, err := manager.Create(context.Background(), CreateInput{Type: TypeChat, Name: "Worktree"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	firstPath := filepath.Join(rootDir, "..", "mindfs-worktree-a")
	secondPath := filepath.Join(rootDir, "..", "mindfs-worktree-b")
	if added, err := manager.RecordRelatedWorktree(context.Background(), created.Key, root.ID, firstPath, "feature/a", "abc123"); err != nil {
		t.Fatalf("record first worktree: %v", err)
	} else if !added {
		t.Fatal("record first worktree added = false, want true")
	}
	if added, err := manager.RecordRelatedWorktree(context.Background(), created.Key, root.ID, secondPath, "feature/b", "def456"); err != nil {
		t.Fatalf("record second worktree: %v", err)
	} else if added {
		t.Fatal("record second worktree added = true, want false")
	}

	current, err := manager.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if current.RelatedWorktree == nil {
		t.Fatal("RelatedWorktree is nil")
	}
	if current.RelatedWorktree.Path != filepath.Clean(firstPath) {
		t.Fatalf("RelatedWorktree.Path = %q, want %q", current.RelatedWorktree.Path, filepath.Clean(firstPath))
	}
	if current.RelatedWorktree.Branch != "feature/a" {
		t.Fatalf("RelatedWorktree.Branch = %q, want feature/a", current.RelatedWorktree.Branch)
	}
}

func TestManagerRelatedFilesAreScopedByRepoHeadAndPath(t *testing.T) {
	rootDir := t.TempDir()
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	manager := NewManager(root)

	created, err := manager.Create(context.Background(), CreateInput{Type: TypeChat, Name: "Related repos"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	repoA := filepath.Join(rootDir, "repo-a")
	repoB := filepath.Join(rootDir, "repo-b")
	for _, repo := range []string{repoA, repoB} {
		if err := manager.RecordOutputFileInRepo(context.Background(), created.Key, root.ID, "git", repo, filepath.Base(repo), "src/main.go", "abc123"); err != nil {
			t.Fatalf("record related file %s: %v", repo, err)
		}
	}
	if err := manager.RecordOutputFileInRepo(context.Background(), created.Key, root.ID, "git", repoA, filepath.Base(repoA), "src/main.go", "abc123"); err != nil {
		t.Fatalf("record duplicate related file: %v", err)
	}

	current, err := manager.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if len(current.RelatedFiles) != 2 {
		t.Fatalf("related files len = %d, want 2: %#v", len(current.RelatedFiles), current.RelatedFiles)
	}

	if err := manager.RemoveRelatedFileAtHead(context.Background(), created.Key, "src/main.go", "abc123", repoA, "git"); err != nil {
		t.Fatalf("remove related file: %v", err)
	}
	current, err = manager.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatalf("get after remove: %v", err)
	}
	if len(current.RelatedFiles) != 1 {
		t.Fatalf("related files len after remove = %d, want 1: %#v", len(current.RelatedFiles), current.RelatedFiles)
	}
	if current.RelatedFiles[0].RepoPath != filepath.Clean(repoB) {
		t.Fatalf("remaining repo = %q, want %q", current.RelatedFiles[0].RepoPath, filepath.Clean(repoB))
	}
}

func TestManagerRecordsSubSessionRelatedFileOnParent(t *testing.T) {
	rootDir := t.TempDir()
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	manager := NewManager(root)

	parent, err := manager.Create(context.Background(), CreateInput{Type: TypeChat, Name: "Parent"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := manager.Create(context.Background(), CreateInput{
		Type:             TypeChat,
		ParentSessionKey: parent.Key,
		Name:             "Child",
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	repo := filepath.Join(rootDir, "repo")
	if err := manager.RecordOutputFileInRepo(context.Background(), child.Key, root.ID, "git", repo, filepath.Base(repo), "src/main.go", "abc123"); err != nil {
		t.Fatalf("record child related file: %v", err)
	}
	if err := manager.RecordOutputFileInRepo(context.Background(), child.Key, root.ID, "git", repo, filepath.Base(repo), "src/main.go", "abc123"); err != nil {
		t.Fatalf("record duplicate child related file: %v", err)
	}

	loadedChild, err := manager.Get(context.Background(), child.Key, 0)
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	loadedParent, err := manager.Get(context.Background(), parent.Key, 0)
	if err != nil {
		t.Fatalf("get parent: %v", err)
	}
	for label, sess := range map[string]*Session{"child": loadedChild, "parent": loadedParent} {
		if len(sess.RelatedFiles) != 1 {
			t.Fatalf("%s related files len = %d, want 1: %#v", label, len(sess.RelatedFiles), sess.RelatedFiles)
		}
		file := sess.RelatedFiles[0]
		if file.Path != "src/main.go" || file.Head != "abc123" || file.RepoPath != filepath.Clean(repo) || file.RepoKind != "git" {
			t.Fatalf("%s related file = %#v", label, file)
		}
	}
}

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

func TestManagerDoesNotPropagateForkRelatedFilesToSource(t *testing.T) {
	ctx := context.Background()
	rootDir := t.TempDir()
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	manager := NewManager(root)

	source, err := manager.Create(ctx, CreateInput{Type: TypeChat, Name: "Source"})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	fork, err := manager.Create(ctx, CreateInput{
		Type:   TypeChat,
		Source: `{"type":"fork","session_key":"source","seq":1}`,
		Name:   "Fork",
	})
	if err != nil {
		t.Fatalf("create fork: %v", err)
	}
	repo := filepath.Join(rootDir, "repo")
	if err := manager.RecordOutputFileInRepo(ctx, fork.Key, root.ID, "git", repo, filepath.Base(repo), "src/fork.go", "abc123"); err != nil {
		t.Fatalf("record fork related file: %v", err)
	}
	loadedFork, err := manager.Get(ctx, fork.Key, 0)
	if err != nil {
		t.Fatalf("get fork: %v", err)
	}
	loadedSource, err := manager.Get(ctx, source.Key, 0)
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	if len(loadedFork.RelatedFiles) != 1 {
		t.Fatalf("fork related files = %#v, want one", loadedFork.RelatedFiles)
	}
	if len(loadedSource.RelatedFiles) != 0 {
		t.Fatalf("source related files = %#v, want none", loadedSource.RelatedFiles)
	}
}

func TestManagerFallsBackToUserDataSessionDBOnSQLitePanic(t *testing.T) {
	rootDir := t.TempDir()
	root := rootfs.NewRootInfo("panic-root", "panic-root", rootDir)
	manager := NewManager(root)

	originalOpen := openSQLiteDB
	originalConfigDir := mindFSConfigDir
	defer func() {
		openSQLiteDB = originalOpen
		mindFSConfigDir = originalConfigDir
	}()
	configDir := t.TempDir()
	mindFSConfigDir = func() (string, error) {
		return configDir, nil
	}

	var opened []string
	openSQLiteDB = func(path string) (*sql.DB, error) {
		opened = append(opened, path)
		if strings.Contains(path, rootDir) {
			panic("sqlite legacy panic")
		}
		return originalOpen(path)
	}

	if _, err := manager.Create(context.Background(), CreateInput{Type: TypeChat, Name: "Fallback"}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if len(opened) < 2 {
		t.Fatalf("opened paths = %#v, want legacy then fallback", opened)
	}
	linkFile := filepath.Join(root.MetaDir(), "sessions", "session-list.db.link")
	payload, err := root.ReadMetaFile("sessions/session-list.db.link")
	if err != nil {
		t.Fatalf("read link: %v", err)
	}
	linked := strings.TrimSpace(string(payload))
	if linked == "" || strings.Contains(linked, rootDir) {
		t.Fatalf("link target = %q, want user-data path", linked)
	}
	if got, ok, err := readSessionDBLink(linkFile); err != nil || !ok || got != linked {
		t.Fatalf("readSessionDBLink = %q, %v, %v; want %q, true, nil", got, ok, err, linked)
	}
}

func TestManagerPersistsParentSessionMetadata(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)

	created, err := manager.Create(context.Background(), CreateInput{
		Type:             TypeChat,
		ParentSessionKey: "parent-session",
		ParentToolCallID: "tool-call-1",
		Agent:            "codex",
		Model:            "gpt-test",
		Name:             "Subagent",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	loaded, err := manager.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if loaded.ParentSessionKey != "parent-session" {
		t.Fatalf("ParentSessionKey = %q", loaded.ParentSessionKey)
	}
	if loaded.ParentToolCallID != "tool-call-1" {
		t.Fatalf("ParentToolCallID = %q", loaded.ParentToolCallID)
	}
}

func TestManagerPersistsArchivedAtWithoutChangingUpdatedAt(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	manager := NewManager(root, WithClock(func() time.Time { return now }))

	created, err := manager.Create(context.Background(), CreateInput{Type: TypeChat, Name: "Archived"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	originalUpdatedAt := created.UpdatedAt

	archivedAt := now.Add(5 * time.Minute)
	manager.now = func() time.Time { return archivedAt }
	archived, err := manager.SetArchived(context.Background(), created.Key, true)
	if err != nil {
		t.Fatalf("archive session: %v", err)
	}
	if archived.ArchivedAt == nil || !archived.ArchivedAt.Equal(archivedAt) {
		t.Fatalf("ArchivedAt = %v, want %v", archived.ArchivedAt, archivedAt)
	}
	if !archived.UpdatedAt.Equal(originalUpdatedAt) {
		t.Fatalf("UpdatedAt changed on archive: got %v, want %v", archived.UpdatedAt, originalUpdatedAt)
	}

	manager.sessions = map[string]*Session{}
	loaded, err := manager.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if loaded.ArchivedAt == nil || !loaded.ArchivedAt.Equal(archivedAt) {
		t.Fatalf("reloaded ArchivedAt = %v, want %v", loaded.ArchivedAt, archivedAt)
	}

	clearedAt := now.Add(10 * time.Minute)
	manager.now = func() time.Time { return clearedAt }
	cleared, err := manager.SetArchived(context.Background(), created.Key, false)
	if err != nil {
		t.Fatalf("unarchive session: %v", err)
	}
	if cleared.ArchivedAt != nil {
		t.Fatalf("ArchivedAt after unarchive = %v, want nil", cleared.ArchivedAt)
	}
	if !cleared.UpdatedAt.Equal(originalUpdatedAt) {
		t.Fatalf("UpdatedAt changed on unarchive: got %v, want %v", cleared.UpdatedAt, originalUpdatedAt)
	}
}

func TestSetArchivedIsIdempotent(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	manager := NewManager(root, WithClock(func() time.Time { return now }))
	ctx := context.Background()

	created, err := manager.Create(ctx, CreateInput{Type: TypeChat, Name: "Twice"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	first, err := manager.SetArchived(ctx, created.Key, true)
	if err != nil {
		t.Fatalf("first archive: %v", err)
	}
	later := now.Add(time.Hour)
	manager.now = func() time.Time { return later }
	second, err := manager.SetArchived(ctx, created.Key, true)
	if err != nil {
		t.Fatalf("second archive should be a no-op: %v", err)
	}
	// 已是目标态就早返回：不重写 archived_at（否则时间戳会跟着漂）
	if !second.ArchivedAt.Equal(*first.ArchivedAt) {
		t.Fatalf("ArchivedAt drifted on repeat archive: %v -> %v", first.ArchivedAt, second.ArchivedAt)
	}
	third, err := manager.SetArchived(ctx, created.Key, false)
	if err != nil {
		t.Fatalf("first unarchive: %v", err)
	}
	if third.ArchivedAt != nil {
		t.Fatalf("ArchivedAt = %v, want nil", third.ArchivedAt)
	}
	if _, err := manager.SetArchived(ctx, created.Key, false); err != nil {
		t.Fatalf("repeat unarchive should be a no-op: %v", err)
	}
}

func TestListExcludesArchivedByDefaultAndSupportsArchivedOnly(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	ctx := context.Background()

	plain, err := manager.Create(ctx, CreateInput{Type: TypeChat, Name: "Plain"})
	if err != nil {
		t.Fatalf("create plain: %v", err)
	}
	hidden, err := manager.Create(ctx, CreateInput{Type: TypeChat, Name: "Hidden"})
	if err != nil {
		t.Fatalf("create hidden: %v", err)
	}
	if _, err := manager.SetArchived(ctx, hidden.Key, true); err != nil {
		t.Fatalf("archive: %v", err)
	}

	// 默认（主面板）：排除归档
	items, err := manager.List(ctx, ListOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 || items[0].Key != plain.Key {
		keys := make([]string, 0, len(items))
		for _, item := range items {
			keys = append(keys, item.Key)
		}
		t.Fatalf("default list should exclude archived, got %v", keys)
	}
	count, err := manager.Count(ctx, ListOptions{})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("default count = %d, want 1", count)
	}

	// only（归档区）：只要归档的
	archived, err := manager.List(ctx, ListOptions{ArchivedMode: "only"})
	if err != nil {
		t.Fatalf("list archived: %v", err)
	}
	if len(archived) != 1 || archived[0].Key != hidden.Key {
		t.Fatalf("archived list = %v, want only %s", archived, hidden.Key)
	}
	archivedCount, err := manager.Count(ctx, ListOptions{ArchivedMode: "only"})
	if err != nil {
		t.Fatalf("count archived: %v", err)
	}
	if archivedCount != 1 {
		t.Fatalf("archived count = %d, want 1", archivedCount)
	}

	// 取消归档后回到主列表
	if _, err := manager.SetArchived(ctx, hidden.Key, false); err != nil {
		t.Fatalf("unarchive: %v", err)
	}
	items, err = manager.List(ctx, ListOptions{})
	if err != nil {
		t.Fatalf("list after unarchive: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("after unarchive list = %d items, want 2", len(items))
	}
}

// 置顶不再存在会话库里（权威在主节点的置顶表），但「归档后从置顶区掉进归档区」
// 这条**产品规则**还在 —— 由 api 层过滤。所以这里守的是 ListByKeys 那道过滤：
// 归档的会话不该被当成「已置顶」捞出来。
func TestListByKeysCanExcludeArchivedSessions(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	ctx := context.Background()

	created, err := manager.Create(ctx, CreateInput{Type: TypeChat, Name: "PinnedThenArchived"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	found, err := manager.ListByKeys(ctx, []string{created.Key})
	if err != nil {
		t.Fatalf("ListByKeys: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("ListByKeys = %d, want 1", len(found))
	}

	if _, err := manager.SetArchived(ctx, created.Key, true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	// 归档不该让会话从库里消失（深链接仍要能开），所以这里只钉住「还在」，
	// 过滤发生在 api 层按 archived 标记挑的那一层。
	found, err = manager.ListByKeys(ctx, []string{created.Key})
	if err != nil {
		t.Fatalf("ListByKeys after archive: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("归档后会话必须仍能查到, got %d", len(found))
	}
	if found[0].ArchivedAt == nil {
		t.Fatal("归档标记丢了")
	}
}

// ListByKeys 只认本机库里有的键：置顶表跨节点，别的机器的键查不到就静默丢弃，
// 不能报错也不能凭空造出条目。顺序按传入顺序，不按库里的顺序。
func TestListByKeysSkipsUnknownKeysAndKeepsOrder(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	ctx := context.Background()

	a, err := manager.Create(ctx, CreateInput{Type: TypeChat, Name: "A"})
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	b, err := manager.Create(ctx, CreateInput{Type: TypeChat, Name: "B"})
	if err != nil {
		t.Fatalf("create b: %v", err)
	}
	got, err := manager.ListByKeys(ctx, []string{"别的机器的键", b.Key, a.Key, b.Key})
	if err != nil {
		t.Fatalf("ListByKeys: %v", err)
	}
	if len(got) != 2 || got[0].Key != b.Key || got[1].Key != a.Key {
		t.Fatalf("ListByKeys = %v, want [%s %s]", keysOf(got), b.Key, a.Key)
	}
	if empty, err := manager.ListByKeys(ctx, nil); err != nil || len(empty) != 0 {
		t.Fatalf("空入参应返回空结果, got %v err %v", empty, err)
	}
}

func keysOf(items []*Session) []string {
	out := make([]string, 0, len(items))
	for _, s := range items {
		out = append(out, s.Key)
	}
	return out
}

func TestManagerPersistsExchangeModelDisplayName(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)

	created, err := manager.Create(context.Background(), CreateInput{
		Type:  TypeChat,
		Agent: "claude",
		Model: "opus",
		Name:  "Chat",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	ctx := WithExchangeModelDisplayName(context.Background(), "glm-4.7")
	if err := manager.AddExchangeForAgent(ctx, created, "agent", "reply", "claude", "", "", ""); err != nil {
		t.Fatalf("add exchange: %v", err)
	}

	loaded, err := manager.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if len(loaded.Exchanges) != 1 {
		t.Fatalf("exchange count = %d, want 1", len(loaded.Exchanges))
	}
	if got := loaded.Exchanges[0].Model; got != "opus" {
		t.Fatalf("exchange model = %q, want runtime id", got)
	}
	if got := loaded.Exchanges[0].ModelDisplayName; got != "glm-4.7" {
		t.Fatalf("exchange model display name = %q, want snapshot", got)
	}
}

func TestManagerPersistsExchangeTokenUsage(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	created, err := manager.Create(context.Background(), CreateInput{
		Type: TypeChat, Agent: "codex", Model: "gpt-test", Name: "Chat",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	cacheRead := 8_000
	usage := &agenttypes.TokenUsage{
		InputTokens: 10_000, OutputTokens: 1_200, CacheReadTokens: &cacheRead,
	}
	ctx := WithExchangeTokenUsage(context.Background(), usage)
	if err := manager.AddExchangeForAgent(ctx, created, "agent", "reply", "codex", "", "", ""); err != nil {
		t.Fatalf("add exchange: %v", err)
	}

	loaded, err := manager.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	got := loaded.Exchanges[0].TokenUsage
	if got == nil || got.InputTokens != 10_000 || got.OutputTokens != 1_200 {
		t.Fatalf("token usage = %#v", got)
	}
	if got.CacheReadTokens == nil || *got.CacheReadTokens != 8_000 {
		t.Fatalf("cache read = %#v", got.CacheReadTokens)
	}
}

func TestManagerPersistsLastContextWindow(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)

	created, err := manager.Create(context.Background(), CreateInput{
		Type:  TypeChat,
		Agent: "codex",
		Model: "gpt-test",
		Name:  "Chat",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	want := agenttypes.ContextWindow{
		TotalTokens:        12345,
		ModelContextWindow: 200000,
	}
	if err := manager.UpdateLastContextWindow(context.Background(), created, want); err != nil {
		t.Fatalf("update context window: %v", err)
	}
	manager.sessions = map[string]*Session{}

	loaded, err := manager.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if loaded.LastContextWindow != want {
		t.Fatalf("LastContextWindow = %#v, want %#v", loaded.LastContextWindow, want)
	}
}

func TestManagerStoresFullToolCallAndReturnsCompactedAux(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)

	created, err := manager.Create(context.Background(), CreateInput{
		Type: TypeChat,
		Name: "Chat",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	content := "full search output"
	err = manager.AddExchangeAux(context.Background(), created.Key, ExchangeAux{
		Seq:  2,
		Line: 1,
		ToolCall: &agenttypes.ToolCall{
			CallID:  "call-1",
			Title:   "search",
			Status:  "complete",
			Kind:    agenttypes.ToolKindSearch,
			Content: []agenttypes.ToolCallContentItem{{Type: "text", Text: content}},
			Meta:    map[string]any{"output": content, "query": "full"},
		},
	})
	if err != nil {
		t.Fatalf("add aux: %v", err)
	}

	aux, err := manager.GetExchangeAux(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatalf("get aux: %v", err)
	}
	if len(aux[2]) != 1 || aux[2][0].ToolCall == nil {
		t.Fatalf("aux[2] = %#v, want compacted toolcall", aux[2])
	}
	if len(aux[2][0].ToolCall.Content) != 0 {
		t.Fatalf("compacted content = %#v, want empty", aux[2][0].ToolCall.Content)
	}
	if output, ok := aux[2][0].ToolCall.Meta["output"]; ok {
		t.Fatalf("compacted meta output = %#v, want omitted", output)
	}
	if aux[2][0].ToolCall.Meta["query"] != "full" {
		t.Fatalf("compacted meta = %#v, want non-output keys preserved", aux[2][0].ToolCall.Meta)
	}

	toolCall, err := manager.GetFullToolCall(context.Background(), created.Key, "call-1")
	if err != nil {
		t.Fatalf("get full toolcall: %v", err)
	}
	if len(toolCall.Content) != 1 || !strings.Contains(toolCall.Content[0].Text, content) {
		t.Fatalf("full content = %#v, want %q", toolCall.Content, content)
	}
	if toolCall.Meta["output"] != content {
		t.Fatalf("full meta output = %#v, want %q", toolCall.Meta["output"], content)
	}
}

func TestManagerStoresPlanAndCompactAux(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)

	created, err := manager.Create(context.Background(), CreateInput{
		Type: TypeChat,
		Name: "Chat",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	if err := manager.AddExchangeAux(context.Background(), created.Key, ExchangeAux{
		Seq:  2,
		Line: 0,
		Plan: &agenttypes.PlanUpdate{
			ID:      "plan-1",
			Content: "- inspect\n- patch",
		},
	}); err != nil {
		t.Fatalf("add plan aux: %v", err)
	}
	if err := manager.AddExchangeAux(context.Background(), created.Key, ExchangeAux{
		Seq:  2,
		Line: 0,
		Compact: &agenttypes.CompactNotice{
			ID:     "compact-1",
			Status: "complete",
		},
	}); err != nil {
		t.Fatalf("add compact aux: %v", err)
	}

	aux, err := manager.GetExchangeAux(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatalf("get aux: %v", err)
	}
	if len(aux[2]) != 2 {
		t.Fatalf("aux[2] length = %d, want 2: %#v", len(aux[2]), aux[2])
	}
	if aux[2][0].Plan == nil || aux[2][0].Plan.Content != "- inspect\n- patch" {
		t.Fatalf("plan aux = %#v", aux[2][0])
	}
	if aux[2][1].Compact == nil || aux[2][1].Compact.Status != "complete" {
		t.Fatalf("compact aux = %#v", aux[2][1])
	}
}

func TestManagerStoresTodoAux(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)

	created, err := manager.Create(context.Background(), CreateInput{
		Type: TypeChat,
		Name: "Chat",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	if err := manager.AddExchangeAux(context.Background(), created.Key, ExchangeAux{
		Seq:  2,
		Line: 0,
		Todo: &agenttypes.TodoUpdate{
			Items: []agenttypes.TodoItem{{Content: "persist todos", Status: "in_progress"}},
		},
	}); err != nil {
		t.Fatalf("add todo aux: %v", err)
	}

	aux, err := manager.GetExchangeAux(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatalf("get aux: %v", err)
	}
	if len(aux[2]) != 1 || aux[2][0].Todo == nil {
		t.Fatalf("aux[2] = %#v, want todo aux", aux[2])
	}
	if got := aux[2][0].Todo.Items[0].Content; got != "persist todos" {
		t.Fatalf("todo content = %q, want persist todos", got)
	}
}

func TestManagerIncrementalExchangeLoad(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)

	created, err := manager.Create(context.Background(), CreateInput{Type: TypeChat, Agent: "claude", Name: "Incremental"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	ctx := context.Background()
	add := func(role, content string) {
		if err := manager.AddExchangeForAgent(ctx, created, role, content, "claude", "", "", ""); err != nil {
			t.Fatalf("add exchange: %v", err)
		}
	}

	// 首次加载建立缓存 + 游标
	add("user", "q1")
	loaded, err := manager.Get(ctx, created.Key, 0)
	if err != nil || len(loaded.Exchanges) != 1 {
		t.Fatalf("first get exchanges = %d err=%v, want 1", len(loaded.Exchanges), err)
	}

	// 追加后增量读取：Get(seq=1) 应只返回新条目，且不重复缓存已有条目
	add("agent", "a1")
	delta, err := manager.Get(ctx, created.Key, 1)
	if err != nil {
		t.Fatalf("incremental get: %v", err)
	}
	if len(delta.Exchanges) != 1 || delta.Exchanges[0].Content != "a1" {
		t.Fatalf("incremental exchanges = %#v, want [a1]", delta.Exchanges)
	}

	// 文件未变时增量：Get(seq=2) 返回空
	empty, err := manager.Get(ctx, created.Key, 2)
	if err != nil {
		t.Fatalf("empty incremental get: %v", err)
	}
	if len(empty.Exchanges) != 0 {
		t.Fatalf("empty incremental exchanges = %#v, want []", empty.Exchanges)
	}

	// 缓存保持完整
	full, err := manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatalf("full get: %v", err)
	}
	if len(full.Exchanges) != 2 {
		t.Fatalf("full exchanges = %d, want 2", len(full.Exchanges))
	}
}

func TestManagerIncrementalAuxLoad(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)

	created, err := manager.Create(context.Background(), CreateInput{Type: TypeChat, Agent: "claude", Name: "Aux"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	ctx := context.Background()
	addAux := func(seq int, callID string) {
		if err := manager.AddExchangeAux(ctx, created.Key, ExchangeAux{
			Seq:      seq,
			ToolCall: &agenttypes.ToolCall{CallID: callID, Title: callID, Kind: agenttypes.ToolKindSearch},
		}); err != nil {
			t.Fatalf("add aux: %v", err)
		}
	}

	// 首次加载建立游标
	addAux(1, "call-1")
	aux, err := manager.GetExchangeAux(ctx, created.Key, 0)
	if err != nil || len(aux) != 1 {
		t.Fatalf("first aux = %#v err=%v, want 1 entry", aux, err)
	}

	// 文件未变增量：GetExchangeAux(seq=1) 返回空
	empty, err := manager.GetExchangeAux(ctx, created.Key, 1)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty aux delta = %#v err=%v, want empty", empty, err)
	}

	// 追加后增量读取
	addAux(2, "call-2")
	delta, err := manager.GetExchangeAux(ctx, created.Key, 1)
	if err != nil {
		t.Fatalf("aux delta: %v", err)
	}
	if len(delta) != 1 || delta[2][0].ToolCall.CallID != "call-2" {
		t.Fatalf("aux delta = %#v, want [call-2] at seq 2", delta)
	}

	// afterSeq=0 全量读取
	full, err := manager.GetExchangeAux(ctx, created.Key, 0)
	if err != nil {
		t.Fatalf("full aux: %v", err)
	}
	if len(full) != 2 {
		t.Fatalf("full aux = %d entries, want 2", len(full))
	}
}

func TestManagerGetFullToolCallReadsPendingAuxBeforeDisk(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)

	created, err := manager.Create(context.Background(), CreateInput{
		Type: TypeChat,
		Name: "Chat",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	callID := "call-pending"
	if err := manager.UpsertPendingExchangeAux(context.Background(), created.Key, ExchangeAux{
		Seq:  2,
		Line: 1,
		ToolCall: &agenttypes.ToolCall{
			CallID:  callID,
			Title:   "git diff",
			Status:  "running",
			Kind:    agenttypes.ToolKindExecute,
			Content: []agenttypes.ToolCallContentItem{{Type: "text", Text: "running output"}},
		},
	}); err != nil {
		t.Fatalf("upsert pending start: %v", err)
	}
	if err := manager.UpsertPendingExchangeAux(context.Background(), created.Key, ExchangeAux{
		Seq:  2,
		Line: 1,
		ToolCall: &agenttypes.ToolCall{
			CallID:  callID,
			Status:  "complete",
			Content: []agenttypes.ToolCallContentItem{{Type: "text", Text: "final diff output"}},
			Meta:    map[string]any{"outputBytes": 17},
		},
	}); err != nil {
		t.Fatalf("upsert pending final: %v", err)
	}

	toolCall, err := manager.GetFullToolCall(context.Background(), created.Key, callID)
	if err != nil {
		t.Fatalf("get pending full toolcall: %v", err)
	}
	if toolCall.Status != "complete" {
		t.Fatalf("status = %q, want complete", toolCall.Status)
	}
	if toolCall.Title != "git diff" {
		t.Fatalf("title = %q, want git diff", toolCall.Title)
	}
	if len(toolCall.Content) != 1 || toolCall.Content[0].Text != "final diff output" {
		t.Fatalf("content = %#v, want final diff output", toolCall.Content)
	}

	manager.ClearPendingExchangeAux(context.Background(), created.Key)
	if _, err := manager.GetFullToolCall(context.Background(), created.Key, callID); err == nil {
		t.Fatal("GetFullToolCall after clear returned nil error, want not found")
	}
}

func TestManagerMarkPendingAskUserAnsweredMergesAnswers(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)

	created, err := manager.Create(context.Background(), CreateInput{
		Type: TypeChat,
		Name: "Chat",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	callID := "ask-1"
	questions := []agenttypes.AskUserQuestionItem{{Question: "Pick one"}}
	if err := manager.UpsertPendingExchangeAux(context.Background(), created.Key, ExchangeAux{
		Seq:  2,
		Line: 0,
		ToolCall: &agenttypes.ToolCall{
			CallID: callID,
			Title:  "ask user",
			Status: "running",
			Kind:   agenttypes.ToolKindAskUser,
			Meta: map[string]any{
				"toolUseId": callID,
				"questions": questions,
			},
		},
	}); err != nil {
		t.Fatalf("upsert pending ask user: %v", err)
	}

	answeredAt := time.Date(2026, 6, 22, 1, 2, 3, 0, time.UTC)
	if err := manager.MarkPendingAskUserAnswered(context.Background(), created.Key, callID, map[string]string{
		"q_0": "Yes",
	}, answeredAt); err != nil {
		t.Fatalf("mark answered: %v", err)
	}

	toolCall, err := manager.GetFullToolCall(context.Background(), created.Key, callID)
	if err != nil {
		t.Fatalf("get full toolcall: %v", err)
	}
	if toolCall.Status != "complete" {
		t.Fatalf("status = %q, want complete", toolCall.Status)
	}
	if toolCall.Meta["questions"] == nil {
		t.Fatalf("questions were not preserved: %#v", toolCall.Meta)
	}
	answers, ok := toolCall.Meta["answers"].(map[string]string)
	if !ok || answers["q_0"] != "Yes" {
		t.Fatalf("answers = %#v, want q_0=Yes", toolCall.Meta["answers"])
	}
	if toolCall.Meta["answeredAt"] != answeredAt.Format(time.RFC3339Nano) {
		t.Fatalf("answeredAt = %#v, want %s", toolCall.Meta["answeredAt"], answeredAt.Format(time.RFC3339Nano))
	}
}

func TestExchangeSourceComesFromContext(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	created, err := manager.Create(context.Background(), CreateInput{Type: TypeChat, Name: "Source"})
	if err != nil {
		t.Fatal(err)
	}
	// 无 ctx 标注 = 历史数据，Source 必须为空（投影只按「时间戳逐字节相同」处理）
	if err := manager.AddExchangeForAgent(context.Background(), created, "user", "legacy", "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	liveCtx := WithExchangeSource(context.Background(), ExchangeSourceLive)
	if err := manager.AddExchangeForAgent(liveCtx, created, "agent", "live", "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	importCtx := WithExchangeSource(context.Background(), ExchangeSourceImport)
	if err := manager.AddExchangeForAgentAt(importCtx, created, "user", "imported", "", "", "", "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	payload, err := os.ReadFile(filepath.Join(root.MetaDir(), "sessions", created.Key+".jsonl"))
	if err != nil {
		t.Fatalf("read session file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(payload)), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want 3", len(lines))
	}
	wantSources := []string{"", ExchangeSourceLive, ExchangeSourceImport}
	for index, line := range lines {
		var entry Exchange
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("unmarshal line %d: %v", index, err)
		}
		if entry.Source != wantSources[index] {
			t.Fatalf("line %d source = %q, want %q", index, entry.Source, wantSources[index])
		}
	}
}

func writeExchangeFile(t *testing.T, root rootfs.RootInfo, key string, lines []string) string {
	t.Helper()
	path := filepath.Join(root.MetaDir(), "sessions", key+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write exchanges: %v", err)
	}
	return path
}

func exchangeJSON(t *testing.T, seq int, content string) string {
	t.Helper()
	payload, err := json.Marshal(Exchange{Seq: seq, Role: "user", Content: content, Timestamp: time.Date(2026, 9, 14, 10, 0, seq, 0, time.UTC)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(payload)
}

// seq 有洞时按「最大 seq + 1」分配，不按缓存长度：否则下一次写入会撞上已存在的 seq，
// 前端按 seq 合并 → 消息直接不显示。
func TestNextSeqUsesMaxSeqNotLength(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	created, err := manager.Create(context.Background(), CreateInput{Type: TypeChat, Name: "Gap"})
	if err != nil {
		t.Fatal(err)
	}
	writeExchangeFile(t, root, created.Key, []string{
		exchangeJSON(t, 1, "a"), exchangeJSON(t, 2, "b"), exchangeJSON(t, 3, "c"), exchangeJSON(t, 5, "e"),
	})
	loaded, err := manager.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Exchanges) != 4 || maxExchangeSeq(loaded.Exchanges) != 5 {
		t.Fatalf("setup = %d rows max=%d", len(loaded.Exchanges), maxExchangeSeq(loaded.Exchanges))
	}
	if err := manager.AddExchangeForAgent(context.Background(), loaded, "user", "new", "claude", "", "", ""); err != nil {
		t.Fatal(err)
	}
	after, err := manager.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if last := after.Exchanges[len(after.Exchanges)-1]; last.Seq != 6 {
		t.Fatalf("new seq = %d, want 6（旧的 len+1 会给 5，与已有行撞号）", last.Seq)
	}
}

// 行首被 NUL 零填充的损坏行（外部/并发写入留下的空洞）必须能被还原，不能当坏行丢掉。
func TestReadExchangesRepairsNulFilledLine(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	created, err := manager.Create(context.Background(), CreateInput{Type: TypeChat, Name: "Nul"})
	if err != nil {
		t.Fatal(err)
	}
	writeExchangeFile(t, root, created.Key, []string{
		exchangeJSON(t, 1, "a"),
		strings.Repeat("\x00", 64) + exchangeJSON(t, 2, "b"),
	})
	loaded, err := manager.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Exchanges) != 2 {
		t.Fatalf("rows = %d, want 2（NUL 行应被还原）", len(loaded.Exchanges))
	}
	if loaded.Exchanges[1].Content != "b" {
		t.Fatalf("repaired content = %q, want %q", loaded.Exchanges[1].Content, "b")
	}
}

// 文件在盘上被外部改动后，缓存不能当真相继续用：下一次读必须看到新行。
func TestGetSessionRefreshesStaleCache(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	created, err := manager.Create(context.Background(), CreateInput{Type: TypeChat, Name: "External"})
	if err != nil {
		t.Fatal(err)
	}
	path := writeExchangeFile(t, root, created.Key, []string{exchangeJSON(t, 1, "a")})
	if _, err := manager.Get(context.Background(), created.Key, 0); err != nil {
		t.Fatal(err)
	}
	// 模拟外部写入者（另一个进程 / 手工修文件）追加一行
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(exchangeJSON(t, 2, "b") + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	loaded, err := manager.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Exchanges) != 2 {
		t.Fatalf("rows = %d, want 2（陈旧缓存必须被刷新）", len(loaded.Exchanges))
	}
}

func TestSessionIsLiveOwned(t *testing.T) {
	cases := []struct {
		name      string
		exchanges []Exchange
		want      bool
	}{
		{"空会话", nil, false},
		{"只有导入行", []Exchange{{Source: ExchangeSourceImport, Content: "x"}, {Source: ExchangeSourceImport, Content: "y"}}, false},
		{"有实时行", []Exchange{{Source: ExchangeSourceImport, Content: "x"}, {Source: ExchangeSourceLive, Content: "y"}}, true},
		// 老数据（无 Source 标注）用实时路径独有签名兜底
		{"老数据带 model_display_name", []Exchange{{ModelDisplayName: "Sonnet 4.5", Content: "x"}}, true},
		{"老数据带 token_usage", []Exchange{{TokenUsage: &agenttypes.TokenUsage{InputTokens: 1}, Content: "x"}}, true},
		{"老数据无签名", []Exchange{{Content: "x"}, {Content: "y"}}, false},
		// 明确标了 import 的行不参与签名兜底（避免把导入行误判成实时行）
		{"导入行带残留字段", []Exchange{{Source: ExchangeSourceImport, ModelDisplayName: "Sonnet 4.5", Content: "x"}}, false},
	}
	for _, tc := range cases {
		if got := SessionIsLiveOwned(tc.exchanges); got != tc.want {
			t.Fatalf("%s: SessionIsLiveOwned = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestAuditSessionReportsGapsDamagedAndAuxOrphans(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	created, err := manager.Create(context.Background(), CreateInput{Type: TypeChat, Name: "Audit"})
	if err != nil {
		t.Fatal(err)
	}
	writeExchangeFile(t, root, created.Key, []string{
		exchangeJSON(t, 1, "a"),
		exchangeJSON(t, 3, "c"), // seq 2 缺失 → 空洞
		strings.Repeat("\x00", 32) + exchangeJSON(t, 4, "d"),
		"{ 这不是 JSON", // 坏行
	})
	auxPath := filepath.Join(root.MetaDir(), "sessions", created.Key+".aux.jsonl")
	if err := os.WriteFile(auxPath, []byte(strings.Join([]string{
		`{"seq":1,"line":0}`, // 有对应 exchange
		`{"seq":9,"line":0}`, // 悬空
	}, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	audit, err := manager.AuditSession(created.Key)
	if err != nil {
		t.Fatal(err)
	}
	if audit.Rows != 3 {
		t.Fatalf("rows = %d, want 3", audit.Rows)
	}
	if len(audit.SeqGaps) != 1 || audit.SeqGaps[0] != 2 {
		t.Fatalf("seq gaps = %v, want [2]", audit.SeqGaps)
	}
	if audit.DamagedLines != 1 {
		t.Fatalf("damaged = %d, want 1", audit.DamagedLines)
	}
	if audit.NulRepairedLines != 1 {
		t.Fatalf("nul repaired = %d, want 1", audit.NulRepairedLines)
	}
	if len(audit.AuxOrphanSeqs) != 1 || audit.AuxOrphanSeqs[0] != 9 {
		t.Fatalf("aux orphans = %v, want [9]", audit.AuxOrphanSeqs)
	}
}

// 徽标的 agent 来源是列表路径的 AgentCtxSeq 单键推导，而它靠 session_agent_bindings 灌入。
// 绑定只在回合结束时写 → 首轮运行期间列表行 agent=="" → 前端 AgentIcon 落回「AI」占位。
// 这一组钉住「建会话的那一刻起，列表就能报出 agent」。
func TestManagerCreateBindsAgentForListInference(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	ctx := context.Background()

	created, err := manager.Create(ctx, CreateInput{Type: TypeChat, Agent: "claude", Name: "Fresh"})
	if err != nil {
		t.Fatal(err)
	}

	// 必须走列表路径：它从库里重读 meta 再用绑定回填 AgentCtxSeq，
	// 用 Create 的返回值断言会拿到内存里那个已经带 AgentCtxSeq 的对象，等于没测。
	items, err := manager.List(ctx, ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var listed *Session
	for _, item := range items {
		if item.Key == created.Key {
			listed = item
			break
		}
	}
	if listed == nil {
		t.Fatalf("created session %s missing from list", created.Key)
	}
	if got := InferAgentFromSession(listed); got != "claude" {
		t.Fatalf("列表行推断 agent = %q, want %q（首轮未结束时徽标必须已是 Claude，而不是 AI 占位）", got, "claude")
	}
}

func TestManagerCreateBindsAgentWithoutTranscriptID(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	ctx := context.Background()

	created, err := manager.Create(ctx, CreateInput{Type: TypeChat, Agent: "claude", Name: "No transcript yet"})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := manager.GetAgentBinding(ctx, created.Key, "claude")
	if err != nil {
		t.Fatalf("create 后必须已有绑定行: %v", err)
	}
	if binding.AgentSessionID != "" {
		t.Fatalf("agent_session_id = %q, want 空（表示已选 agent、尚无转录）", binding.AgentSessionID)
	}
	if binding.AgentCtxSeq != 0 {
		t.Fatalf("agent_ctx_seq = %d, want 0（非 0 会被 prependSwitchHint 当成已同步而吞掉切换提示）", binding.AgentCtxSeq)
	}

	// 回合结束时真实 id 必须**覆盖**这一行，而不是多出一行。
	if err := manager.UpdateAgentState(ctx, created, "claude", 2, "real-id"); err != nil {
		t.Fatal(err)
	}
	again, err := manager.GetAgentBinding(ctx, created.Key, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if again.AgentSessionID != "real-id" || again.AgentCtxSeq != 2 {
		t.Fatalf("回合结束后 binding = %+v, want id=real-id ctx=2", again)
	}
	bindings, err := manager.listAgentBindingsUnsafe(created.Key)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 {
		t.Fatalf("绑定行数 = %d, want 1（占位行必须被覆盖而不是并存）", len(bindings))
	}
	items, err := manager.List(ctx, ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Key == created.Key && InferAgentFromSession(item) != "claude" {
			t.Fatalf("回合结束后列表 agent = %q, want claude", InferAgentFromSession(item))
		}
	}
}

func TestManagerEnsureAgentBindingIsIdempotent(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	ctx := context.Background()

	created, err := manager.Create(ctx, CreateInput{Type: TypeChat, Agent: "claude", Name: "Switch"})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.UpdateAgentState(ctx, created, "claude", 7, "claude-id"); err != nil {
		t.Fatal(err)
	}
	// 回合开始时的补写不能把真实转录 id 打回空。
	if err := manager.EnsureAgentBinding(ctx, created.Key, "claude"); err != nil {
		t.Fatal(err)
	}
	binding, err := manager.GetAgentBinding(ctx, created.Key, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if binding.AgentSessionID != "claude-id" || binding.AgentCtxSeq != 7 {
		t.Fatalf("EnsureAgentBinding 覆盖了已有绑定: %+v", binding)
	}

	// 中途换 agent：新 agent 要立刻有占位行，老 agent 的行不动。
	if err := manager.EnsureAgentBinding(ctx, created.Key, "codex"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.GetAgentBinding(ctx, created.Key, "codex"); err != nil {
		t.Fatalf("换 agent 后新 agent 必须有占位绑定: %v", err)
	}
	stillClaude, err := manager.GetAgentBinding(ctx, created.Key, "claude")
	if err != nil || stillClaude.AgentSessionID != "claude-id" {
		t.Fatalf("老 agent 的绑定被改动了: %+v err=%v", stillClaude, err)
	}

	// 空 agent（command 会话）不写行、不报错。
	if err := manager.EnsureAgentBinding(ctx, created.Key, "  "); err != nil {
		t.Fatalf("空 agent 必须静默跳过: %v", err)
	}
}

func TestManagerEnsureAgentBindingDoesNotLeakEmptyIDIntoAliases(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	ctx := context.Background()

	a, err := manager.Create(ctx, CreateInput{Type: TypeChat, Agent: "claude", Name: "Alpha"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := manager.Create(ctx, CreateInput{Type: TypeChat, Agent: "claude", Name: "Beta"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.LookupAliasForAgent("claude", ""); ok {
		t.Fatal("空 agent_session_id 不得命中别名表——否则两个会话会串名")
	}
	// Rename 会拿 agentSessionIDsUnsafe 的每个 (agent,id) 去写 session_external_names；
	// 空 id 必须被挡在门外，不能落进表里。
	if _, err := manager.Rename(ctx, a.Key, "Alpha renamed"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Rename(ctx, b.Key, "Beta renamed"); err != nil {
		t.Fatal(err)
	}
	db, err := manager.ensureSessionMetaDBUnsafe()
	if err != nil {
		t.Fatal(err)
	}
	var leaked int
	if err := db.QueryRow(`SELECT count(*) FROM session_external_names WHERE agent_session_id = ''`).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 {
		t.Fatalf("session_external_names 里有 %d 行空 agent_session_id", leaked)
	}
}
