package gitview

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 嵌套工作树（w2 → w1 → main）的第一跳：节点树并回任务树时，目标分支 task-1
// 正被任务树这个 linked worktree 检出。老实现一律在主 checkout 里合并，validateTarget
// 会判「目标被另一个 worktree 占用」直接失败 —— 任务树 + 节点树这个组合根本走不通。
func TestMergeBranchMergesIntoTheWorktreeThatHoldsTheTarget(t *testing.T) {
	ctx := context.Background()
	main := newMainRepo(t)
	taskWT := filepath.Join(main, ".worktree", "task-1")
	runTestGit(t, main, "worktree", "add", "-b", "task-1", taskWT)
	nodeWT := filepath.Join(main, ".worktree", "node-1")
	runTestGit(t, main, "worktree", "add", "-b", "node-1", nodeWT, "task-1")
	writeTestFile(t, nodeWT, "feature.txt", "from node\n")
	runTestGit(t, nodeWT, "add", "feature.txt")
	runTestGit(t, nodeWT, "commit", "-m", "node work")

	dir, err := MergeTargetDir(ctx, main, "task-1")
	if err != nil {
		t.Fatalf("MergeTargetDir: %v", err)
	}
	if !samePath(dir, taskWT) {
		t.Fatalf("MergeTargetDir = %q, want the task worktree %q", dir, taskWT)
	}

	res, err := MergeBranch(ctx, MergeOptions{Source: "node-1", Target: "task-1", MainDir: main, TargetDir: dir})
	if err != nil {
		t.Fatalf("MergeBranch into occupied target worktree: %v", err)
	}
	if !res.Merged {
		t.Fatal("merge should have happened inside the task worktree")
	}
	if got := strings.TrimSpace(runTestGit(t, taskWT, "show", "task-1:feature.txt")); got != "from node" {
		t.Fatalf("task-1 content = %q, want merged node work", got)
	}
	// 主 checkout 必须原地不动：它的当前分支不该被切到 task-1。
	if got := strings.TrimSpace(runTestGit(t, main, "rev-parse", "--abbrev-ref", "HEAD")); got != "main" {
		t.Fatalf("main checkout HEAD = %q, want main untouched", got)
	}

	// 第二跳：任务树并回主分支（正常的 TargetDir 为空路径）。
	if _, err := MergeBranch(ctx, MergeOptions{Source: "task-1", Target: "main", MainDir: main}); err != nil {
		t.Fatalf("MergeBranch task worktree into main: %v", err)
	}
	if got := strings.TrimSpace(runTestGit(t, main, "show", "main:feature.txt")); got != "from node" {
		t.Fatalf("main content = %q, want the nested work to arrive via w2 -> w1 -> main", got)
	}
}

// 没有 TargetDir 时保持老行为：目标被别的 worktree 占用就明确拒绝（不是静默跑去
// 错误的地方合并）。这是「缺陷 A 被修掉」与「校验被削掉」的分界线。
func TestMergeBranchStillRefusesOccupiedTargetWithoutTargetDir(t *testing.T) {
	ctx := context.Background()
	main := newMainRepo(t)
	taskWT := filepath.Join(main, ".worktree", "task-1")
	runTestGit(t, main, "worktree", "add", "-b", "task-1", taskWT)

	_, err := MergeBranch(ctx, MergeOptions{Source: "main", Target: "task-1", MainDir: main})
	if err == nil {
		t.Fatal("merge into a target held by another worktree must fail when no TargetDir is given")
	}
	if !strings.Contains(err.Error(), "占用") {
		t.Fatalf("error should name the occupancy, got %v", err)
	}
}

