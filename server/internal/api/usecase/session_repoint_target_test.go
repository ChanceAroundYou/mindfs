package usecase

import (
	"os"
	"path/filepath"
	"testing"

	"mindfs/server/internal/session"
)

func TestValidateRepointTarget(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}

	if got, err := validateRepointTarget(dir + "/"); err != nil {
		t.Fatalf("existing dir rejected: %v", err)
	} else if got != filepath.Clean(dir) {
		t.Fatalf("cleaned path = %q, want %q", got, filepath.Clean(dir))
	}
	for label, input := range map[string]string{
		"empty":      "",
		"relative":   "some/relative/dir",
		"missing":    filepath.Join(dir, "nope"),
		"not a dir":  file,
		"with space": "   ",
	} {
		if _, err := validateRepointTarget(input); err == nil {
			t.Fatalf("%s (%q) must be rejected", label, input)
		}
	}
}

// 换目录之后运行时 cwd 必须跟着走。WorkDir 优先于工作树归属 —— 换目录时工作树归属
// 已被清掉，两者同时存在说明是别的路径写进来的，以用户显式选的那个为准。
func TestSessionRuntimeRootPathPrefersWorkDir(t *testing.T) {
	if got := sessionRuntimeRootPathFor(&session.Session{WorkDir: "/chosen/dir"}); got != "/chosen/dir" {
		t.Fatalf("WorkDir path = %q, want /chosen/dir", got)
	}
	worktree := &session.Session{
		TaskID:          "task-1",
		RelatedWorktree: &session.RelatedWorktree{Path: "/worktree/dir"},
	}
	if got := sessionRuntimeRootPathFor(worktree); got != "/worktree/dir" {
		t.Fatalf("worktree path = %q, want /worktree/dir", got)
	}
	worktree.WorkDir = "/chosen/dir"
	if got := sessionRuntimeRootPathFor(worktree); got != "/chosen/dir" {
		t.Fatalf("WorkDir must win over worktree, got %q", got)
	}
	if got := sessionRuntimeRootPathFor(nil); got != "" {
		t.Fatalf("nil session path = %q, want empty", got)
	}
}
