package gitview

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPruneOrphanDirsSpotsLeftoversButNotLiveWorktrees(t *testing.T) {
	ctx := context.Background()
	main := newMainRepo(t)
	live := filepath.Join(main, ".worktree", "task-live")
	runTestGit(t, main, "worktree", "add", "-b", "task-live", live)
	// 模拟 git 记录没了、目录还在（worktree remove 失败 / prune 后残留）：
	// 这里直接造一个没有 .git 文件的目录，不去动 git 的登记。
	orphan := filepath.Join(main, ".worktree", "task-orphan")
	writeTestFile(t, orphan, ".mindfs/sessions.db", "state")
	writeTestFile(t, orphan, "leftover.txt", "junk")

	orphans, err := PruneOrphanDirs(ctx, main)
	if err != nil {
		t.Fatalf("PruneOrphanDirs: %v", err)
	}
	if len(orphans) != 1 {
		t.Fatalf("orphans = %+v, want exactly the one dir without a .git file", orphans)
	}
	if orphans[0].Path != orphan {
		t.Fatalf("orphan path = %q, want %q", orphans[0].Path, orphan)
	}
	if !orphans[0].NonEmpty || len(orphans[0].Files) == 0 {
		t.Fatalf("orphan = %+v, want NonEmpty with a file preview", orphans[0])
	}
	// 判据是「没有 .git 文件」，所以还活着的 worktree 绝不可能被列进来 —— 这条是硬保证。
	if strings.Contains(orphans[0].Path, "task-live") {
		t.Fatal("a live worktree was flagged as an orphan")
	}
}

func TestPruneOrphanDirsIsEmptyWhenNothingIsLeft(t *testing.T) {
	main := newMainRepo(t)
	orphans, err := PruneOrphanDirs(context.Background(), main)
	if err != nil {
		t.Fatalf("PruneOrphanDirs: %v", err)
	}
	if len(orphans) != 0 {
		t.Fatalf("orphans = %+v, want none", orphans)
	}
}

func TestRemoveOrphanDirNeedsForceForNonEmpty(t *testing.T) {
	main := newMainRepo(t)
	orphan := filepath.Join(main, ".worktree", "task-orphan")
	writeTestFile(t, orphan, "leftover.txt", "junk")

	// 非空默认不删：里面可能是人放的东西。
	if err := RemoveOrphanDir(OrphanWorktreeDir{Path: orphan, NonEmpty: true}, false); err == nil {
		t.Fatal("a non-empty orphan must require force")
	}
	if _, statErr := os.Stat(orphan); statErr != nil {
		t.Fatalf("the directory must survive the refusal: %v", statErr)
	}
	if err := RemoveOrphanDir(OrphanWorktreeDir{Path: orphan, NonEmpty: true}, true); err != nil {
		t.Fatalf("force delete: %v", err)
	}
	if _, statErr := os.Stat(orphan); statErr == nil {
		t.Fatal("the directory should be gone")
	}
}

func TestRemoveOrphanDirRefusesLiveWorktree(t *testing.T) {
	main := newMainRepo(t)
	live := filepath.Join(main, ".worktree", "task-live")
	runTestGit(t, main, "worktree", "add", "-b", "task-live", live)
	// 扫描到删除之间有人建了 worktree 进来：删除前再判一次 .git 才是真保证。
	if err := RemoveOrphanDir(OrphanWorktreeDir{Path: live, NonEmpty: true}, true); err == nil {
		t.Fatal("force must still not delete a registered worktree")
	}
	if _, statErr := os.Stat(live); statErr != nil {
		t.Fatalf("the worktree must survive: %v", statErr)
	}
}

