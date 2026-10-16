package session

import (
	"context"
	"path/filepath"
	"testing"

	rootfs "mindfs/server/internal/fs"
)

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
