package gitview

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
