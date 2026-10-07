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

// newMainRepo 建一个主 checkout，当前分支就叫 main——跟 MergeBranch 的默认 target 对齐。
// initTestRepo 用 git init 的默认分支名，随 git 版本变（master/main），不能在测试里赌。
func newMainRepo(t *testing.T) string {
	t.Helper()
	root := initTestRepo(t)
	runTestGit(t, root, "symbolic-ref", "HEAD", "refs/heads/main")
	writeTestFile(t, root, "note.txt", "base\n")
	runTestGit(t, root, "add", "note.txt")
	runTestGit(t, root, "commit", "-m", "initial")
	return root
}

func TestMergeBranchMergesWorktreeBranchIntoMain(t *testing.T) {
	ctx := context.Background()
	main := newMainRepo(t)
	wt := filepath.Join(main, ".worktree", "task-1")
	runTestGit(t, main, "worktree", "add", "-b", "task-1", wt)
	writeTestFile(t, wt, "note.txt", "from worktree\n")
	runTestGit(t, wt, "add", "note.txt")
	runTestGit(t, wt, "commit", "-m", "work")

	result, err := MergeBranch(ctx, MergeOptions{Source: "task-1", Target: "main", MainDir: main})
	if err != nil {
		t.Fatalf("MergeBranch: %v", err)
	}
	if !result.Merged {
		t.Fatalf("Merged = false, want true: %+v", result)
	}
	if got := strings.TrimSpace(runTestGit(t, main, "log", "-1", "--pretty=%s")); got != "Merge branch 'task-1'" {
		t.Fatalf("HEAD subject = %q, want the merge commit", got)
	}
	if got := strings.TrimSpace(runTestGit(t, main, "rev-parse", "--abbrev-ref", "HEAD")); got != "main" {
		t.Fatalf("merge must land on main, HEAD is on %q", got)
	}
	body := runTestGit(t, main, "show", "main:note.txt")
	if !strings.Contains(body, "from worktree") {
		t.Fatalf("main did not receive the worktree content:\n%s", body)
	}
}

func TestMergeBranchIsIdempotent(t *testing.T) {
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

	// 第二次：source 已是 main 的祖先。必须报「跳过」而不是把 Already up to date
	// 当成一次成功合并——那正是脚本原来误判的点。
	second, err := MergeBranch(ctx, MergeOptions{Source: "task-1", Target: "main", MainDir: main})
	if err != nil {
		t.Fatalf("second merge must not error: %v", err)
	}
	if second.Merged {
		t.Fatalf("Merged = true on the second run, want false (idempotent skip): %+v", second)
	}
}

func TestMergeBranchReportsConflictWithFiles(t *testing.T) {
	ctx := context.Background()
	main := newMainRepo(t)
	wt := filepath.Join(main, ".worktree", "task-1")
	runTestGit(t, main, "worktree", "add", "-b", "task-1", wt)

	// 两边改同一行同一处 —— 必冲突。
	writeTestFile(t, main, "note.txt", "main wins\n")
	runTestGit(t, main, "add", "note.txt")
	runTestGit(t, main, "commit", "-m", "main edit")
	writeTestFile(t, wt, "note.txt", "worktree wins\n")
	runTestGit(t, wt, "add", "note.txt")
	runTestGit(t, wt, "commit", "-m", "worktree edit")

	_, err := MergeBranch(ctx, MergeOptions{Source: "task-1", Target: "main", MainDir: main})
	if err == nil {
		t.Fatal("conflicting merge must not succeed")
	}
	// 上层要靠这个把「要人工处理」和「别的错」分开。
	if !errors.Is(err, ErrMergeConflict) {
		t.Fatalf("err = %v, want it to unwrap to ErrMergeConflict", err)
	}
	conflict, ok := err.(MergeConflict)
	if !ok {
		t.Fatalf("err type = %T, want MergeConflict (errors.As-friendly value type)", err)
	}
	if len(conflict.ConflictFiles) != 1 || conflict.ConflictFiles[0] != "note.txt" {
		t.Fatalf("ConflictFiles = %v, want [note.txt]", conflict.ConflictFiles)
	}
	// 停在冲突态就是预期：自动 abort 会把用户手工解到一半的取舍连同 MERGE_MSG 一起丢掉。
	if _, statErr := os.Stat(filepath.Join(main, ".git", "MERGE_HEAD")); statErr != nil {
		t.Fatalf("MERGE_HEAD should survive so the user can resolve by hand: %v", statErr)
	}
}

