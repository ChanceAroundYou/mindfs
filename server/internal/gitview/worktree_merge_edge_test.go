package gitview

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeBranchTreatsAVanishedSourceAsAlreadyMerged(t *testing.T) {
	// 重复收尾必然走到这：第一次已经 branch -d 掉了，第二次如果报「分支不存在」
	// 就成了「一次成功的收尾、第二次点却失败」。
	ctx := context.Background()
	main := newMainRepo(t)
	wt := filepath.Join(main, ".worktree", "task-1")
	runTestGit(t, main, "worktree", "add", "-b", "task-1", wt)
	writeTestFile(t, wt, "note.txt", "from worktree\n")
	runTestGit(t, wt, "add", "note.txt")
	runTestGit(t, wt, "commit", "-m", "work")
	if _, err := MergeBranch(ctx, MergeOptions{Source: "task-1", Target: "main", MainDir: main}); err != nil {
		t.Fatalf("first merge: %v", err)
	}
	runTestGit(t, main, "worktree", "remove", "--force", wt)
	runTestGit(t, main, "branch", "-D", "task-1")

	result, err := MergeBranch(ctx, MergeOptions{Source: "task-1", Target: "main", MainDir: main})
	if err != nil {
		t.Fatalf("merging a vanished branch must not error: %v", err)
	}
	if result.Merged {
		t.Fatalf("Merged = true for a branch that is gone, want the idempotent skip: %+v", result)
	}
}

func TestMergeBranchRefusesToLoseAWorktreeThatHoldsTheOnlyCopyOfItsWork(t *testing.T) {
	// 危险形态（实测复现过）：分支被 `git branch -D` 强删，而 worktree 还停在
	// detached HEAD 上 —— 那份提交唯一的引用就是这个 worktree，main 上没有。
	// 若把「分支没了」当「已合入」直接跳过合并，收尾接着把 worktree 拆掉，
	// 那些提交就一个引用都不剩了。
	ctx := context.Background()
	main := newMainRepo(t)
	wt := filepath.Join(main, ".worktree", "task-1")
	runTestGit(t, main, "worktree", "add", "-b", "task-1", wt)
	writeTestFile(t, wt, "note.txt", "unmerged work\n")
	runTestGit(t, wt, "add", "note.txt")
	runTestGit(t, wt, "commit", "-m", "work")
	commit := strings.TrimSpace(runTestGit(t, wt, "rev-parse", "HEAD"))

	// detached 之后分支才删得掉（branch -D 拒绝删还被 worktree 占着的分支）。
	runTestGit(t, wt, "checkout", "--detach", "HEAD")
	runTestGit(t, main, "branch", "-D", "task-1")
	if ancestor, _ := commitIsAncestorOf(ctx, main, commit, "main"); ancestor {
		t.Fatal("setup is wrong: the work commit must NOT be reachable from main")
	}

	_, err := MergeBranch(ctx, MergeOptions{
		Source: "task-1", Target: "main", MainDir: main, WorktreePath: wt,
	})
	if err == nil {
		t.Fatal("a vanished branch whose only copy of the work is the worktree must not merge-skip")
	}
	if !strings.Contains(err.Error(), "唯一的引用") {
		t.Fatalf("error must say the commits are unreferenced, got %v", err)
	}
	// worktree 和提交都必须原样留着，让用户自己去捞。
	if _, statErr := os.Stat(filepath.Join(wt, ".git")); statErr != nil {
		t.Fatalf("the worktree must survive: %v", statErr)
	}
}

func TestMergeBranchSkipsVanishedSourceWhenNothingIsLeftToLose(t *testing.T) {
	// 重复收尾：第一次已经合并 + 删了分支 + 拆了树。第二次必须幂等通过，
	// 而不是因为「分支没了」就算成功 —— 是因为确实没有任何提交只剩它一个引用。
	ctx := context.Background()
	main := newMainRepo(t)
	wt := filepath.Join(main, ".worktree", "task-1")
	runTestGit(t, main, "worktree", "add", "-b", "task-1", wt)
	writeTestFile(t, wt, "note.txt", "from worktree\n")
	runTestGit(t, wt, "add", "note.txt")
	runTestGit(t, wt, "commit", "-m", "work")
	if _, err := MergeBranch(ctx, MergeOptions{Source: "task-1", Target: "main", MainDir: main, WorktreePath: wt}); err != nil {
		t.Fatalf("first merge: %v", err)
	}
	runTestGit(t, main, "worktree", "remove", "--force", wt)
	runTestGit(t, main, "branch", "-D", "task-1")

	result, err := MergeBranch(ctx, MergeOptions{
		Source: "task-1", Target: "main", MainDir: main, WorktreePath: wt,
	})
	if err != nil {
		t.Fatalf("a repeated finish must not error: %v", err)
	}
	if result.Merged {
		t.Fatalf("Merged = true on a repeat run, want the idempotent skip: %+v", result)
	}
}

