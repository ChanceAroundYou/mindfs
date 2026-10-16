package session

import (
	"context"
	"path/filepath"
	"testing"

	rootfs "mindfs/server/internal/fs"
)

// WorkDir 是「会话在哪个目录里干活」，与 RelatedWorktree（属于哪个工作树）是两件事。
// 它必须能落盘并原样读回 —— 换目录后重启服务，会话得还在新目录里。
func TestManagerSetWorkDirPersistsAndReloads(t *testing.T) {
	ctx := context.Background()
	rootDir := t.TempDir()
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	manager := NewManager(root)

	created, err := manager.Create(ctx, CreateInput{Type: TypeChat, Name: "WorkDir"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	target := filepath.Join(rootDir, "elsewhere")

	changed, err := manager.SetWorkDir(ctx, created.Key, target)
	if err != nil {
		t.Fatalf("set work dir: %v", err)
	}
	if !changed {
		t.Fatal("set work dir changed = false, want true")
	}
	reloaded, err := manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if reloaded.WorkDir != filepath.Clean(target) {
		t.Fatalf("WorkDir = %q, want %q", reloaded.WorkDir, filepath.Clean(target))
	}
	// 换目录不该动工作树归属：那是任务收尾在管的东西。
	if reloaded.RelatedWorktree != nil {
		t.Fatalf("RelatedWorktree = %+v, want nil (SetWorkDir must not touch it)", reloaded.RelatedWorktree)
	}
}

// 空串 = 回项目主 checkout，是合法值而不是「没改」。同一目录再写一次才算没改。
func TestManagerSetWorkDirEmptyMeansMainCheckout(t *testing.T) {
	ctx := context.Background()
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	created, err := manager.Create(ctx, CreateInput{Type: TypeChat, Name: "WorkDir"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	target := filepath.Join(root.RootPath, "sub")
	if _, err := manager.SetWorkDir(ctx, created.Key, target); err != nil {
		t.Fatalf("set work dir: %v", err)
	}
	if changed, err := manager.SetWorkDir(ctx, created.Key, target); err != nil {
		t.Fatalf("set same work dir: %v", err)
	} else if changed {
		t.Fatal("setting the same work dir changed = true, want false")
	}
	changed, err := manager.SetWorkDir(ctx, created.Key, "")
	if err != nil {
		t.Fatalf("clear work dir: %v", err)
	}
	if !changed {
		t.Fatal("clearing a non-empty work dir changed = false, want true")
	}
	reloaded, err := manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if reloaded.WorkDir != "" {
		t.Fatalf("WorkDir = %q, want empty after clear", reloaded.WorkDir)
	}
}

func TestManagerSetWorkDirRejectsRelativePath(t *testing.T) {
	ctx := context.Background()
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	created, err := manager.Create(ctx, CreateInput{Type: TypeChat, Name: "WorkDir"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := manager.SetWorkDir(ctx, created.Key, "relative/path"); err == nil {
		t.Fatal("relative work dir must be rejected")
	}
}

// repoint 换目录后要清掉旧目录下的 related 条目（那批路径多半已经不存在），
// 但只能清落在旧目录之下的 —— 别处的条目还得留着。
func TestManagerRemoveRelatedFilesUnder(t *testing.T) {
	ctx := context.Background()
	rootDir := t.TempDir()
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	manager := NewManager(root)
	created, err := manager.Create(ctx, CreateInput{Type: TypeChat, Name: "Related"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	worktree := filepath.Join(rootDir, ".worktree", "task-1")
	keep := filepath.Join(rootDir, "kept")
	// 前缀边界用例：/a/bc 不能被 /a/b 误伤。
	sibling := worktree + "-sibling"
	for _, dir := range []string{worktree, keep, sibling} {
		if err := manager.RecordOutputFileInRepo(ctx, created.Key, root.ID, "git", dir, filepath.Base(dir), "src/main.go", "abc123"); err != nil {
			t.Fatalf("record related file in %s: %v", dir, err)
		}
	}

	removed, err := manager.RemoveRelatedFilesUnder(ctx, created.Key, worktree)
	if err != nil {
		t.Fatalf("remove related files under: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	reloaded, err := manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if len(reloaded.RelatedFiles) != 2 {
		t.Fatalf("related files = %#v, want the two outside the old dir", reloaded.RelatedFiles)
	}
	for _, file := range reloaded.RelatedFiles {
		if filepath.Clean(file.RepoPath) == filepath.Clean(worktree) {
			t.Fatalf("entry under the removed dir survived: %#v", file)
		}
	}

	// 再清一次是空操作，不报错（repoint 可能被重放）。
	if removed, err := manager.RemoveRelatedFilesUnder(ctx, created.Key, worktree); err != nil || removed != 0 {
		t.Fatalf("second remove = (%d, %v), want (0, nil)", removed, err)
	}
}
