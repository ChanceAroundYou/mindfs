package fs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestRegistryUpsertRejectsSameNameDifferentPath(t *testing.T) {
	registry := NewRegistry(filepath.Join(t.TempDir(), "registry.json"))
	first := filepath.Join(t.TempDir(), "project")
	second := filepath.Join(t.TempDir(), "project")
	if err := os.Mkdir(first, 0o755); err != nil {
		t.Fatalf("Mkdir first returned error: %v", err)
	}
	if err := os.Mkdir(second, 0o755); err != nil {
		t.Fatalf("Mkdir second returned error: %v", err)
	}

	created, err := registry.Upsert(first)
	if err != nil {
		t.Fatalf("first Upsert returned error: %v", err)
	}
	again, err := registry.Upsert(first)
	if err != nil {
		t.Fatalf("same-path Upsert returned error: %v", err)
	}
	if again.RootPath != created.RootPath {
		t.Fatalf("same-path Upsert RootPath = %q, want %q", again.RootPath, created.RootPath)
	}

	_, err = registry.Upsert(second)
	if !errors.Is(err, ErrRootNameConflict) {
		t.Fatalf("different-path Upsert error = %v, want ErrRootNameConflict", err)
	}
}

// 空工作区（app.emptyWorkspace）就是这么来的：一个 path 为空的注册表。
// 它只读没问题，但任何写操作都必须失败 —— 而且错误要让人看懂，
// 不能把内部实现细节「registry path required」直接抛给用户。
func TestRegistrySaveWithoutPathReturnsActionableError(t *testing.T) {
	err := NewRegistry("").Save()
	if err == nil {
		t.Fatal("Save on pathless registry returned nil, want error")
	}
	if strings.Contains(err.Error(), "registry path required") {
		t.Fatalf("Save error still leaks internal detail: %q", err.Error())
	}
	if strings.TrimSpace(err.Error()) == "" {
		t.Fatal("Save error is empty, want a user-actionable message")
	}
}

func TestRegistryRenameMovesHomeMetadataAndUpdatesIdentity(t *testing.T) {
	originalHome := userHomeDir
	home := t.TempDir()
	userHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHomeDir = originalHome })

	parent := t.TempDir()
	oldPath := filepath.Join(parent, "old-project")
	newPath := filepath.Join(parent, "new-project")
	if err := os.Mkdir(oldPath, 0o755); err != nil {
		t.Fatal(err)
	}
	root := NewRootInfo("old-project", "old-project", oldPath)
	root.MetaLocation = MetaLocationHome
	if _, err := root.EnsureMetaDir(); err != nil {
		t.Fatal(err)
	}
	if err := root.WriteMetaFile("state.json", []byte(`{"cursor":"kept"}`)); err != nil {
		t.Fatal(err)
	}

	registry := NewRegistry(filepath.Join(t.TempDir(), "registry.json"))
	if _, err := registry.UpsertWithMetaLocation(oldPath, MetaLocationHome); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatal(err)
	}
	renamed, err := registry.Rename("old-project", "new-project", newPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".mindfs", "old-project")); !os.IsNotExist(err) {
		t.Fatalf("old metadata directory still exists: %v", err)
	}
	if _, err := renamed.EnsureMetaDir(); err != nil {
		t.Fatalf("renamed identity invalid: %v", err)
	}
	state, err := renamed.ReadMetaFile("state.json")
	if err != nil || !strings.Contains(string(state), "kept") {
		t.Fatalf("renamed metadata not preserved: %q, %v", state, err)
	}
}

func TestSharedFileWatcherResolveRelatedFileRecordPlainRoot(t *testing.T) {
	rootDir := t.TempDir()
	root := NewRootInfo("root", "plain-root", rootDir)
	watcher := &SharedFileWatcher{root: root}

	record, ok := watcher.resolveRelatedFileRecord(context.Background(), filepath.Join(rootDir, "notes", "todo.txt"))
	if !ok {
		t.Fatal("resolveRelatedFileRecord returned false")
	}
	if record.repoKind != "plain" {
		t.Fatalf("repoKind = %q, want plain", record.repoKind)
	}
	if record.repoPath != filepath.Clean(rootDir) {
		t.Fatalf("repoPath = %q, want %q", record.repoPath, filepath.Clean(rootDir))
	}
	if record.repoName != "plain-root" {
		t.Fatalf("repoName = %q, want plain-root", record.repoName)
	}
	if record.path != "notes/todo.txt" {
		t.Fatalf("path = %q, want notes/todo.txt", record.path)
	}
}

func TestSharedFileWatcherResolveRelatedFileRecordSiblingGitWorktree(t *testing.T) {
	rootDir := t.TempDir()
	worktreeDir := t.TempDir()
	root := NewRootInfo("root", "main-root", rootDir)
	watcher := &SharedFileWatcher{
		root: root,
		worktreeResolver: func(ctx context.Context, root RootInfo, filePath string) (RelatedWorktreeMatch, bool) {
			return RelatedWorktreeMatch{
				Path: worktreeDir,
				Head: "abc123",
			}, true
		},
	}

	record, ok := watcher.resolveRelatedFileRecord(context.Background(), filepath.Join(worktreeDir, "src", "main.go"))
	if !ok {
		t.Fatal("resolveRelatedFileRecord returned false")
	}
	if record.repoKind != "git" {
		t.Fatalf("repoKind = %q, want git", record.repoKind)
	}
	if record.repoPath != filepath.Clean(worktreeDir) {
		t.Fatalf("repoPath = %q, want %q", record.repoPath, filepath.Clean(worktreeDir))
	}
	if record.repoName != filepath.Base(filepath.Clean(worktreeDir)) {
		t.Fatalf("repoName = %q, want %q", record.repoName, filepath.Base(filepath.Clean(worktreeDir)))
	}
	if record.path != "src/main.go" {
		t.Fatalf("path = %q, want src/main.go", record.path)
	}
	if record.head != "abc123" {
		t.Fatalf("head = %q, want abc123", record.head)
	}
}

