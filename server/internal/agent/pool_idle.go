package agent

import (
	"context"
	"log"
	"sort"
	"sync"
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

func (p *Pool) lockRuntimeGroups(groups []runtimeGroup) func() {
	if len(groups) == 0 {
		return func() {}
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
	locks := make([]*sync.Mutex, 0, len(ordered))
	p.runtimeLocksMu.Lock()
	for _, group := range ordered {
		lock := p.runtimeLocks[group]
		if lock == nil {
			lock = &sync.Mutex{}
			p.runtimeLocks[group] = lock
		}
		locks = append(locks, lock)
	}
	p.runtimeLocksMu.Unlock()
	for _, lock := range locks {
		lock.Lock()
	}
	return func() {
		for index := len(locks) - 1; index >= 0; index-- {
			locks[index].Unlock()
		}
	}
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
