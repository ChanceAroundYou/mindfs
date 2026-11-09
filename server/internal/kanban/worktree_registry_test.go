package kanban

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

// registry 持久化：CRUD、幂等 upsert、state 单调守卫、冲突字段落库、
// 引用计数（registry 口径 + 混合口径），以及「空表 = 只有主 worktree」的兼容
// （ListActiveWorktreeEntries 为空时 legacy 路径不受影响，见 service 层测试）。
func TestWorktreeRegistryCRUD(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	detail, err := svc.CreateTask(ctx, CreateTaskInput{RootID: root.ID, Stages: []StageTemplate{userStage("Describe")}, Input: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	taskID := detail.Task.ID

	// 兼容：新任务 registry 为空（只有主 worktree 语义，legacy 列说了算）。
	list, err := store.ListWorktreeEntries(ctx, taskID)
	if err != nil {
		t.Fatalf("ListWorktreeEntries: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("new task registry = %d rows, want 0", len(list))
	}

	// upsert + get
	e, err := store.UpsertWorktreeEntry(ctx, WorktreeEntry{TaskID: taskID, WorktreeID: "agent/a", Branch: "agent/a", Path: "/trees/a", RootID: root.ID, NodeRunID: "nr-1", State: WorktreeStateActive})
	if err != nil {
		t.Fatalf("UpsertWorktreeEntry: %v", err)
	}
	if e.WorktreeID != "agent/a" || e.NodeRunID != "nr-1" || e.State != WorktreeStateActive {
		t.Fatalf("upserted = %+v, want active agent/a nr-1", e)
	}
	// 幂等 upsert：改 path 不增行
	e2, err := store.UpsertWorktreeEntry(ctx, WorktreeEntry{TaskID: taskID, WorktreeID: "agent/a", Branch: "agent/a", Path: "/trees/a2", RootID: root.ID, NodeRunID: "nr-1", State: WorktreeStateActive})
	if err != nil {
		t.Fatalf("second UpsertWorktreeEntry: %v", err)
	}
	if e2.Path != "/trees/a2" {
		t.Fatalf("upsert did not update path: %q", e2.Path)
	}
	list, _ = store.ListWorktreeEntries(ctx, taskID)
	if len(list) != 1 {
		t.Fatalf("after upsert registry = %d rows, want 1", len(list))
	}

	// 第二棵树（不同 NodeRun）并存
	if _, err := store.UpsertWorktreeEntry(ctx, WorktreeEntry{TaskID: taskID, WorktreeID: "agent/b", Branch: "agent/b", Path: "/trees/b", RootID: root.ID, NodeRunID: "nr-2", State: WorktreeStateActive}); err != nil {
		t.Fatalf("upsert b: %v", err)
	}
	active, err := store.ListActiveWorktreeEntries(ctx, taskID)
	if err != nil {
		t.Fatalf("ListActiveWorktreeEntries: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("active = %d, want 2", len(active))
	}

	// 按 NodeRun 归属查
	byNR, err := store.FindWorktreeEntryByNodeRun(ctx, taskID, "nr-2")
	if err != nil {
		t.Fatalf("FindWorktreeEntryByNodeRun: %v", err)
	}
	if byNR.WorktreeID != "agent/b" {
		t.Fatalf("by node run = %q, want agent/b", byNR.WorktreeID)
	}
	if _, err := store.FindWorktreeEntryByNodeRun(ctx, taskID, "nope"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing NodeRun err = %v, want ErrNoRows", err)
	}

	// state 守卫：merged 不能静默回到 active
	if _, err := store.UpdateWorktreeEntryState(ctx, taskID, "agent/a", WorktreeStateMerged, "abc123", nil, ""); err != nil {
		t.Fatalf("mark merged: %v", err)
	}
	if _, err := store.UpdateWorktreeEntryState(ctx, taskID, "agent/a", WorktreeStateActive, "", nil, ""); err == nil {
		t.Fatal("reopening a merged worktree via UpdateWorktreeEntryState must be rejected")
	}
	merged, err := store.GetWorktreeEntry(ctx, taskID, "agent/a")
	if err != nil {
		t.Fatalf("get merged: %v", err)
	}
	if merged.State != WorktreeStateMerged || merged.MergedCommit != "abc123" {
		t.Fatalf("merged = %+v, want state merged commit abc123", merged)
	}

	// 冲突字段落库 + 离开 needs_user 时清空
	if _, err := store.UpdateWorktreeEntryState(ctx, taskID, "agent/b", WorktreeStateNeedsUser, "", []string{"note.txt"}, "CONFLICT"); err != nil {
		t.Fatalf("mark needs_user: %v", err)
	}
	needy, _ := store.GetWorktreeEntry(ctx, taskID, "agent/b")
	if needy.State != WorktreeStateNeedsUser || len(needy.ConflictFiles) != 1 || needy.ConflictOutput != "CONFLICT" {
		t.Fatalf("needs_user = %+v, want files [note.txt] output CONFLICT", needy)
	}
	if _, err := store.UpdateWorktreeEntryState(ctx, taskID, "agent/b", WorktreeStateMerged, "def456", nil, ""); err != nil {
		t.Fatalf("needs_user -> merged (resolve): %v", err)
	}
	resolved, _ := store.GetWorktreeEntry(ctx, taskID, "agent/b")
	if len(resolved.ConflictFiles) != 0 || resolved.ConflictOutput != "" {
		t.Fatalf("conflict fields must clear on merged: %+v", resolved)
	}

	// 非法 state / 空 task
	if _, err := store.UpsertWorktreeEntry(ctx, WorktreeEntry{TaskID: taskID, WorktreeID: "x", State: "bogus"}); err == nil {
		t.Fatal("invalid state must be rejected")
	}
	if _, err := store.UpsertWorktreeEntry(ctx, WorktreeEntry{WorktreeID: "x"}); err == nil {
		t.Fatal("empty task id must be rejected")
	}
	// worktree_id 空 → branch 兜底
	eb, err := store.UpsertWorktreeEntry(ctx, WorktreeEntry{TaskID: taskID, Branch: "agent/c", Path: "/trees/c", State: WorktreeStateActive})
	if err != nil {
		t.Fatalf("branch fallback: %v", err)
	}
	if eb.WorktreeID != "agent/c" {
		t.Fatalf("worktree_id fallback = %q, want branch agent/c", eb.WorktreeID)
	}
}

func TestWorktreeRegistryReferenceCount(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	store, _ := svc.taskStore(root.ID)
	mk := func(name string) Task {
		d, err := svc.CreateTask(ctx, CreateTaskInput{RootID: root.ID, Stages: []StageTemplate{userStage("Describe")}, Input: name})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		return d.Task
	}
	a, b, c := mk("a"), mk("b"), mk("c")
	// registry：a/b 同路径 active，c 不同路径 + 一个 merged（不应计数）
	for _, w := range []struct {
		task, wid, path, state string
	}{
		{a.ID, "w1", "/trees/shared", WorktreeStateActive},
		{b.ID, "w2", "/trees/shared", WorktreeStateActive},
		{c.ID, "w3", "/trees/shared", WorktreeStateMerged},
		{c.ID, "w4", "/trees/other", WorktreeStateActive},
	} {
		if _, err := store.UpsertWorktreeEntry(ctx, WorktreeEntry{TaskID: w.task, WorktreeID: w.wid, Branch: w.wid, Path: w.path, State: w.state}); err != nil {
			t.Fatalf("upsert %s: %v", w.wid, err)
		}
	}
	cases := []struct {
		name, path, excl string
		want             int
	}{
		{"shared excl w1", "/trees/shared", "w1", 1},
		{"shared no excl (merged not counted)", "/trees/shared", "", 2},
		{"other", "/trees/other", "", 1},
		{"empty path", "", "", 0},
	}
	for _, tc := range cases {
		got, err := store.RegistryReferenceCount(ctx, tc.path, tc.excl)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Fatalf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
	// 混合口径：legacy tasks 列 + registry
	pinWorktree(t, store, a, root.ID, "/trees/legacy")
	if _, err := store.UpsertWorktreeEntry(ctx, WorktreeEntry{TaskID: b.ID, WorktreeID: "w9", Branch: "w9", Path: "/trees/legacy", State: WorktreeStateActive}); err != nil {
		t.Fatalf("upsert legacy shared: %v", err)
	}
	got, err := store.WorktreePathReferenceCount(ctx, "/trees/legacy", "excluded-task", "excluded-wt")
	if err != nil {
		t.Fatalf("WorktreePathReferenceCount: %v", err)
	}
	// legacy: a 钉着 /trees/legacy（exclude 是别的 task）=1；registry: w9（exclude 别的 wt）=1 → 2
	if got != 2 {
		t.Fatalf("combined = %d, want 2 (legacy 1 + registry 1)", got)
	}
	// 排除 a 自己：legacy 0 + registry 1 = 1
	got, _ = store.WorktreePathReferenceCount(ctx, "/trees/legacy", a.ID, "excluded-wt")
	if got != 1 {
		t.Fatalf("combined excl self = %d, want 1", got)
	}
}

func TestMergeConflictActivityInsert(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	store, _ := svc.taskStore(root.ID)
	d, err := svc.CreateTask(ctx, CreateTaskInput{RootID: root.ID, Stages: []StageTemplate{userStage("Describe")}, Input: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	act, err := store.InsertMergeConflictActivity(ctx, d.Task.ID, "agent/a", "agent/a", "note.txt")
	if err != nil {
		t.Fatalf("InsertMergeConflictActivity: %v", err)
	}
	if act.Type != ActivityTypeMergeConflict {
		t.Fatalf("type = %q, want merge_conflict", act.Type)
	}
	if !strings.Contains(act.Body, "agent/a") || !strings.Contains(act.Body, "note.txt") {
		t.Fatalf("body = %q, want worktree + files", act.Body)
	}
	acts, err := store.ListActivities(ctx, d.Task.ID)
	if err != nil {
		t.Fatalf("ListActivities: %v", err)
	}
	found := false
	for _, a := range acts {
		if a.Type == ActivityTypeMergeConflict && a.ID == act.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("merge_conflict activity not in timeline: %+v", acts)
	}
	// 不建 ContinueRequest：冲突不是新留言
	reqs, err := store.ListContinueRequests(ctx, d.Task.ID)
	if err != nil {
		t.Fatalf("ListContinueRequests: %v", err)
	}
	if len(reqs) != 0 {
		t.Fatalf("conflict must not create continue requests: %d", len(reqs))
	}
	// 不存在 task 拒绝
	if _, err := store.InsertMergeConflictActivity(ctx, "nope", "w", "w", ""); err == nil {
		t.Fatal("missing task must be rejected")
	}
}
