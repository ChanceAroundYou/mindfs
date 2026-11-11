package kanban

import (
	"context"
	"errors"
	"testing"
	"time"
)

// D10/D15：工作流节点的 workspace intent 必须能表达「写/只读」与「共享主树/独立树」，
// 且写节点共用一个 app 级物理 gate。只读节点不占 gate；不同物理树可并发；
// 同一物理树即使来自不同账户/Service 也必须串行。

func TestWorkflowNodeWorkspaceIntent(t *testing.T) {
	cases := []struct {
		name        string
		params      map[string]any
		writes      bool
		independent bool
	}{
		{name: "default is writing on shared tree", params: nil, writes: true},
		{name: "explicit read only", params: map[string]any{"writes_workspace": false}, writes: false},
		{name: "independent opt in", params: map[string]any{"worktree": "independent"}, writes: true, independent: true},
		{name: "read only independent", params: map[string]any{"writes_workspace": false, "worktree": "independent"}, writes: false, independent: true},
		// 类型不对时 fail closed：默认为写，不能因为解析不了就把写节点当只读放行。
		{name: "invalid writes type stays writing", params: map[string]any{"writes_workspace": "false"}, writes: true},
		{name: "unknown worktree mode stays shared", params: map[string]any{"worktree": "per-node"}, writes: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := WorkflowNodeWorkspaceOf(WorkflowNode{ID: "n", Kind: NodeKindAgent, Params: tc.params})
			if got.WritesWorkspace != tc.writes {
				t.Fatalf("WritesWorkspace = %v, want %v", got.WritesWorkspace, tc.writes)
			}
			if got.Independent != tc.independent {
				t.Fatalf("Independent = %v, want %v", got.Independent, tc.independent)
			}
		})
	}
}

