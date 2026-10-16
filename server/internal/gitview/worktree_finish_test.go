package gitview

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 这里的每个用例都在真仓库里跑真 git：merge / branch -d 的行为（哪些退出码代表什么、
// 什么情况下留 MERGE_HEAD）全靠 git 自己，改用 mock 等于把要验的东西也一起 mock 掉了。

func TestMergeBranchTellsResolvedButUncommittedApartFromConflict(t *testing.T) {
	// 冲突解完、git add 了、还没提交 —— 这是用户正打着提交信息时的状态。
	// 此时 MERGE_HEAD 还在但 unmerged 是空的。报成「冲突」会让前端渲染一个空文件
	// 清单加一句「去解冲突」，而根本没冲突可解：用户反复点这个按钮永远不会成功。
	ctx := context.Background()
	main := newMainRepo(t)
	wt := filepath.Join(main, ".worktree", "task-1")
	runTestGit(t, main, "worktree", "add", "-b", "task-1", wt)
	writeTestFile(t, main, "note.txt", "main side\n")
	runTestGit(t, main, "add", "note.txt")
	runTestGit(t, main, "commit", "-m", "main edit")
	writeTestFile(t, wt, "note.txt", "worktree side\n")
	runTestGit(t, wt, "add", "note.txt")
	runTestGit(t, wt, "commit", "-m", "worktree edit")

	// 真撞一次冲突，再把它解完但不提交。
	if _, err := MergeBranch(ctx, MergeOptions{Source: "task-1", Target: "main", MainDir: main}); err == nil {
		t.Fatal("the setup merge must conflict")
	}
	writeTestFile(t, main, "note.txt", "resolved\n")
	runTestGit(t, main, "add", "note.txt")
	if out := strings.TrimSpace(runTestGit(t, main, "diff", "--name-only", "--diff-filter=U")); out != "" {
		t.Fatalf("setup should have no unmerged files left, got %q", out)
	}
	if _, statErr := os.Stat(filepath.Join(main, ".git", "MERGE_HEAD")); statErr != nil {
		t.Fatalf("MERGE_HEAD should still be there after resolving without committing: %v", statErr)
	}

	_, err := MergeBranch(ctx, MergeOptions{Source: "task-1", Target: "main", MainDir: main})
	if !errors.Is(err, ErrMergeUncommitted) {
		t.Fatalf("err = %v, want ErrMergeUncommitted (not a conflict: nothing is unresolved)", err)
	}
	if errors.Is(err, ErrMergeConflict) {
		t.Fatal("a fully resolved merge must not be reported as a conflict")
	}
}