func TestMergeBranchRefusesDirtyMainCheckout(t *testing.T) {
	ctx := context.Background()
	main := newMainRepo(t)
	wt := filepath.Join(main, ".worktree", "task-1")
	runTestGit(t, main, "worktree", "add", "-b", "task-1", wt)
	writeTestFile(t, wt, "note.txt", "from worktree\n")
	runTestGit(t, wt, "add", "note.txt")
	runTestGit(t, wt, "commit", "-m", "work")

	// 主 checkout 有**已跟踪文件**的未提交改动时，合并会把它顶掉 → 必须在动手前拦下。
	// 跟踪起来再改是关键：未跟踪文件不该挡路（见
	// TestMergeBranchIgnoresUntrackedFilesInTheMainCheckout），所以这条测试必须用
	// 已跟踪文件的改动来表达「有人的活会被毁」。
	writeTestFile(t, main, "uncommitted.txt", "someone is working here\n")
	runTestGit(t, main, "add", "uncommitted.txt")
	runTestGit(t, main, "commit", "-m", "seed tracked file")
	writeTestFile(t, main, "uncommitted.txt", "someone is still working here\n")

	_, err := MergeBranch(ctx, MergeOptions{Source: "task-1", Target: "main", MainDir: main})
	if err == nil {
		t.Fatal("merge must refuse while the main checkout is dirty")
	}
	if !strings.Contains(err.Error(), "uncommitted.txt") {
		t.Fatalf("error must name the dirty file, got %v", err)
	}
}

// 未跟踪文件**不该**挡住合并 —— 2026-10-08 用户实测：主 checkout 里两个别人留下的
// e2e 探针脚本把 task-37 的收尾卡死在「主 checkout 有未提交改动」，而它们一个字节
// 都不会丢（合并碰不到那两个路径）。
//
// 真会覆盖同名未跟踪文件的情况由 git 自己拦，见
// TestMergeBranchLetsGitRefuseAnUntrackedCollision。
func TestMergeBranchIgnoresUntrackedFilesInTheMainCheckout(t *testing.T) {
	ctx := context.Background()
	main := newMainRepo(t)
	wt := filepath.Join(main, ".worktree", "task-1")
	runTestGit(t, main, "worktree", "add", "-b", "task-1", wt)
	writeTestFile(t, wt, "note.txt", "from worktree\n")
	runTestGit(t, wt, "add", "note.txt")
	runTestGit(t, wt, "commit", "-m", "work")

	// 别人留下的散落文件：没 add 过，也不在分支的改动范围里。
	writeTestFile(t, main, "someone-elses-probe.mjs", "// 别人的探针\n")

	result, err := MergeBranch(ctx, MergeOptions{Source: "task-1", Target: "main", MainDir: main})
	if err != nil {
		t.Fatalf("an untracked file must not block the merge: %v", err)
	}
	if !result.Merged {
		t.Fatalf("Merged = false (message=%q), want the branch merged", result.Message)
	}
	// 合完了，别人的文件必须一字不少地还在。
	if got := readTestFile(t, filepath.Join(main, "someone-elses-probe.mjs")); got != "// 别人的探针\n" {
		t.Fatalf("the untracked file was touched: %q", got)
	}
}

