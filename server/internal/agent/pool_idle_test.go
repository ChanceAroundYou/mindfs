package agent

import (
	"errors"
	"testing"
	"time"
)

func TestPoolReleaseIdleSessionsSkipsActiveUse(t *testing.T) {
	pool := NewPool(loadPoolTestConfig(t))
	defer pool.CloseAll()
	now := time.Now()
	sess := &idleReleaseTestSession{}
	pool.sessions["idle"] = &sessionEntry{
		agentName:  "test-agent",
		sessionKey: "idle",
		protocol:   ProtocolClaudeSDK,
		session:    sess,
		lastUsedAt: now.Add(-3 * time.Hour),
	}
	finishUse := pool.BeginSessionUse("idle")
	if got := pool.ReleaseIdleSessions(time.Hour, now); got != 0 {
		t.Fatalf("released active sessions = %d, want 0", got)
	}
	finishUse()
	if got := pool.ReleaseIdleSessions(time.Hour, now.Add(2*time.Hour)); got != 1 {
		t.Fatalf("released idle sessions = %d, want 1", got)
	}
	if sess.closeCount != 1 {
		t.Fatalf("Close calls = %d, want 1", sess.closeCount)
	}
	if _, ok := pool.sessions["idle"]; ok {
		t.Fatal("released session remains in pool")
	}
}

func TestPoolReleaseIdleSessionsKeepsEntryWhenCloseFails(t *testing.T) {
	pool := NewPool(loadPoolTestConfig(t))
	defer pool.CloseAll()
	now := time.Now()
	sess := &idleReleaseTestSession{closeErr: errors.New("close failed")}
	pool.sessions["idle"] = &sessionEntry{
		agentName:  "test-agent",
		sessionKey: "idle",
		protocol:   ProtocolClaudeSDK,
		session:    sess,
		lastUsedAt: now.Add(-3 * time.Hour),
	}
	if got := pool.ReleaseIdleSessions(time.Hour, now); got != 0 {
		t.Fatalf("released failed sessions = %d, want 0", got)
	}
	entry := pool.sessions["idle"]
	if entry == nil || entry.closing {
		t.Fatalf("failed session entry not restored: %#v", entry)
	}
}

func TestPoolReleaseInactiveSessionsImmediatelySkipsActiveUse(t *testing.T) {
	pool := NewPool(loadPoolTestConfig(t))
	defer pool.CloseAll()
	now := time.Now()
	inactive := &idleReleaseTestSession{}
	active := &idleReleaseTestSession{}
	pool.sessions["inactive"] = &sessionEntry{
		agentName:  "test-agent",
		sessionKey: "inactive",
		protocol:   ProtocolClaudeSDK,
		session:    inactive,
		lastUsedAt: now,
	}
	pool.sessions["active"] = &sessionEntry{
		agentName:  "test-agent",
		sessionKey: "active",
		protocol:   ProtocolClaudeSDK,
		session:    active,
		lastUsedAt: now.Add(-24 * time.Hour),
		activeUses: 1,
	}

	result := pool.ReleaseInactiveSessionsDetailed(now)
	if result.ReleasedSessions != 1 || inactive.closeCount != 1 || active.closeCount != 0 {
		t.Fatalf("release = %#v, inactive closes=%d active closes=%d", result, inactive.closeCount, active.closeCount)
	}
	if pool.sessions["inactive"] != nil || pool.sessions["active"] == nil {
		t.Fatalf("unexpected remaining sessions: %#v", pool.sessions)
	}
}

func TestPoolReleaseIdleSessionsReleasesLastSharedRuntimeSession(t *testing.T) {
	pool := NewPool(loadPoolTestConfig(t))
	defer pool.CloseAll()
	now := time.Now()
	idleSession := &idleReleaseTestSession{}
	activeSession := &idleReleaseTestSession{}
	pool.sessions["idle-1"] = &sessionEntry{
		agentName:  "codex",
		sessionKey: "idle-1",
		protocol:   ProtocolCodexSDK,
		session:    idleSession,
		lastUsedAt: now.Add(-3 * time.Hour),
	}
	pool.sessions["active"] = &sessionEntry{
		agentName:  "codex",
		sessionKey: "active",
		protocol:   ProtocolCodexSDK,
		session:    activeSession,
		lastUsedAt: now,
	}

	result := pool.ReleaseIdleSessionsDetailed(time.Hour, now)
	if result.ReleasedSessions != 1 || idleSession.closeCount != 1 || activeSession.closeCount != 0 {
		t.Fatalf("first release = %#v, idle closes=%d active closes=%d", result, idleSession.closeCount, activeSession.closeCount)
	}
	result = pool.ReleaseIdleSessionsDetailed(time.Hour, now.Add(2*time.Hour))
	if result.ReleasedSessions != 1 || activeSession.closeCount != 1 {
		t.Fatalf("second release = %#v, active closes=%d", result, activeSession.closeCount)
	}
}

func TestPoolMemorySnapshotAddsSessionCounts(t *testing.T) {
	pool := NewPool(loadPoolTestConfig(t))
	defer pool.CloseAll()
	now := time.Now()
	pool.memorySnapshot = MemorySnapshot{MeasuredAt: now}
	pool.sessions["idle"] = &sessionEntry{
		agentName:  "claude",
		sessionKey: "idle",
		protocol:   ProtocolClaudeSDK,
		session:    &idleReleaseTestSession{},
		lastUsedAt: now.Add(-2 * time.Hour),
	}
	pool.sessions["active"] = &sessionEntry{
		agentName:  "claude",
		sessionKey: "active",
		protocol:   ProtocolClaudeSDK,
		session:    &idleReleaseTestSession{},
		lastUsedAt: now,
		activeUses: 1,
	}

	snapshot := pool.MemorySnapshot(false)
	if len(snapshot.Agents) != 1 || snapshot.Agents[0].Name != "claude" || snapshot.Agents[0].SessionCount != 2 {
		t.Fatalf("memory snapshot agents = %#v", snapshot.Agents)
	}
}
