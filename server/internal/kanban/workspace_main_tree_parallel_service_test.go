package kanban

import (
	"context"
	"errors"
	"testing"
	"time"
)

// 用户决策（2026-11-17）：没有设置独立 worktree 的任务默认允许多个并行写主树，
// 由总开关 `workflow.parallel_main_tree` 收回串行行为。同一任务内仍串行；
// 合并/收尾这类独占写者照旧与所有共享写者互斥。

func addSharedMainTreeTask(t *testing.T, store *TaskStore, rootID, taskID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.CreateTask(ctx, Task{ID: taskID, RootID: rootID}, TaskEvent{ID: taskID + "-event", TaskID: taskID, Type: "created"}); err != nil {
		t.Fatalf("CreateTask %s: %v", taskID, err)
	}
}

func prepareSharedMainTreeLease(t *testing.T, svc *Service, store *TaskStore, run *WorkflowRun, node WorkflowNode, nodeRunID string) (*WorkflowNodeWorkspaceLease, error) {
	t.Helper()
	ctx := context.Background()
	claimed, won, err := store.ClaimWorkflowNodeRun(ctx, NodeRun{
		ID: nodeRunID, RunID: run.ID, NodeID: node.ID, Attempt: 1, Status: NodeRunQueued,
	})
	if err != nil || !won {
		t.Fatalf("ClaimWorkflowNodeRun %s won=%v err=%v", nodeRunID, won, err)
	}
	if err := store.BeginWorkflowNodeRun(ctx, claimed, time.Now().UTC()); err != nil {
		t.Fatalf("BeginWorkflowNodeRun: %v", err)
	}
	claimed.Status = NodeRunRunning
	return svc.PrepareWorkflowNodeWorktree(ctx, store, run, node, claimed)
}

func addSecondSharedRun(t *testing.T, store *TaskStore, taskID, runID string) (*WorkflowRun, WorkflowNode) {
	t.Helper()
	node := WorkflowNode{ID: "build", Kind: NodeKindAgent, Params: map[string]any{"prompt": "并行任务"}}
	run := &WorkflowRun{
		ID: runID, TaskID: taskID, Revision: 1, IntegrationBranch: "main",
		Graph: WorkflowDefinition{Nodes: []WorkflowNode{node}},
	}
	if err := store.CreateWorkflowRunAndSetEngine(context.Background(), run); err != nil {
		t.Fatalf("CreateWorkflowRunAndSetEngine: %v", err)
	}
	return run, node
}

func TestPrepareSharedMainTreeAllowsDifferentTasksInParallel(t *testing.T) {
	f := newWorkflowWorktreeFixture(t, "parallel-task-a", "parallel-run-a", false, "main")
	addSharedMainTreeTask(t, f.store, f.root.ID, "parallel-task-b")
	runB, nodeB := addSecondSharedRun(t, f.store, "parallel-task-b", "parallel-run-b")

	leaseA, err := prepareSharedMainTreeLease(t, f.svc, f.store, f.run, f.node, "nr-par-a")
	if err != nil {
		t.Fatalf("lease A: %v", err)
	}
	defer leaseA.Release()

	acquired := make(chan *WorkflowNodeWorkspaceLease, 1)
	errc := make(chan error, 1)
	go func() {
		leaseB, err := prepareSharedMainTreeLease(t, f.svc, f.store, runB, nodeB, "nr-par-b")
		if err != nil {
			errc <- err
			return
		}
		acquired <- leaseB
	}()
	select {
	case leaseB := <-acquired:
		leaseB.Release()
	case err := <-errc:
		t.Fatalf("second task prepare: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("second task waited on the main tree although parallel main tree is on by default")
	}
}

func TestPrepareSharedMainTreeSerializesSameTask(t *testing.T) {
	f := newWorkflowWorktreeFixture(t, "serial-task", "serial-run", false, "main")
	lease1, err := prepareSharedMainTreeLease(t, f.svc, f.store, f.run, f.node, "nr-serial-1")
	if err != nil {
		t.Fatalf("lease 1: %v", err)
	}
	defer lease1.Release()

	node2 := WorkflowNode{ID: "build-2", Kind: NodeKindAgent, Params: map[string]any{"prompt": "同任务第二个节点"}}
	acquired := make(chan *WorkflowNodeWorkspaceLease, 1)
	go func() {
		lease2, err := prepareSharedMainTreeLease(t, f.svc, f.store, f.run, node2, "nr-serial-2")
		if err == nil {
			acquired <- lease2
		}
	}()
	select {
	case lease2 := <-acquired:
		lease2.Release()
		t.Fatal("two nodes of the same task wrote the shared main tree concurrently")
	case <-time.After(150 * time.Millisecond):
	}
	lease1.Release()
	select {
	case lease2 := <-acquired:
		lease2.Release()
	case <-time.After(2 * time.Second):
		t.Fatal("second node of the same task did not acquire after the first released")
	}
}

func TestPrepareSharedMainTreeSwitchRestoresSerialAcrossTasks(t *testing.T) {
	f := newWorkflowWorktreeFixture(t, "serial-switch-a", "serial-switch-run-a", false, "main")
	f.svc.SetParallelMainTree(false)
	addSharedMainTreeTask(t, f.store, f.root.ID, "serial-switch-b")
	runB, nodeB := addSecondSharedRun(t, f.store, "serial-switch-b", "serial-switch-run-b")

	leaseA, err := prepareSharedMainTreeLease(t, f.svc, f.store, f.run, f.node, "nr-switch-a")
	if err != nil {
		t.Fatalf("lease A: %v", err)
	}
	defer leaseA.Release()

	acquired := make(chan *WorkflowNodeWorkspaceLease, 1)
	go func() {
		leaseB, err := prepareSharedMainTreeLease(t, f.svc, f.store, runB, nodeB, "nr-switch-b")
		if err == nil {
			acquired <- leaseB
		}
	}()
	select {
	case leaseB := <-acquired:
		leaseB.Release()
		t.Fatal("parallel main tree switch off, but the second task still acquired the tree")
	case <-time.After(150 * time.Millisecond):
	}
	leaseA.Release()
	select {
	case leaseB := <-acquired:
		leaseB.Release()
	case <-time.After(2 * time.Second):
		t.Fatal("second task did not acquire after the first released with the switch off")
	}
}

func TestExclusiveWriterWaitsForParallelMainTreeNodes(t *testing.T) {
	f := newWorkflowWorktreeFixture(t, "merge-wait-task", "merge-wait-run", false, "main")
	lease, err := prepareSharedMainTreeLease(t, f.svc, f.store, f.run, f.node, "nr-merge-wait")
	if err != nil {
		t.Fatalf("lease: %v", err)
	}

	// 合并/收尾走独占 gate：并行节点在跑时必须等它们全部退出。
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := f.svc.AcquireNodeWriter(ctx, lease.RootPath, "main"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("exclusive writer err = %v, want it to wait for the parallel node", err)
	}
	lease.Release()

	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	release, err := f.svc.AcquireNodeWriter(ctx2, lease.RootPath, "main")
	if err != nil {
		t.Fatalf("exclusive writer after release: %v", err)
	}
	release()
}