// 未跟踪文件真会和合并撞上时，**git 自己**会拦（"untracked working tree files would
// be overwritten by merge"）。这条测试钉的是「别静默丢东西」，不是「必须提前拦」。
func TestMergeBranchLetsGitRefuseAnUntrackedCollision(t *testing.T) {
	ctx := context.Background()
	main := newMainRepo(t)
	wt := filepath.Join(main, ".worktree", "task-1")
	runTestGit(t, main, "worktree", "add", "-b", "task-1", wt)
	// 分支新建一个 main 上还没有的文件，而 main 里恰好有个同名的未跟踪文件。
	writeTestFile(t, wt, "fresh.txt", "from worktree\n")
	runTestGit(t, wt, "add", "fresh.txt")
	runTestGit(t, wt, "commit", "-m", "add fresh.txt")
	writeTestFile(t, main, "fresh.txt", "mine, untracked\n")

	if _, err := MergeBranch(ctx, MergeOptions{Source: "task-1", Target: "main", MainDir: main}); err == nil {
		t.Fatal("git must refuse to overwrite an untracked file")
	}
	// 拦下之后那个文件必须原样还在 —— 这是「宁可失败也别丢东西」的那条线。
	if got := readTestFile(t, filepath.Join(main, "fresh.txt")); got != "mine, untracked\n" {
		t.Fatalf("the untracked file was overwritten: %q", got)
	}
}

// 分支已经合进主干时，主 checkout 脏不脏**完全无关** —— 一步 checkout/merge 都不会
// 发生。2026-10-08 用户实测：task-37 的活早就合完了，收尾却因为主 checkout 里两个
// 未跟踪文件报「主 checkout 有未提交改动」。这条测试连**已跟踪**改动一起钉住，因为
// 顺序错位（脏检查排在祖先判断之前）时它同样会误报。
func TestMergeBranchSkipsAnAlreadyMergedBranchEvenWhenTheMainCheckoutIsDirty(t *testing.T) {
	ctx := context.Background()
	main := newMainRepo(t)
	wt := filepath.Join(main, ".worktree", "task-1")
	runTestGit(t, main, "worktree", "add", "-b", "task-1", wt)
	writeTestFile(t, wt, "note.txt", "from worktree\n")
	runTestGit(t, wt, "add", "note.txt")
	runTestGit(t, wt, "commit", "-m", "work")
	// 先真合一次，让分支成为 main 的祖先。
	runTestGit(t, main, "merge", "-q", "--no-ff", "task-1")

	// 再制造两类脏：已跟踪文件的改动 + 未跟踪文件。
	writeTestFile(t, main, "uncommitted.txt", "seed\n")
	runTestGit(t, main, "add", "uncommitted.txt")
	runTestGit(t, main, "commit", "-m", "seed tracked file")
	writeTestFile(t, main, "uncommitted.txt", "still working\n")
	writeTestFile(t, main, "probe.mjs", "// 别人的探针\n")

	result, err := MergeBranch(ctx, MergeOptions{Source: "task-1", Target: "main", MainDir: main})
	if err != nil {
		t.Fatalf("nothing to merge means the dirty checkout is irrelevant: %v", err)
	}
	if result.Merged {
		t.Fatalf("Merged = true, want the already-merged branch skipped (message=%q)", result.Message)
	}
	// 跳过合并不该动任何东西。
	if got := readTestFile(t, filepath.Join(main, "uncommitted.txt")); got != "still working\n" {
		t.Fatalf("the tracked change was touched: %q", got)
	}
	if got := readTestFile(t, filepath.Join(main, "probe.mjs")); got != "// 别人的探针\n" {
		t.Fatalf("the untracked file was touched: %q", got)
	}
}