func TestMergeBranchRefusesABogusOrSelfTarget(t *testing.T) {
	// target 来自 HTTP 请求体：不校验就是「把用户的工作树并进任意分支」。
	ctx := context.Background()
	main := newMainRepo(t)
	wt := filepath.Join(main, ".worktree", "task-1")
	runTestGit(t, main, "worktree", "add", "-b", "task-1", wt)
	writeTestFile(t, wt, "note.txt", "work\n")
	runTestGit(t, wt, "add", "note.txt")
	runTestGit(t, wt, "commit", "-m", "work")

	if _, err := MergeBranch(ctx, MergeOptions{Source: "task-1", Target: "no-such-branch", MainDir: main}); err == nil {
		t.Fatal("a target that does not exist must be refused")
	} else if !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("err = %v, want it to say the target does not exist", err)
	}
	// 往源自己里并自己 = 静默无操作，用户会以为合过了。
	if _, err := MergeBranch(ctx, MergeOptions{Source: "task-1", Target: "task-1", MainDir: main}); err == nil {
		t.Fatal("merging a branch into itself must be refused")
	}
}

func TestMergeBranchRefusesATargetHeldByAnotherWorktree(t *testing.T) {
	// 目标分支被别的 worktree 占用：checkout 会失败，而那时状态已经改了一半。
	ctx := context.Background()
	main := newMainRepo(t)
	runTestGit(t, main, "worktree", "add", "-q", "-b", "release", filepath.Join(main, ".worktree", "release"))
	wt := filepath.Join(main, ".worktree", "task-1")
	runTestGit(t, main, "worktree", "add", "-q", "-b", "task-1", wt)
	writeTestFile(t, wt, "note.txt", "work\n")
	runTestGit(t, wt, "add", "note.txt")
	runTestGit(t, wt, "commit", "-m", "work")

	_, err := MergeBranch(ctx, MergeOptions{Source: "task-1", Target: "release", MainDir: main})
	if err == nil {
		t.Fatal("a target checked out in another worktree must be refused")
	}
	if !strings.Contains(err.Error(), "worktree") {
		t.Fatalf("err = %v, want it to name the occupying worktree", err)
	}
}

func TestDeleteBranchRefusesUnmergedBranch(t *testing.T) {
	ctx := context.Background()
	main := newMainRepo(t)
	wt := filepath.Join(main, ".worktree", "task-1")
	runTestGit(t, main, "worktree", "add", "-b", "task-1", wt)
	writeTestFile(t, wt, "note.txt", "from worktree\n")
	runTestGit(t, wt, "add", "note.txt")
	runTestGit(t, wt, "commit", "-m", "work")

	// `branch -d`（用户定的口径）：未合并就删等于丢提交，宁可停下来。
	if err := DeleteBranch(ctx, main, "task-1"); err == nil {
		t.Fatal("deleting an unmerged branch must fail")
	}
	if _, statErr := os.Stat(filepath.Join(main, ".git", "refs", "heads", "task-1")); statErr != nil {
		t.Fatalf("the branch must survive the refusal: %v", statErr)
	}
}

func TestDeleteBranchSucceedsAfterMerge(t *testing.T) {
	ctx := context.Background()
	main := newMainRepo(t)
	wt := filepath.Join(main, ".worktree", "task-1")
	runTestGit(t, main, "worktree", "add", "-b", "task-1", wt)
	writeTestFile(t, wt, "note.txt", "from worktree\n")
	runTestGit(t, wt, "add", "note.txt")
	runTestGit(t, wt, "commit", "-m", "work")
	if _, err := MergeBranch(ctx, MergeOptions{Source: "task-1", Target: "main", MainDir: main}); err != nil {
		t.Fatalf("MergeBranch: %v", err)
	}
	// 分支还被 worktree 占着，先摘掉才能删——收尾流程正是这个顺序。
	runTestGit(t, main, "worktree", "remove", "--force", wt)
	if err := DeleteBranch(ctx, main, "task-1"); err != nil {
		t.Fatalf("DeleteBranch after merge: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(main, ".git", "refs", "heads", "task-1")); statErr == nil {
		t.Fatal("the branch should be gone")
	}
}
