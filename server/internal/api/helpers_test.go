package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/server/internal/apperr"
	rootfs "mindfs/server/internal/fs"
)

func TestRespondErrorIncludesAppErrorFields(t *testing.T) {
	rec := httptest.NewRecorder()
	respondError(rec, http.StatusBadRequest, apperr.Wrap("open", "/private/session.jsonl", os.ErrPermission))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["code"] != apperr.CodePermissionDenied {
		t.Fatalf("code = %v", payload["code"])
	}
	if payload["operation"] != "open" {
		t.Fatalf("operation = %v", payload["operation"])
	}
	if payload["path"] != "/private/session.jsonl" {
		t.Fatalf("path = %v", payload["path"])
	}
	if payload["message"] == "" || payload["detail"] == "" || payload["error"] == "" {
		t.Fatalf("missing expected message fields: %#v", payload)
	}
}

func TestEnsureTaskWorktreeExcluded(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("MkdirAll .git: %v", err)
	}
	if err := ensureTaskWorktreeExcluded(root); err != nil {
		t.Fatalf("ensureTaskWorktreeExcluded: %v", err)
	}
	if err := ensureTaskWorktreeExcluded(root); err != nil {
		t.Fatalf("ensureTaskWorktreeExcluded second call: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".git", "info", "exclude"))
	if err != nil {
		t.Fatalf("ReadFile exclude: %v", err)
	}
	if got := strings.Count(string(data), "/.worktree/"); got != 1 {
		t.Fatalf("exclude entry count=%d, want 1; content=%q", got, string(data))
	}
}

func TestResolveRelatedWorktreePrefersNestedTaskWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}
	root := t.TempDir()
	runAPITestGit(t, root, "init")
	runAPITestGit(t, root, "config", "user.email", "test@example.com")
	runAPITestGit(t, root, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("WriteFile README.md: %v", err)
	}
	runAPITestGit(t, root, "add", "README.md")
	runAPITestGit(t, root, "commit", "-m", "initial")
	runAPITestGit(t, root, "checkout", "-b", "task-1")
	runAPITestGit(t, root, "checkout", "-")

	worktreeRoot := filepath.Join(root, ".worktree", "task-1")
	runAPITestGit(t, root, "worktree", "add", worktreeRoot, "task-1")
	if err := os.WriteFile(filepath.Join(worktreeRoot, "test.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile test.json: %v", err)
	}

	match, ok := resolveRelatedWorktree(
		context.Background(),
		rootfs.NewRootInfo("root", "root", root),
		filepath.Join(".worktree", "task-1", "test.json"),
	)
	if !ok {
		t.Fatal("resolveRelatedWorktree returned false")
	}
	if !sameAPITestPath(match.Path, worktreeRoot) {
		t.Fatalf("match.Path = %q, want %q", match.Path, worktreeRoot)
	}
}

// 主 checkout 不是 worktree 归属，别记。
//
// 这是 2026-10-02 实测到的那个来回的根因：watcher 因为会话动了文件把主 checkout
// 填进 related_worktree，repoint 清空它，下次动文件又填回来 —— 清空成了非持久操作。
// 记主 checkout 对 sessionRuntimeRootPath 也毫无用处（它只在有 task_id 或
// source=worktree 时才采信这个字段）。
//
// 三个出口都要挡住，所以这里三个都验：worktree 列表里的主 checkout、
// ResolveRepositoryForPath 兜底返回的 root 自己、以及两者都不含时的普通文件。
func TestResolveRelatedWorktreeIgnoresMainCheckout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}
	root := t.TempDir()
	runAPITestGit(t, root, "init")
	runAPITestGit(t, root, "config", "user.email", "test@example.com")
	runAPITestGit(t, root, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("WriteFile README.md: %v", err)
	}
	runAPITestGit(t, root, "add", "README.md")
	runAPITestGit(t, root, "commit", "-m", "initial")

	rootInfo := rootfs.NewRootInfo("root", "root", root)

	// 一、root 目录内的文件：ListWorktrees 会返回主 checkout 那条（Current=true）。
	if _, ok := resolveRelatedWorktree(context.Background(), rootInfo, "README.md"); ok {
		t.Fatal("a file in the main checkout must not resolve to a worktree match")
	}

	// 二、绝对路径指向 root 自己：走 ListWorktrees 的 Current 分支。
	if _, ok := resolveRelatedWorktree(context.Background(), rootInfo, filepath.Join(root, "README.md")); ok {
		t.Fatal("the main checkout path itself must not resolve to a worktree match")
	}

	// 三、仓库里的子目录：不在任何 worktree 下，但 ResolveRepositoryForPath
	// 会把 root 自己返回 —— 这个出口同样不能记。
	nested := filepath.Join(root, "pkg", "internal")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll nested: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "x.go"), []byte("package internal\n"), 0o644); err != nil {
		t.Fatalf("WriteFile x.go: %v", err)
	}
	match, ok := resolveRelatedWorktree(context.Background(), rootInfo, filepath.Join("pkg", "internal", "x.go"))
	if ok && !match.Current {
		t.Fatalf("a file under the main checkout resolved to %q as a worktree", match.Path)
	}
}

// 反向守卫：真 worktree 里的文件仍然要认出来。上一个测试把 Current 全滤掉了，
// 这个确保没有把该记的一起滤掉 —— 两个方向缺一个都算回归。
func TestResolveRelatedWorktreeStillRecordsRealWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}
	root := t.TempDir()
	runAPITestGit(t, root, "init")
	runAPITestGit(t, root, "config", "user.email", "test@example.com")
	runAPITestGit(t, root, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("WriteFile README.md: %v", err)
	}
	runAPITestGit(t, root, "add", "README.md")
	runAPITestGit(t, root, "commit", "-m", "initial")
	runAPITestGit(t, root, "checkout", "-b", "task-7")
	runAPITestGit(t, root, "checkout", "-")

	worktreeRoot := filepath.Join(root, ".worktree", "task-7")
	runAPITestGit(t, root, "worktree", "add", worktreeRoot, "task-7")
	if err := os.WriteFile(filepath.Join(worktreeRoot, "a.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("WriteFile a.txt: %v", err)
	}

	rootInfo := rootfs.NewRootInfo("root", "root", root)
	match, ok := resolveRelatedWorktree(context.Background(), rootInfo, filepath.Join(".worktree", "task-7", "a.txt"))
	if !ok {
		t.Fatal("a file inside a real worktree must still resolve")
	}
	if match.Current {
		t.Fatalf("match.Current = true for worktree %q; the field would be meaningless", match.Path)
	}
	if !sameAPITestPath(match.Path, worktreeRoot) {
		t.Fatalf("match.Path = %q, want %q", match.Path, worktreeRoot)
	}
	if got := strings.TrimSpace(match.Branch); got != "task-7" {
		t.Fatalf("match.Branch = %q, want task-7", got)
	}
}

func runAPITestGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s returned error: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out)
}

func sameAPITestPath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if resolved, err := filepath.EvalSymlinks(left); err == nil {
		left = filepath.Clean(resolved)
	}
	if resolved, err := filepath.EvalSymlinks(right); err == nil {
		right = filepath.Clean(resolved)
	}
	return left == right
}