func TestSharedFileWatcherResolveRelatedFileRecordTaskWorktreeAbsolutePath(t *testing.T) {
	rootDir := t.TempDir()
	worktreeDir := filepath.Join(rootDir, ".worktree", "task-55")
	root := NewRootInfo("root", "main-root", rootDir)
	watcher := &SharedFileWatcher{
		root: root,
		worktreeResolver: func(ctx context.Context, root RootInfo, filePath string) (RelatedWorktreeMatch, bool) {
			return RelatedWorktreeMatch{
				Path: worktreeDir,
				Head: "task-head",
			}, true
		},
	}

	record, ok := watcher.resolveRelatedFileRecord(context.Background(), filepath.Join(worktreeDir, "test.json"))
	if !ok {
		t.Fatal("resolveRelatedFileRecord returned false")
	}
	if record.repoPath != filepath.Clean(worktreeDir) {
		t.Fatalf("repoPath = %q, want %q", record.repoPath, filepath.Clean(worktreeDir))
	}
	if record.path != "test.json" {
		t.Fatalf("path = %q, want test.json", record.path)
	}
	if record.head != "task-head" {
		t.Fatalf("head = %q, want task-head", record.head)
	}
}

func TestSharedFileWatcherShouldIgnoreLargeGeneratedDirectories(t *testing.T) {
	rootDir := t.TempDir()
	root := NewRootInfo("mindfs", "mindfs", rootDir)
	watcher := &SharedFileWatcher{root: root}

	tests := []struct {
		path string
		want bool
	}{
		{filepath.Join(rootDir, "node_modules"), true},
		{filepath.Join(rootDir, "web", "dist"), true},
		{filepath.Join(rootDir, ".next", "cache"), true},
		{filepath.Join(rootDir, ".mindfs"), true},
		{filepath.Join(rootDir, ".mindfs", "state.json"), true},
		{filepath.Join(rootDir, ".mindfs2"), false},
		{filepath.Join(rootDir, "src"), false},
		{filepath.Join(rootDir, "tmpfile"), false},
	}

	for _, tc := range tests {
		if got := watcher.shouldIgnore(tc.path); got != tc.want {
			t.Fatalf("shouldIgnore(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestSharedFileWatcherWatchesOnlyRequestedDirectory(t *testing.T) {
	rootDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(rootDir, "a", "b", "c"), 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}

	root := NewRootInfo("mindfs", "mindfs", rootDir)
	watcher, err := NewSharedFileWatcher(root, nil)
	if err != nil {
		t.Fatalf("NewSharedFileWatcher returned error: %v", err)
	}
	defer watcher.Close()

	assertWatched := func(path string, want bool) {
		t.Helper()
		watcher.mu.RLock()
		_, got := watcher.watchedDirs[filepath.Clean(path)]
		watcher.mu.RUnlock()
		if got != want {
			t.Fatalf("watchedDirs[%q] = %v, want %v", path, got, want)
		}
	}

	assertWatched(rootDir, true)
	assertWatched(filepath.Join(rootDir, "a"), false)
	assertWatched(filepath.Join(rootDir, "a", "b"), false)
	assertWatched(filepath.Join(rootDir, "a", "b", "c"), false)

	if err := watcher.WatchDir("a"); err != nil {
		t.Fatalf("WatchDir returned error: %v", err)
	}
	assertWatched(filepath.Join(rootDir, "a"), true)
	assertWatched(filepath.Join(rootDir, "a", "b"), false)
	assertWatched(filepath.Join(rootDir, "a", "b", "c"), false)
}

func TestSharedFileWatcherConcurrentWatchDirAndEvents(t *testing.T) {
	rootDir := t.TempDir()
	root := NewRootInfo("mindfs", "mindfs", rootDir)
	watcher, err := NewSharedFileWatcher(root, nil)
	if err != nil {
		t.Fatalf("NewSharedFileWatcher returned error: %v", err)
	}
	defer watcher.Close()

	const dirCount = 16
	var wg sync.WaitGroup
	for i := 0; i < dirCount; i++ {
		dirName := filepath.Join("dir", string(rune('a'+i)))
		dirPath := filepath.Join(rootDir, dirName)
		if err := os.MkdirAll(dirPath, 0o755); err != nil {
			t.Fatalf("MkdirAll returned error: %v", err)
		}

		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := watcher.WatchDir(dirName); err != nil {
				t.Errorf("WatchDir returned error: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 8; j++ {
				path := filepath.Join(dirPath, "file.txt")
				if err := os.WriteFile(path, []byte("changed"), 0o644); err != nil {
					t.Errorf("WriteFile returned error: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
