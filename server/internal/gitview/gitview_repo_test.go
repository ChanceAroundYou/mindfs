package gitview

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListWorktreesOnNonRepoReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	if _, err := loadRepoContext(context.Background(), dir); err == nil {
		t.Skip("temp dir resolves inside a git repo")
	}
	// 非 git 根（如 ~/projects/llmux 这类 plain 根）不该报错，只是没有 worktree。
	// 曾经这里抛 400，前端把 git 的原始报错渲染到面板上。
	result, err := ListWorktrees(context.Background(), dir)
	if err != nil {
		t.Fatalf("ListWorktrees on non-repo should not error: %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("Items = %+v, want empty", result.Items)
	}
}

// samePath 必须先解析符号链接再比 —— git 报的路径是解析后的，而 MainCheckoutPath
// 对主 checkout 返回的是调用方给的那个路径（可能是符号链接本身）。
func TestSamePathResolvesSymlinks(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	if !samePath(real, link) {
		t.Fatal("samePath should resolve symlinks")
	}
	if !samePath(link, real) {
		t.Fatal("samePath should be symmetric")
	}
	if !samePath(real, real) {
		t.Fatal("samePath should handle identical paths")
	}
	if samePath(real, t.TempDir()) {
		t.Fatal("samePath should return false for different paths")
	}
}

// validateTarget 不能因为主 checkout 是符号链接就自我阻塞 —— 实测
// /home/xiaokubao/family → /mnt/fnos/family，收尾时报「main 正被另一个 worktree
// 占用」，而那个「另一个 worktree」就是主 checkout 自己。
func TestValidateTargetDoesNotSelfBlockViaSymlink(t *testing.T) {
	root := initTestRepo(t)
	writeTestFile(t, root, "note.txt", "before\n")
	runTestGit(t, root, "add", "note.txt")
	runTestGit(t, root, "commit", "-m", "initial")
	branch := strings.TrimSpace(runTestGit(t, root, "rev-parse", "--abbrev-ref", "HEAD"))

	// 通过符号链接访问主 checkout —— 这正是 /home/xiaokubao/family → /mnt/fnos/family 的形态。
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	// MainCheckoutPath 对主 checkout 返回的是**调用方给的那个路径**（即符号链接本身），
	// 而 git 报的是解析后的路径。validateTarget 必须能认出这是同一个目录。
	mainDir, err := MainCheckoutPath(context.Background(), link)
	if err != nil {
		t.Fatalf("MainCheckoutPath: %v", err)
	}
	if err := validateTarget(context.Background(), mainDir, "", branch); err != nil {
		t.Fatalf("validateTarget should not self-block via symlink: %v", err)
	}
}

// ListWorktrees 的 Current 标记必须对符号链接访问也成立 —— 否则根 worktree 的
// Current 标志在符号链接路径下会错误地变成 false。
func TestListWorktreesCurrentViaSymlink(t *testing.T) {
	root := initTestRepo(t)
	writeTestFile(t, root, "note.txt", "before\n")
	runTestGit(t, root, "add", "note.txt")
	runTestGit(t, root, "commit", "-m", "initial")

	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	result, err := ListWorktrees(context.Background(), link)
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}
	if len(result.Items) == 0 {
		t.Fatal("expected at least the root worktree")
	}
	var found bool
	for _, item := range result.Items {
		if item.Current {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("root worktree should be marked Current even when accessed via symlink")
	}
}

func TestIsInsideWorktreeDetectsSubdirectories(t *testing.T) {
	root := initTestRepo(t)
	writeTestFile(t, root, "note.txt", "base\n")
	runTestGit(t, root, "add", "note.txt")
	runTestGit(t, root, "commit", "-m", "initial")
	runTestGit(t, root, "checkout", "-b", "task-1")
	runTestGit(t, root, "checkout", "-")

	worktreeRoot := filepath.Join(root, ".worktree", "task-1")
	runTestGit(t, root, "worktree", "add", worktreeRoot, "task-1")
	subdir := filepath.Join(worktreeRoot, "src")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}

	inside, err := IsInsideWorktree(context.Background(), subdir)
	if err != nil {
		t.Fatalf("IsInsideWorktree: %v", err)
	}
	if !inside {
		t.Fatal("IsInsideWorktree returned false for worktree subdir")
	}

	inside, err = IsInsideWorktree(context.Background(), root)
	if err != nil {
		t.Fatalf("IsInsideWorktree main root: %v", err)
	}
	if inside {
		t.Fatal("IsInsideWorktree returned true for main root")
	}
}