// TargetDir 必须真的停在该分支上：传错目录（比如另一条分支的 worktree）时不能在那里
// 合并。目标分支刻意不被任何 worktree 检出，这样「占用」检查放行，只有分支比对能拦。
func TestMergeBranchRejectsTargetDirOnTheWrongBranch(t *testing.T) {
	ctx := context.Background()
	main := newMainRepo(t)
	runTestGit(t, main, "branch", "task-1")
	other := filepath.Join(main, ".worktree", "other")
	runTestGit(t, main, "worktree", "add", "-b", "other", other)

	_, err := MergeBranch(ctx, MergeOptions{Source: "main", Target: "task-1", MainDir: main, TargetDir: other})
	if err == nil {
		t.Fatal("TargetDir holding a different branch must be rejected")
	}
	if !strings.Contains(err.Error(), "不是 task-1") {
		t.Fatalf("error should explain the branch mismatch, got %v", err)
	}
}

// MergeTargetDir 的三种形态：主 checkout 上 → mainDir；被 linked worktree 占用 →
// 那棵树；分支没被任何 worktree 检出 → 空串（调用方回落到主 checkout 的 checkout+merge）。
func TestMergeTargetDirResolvesAllThreeCases(t *testing.T) {
	ctx := context.Background()
	main := newMainRepo(t)
	taskWT := filepath.Join(main, ".worktree", "task-1")
	runTestGit(t, main, "worktree", "add", "-b", "task-1", taskWT)
	runTestGit(t, main, "branch", "idle-branch")

	if dir, err := MergeTargetDir(ctx, main, "main"); err != nil || !samePath(dir, main) {
		t.Fatalf("main branch should resolve to the main checkout, got %q err=%v", dir, err)
	}
	if dir, err := MergeTargetDir(ctx, main, "task-1"); err != nil || !samePath(dir, taskWT) {
		t.Fatalf("task-1 should resolve to its worktree, got %q err=%v", dir, err)
	}
	if dir, err := MergeTargetDir(ctx, main, "idle-branch"); err != nil || dir != "" {
		t.Fatalf("unchecked-out branch should resolve to empty, got %q err=%v", dir, err)
	}
}

// AddWorktreeFrom：新分支的基线是 base 的 tip，而不是主 checkout HEAD（缺陷 B）。
func TestAddWorktreeFromBasesNewBranchOnGivenTip(t *testing.T) {
	ctx := context.Background()
	main := newMainRepo(t)
	taskWT := filepath.Join(main, ".worktree", "task-1")
	runTestGit(t, main, "worktree", "add", "-b", "task-1", taskWT)
	writeTestFile(t, taskWT, "task.txt", "task work\n")
	runTestGit(t, taskWT, "add", "task.txt")
	runTestGit(t, taskWT, "commit", "-m", "task work")

	nodeWT := filepath.Join(main, ".worktree", "node-1")
	if err := AddWorktreeFrom(ctx, main, nodeWT, "new", "node-1", "task-1"); err != nil {
		t.Fatalf("AddWorktreeFrom: %v", err)
	}
	// 节点树里应该已经能看到任务树的活；main 上则不该有。
	if got := strings.TrimSpace(runTestGit(t, nodeWT, "show", "node-1:task.txt")); got != "task work" {
		t.Fatalf("node branch base = %q, want task-1 tip", got)
	}
	if _, err := runGitAllowError(main, "show", "main:task.txt"); err == nil {
		t.Fatal("task work must not leak into main before the task tree merges back")
	}
}