// 唯一必须拒绝的情形：目录里还有 .git（仍是活 worktree）。那说明 git 的拆除没成功，
// 这时删目录等于绕过 git 的保护 —— 未提交的活会被无声丢掉。
func TestRemoveWorktreeDirRefusesALiveWorktree(t *testing.T) {
	main := newMainRepo(t)
	wt := filepath.Join(main, ".worktree", "task-1")
	runTestGit(t, main, "worktree", "add", "-b", "task-1", wt)
	writeTestFile(t, wt, "uncommitted.txt", "still mine\n")

	if _, err := RemoveWorktreeDir(wt); err == nil {
		t.Fatal("a live worktree must not be deletable through RemoveWorktreeDir")
	}
	if _, statErr := os.Stat(filepath.Join(wt, "uncommitted.txt")); statErr != nil {
		t.Fatalf("the uncommitted file must survive: %v", statErr)
	}
}

// 目录已经不在了 = 已经拆过（幂等），不是错误。
func TestRemoveWorktreeDirIsIdempotent(t *testing.T) {
	removed, err := RemoveWorktreeDir(filepath.Join(t.TempDir(), "never-existed"))
	if err != nil {
		t.Fatalf("RemoveWorktreeDir: %v", err)
	}
	if removed {
		t.Fatal("removed = true for a path that never existed")
	}
}

// git 已经不认、磁盘上还在的空壳（含 git 不跟踪的状态目录）必须被整个删掉。
func TestRemoveWorktreeDirDeletesAnOrphanShell(t *testing.T) {
	main := newMainRepo(t)
	wt := filepath.Join(main, ".worktree", "task-1")
	runTestGit(t, main, "worktree", "add", "-b", "task-1", wt)
	runTestGit(t, main, "worktree", "remove", "--force", wt)
	if err := os.MkdirAll(filepath.Join(wt, ".mindfs"), 0o755); err != nil {
		t.Fatalf("reseed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".mindfs", "sessions.db"), []byte("state"), 0o644); err != nil {
		t.Fatalf("reseed file: %v", err)
	}

	removed, err := RemoveWorktreeDir(wt)
	if err != nil {
		t.Fatalf("RemoveWorktreeDir: %v", err)
	}
	if !removed {
		t.Fatal("removed = false, want the orphan shell gone")
	}
	if _, statErr := os.Stat(wt); !os.IsNotExist(statErr) {
		t.Fatalf("the shell must be gone, stat err = %v", statErr)
	}
}

// 没有 upload/ 时是安静的空操作 —— 绝大多数 worktree 都没有（任务 worktree 不作为
// 独立 root 注册，上传落在主 checkout）。
func TestMigrateWorktreeUploadsIsANoOpWithoutUploads(t *testing.T) {
	main := newMainRepo(t)
	wt := filepath.Join(main, ".worktree", "task-1")
	runTestGit(t, main, "worktree", "add", "-b", "task-1", wt)

	migrated, err := MigrateWorktreeUploads(wt, main)
	if err != nil {
		t.Fatalf("MigrateWorktreeUploads: %v", err)
	}
	if len(migrated) != 0 {
		t.Fatalf("migrated = %v, want empty", migrated)
	}
}

// 目标已有同名文件时跳过不覆盖：主 checkout 那份是权威的。
func TestMigrateWorktreeUploadsKeepsExistingTargets(t *testing.T) {
	main := newMainRepo(t)
	wt := filepath.Join(main, ".worktree", "task-1")
	runTestGit(t, main, "worktree", "add", "-b", "task-1", wt)
	rel := filepath.Join("2026-01-01", "a.png")
	writeTestFile(t, filepath.Join(wt, ".mindfs", "upload"), rel, "stale\n")
	writeTestFile(t, filepath.Join(main, ".mindfs", "upload"), rel, "authoritative\n")

	migrated, err := MigrateWorktreeUploads(wt, main)
	if err != nil {
		t.Fatalf("MigrateWorktreeUploads: %v", err)
	}
	if len(migrated) != 0 {
		t.Fatalf("migrated = %v, want empty — the existing target must win", migrated)
	}
	got, readErr := os.ReadFile(filepath.Join(main, ".mindfs", "upload", rel))
	if readErr != nil {
		t.Fatalf("read target: %v", readErr)
	}
	if strings.TrimSpace(string(got)) != "authoritative" {
		t.Fatalf("target was overwritten: %q", got)
	}
}
