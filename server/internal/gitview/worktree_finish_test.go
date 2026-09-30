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

	// 主 checkout 有别人的未提交改动时，checkout -q target 会静默丢弃 → 必须在动手前拦下。
	writeTestFile(t, main, "uncommitted.txt", "someone is working here\n")
	_, err := MergeBranch(ctx, MergeOptions{Source: "task-1", Target: "main", MainDir: main})
	if err == nil {
		t.Fatal("merge must refuse while the main checkout is dirty")
	}
	if !strings.Contains(err.Error(), "uncommitted.txt") {
		t.Fatalf("error must name the dirty file, got %v", err)
	}
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
