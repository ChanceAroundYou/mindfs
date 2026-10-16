package gitview

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

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