// 节点树合进任务树之后，节点分支必须能删掉。`git branch -d` 只在「已并入当前
// HEAD」时才允许删 —— 主 checkout 停在 main 上看这条已并入 task-1 的分支，会
// 永远拒绝，于是每个嵌套节点都漏一条分支（「结束自动合并并清理」缺了清理）。
func TestDeleteBranchMergedAtAcceptsBranchMergedIntoAnotherBranch(t *testing.T) {
	ctx := context.Background()
	main := newMainRepo(t)
	taskWT := filepath.Join(main, ".worktree", "task-1")
	runTestGit(t, main, "worktree", "add", "-b", "task-1", taskWT)
	nodeWT := filepath.Join(main, ".worktree", "node-1")
	runTestGit(t, main, "worktree", "add", "-b", "node-1", nodeWT, "task-1")
	writeTestFile(t, nodeWT, "node.txt", "node work\n")
	runTestGit(t, nodeWT, "add", "node.txt")
	runTestGit(t, nodeWT, "commit", "-m", "node work")
	runTestGit(t, taskWT, "merge", "--no-ff", "-m", "merge node", "node-1")
	mergedCommit := strings.TrimSpace(runTestGit(t, taskWT, "rev-parse", "HEAD"))
	runTestGit(t, main, "worktree", "remove", nodeWT)

	if err := DeleteBranchMergedAt(ctx, main, "node-1", mergedCommit); err != nil {
		t.Fatalf("DeleteBranchMergedAt: %v", err)
	}
	if _, err := runGitAllowError(main, "show-ref", "--verify", "refs/heads/node-1"); err == nil {
		t.Fatal("node branch should be gone after its work landed in task-1")
	}
}

// 历史落库的 merged_commit 是 `log --oneline` 的整行，不是裸 sha —— 删分支时
// 必须能吃下这种形状（库里已有按这种格式写的行）。
func TestDeleteBranchMergedAtAcceptsOnelineCommit(t *testing.T) {
	ctx := context.Background()
	main := newMainRepo(t)
	taskWT := filepath.Join(main, ".worktree", "task-1")
	runTestGit(t, main, "worktree", "add", "-b", "task-1", taskWT)
	nodeWT := filepath.Join(main, ".worktree", "node-1")
	runTestGit(t, main, "worktree", "add", "-b", "node-1", nodeWT, "task-1")
	writeTestFile(t, nodeWT, "node.txt", "node work\n")
	runTestGit(t, nodeWT, "add", "node.txt")
	runTestGit(t, nodeWT, "commit", "-m", "node work")
	runTestGit(t, taskWT, "merge", "--no-ff", "-m", "merge node", "node-1")
	oneline := strings.TrimSpace(runTestGit(t, taskWT, "log", "--oneline", "-1"))
	runTestGit(t, main, "worktree", "remove", nodeWT)

	if err := DeleteBranchMergedAt(ctx, main, "node-1", oneline); err != nil {
		t.Fatalf("DeleteBranchMergedAt with an oneline commit: %v", err)
	}
	if _, err := runGitAllowError(main, "show-ref", "--verify", "refs/heads/node-1"); err == nil {
		t.Fatal("node branch should be gone")
	}
}

// 没合进去的分支一律拒绝 —— 与 `branch -d` 同一条底线：宁可留分支也不丢提交。
func TestDeleteBranchMergedAtRefusesUnmergedBranch(t *testing.T) {
	ctx := context.Background()
	main := newMainRepo(t)
	taskWT := filepath.Join(main, ".worktree", "task-1")
	runTestGit(t, main, "worktree", "add", "-b", "task-1", taskWT)
	nodeWT := filepath.Join(main, ".worktree", "node-1")
	runTestGit(t, main, "worktree", "add", "-b", "node-1", nodeWT, "task-1")
	writeTestFile(t, nodeWT, "node.txt", "node work\n")
	runTestGit(t, nodeWT, "add", "node.txt")
	runTestGit(t, nodeWT, "commit", "-m", "node work")
	runTestGit(t, main, "worktree", "remove", nodeWT)
	mergedCommit := strings.TrimSpace(runTestGit(t, taskWT, "rev-parse", "HEAD"))

	if err := DeleteBranchMergedAt(ctx, main, "node-1", mergedCommit); err == nil {
		t.Fatal("unmerged node branch must not be deleted")
	}
	if _, err := runGitAllowError(main, "show-ref", "--verify", "refs/heads/node-1"); err != nil {
		t.Fatal("unmerged node branch should still exist")
	}
}

// runGitAllowError 跑一条 git 命令并把失败当返回值（断言「这里不该有」用）。
func runGitAllowError(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}
