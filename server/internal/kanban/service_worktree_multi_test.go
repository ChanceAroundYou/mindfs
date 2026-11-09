package kanban

import (
	"context"
	"os"
	"strings"
	"testing"
)

// 多 worktree 创建：同一 task 两个 NodeRun 各一棵树（fakeRunner 真建目录），
// registry 双行 active、legacy 主列仍是 ensureTaskWorktree 的那棵（兼容）。
// 关闭：按 NodeRun 归属关，幂等；needs_user 拒绝关。
func TestEnsureNodeWorktreeCreatesPerNodeRun(t *testing.T) {
	ctx := context.Background()
	runner := &fakeRunner{}
	svc, root := newTestService(t, runner)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:         root.ID,
		Stages:         []StageTemplate{userStage("Describe")},
		Input:          "multi",
		CreateWorktree: true,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	store, _ := svc.taskStore(root.ID)
	storeNow := store

	e1, err := svc.EnsureNodeWorktree(ctx, EnsureNodeWorktreeInput{RootID: root.ID, TaskID: detail.Task.ID, NodeRunID: "nr-1"})
	if err != nil {
		t.Fatalf("Ensure(nr-1): %v", err)
	}
	e2, err := svc.EnsureNodeWorktree(ctx, EnsureNodeWorktreeInput{RootID: root.ID, TaskID: detail.Task.ID, NodeRunID: "nr-2"})
	if err != nil {
		t.Fatalf("Ensure(nr-2): %v", err)
	}
	if e1.Path == e2.Path {
		t.Fatalf("two NodeRuns must get distinct worktrees: %q == %q", e1.Path, e2.Path)
	}
	if e1.NodeRunID != "nr-1" || e2.NodeRunID != "nr-2" {
		t.Fatalf("attribution lost: %+v / %+v", e1, e2)
	}
	for _, e := range []WorktreeEntry{e1, e2} {
		if e.State != WorktreeStateActive {
			t.Fatalf("state = %q, want active", e.State)
		}
		if !worktreeDirUsable(e.Path) {
			t.Fatalf("dir not usable: %q", e.Path)
		}
	}
	// 幂等：同 NodeRun 再 ensure 返回同一行，不新建
	e1b, err := svc.EnsureNodeWorktree(ctx, EnsureNodeWorktreeInput{RootID: root.ID, TaskID: detail.Task.ID, NodeRunID: "nr-1"})
	if err != nil {
		t.Fatalf("Ensure idempotent: %v", err)
	}
	if e1b.WorktreeID != e1.WorktreeID || e1b.Path != e1.Path {
		t.Fatalf("idempotent ensure returned different tree: %+v vs %+v", e1b, e1)
	}
	active, err := storeNow.ListActiveWorktreeEntries(ctx, detail.Task.ID)
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("active = %d, want 2", len(active))
	}
	// legacy 主列未被多 worktree 路径污染：此前 ensureTaskWorktree 未跑过，
	// 主列保持空（NodeRun 路径不抢主列）。
	task, err := storeNow.GetTask(ctx, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if strings.TrimSpace(task.WorktreePath) != "" {
		t.Fatalf("legacy primary path polluted by NodeRun ensure: %q", task.WorktreePath)
	}
}

func TestCloseNodeWorktreeByAttribution(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{RootID: root.ID, Stages: []StageTemplate{userStage("Describe")}, Input: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	e, err := svc.EnsureNodeWorktree(ctx, EnsureNodeWorktreeInput{RootID: root.ID, TaskID: detail.Task.ID, NodeRunID: "nr-1"})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	path := e.Path
	closed, err := svc.CloseNodeWorktree(ctx, CloseNodeWorktreeInput{RootID: root.ID, TaskID: detail.Task.ID, NodeRunID: "nr-1"})
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if closed.State != WorktreeStateClosed {
		t.Fatalf("state = %q, want closed", closed.State)
	}
	// 不带 RemoveDir 时目录不动（只翻状态）
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("plain close must keep dir: %v", statErr)
	}
	// 幂等再关
	again, err := svc.CloseNodeWorktree(ctx, CloseNodeWorktreeInput{RootID: root.ID, TaskID: detail.Task.ID, NodeRunID: "nr-1"})
	if err != nil {
		t.Fatalf("Close idempotent: %v", err)
	}
	if again.State != WorktreeStateClosed {
		t.Fatalf("idempotent close state = %q", again.State)
	}
	// 缺归属拒绝
	if _, err := svc.CloseNodeWorktree(ctx, CloseNodeWorktreeInput{RootID: root.ID, TaskID: detail.Task.ID}); err == nil {
		t.Fatal("close without attribution must fail")
	}
}

func TestCloseNodeWorktreeRefusesWhenNeedsUser(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, _ := svc.CreateTask(ctx, CreateTaskInput{RootID: root.ID, Stages: []StageTemplate{userStage("Describe")}, Input: "x"})
	store, _ := svc.taskStore(root.ID)
	_ = store
	e, err := svc.EnsureNodeWorktree(ctx, EnsureNodeWorktreeInput{RootID: root.ID, TaskID: detail.Task.ID, NodeRunID: "nr-1"})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if _, err := store.UpdateWorktreeEntryState(ctx, detail.Task.ID, e.WorktreeID, WorktreeStateNeedsUser, "", []string{"a.txt"}, "conflict"); err != nil {
		t.Fatalf("mark needs_user: %v", err)
	}
	if _, err := svc.CloseNodeWorktree(ctx, CloseNodeWorktreeInput{RootID: root.ID, TaskID: detail.Task.ID, NodeRunID: "nr-1", RemoveDir: true}); err == nil {
		t.Fatal("closing a needs_user worktree must be refused (would lose work)")
	}
	// 目录还在、状态未变
	if _, statErr := os.Stat(e.Path); statErr != nil {
		t.Fatalf("needs_user dir must survive refused close: %v", statErr)
	}
	cur, _ := store.GetWorktreeEntry(ctx, detail.Task.ID, e.WorktreeID)
	if cur.State != WorktreeStateNeedsUser {
		t.Fatalf("state changed on refused close: %q", cur.State)
	}
}

// ensureTaskWorktree 双写：legacy 主 worktree 也进 registry（node_run_id 空），
// 存量单 worktree 任务的 registry 行数 == 1 且与 tasks 列同路径。
func TestEnsureTaskWorktreeAlsoRegisters(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:         root.ID,
		Stages:         []StageTemplate{userStage("Describe"), agentStage("Fix", "Fix:\n{previous_input}")},
		Input:          "x",
		CreateWorktree: true,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	store, _ := svc.taskStore(root.ID)
	task, _ := store.GetTask(ctx, detail.Task.ID)
	got, err := svc.ensureTaskWorktree(ctx, store, task)
	if err != nil {
		t.Fatalf("ensureTaskWorktree: %v", err)
	}
	entries, err := store.ListActiveWorktreeEntries(ctx, detail.Task.ID)
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("registry active = %d, want 1 (primary dual-write)", len(entries))
	}
	if entries[0].Path != got.WorktreePath {
		t.Fatalf("registry path %q != tasks path %q", entries[0].Path, got.WorktreePath)
	}
}
