package session

import (
	"context"
	"path/filepath"
	"testing"

	rootfs "mindfs/server/internal/fs"
)

func TestClearRelatedWorktreeRemovesOwnership(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	created, err := manager.Create(context.Background(), CreateInput{Type: TypeChat, Source: "worktree", Name: "WT"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	worktreePath := filepath.Join(t.TempDir(), ".worktree", "task-1")
	added, err := manager.RecordRelatedWorktree(context.Background(), created.Key, "mindfs", worktreePath, "task-1", "abc123")
	if err != nil || !added {
		t.Fatalf("record worktree: added=%v err=%v", added, err)
	}

	cleared, err := manager.ClearRelatedWorktree(context.Background(), created.Key)
	if err != nil {
		t.Fatalf("clear worktree: %v", err)
	}
	if !cleared {
		t.Fatalf("clear worktree = false, want true")
	}

	reloaded, err := manager.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if reloaded.RelatedWorktree != nil {
		t.Fatalf("related worktree = %+v, want nil after clear", reloaded.RelatedWorktree)
	}
}

// 再清一次必须是安全的空操作：repoint 被重放（网络重试、用户手点两次）不该报错。
func TestClearRelatedWorktreeIsIdempotent(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	created, err := manager.Create(context.Background(), CreateInput{Type: TypeChat, Source: "worktree", Name: "WT"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := manager.RecordRelatedWorktree(context.Background(), created.Key, "mindfs", filepath.Join(t.TempDir(), "wt"), "b", "h"); err != nil {
		t.Fatalf("record worktree: %v", err)
	}
	if _, err := manager.ClearRelatedWorktree(context.Background(), created.Key); err != nil {
		t.Fatalf("first clear: %v", err)
	}
	cleared, err := manager.ClearRelatedWorktree(context.Background(), created.Key)
	if err != nil {
		t.Fatalf("second clear must not error, got %v", err)
	}
	if cleared {
		t.Fatalf("second clear = true, want false (nothing left to clear)")
	}
}

// 清掉归属之后必须真的持久化，不能只活在内存里 —— 否则进程一重启就又变回 worktree 会话。
func TestClearRelatedWorktreeSurvivesReload(t *testing.T) {
	rootDir := t.TempDir()
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	manager := NewManager(root)
	created, err := manager.Create(context.Background(), CreateInput{Type: TypeChat, Source: "worktree", Name: "WT"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := manager.RecordRelatedWorktree(context.Background(), created.Key, "mindfs", filepath.Join(t.TempDir(), "wt"), "b", "h"); err != nil {
		t.Fatalf("record worktree: %v", err)
	}
	if _, err := manager.ClearRelatedWorktree(context.Background(), created.Key); err != nil {
		t.Fatalf("clear worktree: %v", err)
	}

	// 用同一个 root 目录重新开一个 manager，模拟进程重启后从 DB 读回。
	reopened := NewManager(rootfs.NewRootInfo("mindfs", "mindfs", rootDir))
	reloaded, err := reopened.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatalf("reload after reopen: %v", err)
	}
	if reloaded.RelatedWorktree != nil {
		t.Fatalf("related worktree came back after reopen: %+v", reloaded.RelatedWorktree)
	}
}
