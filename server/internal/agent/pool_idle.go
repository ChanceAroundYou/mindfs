package agent

import (
	"context"
	"log"
	"sort"
	"time"
)

// ReleaseIdleSessions closes inactive runtime sessions without changing the
// persisted MindFS session or its closed_at metadata.
type IdleReleaseResult struct {
	ReleasedSessions int `json:"released_sessions"`
	FailedSessions   int `json:"failed_sessions"`
}

func (p *Pool) ReleaseIdleSessions(idleFor time.Duration, now time.Time) int {
	return p.ReleaseIdleSessionsDetailed(idleFor, now).ReleasedSessions
}

func (p *Pool) ReleaseIdleSessionsDetailed(idleFor time.Duration, now time.Time) IdleReleaseResult {
	if idleFor <= 0 {
		return IdleReleaseResult{}
	}
	cutoff := now.Add(-idleFor)
	return p.releaseSessionsDetailed(now, "idle_for="+idleFor.String(), func(entry *sessionEntry) bool {
		return !entry.lastUsedAt.After(cutoff)
	})
}

// ReleaseInactiveSessionsDetailed immediately releases every session that is
// not currently in use. It is intended for an explicit user action; automatic
// cleanup should continue to use ReleaseIdleSessionsDetailed with a threshold.
func (p *Pool) ReleaseInactiveSessionsDetailed(now time.Time) IdleReleaseResult {
	return p.releaseSessionsDetailed(now, "manual", func(_ *sessionEntry) bool { return true })
}

func (p *Pool) releaseSessionsDetailed(now time.Time, reason string, eligible func(*sessionEntry) bool) IdleReleaseResult {
	result := IdleReleaseResult{}
	p.mu.Lock()
	groups := make(map[runtimeGroup]struct{})
	for _, entry := range p.sessions {
		if entry == nil || entry.closing || entry.activeUses > 0 || !eligible(entry) {
			continue
		}
		groups[runtimeGroup{agentName: entry.agentName, protocol: entry.protocol}] = struct{}{}
	}
	p.mu.Unlock()
	if len(groups) == 0 {
		return result
	}
	groupList := make([]runtimeGroup, 0, len(groups))
	for group := range groups {
		groupList = append(groupList, group)
	}
	unlockRuntimes := p.lockRuntimeGroups(groupList)
	defer unlockRuntimes()

	p.mu.Lock()
	entries := make([]*sessionEntry, 0)
	for _, entry := range p.sessions {
		group := runtimeGroup{}
		if entry != nil {
			group = runtimeGroup{agentName: entry.agentName, protocol: entry.protocol}
		}
		if _, selected := groups[group]; !selected || entry == nil || entry.closing || entry.activeUses > 0 || !eligible(entry) {
			continue
		}
		entry.closing = true
		entry.closeDone = make(chan struct{})
		entries = append(entries, entry)
	}
	p.mu.Unlock()

	for _, entry := range entries {
		err := entry.session.Close()
		p.mu.Lock()
		current := p.sessions[entry.sessionKey]
		if current == entry {
			if err == nil {
				delete(p.sessions, entry.sessionKey)
				result.ReleasedSessions++
			} else {
				entry.closing = false
				entry.lastUsedAt = now
				result.FailedSessions++
			}
		}
		close(entry.closeDone)
		p.mu.Unlock()
		if err != nil {
			log.Printf("[agent/pool] idle_release.error session=%s agent=%s protocol=%s err=%v", entry.sessionKey, entry.agentName, entry.protocol, err)
		} else {
			log.Printf("[agent/pool] idle_release.done session=%s agent=%s protocol=%s reason=%s", entry.sessionKey, entry.agentName, entry.protocol, reason)
		}
	}
	return result
}

type runtimeGroup struct {
	agentName string
	protocol  Protocol
}

// runtimeLock 是可带上下文等待的互斥量。
//
// 之前这里是裸 sync.Mutex：一个卡住的 Claude 初始化会永远持有该 protocol 的锁，
// 之后所有同 protocol 的会话打开都会无超时地排队，只能等进程重启（此时又统一报
// agent pool closed），表现为「一批任务同时卡死几小时」。
type runtimeLock struct {
	ch chan struct{}
}

func newRuntimeLock() *runtimeLock {
	return &runtimeLock{ch: make(chan struct{}, 1)}
}

func (l *runtimeLock) lock(ctx context.Context) error {
	select {
	case l.ch <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *runtimeLock) unlock() {
	select {
	case <-l.ch:
	default:
	}
}

// runtimeGroupLockWaitTimeout 是调用方没有 deadline 时，等运行时锁的上限。
// 它只限制「排队等打开」这一段；正常打开远快于此，超时说明持锁方的初始化确实卡住了。
const runtimeGroupLockWaitTimeout = 5 * time.Minute

// lockRuntimeGroups 无条件等待（供关闭/清理等管理路径使用）。
func (p *Pool) lockRuntimeGroups(groups []runtimeGroup) func() {
	unlock, _ := p.lockRuntimeGroupsWithCap(context.Background(), groups, 0)
	return unlock
}

// lockRuntimeGroupsContext 按需等待运行时锁；ctx 先到期时返回错误并释放已拿到的锁。
func (p *Pool) lockRuntimeGroupsContext(ctx context.Context, groups []runtimeGroup) (func(), error) {
	return p.lockRuntimeGroupsWithCap(ctx, groups, runtimeGroupLockWaitTimeout)
}

// lockRuntimeGroupsWithCap 在 waitCap <= 0 时无条件等待；否则给无 deadline 的 ctx 套上限。
func (p *Pool) lockRuntimeGroupsWithCap(ctx context.Context, groups []runtimeGroup, waitCap time.Duration) (func(), error) {
	if len(groups) == 0 {
		return func() {}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	unique := make(map[runtimeGroup]struct{}, len(groups))
	ordered := make([]runtimeGroup, 0, len(groups))
	for _, group := range groups {
		if _, ok := unique[group]; ok {
			continue
		}
		unique[group] = struct{}{}
		ordered = append(ordered, group)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].protocol != ordered[j].protocol {
			return ordered[i].protocol < ordered[j].protocol
		}
		return ordered[i].agentName < ordered[j].agentName
	})
	locks := make([]*runtimeLock, 0, len(ordered))
	p.runtimeLocksMu.Lock()
	for _, group := range ordered {
		lock := p.runtimeLocks[group]
		if lock == nil {
			lock = newRuntimeLock()
			p.runtimeLocks[group] = lock
		}
		locks = append(locks, lock)
	}
	p.runtimeLocksMu.Unlock()

	waitCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && waitCap > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, waitCap)
		defer cancel()
	}
	acquired := make([]*runtimeLock, 0, len(locks))
	for _, lock := range locks {
		if err := lock.lock(waitCtx); err != nil {
			for index := len(acquired) - 1; index >= 0; index-- {
				acquired[index].unlock()
			}
			return nil, err
		}
		acquired = append(acquired, lock)
	}
	return func() {
		for index := len(acquired) - 1; index >= 0; index-- {
			acquired[index].unlock()
		}
	}, nil
}

func (p *Pool) StartIdleReleaseLoop(ctx context.Context, idleFor func() time.Duration) {
	if p == nil || idleFor == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				p.ReleaseIdleSessions(idleFor(), now)
			}
		}
	}()
}
