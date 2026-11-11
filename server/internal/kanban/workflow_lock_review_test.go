package kanban

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 第三轮评审补充：writer gate 等待被取消时的 nil release 与收尾锁条目回收。
// 两者都是生产路径，不依赖测试 fixture 之外的内部状态。

func TestWorkflowFinishLockTracksWaitersAndPrunes(t *testing.T) {
	svc := &Service{}
	first := svc.lockWorkflowNodeFinish("nr-prune")
	acquired := make(chan func(), 1)
	go func() { acquired <- svc.lockWorkflowNodeFinish("nr-prune") }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		svc.mu.Lock()
		refs := 0
		if lock := svc.workflowFinishLocks["nr-prune"]; lock != nil {
			refs = lock.refs
		}
		svc.mu.Unlock()
		if refs == 2 {
			break // 持有者 + 等待者都登记在 refs 里，此时绝不能删条目
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiter was not registered: refs = %d", refs)
		}
		time.Sleep(time.Millisecond)
	}
	first()
	var second func()
	select {
	case second = <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("waiter did not acquire after the first unlock")
	}
	second()
	svc.mu.Lock()
	remaining := len(svc.workflowFinishLocks)
	svc.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("finish lock table kept %d entries after all users left", remaining)
	}
}

func TestAcquireWorkflowMergeGateReturnsUsableReleaseOnCanceledWait(t *testing.T) {
	ctx := context.Background()
	svc, _, mainDir, _ := multiMergeFixture(t, "a\n", "")
	release, err := svc.AcquireNodeWriter(ctx, mainDir, "main")
	if err != nil {
		t.Fatalf("hold main checkout gate: %v", err)
	}
	defer release()
	waitCtx, cancel := context.WithCancel(ctx)
	cancel()
	releaseGate, gateErr := svc.acquireWorkflowMergeGate(waitCtx, autoMergePlan{mainDir: mainDir, target: "main"}, true)
	if !errors.Is(gateErr, context.Canceled) {
		t.Fatalf("gate err = %v, want context.Canceled", gateErr)
	}
	if releaseGate == nil {
		t.Fatal("acquireWorkflowMergeGate returned a nil release on error")
	}
	releaseGate() // 错误分支会无条件调用；nil 会 panic 整个进程
}

// ── CRITICAL：gate 等待被取消时不得调用 nil release ─────────────────────────
//
// AcquireWorkspace 的每条错误路径都返回 nil release。收尾把返回值直接赋给
// no-op release 后，在错误分支调用它会让整个进程 panic；操作员停止恰好会在
// 这里取消 driver ctx。取消赢后也不能把已完成的节点改写成 fail。
func TestWorkflowFinalizeCancelDuringMergeGateWaitDoesNotPanic(t *testing.T) {
	base := context.Background()
	mainDir := newGitRoot(t)
	f := newGitWorkflowFixture(t, mainDir, "gate-cancel-task", "gate-cancel-run", "main")
	nr := f.beginNodeRun(t, "nr-gate-cancel")
	if err := f.store.BindWorkflowNodeSession(base, nr, "sess-gate-cancel"); err != nil {
		t.Fatalf("BindWorkflowNodeSession: %v", err)
	}
	lease, err := f.svc.PrepareWorkflowNodeWorktree(base, f.store, f.run, f.node, nr)
	if err != nil {
		t.Fatalf("PrepareWorkflowNodeWorktree: %v", err)
	}
	commitInDir(t, lease.RootPath, "feature.txt", "x\n", "work")
	lease.Release()
	nr, err = f.store.WorkflowNodeRunByID(base, nr.ID)
	if err != nil {
		t.Fatalf("WorkflowNodeRunByID: %v", err)
	}

	releaseMain, err := f.svc.AcquireNodeWriter(base, mainDir, "main")
	if err != nil {
		t.Fatalf("hold main checkout gate: %v", err)
	}
	ctx, cancel := context.WithCancel(base)
	defer cancel()
	type outcome struct {
		got        NodeRun
		err        error
		panicValue any
	}
	done := make(chan outcome, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				done <- outcome{panicValue: p}
			}
		}()
		got, ferr := f.svc.finalizeWorkflowNodeWithWorktree(ctx, f.store, f.run, f.node, nr,
			map[string]any{"summary": "done"}, "test")
		done <- outcome{got: got, err: ferr}
	}()
	// agent-done 落库证明收尾已越过收尾锁，正等在 gate 上。
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, recorded, rerr := f.svc.workflowNodeAgentDoneOutput(base, f.store, nr)
		if rerr != nil {
			t.Fatalf("read agent-done evidence: %v", rerr)
		}
		if recorded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("finalize did not record agent-done evidence")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// 真实取消顺序：先把仍 open 的行标 cancelled，再取消 driver ctx。
	if cerr := f.svc.cancelOpenWorkflowNodeRuns(base, f.root.ID, f.store, f.run.ID, "operator"); cerr != nil {
		t.Fatalf("cancelOpenWorkflowNodeRuns: %v", cerr)
	}
	persisted, perr := f.store.WorkflowNodeRunByID(base, nr.ID)
	if perr != nil || persisted.Status != NodeRunCancelled {
		t.Fatalf("cancel persistence: node=%#v err=%v, want cancelled", persisted, perr)
	}
	cancel()
	releaseMain()

	select {
	case got := <-done:
		if got.panicValue != nil {
			t.Fatalf("finalize panicked while the gate wait was cancelled: %v", got.panicValue)
		}
		if got.got.Status != NodeRunCancelled {
			current, readErr := f.store.WorkflowNodeRunByID(base, nr.ID)
			t.Fatalf("finalize status = %q err=%v; persisted status=%q readErr=%v, want cancelled", got.got.Status, got.err, current.Status, readErr)
		}
		if got.err != nil && !errors.Is(got.err, context.Canceled) {
			t.Fatalf("finalize err = %v, want nil or context.Canceled", got.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("finalize did not return after cancellation")
	}
	if n := countMergeCommits(t, mainDir, "main"); n != 0 {
		t.Fatalf("merge commits = %d, want 0 (cancel must win)", n)
	}
	if _, statErr := os.Stat(filepath.Join(mainDir, "feature.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("feature was merged after cancel, stat err = %v", statErr)
	}
}
