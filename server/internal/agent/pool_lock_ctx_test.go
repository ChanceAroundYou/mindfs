package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
)

// 2026-11-27 事故：一个卡住的 Claude 初始化持有运行时锁，后续所有同 protocol 的
// 打开都在裸 sync.Mutex 上无限排队，任务冻结 1~2 小时直到进程重启。
// 这里钉住「等锁必须服从调用方上下文」这一半，另一半是 claude 初始化超时。
func TestPoolRuntimeLockWaitHonorsContext(t *testing.T) {
	pool := NewPool(loadPoolTestConfig(t))
	defer pool.CloseAll()

	group := runtimeGroup{agentName: "gemini", protocol: ProtocolACP}
	unlock, err := pool.lockRuntimeGroupsContext(context.Background(), []runtimeGroup{group})
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	defer unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = pool.lockRuntimeGroupsContext(ctx, []runtimeGroup{group})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second lock err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("lock wait took %s, context deadline was ignored", elapsed)
	}
}

// 持锁方卡住时，GetOrCreate 必须随调用方上下文放弃，而不是永远排队。
func TestPoolGetOrCreateLockWaitHonorsContext(t *testing.T) {
	pool := NewPool(loadPoolTestConfig(t))
	defer pool.CloseAll()

	group := runtimeGroup{agentName: "gemini", protocol: ProtocolACP}
	unlock, err := pool.lockRuntimeGroupsContext(context.Background(), []runtimeGroup{group})
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	defer unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = pool.GetOrCreate(ctx, agenttypes.OpenSessionInput{
		SessionKey: "blocked-open",
		AgentName:  "gemini",
		RootPath:   t.TempDir(),
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("GetOrCreate err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("GetOrCreate waited %s, context deadline was ignored", elapsed)
	}
}

// 部分获取失败时必须释放已经拿到的锁，否则后续会永久死锁。
func TestPoolRuntimeLockReleasesAcquiredOnContextCancel(t *testing.T) {
	pool := NewPool(loadPoolTestConfig(t))
	defer pool.CloseAll()

	gemini := runtimeGroup{agentName: "gemini", protocol: ProtocolACP}
	omp := runtimeGroup{agentName: "omp", protocol: ProtocolACP}
	unlockOmp, err := pool.lockRuntimeGroupsContext(context.Background(), []runtimeGroup{omp})
	if err != nil {
		t.Fatalf("hold omp: %v", err)
	}
	defer unlockOmp()

	// 排序后先拿 gemini、再卡在 omp；超时必须把已拿到的 gemini 还回去。
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err := pool.lockRuntimeGroupsContext(ctx, []runtimeGroup{gemini, omp}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("multi lock err = %v, want context.DeadlineExceeded", err)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	unlockGemini, err := pool.lockRuntimeGroupsContext(ctx2, []runtimeGroup{gemini})
	if err != nil {
		t.Fatalf("gemini leaked after failed multi lock: %v", err)
	}
	unlockGemini()
}
