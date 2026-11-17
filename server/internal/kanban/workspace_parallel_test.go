package kanban

import (
	"context"
	"errors"
	"testing"
	"time"
)

// 共享主树并行：同一个物理 checkout 允许**多个任务的写节点**同时持锁，
// 但合并/收尾等独占写者仍然必须与所有共享写者互斥。
//
// 边界：共享锁只放宽「不同任务之间」，同一任务内仍由 (task, worktree) gate 串行；
// 独占锁（AcquireWorkspace）语义不变。

func TestWorkspaceGateSharedWritersRunConcurrently(t *testing.T) {
	gate := NewWriterGate()
	releaseA, err := gate.AcquireWorkspaceShared(context.Background(), "/trees/main", "main")
	if err != nil {
		t.Fatalf("shared A: %v", err)
	}
	defer releaseA()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	releaseB, err := gate.AcquireWorkspaceShared(ctx, "/trees/main", "main")
	if err != nil {
		t.Fatalf("second shared writer should not wait for the first: %v", err)
	}
	releaseB()
}

func TestWorkspaceGateExclusiveWaitsForSharedWriter(t *testing.T) {
	gate := NewWriterGate()
	releaseShared, err := gate.AcquireWorkspaceShared(context.Background(), "/trees/main", "main")
	if err != nil {
		t.Fatalf("shared: %v", err)
	}

	acquired := make(chan error, 1)
	go func() {
		release, err := gate.AcquireWorkspace(context.Background(), "/trees/main", "main")
		if err == nil {
			release()
		}
		acquired <- err
	}()
	select {
	case err := <-acquired:
		t.Fatalf("exclusive writer acquired while shared writer held the tree (err=%v)", err)
	case <-time.After(150 * time.Millisecond):
	}
	releaseShared()
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatalf("exclusive acquire after shared release: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("exclusive writer did not acquire after shared release")
	}
}

func TestWorkspaceGateSharedWriterWaitsForExclusive(t *testing.T) {
	gate := NewWriterGate()
	releaseExclusive, err := gate.AcquireWorkspace(context.Background(), "/trees/main", "main")
	if err != nil {
		t.Fatalf("exclusive: %v", err)
	}

	acquired := make(chan error, 1)
	go func() {
		release, err := gate.AcquireWorkspaceShared(context.Background(), "/trees/main", "main")
		if err == nil {
			release()
		}
		acquired <- err
	}()
	select {
	case err := <-acquired:
		t.Fatalf("shared writer acquired while exclusive writer held the tree (err=%v)", err)
	case <-time.After(150 * time.Millisecond):
	}
	releaseExclusive()
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatalf("shared acquire after exclusive release: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shared writer did not acquire after exclusive release")
	}
}

func TestWorkspaceGateCanceledWaiterDoesNotLeak(t *testing.T) {
	gate := NewWriterGate()
	releaseExclusive, err := gate.AcquireWorkspace(context.Background(), "/trees/main", "main")
	if err != nil {
		t.Fatalf("exclusive: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := gate.AcquireWorkspaceShared(ctx, "/trees/main", "main")
		done <- err
	}()
	// 等它登记成等待者，再取消。
	deadline := time.Now().Add(2 * time.Second)
	for {
		key := normalizeWorkspaceKey("/trees/main", "main")
		gate.mu.Lock()
		lock := gate.workspaceLocks[key]
		gate.mu.Unlock()
		lock.mu.Lock()
		waiting := len(lock.waiters)
		lock.mu.Unlock()
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("shared waiter was not registered")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled waiter err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled waiter did not return")
	}
	releaseExclusive()

	// 取消的等待者不能把锁留成「永久有人等/有人持」：独占锁必须马上可拿。
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	release, err := gate.AcquireWorkspace(ctx2, "/trees/main", "main")
	if err != nil {
		t.Fatalf("exclusive acquire after canceled waiter: %v", err)
	}
	release()
}

func TestWorkspaceGateExclusiveWaiterPreemptsNewShared(t *testing.T) {
	// FIFO：独占者在等时，后到的共享写者不能插队，否则长回合会饿死合并。
	gate := NewWriterGate()
	releaseShared, err := gate.AcquireWorkspaceShared(context.Background(), "/trees/main", "main")
	if err != nil {
		t.Fatalf("shared: %v", err)
	}
	exclusiveDone := make(chan func(), 1)
	go func() {
		release, err := gate.AcquireWorkspace(context.Background(), "/trees/main", "main")
		if err == nil {
			exclusiveDone <- release
		}
	}()
	time.Sleep(50 * time.Millisecond)

	early := make(chan func(), 1)
	go func() {
		release, err := gate.AcquireWorkspaceShared(context.Background(), "/trees/main", "main")
		if err == nil {
			early <- release
		}
	}()
	select {
	case release := <-early:
		release()
		t.Fatal("new shared writer jumped ahead of a waiting exclusive writer")
	case <-time.After(100 * time.Millisecond):
	}
	releaseShared()
	select {
	case release := <-exclusiveDone:
		release()
	case <-time.After(2 * time.Second):
		t.Fatal("exclusive writer did not acquire")
	}
	select {
	case release := <-early:
		release()
	case <-time.After(2 * time.Second):
		t.Fatal("shared writer did not acquire after exclusive released")
	}
}
