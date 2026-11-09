package kanban

import (
	"context"
	"sync"
)

// WP-04：writer gate，粒度 = (task_id, worktree_id)（spec「多 worktree 与自动合并」）。
// 同一 worktree 内的写节点串行；不同 worktree 可并行；只读不占 gate。
//
// 边界：本 gate 是**进程内**互斥，防的是同进程内多 NodeRun 在同一 worktree 并发写。
// 跨进程的写互斥不归它（那是 task-store 的行锁与 worktree 引用计数的事）。
// durable 的跨进程语义留待 WP-05 workflow engine 接入时补，本结构体不伪装成 durable。
//
// 实现：每键一把容量 1 的 token channel。Acquire 用 select 同时等的 ctx 与投 token，
// 无后台 goroutine —— ctx 取消时不存在「已抢到但无人释放」的 goroutine 泄漏
// （sync.Mutex 版曾有：等待者被 ctx 取消后，后台 goroutine 仍会抢到锁且无人解锁）。
type WriterGate struct {
	mu   sync.Mutex
	sems map[writerGateKey]chan struct{}
}

type writerGateKey struct {
	taskID     string
	worktreeID string
}

func NewWriterGate() *WriterGate {
	return &WriterGate{
		sems: map[writerGateKey]chan struct{}{},
	}
}

func writerKey(taskID, worktreeID string) writerGateKey {
	return writerGateKey{taskID: taskID, worktreeID: worktreeID}
}

// semFor 返回该键的 token channel（容量 1），不存在则新建。
func (g *WriterGate) semFor(key writerGateKey) chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	ch, ok := g.sems[key]
	if !ok {
		ch = make(chan struct{}, 1)
		g.sems[key] = ch
	}
	return ch
}

// Acquire 在 (task, worktree) 键上阻塞等待写入权；worktreeID 为空表示任务默认单
// worktree（沿用既有语义：同 task 内写串行），即与 legacy 路径同粒度。
// ctx 取消时返回 ctx.Err()，且不遗留任何持有该锁的 goroutine。
func (g *WriterGate) Acquire(ctx context.Context, taskID, worktreeID string) (func(), error) {
	ch := g.semFor(writerKey(taskID, worktreeID))
	select {
	case ch <- struct{}{}:
		releaseOnce := sync.Once{}
		return func() { releaseOnce.Do(func() { <-ch }) }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// TryAcquire 不阻塞：已持有返回 false；成功返回 release（重复释放安全）。
func (g *WriterGate) TryAcquire(taskID, worktreeID string) (bool, func()) {
	ch := g.semFor(writerKey(taskID, worktreeID))
	select {
	case ch <- struct{}{}:
		releaseOnce := sync.Once{}
		return true, func() { releaseOnce.Do(func() { <-ch }) }
	default:
		return false, nil
	}
}

// IsHeld 报告该键当前是否被写者持有（只读探测，不改状态）。
func (g *WriterGate) IsHeld(taskID, worktreeID string) bool {
	ch := g.semFor(writerKey(taskID, worktreeID))
	return len(ch) > 0
}

// ReadOnly 返回一个 no-op 释放函数：只读不占 gate。用它标注「我只读」比静默不拿锁清晰。
func (g *WriterGate) ReadOnly(taskID, worktreeID string) func() {
	return func() {}
}