func TestWorkflowReadOnlyNodeDoesNotAcquireWriterGate(t *testing.T) {
	gate := NewWriterGate()
	svc := NewService(NewTemplateStoreAt(t.TempDir()), testRoots{})
	svc.SetWriterGate(gate)

	release, err := svc.AcquireNodeWriter(context.Background(), "/trees/shared", "task/1")
	if err != nil {
		t.Fatalf("writer acquire: %v", err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	readOnly := WorkflowNode{ID: "inspect", Kind: NodeKindAgent, Params: map[string]any{"writes_workspace": false}}
	releaseRead, err := svc.AcquireWorkflowNodeWriter(ctx, readOnly, "/trees/shared", "task/1")
	if err != nil {
		t.Fatalf("read-only acquire should not wait for a held writer gate: %v", err)
	}
	releaseRead()

	writer := WorkflowNode{ID: "build", Kind: NodeKindAgent, Params: map[string]any{"writes_workspace": true}}
	if _, err := svc.AcquireWorkflowNodeWriter(ctx, writer, "/trees/shared", "task/1"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("writing node acquire err = %v, want context.DeadlineExceeded while gate is held", err)
	}
}

func TestWorkflowSharedTreeWritersSerialAcrossServices(t *testing.T) {
	gate := NewWriterGate()
	// 两个 Service 模拟两个账户：它们不共享 TaskStore，但必须共享同一个 app 级 gate。
	svcA := NewService(NewTemplateStoreAt(t.TempDir()), testRoots{})
	svcB := NewService(NewTemplateStoreAt(t.TempDir()), testRoots{})
	svcA.SetWriterGate(gate)
	svcB.SetWriterGate(gate)

	releaseA, err := svcA.AcquireNodeWriter(context.Background(), "/trees/shared", "feature/x")
	if err != nil {
		t.Fatalf("A acquire: %v", err)
	}

	acquired := make(chan error, 1)
	go func() {
		releaseB, err := svcB.AcquireNodeWriter(context.Background(), "/trees/shared", "feature/x")
		if err == nil {
			releaseB()
		}
		acquired <- err
	}()
	select {
	case err := <-acquired:
		t.Fatalf("second account acquired the same physical worktree while first held it (err=%v)", err)
	case <-time.After(150 * time.Millisecond):
	}
	releaseA()
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatalf("second acquire after release: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second account did not acquire after first released")
	}
}

func TestWorkflowIndependentTreesRunConcurrently(t *testing.T) {
	gate := NewWriterGate()
	svc := NewService(NewTemplateStoreAt(t.TempDir()), testRoots{})
	svc.SetWriterGate(gate)

	releaseA, err := svc.AcquireNodeWriter(context.Background(), "/trees/independent-a", "task-a/1")
	if err != nil {
		t.Fatalf("acquire tree a: %v", err)
	}
	defer releaseA()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	releaseB, err := svc.AcquireNodeWriter(ctx, "/trees/independent-b", "task-b/1")
	if err != nil {
		t.Fatalf("independent worktree should not wait for another tree: %v", err)
	}
	releaseB()
}

func TestWorkflowNodeWriterFailsClosedWithoutSharedGate(t *testing.T) {
	svc := NewService(NewTemplateStoreAt(t.TempDir()), testRoots{})
	if _, err := svc.AcquireNodeWriter(context.Background(), "/trees/shared", "feature/x"); !errors.Is(err, ErrWorkflowWriterGateUnavailable) {
		t.Fatalf("missing gate err = %v, want ErrWorkflowWriterGateUnavailable", err)
	}
}

func TestClaimWorktreeEntryRejectsAnotherNodeRunOwner(t *testing.T) {
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

	first, err := store.ClaimWorktreeEntry(ctx, WorktreeEntry{
		TaskID: taskID, WorktreeID: "agent/a", Branch: "agent/a", Path: "/trees/a",
		RootID: root.ID, NodeRunID: "nr-a", State: WorktreeStateActive,
	})
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if first.NodeRunID != "nr-a" {
		t.Fatalf("first owner = %q, want nr-a", first.NodeRunID)
	}

	if _, err := store.ClaimWorktreeEntry(ctx, WorktreeEntry{
		TaskID: taskID, WorktreeID: "agent/a", Branch: "agent/a", Path: "/trees/a",
		RootID: root.ID, NodeRunID: "nr-b", State: WorktreeStateActive,
	}); !errors.Is(err, ErrWorktreeOwnerConflict) {
		t.Fatalf("second owner err = %v, want ErrWorktreeOwnerConflict", err)
	}
	got, err := store.GetWorktreeEntry(ctx, taskID, "agent/a")
	if err != nil {
		t.Fatalf("GetWorktreeEntry: %v", err)
	}
	if got.NodeRunID != "nr-a" || got.Path != "/trees/a" {
		t.Fatalf("conflicting claim overwrote existing owner: %+v", got)
	}
}

func TestClaimWorktreeEntryRejectsSameBranchOtherNodeRun(t *testing.T) {
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

	if _, err := store.ClaimWorktreeEntry(ctx, WorktreeEntry{
		TaskID: taskID, WorktreeID: "agent/a", Branch: "feature/shared", Path: "/trees/a",
		RootID: root.ID, NodeRunID: "nr-a", State: WorktreeStateActive,
	}); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if _, err := store.ClaimWorktreeEntry(ctx, WorktreeEntry{
		TaskID: taskID, WorktreeID: "agent/b", Branch: "feature/shared", Path: "/trees/b",
		RootID: root.ID, NodeRunID: "nr-b", State: WorktreeStateActive,
	}); !errors.Is(err, ErrWorktreeOwnerConflict) {
		t.Fatalf("same branch other owner err = %v, want ErrWorktreeOwnerConflict", err)
	}
	if _, err := store.GetWorktreeEntry(ctx, taskID, "agent/b"); err == nil {
		t.Fatal("conflicting claim inserted a second active row for the same branch")
	}
}

func TestClaimWorktreeEntryIsIdempotentForSameNodeRun(t *testing.T) {
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

	in := WorktreeEntry{
		TaskID: taskID, WorktreeID: "agent/a", Branch: "agent/a", Path: "/trees/a",
		RootID: root.ID, NodeRunID: "nr-a", State: WorktreeStateActive,
	}
	if _, err := store.ClaimWorktreeEntry(ctx, in); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	in.Path = "/trees/a"
	got, err := store.ClaimWorktreeEntry(ctx, in)
	if err != nil {
		t.Fatalf("idempotent claim: %v", err)
	}
	if got.NodeRunID != "nr-a" || got.Path != "/trees/a" {
		t.Fatalf("idempotent claim = %+v", got)
	}
}
