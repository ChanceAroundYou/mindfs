package kanban

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
)

var (
	// ErrWorkflowWriterGateUnavailable：Service 没有注入 app 级共享 gate。
	// 写入节点必须 fail closed —— 没有共享 gate 就无从保证同一物理树的互斥，
	// 宁可拒绝执行，也不能退回「每个账户各拿一把锁」的伪互斥。
	ErrWorkflowWriterGateUnavailable = errors.New("workflow writer gate is not configured")
	// ErrWorkflowWorkspaceIdentityMissing：写节点既没有物理路径也没有分支身份，
	// 无法判断该和谁互斥，同样 fail closed。
	ErrWorkflowWorkspaceIdentityMissing = errors.New("workflow node workspace identity is missing")
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
	// workspaceSems 是 D15 的 app 级共享 gate：键 = 规范化物理路径 + 分支，
	// 不按 Task/账户分片。Kanban Service 每账户一个，所以只有把同一个 gate
	// 实例注入所有 Service，跨账户命中同一物理树时才会串行。
	workspaceSems map[workspaceGateKey]chan struct{}
}

type writerGateKey struct {
	taskID     string
	worktreeID string
}

// workspaceGateKey 是物理工作树身份。path 是目录的规范路径；branch 用于
// 路径缺失时的兜底身份，也避免两棵登记信息不一致但实际同路径的树拿到两把锁。
type workspaceGateKey struct {
	path   string
	branch string
}

func NewWriterGate() *WriterGate {
	return &WriterGate{
		sems:          map[writerGateKey]chan struct{}{},
		workspaceSems: map[workspaceGateKey]chan struct{}{},
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

// normalizeWorkspaceKey 统一物理树身份。
//
// 路径非空时只按路径做键：同一个 checkout 同一时刻只有一个分支，分支名不同
// （例如任务记录的旧分支 vs 主 checkout 当前分支）不能让同一物理目录拿到两把锁。
// 路径缺失时才退回分支；两者都为空由调用方 fail closed。路径同时做 Abs +
// EvalSymlinks，避免 `/repo` 与 `repo`、软链接别名拿到不同键。
func normalizeWorkspaceKey(path, branch string) workspaceGateKey {
	path = strings.TrimSpace(path)
	branch = strings.TrimSpace(branch)
	if path == "" {
		return workspaceGateKey{branch: branch}
	}
	path = filepath.Clean(path)
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return workspaceGateKey{path: path}
}

func (g *WriterGate) workspaceSemFor(key workspaceGateKey) chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.workspaceSems == nil {
		g.workspaceSems = map[workspaceGateKey]chan struct{}{}
	}
	ch, ok := g.workspaceSems[key]
	if !ok {
		ch = make(chan struct{}, 1)
		g.workspaceSems[key] = ch
	}
	return ch
}

// AcquireWorkspace 在物理工作树身份上阻塞等待写入权。key 不含 task/账户：
// 同一个目录无论被哪个 Task 或哪个账户的 Service 命中，都只有一支写者。
func (g *WriterGate) AcquireWorkspace(ctx context.Context, path, branch string) (func(), error) {
	ch := g.workspaceSemFor(normalizeWorkspaceKey(path, branch))
	select {
	case ch <- struct{}{}:
		releaseOnce := sync.Once{}
		return func() { releaseOnce.Do(func() { <-ch }) }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// TryAcquireWorkspace 不阻塞：已持有返回 false；成功返回 release（重复释放安全）。
func (g *WriterGate) TryAcquireWorkspace(path, branch string) (bool, func()) {
	ch := g.workspaceSemFor(normalizeWorkspaceKey(path, branch))
	select {
	case ch <- struct{}{}:
		releaseOnce := sync.Once{}
		return true, func() { releaseOnce.Do(func() { <-ch }) }
	default:
		return false, nil
	}
}

// IsWorkspaceHeld 报告该物理工作树当前是否被写者持有。
func (g *WriterGate) IsWorkspaceHeld(path, branch string) bool {
	ch := g.workspaceSemFor(normalizeWorkspaceKey(path, branch))
	return len(ch) > 0
}
