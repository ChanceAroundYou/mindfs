package kanban

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/server/internal/fs"
	"mindfs/server/internal/gitview"
)

// 自动合并用真 git（merge / worktree remove / branch -d 行为全靠 git 自身，
// mock 掉等于把被验的东西也 mock 掉 —— 与 worktree_finish_test.go 同一口径）。

// multiMergeFixture 造「主 checkout + 两个 registry worktree」的局面：
// wtA（agent/a）与 wtB（agent/b）各带一个提交，归属 nr-a / nr-b。
// task 只是 registry 的宿主（CreateWorktree=false：多 worktree 走 registry 路径）。
func multiMergeFixture(t *testing.T, editA, editB string) (svc *Service, root fs.RootInfo, mainDir string, taskID string) {
	t.Helper()
	mainDir = newGitRoot(t)
	wtA := filepath.Join(mainDir, ".worktree", "agent-a")
	wtB := filepath.Join(mainDir, ".worktree", "agent-b")
	gitForTest(t, mainDir, "worktree", "add", "-q", "-b", "agent/a", wtA)
	gitForTest(t, mainDir, "worktree", "add", "-q", "-b", "agent/b", wtB)
	if editA != "" {
		writeCommit(t, wtA, "a.txt", editA, "work a")
	}
	if editB != "" {
		writeCommit(t, wtB, "b.txt", editB, "work b")
	}
	root = fs.NewRootInfo("root", "root", mainDir)
	svc = NewService(testRoots{root: root})
	svc.SetWriterGate(NewWriterGate())
	svc.SetRunner(&fakeRunner{})
	detail, err := svc.CreateTask(context.Background(), CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{userStage("Describe")},
		Input:  "multi merge",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	taskID = detail.Task.ID
	store, _ := svc.taskStore(root.ID)
	if _, err := store.UpsertWorktreeEntry(context.Background(), WorktreeEntry{
		TaskID: taskID, WorktreeID: "agent/a", Branch: "agent/a", Path: wtA, RootID: root.ID, NodeRunID: "nr-a", State: WorktreeStateActive,
	}); err != nil {
		t.Fatalf("register a: %v", err)
	}
	if _, err := store.UpsertWorktreeEntry(context.Background(), WorktreeEntry{
		TaskID: taskID, WorktreeID: "agent/b", Branch: "agent/b", Path: wtB, RootID: root.ID, NodeRunID: "nr-b", State: WorktreeStateActive,
	}); err != nil {
		t.Fatalf("register b: %v", err)
	}
	return svc, root, mainDir, taskID
}

func writeCommit(t *testing.T, dir, name, content, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	gitForTest(t, dir, "add", name)
	gitForTest(t, dir, "commit", "-qm", msg)
}

// 成功自动合并：两个分支都并入 main，registry 翻 merged，目录/分支清掉（CloseAfterMerge）。
func TestAutoMergeWorktreeSuccess(t *testing.T) {
	ctx := context.Background()
	svc, root, mainDir, taskID := multiMergeFixture(t, "a\n", "b\n")
	store, _ := svc.taskStore(root.ID)
	wtA := filepath.Join(mainDir, ".worktree", "agent-a")

	res, err := svc.AutoMergeWorktree(ctx, AutoMergeWorktreeInput{
		RootID: root.ID, TaskID: taskID, NodeRunID: "nr-a",
		IntegrationBranch: "main", CloseAfterMerge: true, DeleteBranch: true,
	})
	if err != nil {
		t.Fatalf("AutoMerge: %v", err)
	}
	if !res.Merged {
		t.Fatal("Merged must be true for a fresh branch")
	}
	if res.Entry.State != WorktreeStateClosed {
		t.Fatalf("after merge+close state = %q, want closed", res.Entry.State)
	}
	if res.Entry.MergedCommit == "" {
		t.Fatal("merged commit must be recorded")
	}
	// 另一棵树没动
	other, err := store.GetWorktreeEntry(ctx, taskID, "agent/b")
	if err != nil {
		t.Fatalf("get b: %v", err)
	}
	if other.State != WorktreeStateActive {
		t.Fatalf("sibling state = %q, want active (untouched)", other.State)
	}
	// 合并产物在 main 上
	content, err := os.ReadFile(filepath.Join(mainDir, "a.txt"))
	if err != nil {
		t.Fatalf("a.txt not on main: %v", err)
	}
	if string(content) != "a\n" {
		t.Fatalf("a.txt on main = %q, want a", content)
	}
	// 拆掉的目录/分支真没了
	if _, statErr := os.Stat(wtA); !os.IsNotExist(statErr) {
		t.Fatalf("merged worktree dir must be removed, stat err = %v", statErr)
	}
	if out := gitForTest(t, mainDir, "branch", "--list", "agent/a"); strings.TrimSpace(out) != "" {
		t.Fatalf("merged branch must be deleted, got %q", out)
	}
	// 幂等：再合一次直接返回已 merged，不报错
	again, err := svc.AutoMergeWorktree(ctx, AutoMergeWorktreeInput{RootID: root.ID, TaskID: taskID, WorktreeID: "agent/a"})
	if err != nil {
		t.Fatalf("idempotent merge: %v", err)
	}
	if again.Entry.State == "" {
		t.Fatal("idempotent merge must return entry")
	}
	_ = gitview.ErrMergeConflict // 冲突类型存在（本用例不触发）
}

// 冲突自动合并：needs_user + merge_conflict activity + 两分支保留 + 不自动 stash/abort。
func TestAutoMergeWorktreeConflictKeepsBranches(t *testing.T) {
	ctx := context.Background()
	mainDir := newGitRoot(t)
	wtA := filepath.Join(mainDir, ".worktree", "agent-a")
	gitForTest(t, mainDir, "worktree", "add", "-q", "-b", "agent/a", wtA)
	// 两边改同一文件同一行 → 必冲突
	writeCommit(t, wtA, "note.txt", "worktree side\n", "work")
	if err := os.WriteFile(filepath.Join(mainDir, "note.txt"), []byte("main side\n"), 0o644); err != nil {
		t.Fatalf("seed main: %v", err)
	}
	gitForTest(t, mainDir, "add", "note.txt")
	gitForTest(t, mainDir, "commit", "-qm", "main edit")

	root := fs.NewRootInfo("root", "root", mainDir)
	svc := NewService(testRoots{root: root})
	svc.SetWriterGate(NewWriterGate())
	svc.SetRunner(&fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{RootID: root.ID, Stages: []StageTemplate{userStage("Describe")}, Input: "conflict"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	store, _ := svc.taskStore(root.ID)
	if _, err := store.UpsertWorktreeEntry(ctx, WorktreeEntry{
		TaskID: detail.Task.ID, WorktreeID: "agent/a", Branch: "agent/a", Path: wtA, RootID: root.ID, NodeRunID: "nr-a", State: WorktreeStateActive,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	_, err = svc.AutoMergeWorktree(ctx, AutoMergeWorktreeInput{
		RootID: root.ID, TaskID: detail.Task.ID, NodeRunID: "nr-a", IntegrationBranch: "main", CloseAfterMerge: true, DeleteBranch: true,
	})
	if err == nil {
		t.Fatal("conflicting merge must not succeed")
	}
	var mErr *MergeConflictError
	if !errors.As(err, &mErr) {
		t.Fatalf("err = %v (%T), want *MergeConflictError", err, err)
	}
	if len(mErr.ConflictFiles) != 1 || mErr.ConflictFiles[0] != "note.txt" {
		t.Fatalf("ConflictFiles = %v, want [note.txt]", mErr.ConflictFiles)
	}
	// registry → needs_user
	entry, err := store.GetWorktreeEntry(ctx, detail.Task.ID, "agent/a")
	if err != nil {
		t.Fatalf("get entry: %v", err)
	}
	if entry.State != WorktreeStateNeedsUser {
		t.Fatalf("state = %q, want needs_user", entry.State)
	}
	if len(entry.ConflictFiles) != 1 {
		t.Fatalf("conflict files not persisted: %+v", entry.ConflictFiles)
	}
	// merge_conflict activity 落库（留言板时间线）
	acts, err := store.ListActivities(ctx, detail.Task.ID)
	if err != nil {
		t.Fatalf("ListActivities: %v", err)
	}
	found := false
	for _, a := range acts {
		if a.Type == ActivityTypeMergeConflict {
			found = true
		}
	}
	if !found {
		t.Fatalf("merge_conflict activity missing: %+v", acts)
	}
	// 两分支 + worktree 都保留
	if out := gitForTest(t, mainDir, "branch", "--list", "agent/a"); strings.TrimSpace(out) == "" {
		t.Fatal("source branch must survive conflict")
	}
	if _, statErr := os.Stat(wtA); statErr != nil {
		t.Fatalf("worktree must survive conflict: %v", statErr)
	}
	// 没被自动 abort：MERGE_HEAD 还在（用户解完冲突提交后重跑即收敛）
	if _, statErr := os.Stat(filepath.Join(mainDir, ".git", "MERGE_HEAD")); statErr != nil {
		t.Fatalf("MERGE_HEAD must still exist (no auto abort): %v", statErr)
	}
	// 没被自动 stash：stash 列表为空
	if out := gitForTest(t, mainDir, "stash", "list"); strings.TrimSpace(out) != "" {
		t.Fatalf("no stash must be created, got %q", out)
	}
	// needs_user 时关闭被拒
	if _, err := svc.CloseNodeWorktree(ctx, CloseNodeWorktreeInput{RootID: root.ID, TaskID: detail.Task.ID, NodeRunID: "nr-a", RemoveDir: true}); err == nil {
		t.Fatal("closing a needs_user worktree must be refused")
	}
	// 用户解完冲突 → 重跑合并收敛为 merged
	if err := os.WriteFile(filepath.Join(mainDir, "note.txt"), []byte("resolved\n"), 0o644); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	gitForTest(t, mainDir, "add", "note.txt")
	gitForTest(t, mainDir, "commit", "-qm", "merge agent/a (resolve)")
	res, err := svc.AutoMergeWorktree(ctx, AutoMergeWorktreeInput{RootID: root.ID, TaskID: detail.Task.ID, NodeRunID: "nr-a", IntegrationBranch: "main", CloseAfterMerge: true, DeleteBranch: true})
	if err != nil {
		t.Fatalf("re-merge after resolve: %v", err)
	}
	if res.Entry.State != WorktreeStateClosed {
		t.Fatalf("after resolve state = %q, want closed (merged+closed)", res.Entry.State)
	}
}

// 主 checkout 脏时拒绝合并（那是「你自己的活还没收」，不翻 needs_user）。
func TestAutoMergeRefusesDirtyMainCheckout(t *testing.T) {
	ctx := context.Background()
	svc, root, mainDir, taskID := multiMergeFixture(t, "a\n", "")
	if err := os.WriteFile(filepath.Join(mainDir, "dirty.txt"), []byte("uncommitted\n"), 0o644); err != nil {
		t.Fatalf("dirty: %v", err)
	}
	gitForTest(t, mainDir, "add", "dirty.txt")
	// 故意不提交：tracked 脏 → MergeBranch 拒绝
	_, err := svc.AutoMergeWorktree(ctx, AutoMergeWorktreeInput{RootID: root.ID, TaskID: taskID, NodeRunID: "nr-a", IntegrationBranch: "main"})
	if err == nil {
		t.Fatal("dirty main checkout must refuse merge")
	}
	var dirty *FinishWorktreeDirty
	if !errors.As(err, &dirty) {
		t.Fatalf("err = %v (%T), want *FinishWorktreeDirty", err, err)
	}
	// 状态未变：仍 active，下次可重跑
	store, _ := svc.taskStore(root.ID)
	entry, _ := store.GetWorktreeEntry(ctx, taskID, "agent/a")
	if entry.State != WorktreeStateActive {
		t.Fatalf("state changed on dirty refusal: %q", entry.State)
	}
}