// trackedDirtyPaths 是合并用的收窄判据：未跟踪的跳过，已跟踪的照报，
// .worktree/ 容器目录照旧排除。
func TestTrackedDirtyPathsSkipsUntrackedButKeepsTracked(t *testing.T) {
	ctx := context.Background()
	main := newMainRepo(t)
	writeTestFile(t, main, "tracked.txt", "seed\n")
	runTestGit(t, main, "add", "tracked.txt")
	runTestGit(t, main, "commit", "-m", "seed tracked file")
	writeTestFile(t, main, "tracked.txt", "modified\n")
	writeTestFile(t, main, "untracked.mjs", "// 探针\n")
	wt := filepath.Join(main, ".worktree", "task-live")
	runTestGit(t, main, "worktree", "add", "-b", "task-live", wt)

	tracked, err := trackedDirtyPaths(ctx, main)
	if err != nil {
		t.Fatalf("trackedDirtyPaths: %v", err)
	}
	if len(tracked) != 1 || tracked[0] != "tracked.txt" {
		t.Fatalf("trackedDirtyPaths = %v, want [tracked.txt]", tracked)
	}

	// 宽的那个照旧把未跟踪的也列出来（别的调用方要「全部改动」这个语义）。
	all, err := meaningfulDirtyPaths(ctx, main)
	if err != nil {
		t.Fatalf("meaningfulDirtyPaths: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("meaningfulDirtyPaths = %v, want the tracked + untracked files", all)
	}
}

// readTestFile 读一个测试文件的内容；缺失即失败。
func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestMergeBranchRefusesLinkedWorktreeAsMainDir(t *testing.T) {
	ctx := context.Background()
	main := newMainRepo(t)
	wt := filepath.Join(main, ".worktree", "task-1")
	runTestGit(t, main, "worktree", "add", "-b", "task-1", wt)
	writeTestFile(t, wt, "note.txt", "from worktree\n")
	runTestGit(t, wt, "add", "note.txt")
	runTestGit(t, wt, "commit", "-m", "work")

	// 在 worktree 里合自己的分支会得到误导性的 "Already up to date"。入口就该堵死。
	if _, err := MergeBranch(ctx, MergeOptions{Source: "task-1", Target: "main", MainDir: wt}); err == nil {
		t.Fatal("a linked worktree must be rejected as MainDir")
	} else if !strings.Contains(err.Error(), "linked worktree") {
		t.Fatalf("error = %v, want it to explain this is a linked worktree", err)
	}
}

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

func TestMeaningfulDirtyPathsIgnoresWorktreeContainer(t *testing.T) {
	ctx := context.Background()
	main := newMainRepo(t)
	// .worktree/ 是 worktree 的容器目录，主 checkout 必然看到它（未 gitignore 时是 ??）。
	// 不排除它的话每次收尾都误报「主 checkout 不干净」，流程永远走不下去。
	wt := filepath.Join(main, ".worktree", "task-live")
	runTestGit(t, main, "worktree", "add", "-b", "task-live", wt)
	writeTestFile(t, wt, "note.txt", "work\n")

	dirty, err := meaningfulDirtyPaths(ctx, main)
	if err != nil {
		t.Fatalf("meaningfulDirtyPaths: %v", err)
	}
	for _, path := range dirty {
		if strings.HasPrefix(path, ".worktree") {
			t.Fatalf("dirty = %v, want .worktree/ excluded", dirty)
		}
	}
	if len(dirty) != 0 {
		t.Fatalf("dirty = %v, want empty", dirty)
	}
}

// ── CanMergeCleanly：只读合并判定 ──────────────────────────────────

// 判定必须能认出「合得进去」：main 没动过时，分支上的改动不该有冲突。
func TestCanMergeCleanlyReportsCleanWhenMergeSucceeds(t *testing.T) {
	main := newMainRepo(t)
	wt := filepath.Join(main, ".worktree", "task-1")
	runTestGit(t, main, "worktree", "add", "-b", "task-1", wt)
	writeTestFile(t, wt, "note.txt", "from worktree\n")
	runTestGit(t, wt, "add", "note.txt")
	runTestGit(t, wt, "commit", "-qm", "work")

	feasibility, err := CanMergeCleanly(context.Background(), main, "task-1", "main")
	if err != nil {
		t.Fatalf("CanMergeCleanly: %v", err)
	}
	if !feasibility.Clean {
		t.Fatalf("Clean = false (reason=%q), want the merge to be feasible", feasibility.Reason)
	}
}

// 两边改同一行 → 必须报冲突并列出文件。
func TestCanMergeCleanlyReportsConflictWithFiles(t *testing.T) {
	main := newMainRepo(t)
	wt := filepath.Join(main, ".worktree", "task-1")
	runTestGit(t, main, "worktree", "add", "-b", "task-1", wt)
	writeTestFile(t, wt, "note.txt", "from worktree\n")
	runTestGit(t, wt, "add", "note.txt")
	runTestGit(t, wt, "commit", "-qm", "work")
	// 主 checkout 也改同一行。
	writeTestFile(t, main, "note.txt", "from main\n")
	runTestGit(t, main, "add", "note.txt")
	runTestGit(t, main, "commit", "-qm", "main edit")

	feasibility, err := CanMergeCleanly(context.Background(), main, "task-1", "main")
	if err != nil {
		t.Fatalf("CanMergeCleanly: %v", err)
	}
	if feasibility.Clean {
		t.Fatal("Clean = true, want the conflict to be reported")
	}
	if len(feasibility.Files) != 1 || feasibility.Files[0] != "note.txt" {
		t.Fatalf("Files = %v, want [note.txt]", feasibility.Files)
	}
	if strings.TrimSpace(feasibility.Reason) == "" {
		t.Fatal("Reason must say why the merge is not feasible")
	}
}

// 分支已在主干里 = 没什么要合的，机械清场照做（不是冲突）。
func TestCanMergeCleanlyReportsCleanWhenAlreadyMerged(t *testing.T) {
	main := newMainRepo(t)
	wt := filepath.Join(main, ".worktree", "task-1")
	runTestGit(t, main, "worktree", "add", "-b", "task-1", wt)
	writeTestFile(t, wt, "note.txt", "from worktree\n")
	runTestGit(t, wt, "add", "note.txt")
	runTestGit(t, wt, "commit", "-qm", "work")
	runTestGit(t, main, "merge", "--no-ff", "-qm", "merge", "task-1")

	feasibility, err := CanMergeCleanly(context.Background(), main, "task-1", "main")
	if err != nil {
		t.Fatalf("CanMergeCleanly: %v", err)
	}
	if !feasibility.Clean {
		t.Fatalf("Clean = false (reason=%q), want an already-merged branch to be feasible", feasibility.Reason)
	}
}

// 判定**不许碰仓库**：这是它能在「用户还没决定要不要收尾」时安全调用的前提。
func TestCanMergeCleanlyDoesNotTouchRepo(t *testing.T) {
	main := newMainRepo(t)
	wt := filepath.Join(main, ".worktree", "task-1")
	runTestGit(t, main, "worktree", "add", "-b", "task-1", wt)
	writeTestFile(t, wt, "note.txt", "from worktree\n")
	runTestGit(t, wt, "add", "note.txt")
	runTestGit(t, wt, "commit", "-qm", "work")
	writeTestFile(t, main, "note.txt", "from main\n")
	runTestGit(t, main, "add", "note.txt")
	runTestGit(t, main, "commit", "-qm", "main edit")

	before := runTestGit(t, main, "status", "--porcelain")
	_, err := CanMergeCleanly(context.Background(), main, "task-1", "main")
	if err != nil {
		t.Fatalf("CanMergeCleanly: %v", err)
	}
	if after := runTestGit(t, main, "status", "--porcelain"); after != before {
		t.Fatalf("the repo changed during a read-only decision:\nbefore=%q\nafter =%q", before, after)
	}
	// 真合并会留下 MERGE_HEAD；干跑一个字节都不该留。
	if mergeInProgress(context.Background(), main) {
		t.Fatal("a read-only decision must not leave MERGE_HEAD behind")
	}
}

// ── RemoveWorktreeDir ───────────────────────────────────────────────

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

// ── MigrateWorktreeUploads ──────────────────────────────────────────

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
