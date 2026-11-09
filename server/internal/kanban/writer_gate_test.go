package kanban

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// WP-04：writer gate 粒度 = (task_id, worktree_id)。
// 同 worktree 写串行；不同 worktree 可并行；只读不占 gate。

func TestWriterGateSameWorktreeWritesAreSerial(t *testing.T) {
	gate := NewWriterGate()
	release1, err := gate.Acquire(context.Background(), "task-1", "wt-a")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	acquired := make(chan struct{})
	go func() {
		release2, err := gate.Acquire(context.Background(), "task-1", "wt-a")
		if err != nil {
			t.Errorf("second acquire: %v", err)
			close(acquired)
			return
		}
		release2()
		close(acquired)
	}()
	select {
	case <-acquired:
		t.Fatal("second writer acquired the same worktree while the first still held it")
	case <-time.After(200 * time.Millisecond):
	}
	release1()
	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("second writer did not acquire after the first released")
	}
}

func TestWriterGateDifferentWorktreesRunInParallel(t *testing.T) {
	gate := NewWriterGate()
	release1, err := gate.Acquire(context.Background(), "task-1", "wt-a")
	if err != nil {
		t.Fatalf("acquire wt-a: %v", err)
	}
	defer release1()
	// 不同 worktree 立即拿到，不等 wt-a 释放。
	release2, err := gate.Acquire(context.Background(), "task-1", "wt-b")
	if err != nil {
		t.Fatalf("acquire wt-b: %v", err)
	}
	release2()
	// 不同 task 也互不挡。
	release3, err := gate.Acquire(context.Background(), "task-2", "wt-a")
	if err != nil {
		t.Fatalf("acquire other task: %v", err)
	}
	release3()
}

func TestWriterGateReadOnlyNeverBlocks(t *testing.T) {
	gate := NewWriterGate()
	release, err := gate.Acquire(context.Background(), "task-1", "wt-a")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer release()
	// 写者占着时，只读不被挡，也不占锁。
	done := make(chan func(), 1)
	go func() { done <- gate.ReadOnly("task-1", "wt-a") }()
	select {
	case releaseRead := <-done:
		releaseRead()
	case <-time.After(2 * time.Second):
		t.Fatal("read-only holder blocked on a held writer gate")
	}
	if !gate.IsHeld("task-1", "wt-a") {
		t.Fatal("writer should still be held after read-only release")
	}
}

func TestWriterGateWaiterContextCancel(t *testing.T) {
	gate := NewWriterGate()
	release, err := gate.Acquire(context.Background(), "task-1", "wt-a")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	waiterErr := make(chan error, 1)
	go func() {
		_, err := gate.Acquire(ctx, "task-1", "wt-a")
		waiterErr <- err
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-waiterErr:
		if err == nil {
			t.Fatal("cancelled waiter returned no error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiter did not return after context cancel")
	}
}

// TestWriterGateCancelDoesNotLeakLockWedges 钉住 Acquire 的取消泄漏回归：
// 返回 ctx.Err() 而不持有锁、释放后 TryAcquire 必须成功（sync.Mutex 旧实现里
// 被取消的等待者后台 goroutine 抢到锁后无人解锁，TryAcquire 永远 false）。
func TestWriterGateCancelDoesNotLeakLockWedges(t *testing.T) {
	gate := NewWriterGate()
	release, err := gate.Acquire(context.Background(), "task-1", "wt-a")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_, _ = gate.Acquire(ctx, "task-1", "wt-a")
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	time.Sleep(200 * time.Millisecond) // 给被取消的等待者留足泄漏窗口
	release()
	// 若实现泄漏，此处永远 false；修复后立即拿到。
	deadline := time.After(2 * time.Second)
	for {
		if ok, rel := gate.TryAcquire("task-1", "wt-a"); ok {
			rel()
			return
		}
		select {
		case <-deadline:
			t.Fatal("TryAcquire wedged after ctx cancel: cancelled waiter leaked the lock")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// TestWriterGateReleaseIsIdempotent 重复释放不破坏 gate（sync.Once）。
func TestWriterGateReleaseIsIdempotent(t *testing.T) {
	gate := NewWriterGate()
	release, err := gate.Acquire(context.Background(), "task-1", "wt-a")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	release()
	release() // 第二次释放不 panic 且不把已借给别人的 token 抽走
	if ok, rel := gate.TryAcquire("task-1", "wt-a"); !ok {
		t.Fatal("TryAcquire failed after double release")
	} else {
		rel()
	}
	if gate.IsHeld("task-1", "wt-a") {
		t.Fatal("key still held after release")
	}
}

// TestWriterGateCausedByRaceStress 在 -race 下跑：同键 N 个写者竞争时
// 「临界区内」人数恒为 1；不同 worktree 可并发；TryAcquire/IsHeld 与 Acquire 交织无 data race。
func TestWriterGateCausedByRaceStress(t *testing.T) {
	gate := NewWriterGate()
	const writers = 32
	const iterations = 50
	var inCritical atomic.Int64
	var maxConcurrent atomic.Int64
	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		worktree := "wt-a"
		if i%2 == 1 {
			worktree = "wt-b"
		}
		go func(wt string) {
			defer wg.Done()
			for range iterations {
				release, err := gate.Acquire(context.Background(), "task-race", wt)
				if err != nil {
					t.Errorf("acquire %s: %v", wt, err)
					return
				}
				now := inCritical.Add(1)
				for {
					old := maxConcurrent.Load()
					if now <= old || maxConcurrent.CompareAndSwap(old, now) {
						break
					}
				}
				// 高频交叉探测：与 Acquire/Release 交织跑 race 检测。
				_ = gate.IsHeld("task-race", "wt-a")
				_, _ = gate.TryAcquire("task-race", "wt-c")
				inCritical.Add(-1)
				release()
			}
		}(worktree)
	}
	wg.Wait()
	// 同 worktree 串行，所以 wt-a 与 wt-b 各自任意时刻至多一个持有者，
	// maxConcurrent 应等于 2（允许两个 worktree 各持一个）。
	if got := maxConcurrent.Load(); got > 2 {
		t.Fatalf("max concurrent holders = %d, want <= 2 (one per worktree)", got)
	}
	if got := inCritical.Load(); got != 0 {
		t.Fatalf("inCritical after all releases = %d, want 0", got)
	}
	if gate.IsHeld("task-race", "wt-a") || gate.IsHeld("task-race", "wt-b") {
		t.Fatal("gate still held after all writers released")
	}
}

func TestWriterGateTryAcquire(t *testing.T) {
	gate := NewWriterGate()
	release, err := gate.Acquire(context.Background(), "task-1", "wt-a")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if ok, _ := gate.TryAcquire("task-1", "wt-a"); ok {
		t.Fatal("TryAcquire succeeded on a held key")
	}
	if gate.IsHeld("task-1", "wt-b") {
		t.Fatal("wt-b reported held without any acquire")
	}
	release()
	if ok, rel := gate.TryAcquire("task-1", "wt-a"); !ok {
		t.Fatal("TryAcquire failed on a free key")
	} else {
		rel()
	}
}
