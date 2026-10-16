package api

import (
	"context"
	"sync"
	"time"
)

type turnUpdateTracker struct {
	mu           sync.Mutex
	inFlight     int
	lastActivity time.Time
}

func newTurnUpdateTracker() *turnUpdateTracker {
	return &turnUpdateTracker{lastActivity: time.Now()}
}

func (t *turnUpdateTracker) Begin() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.inFlight++
	t.lastActivity = time.Now()
	t.mu.Unlock()
}

func (t *turnUpdateTracker) End() {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.inFlight > 0 {
		t.inFlight--
	}
	t.lastActivity = time.Now()
	t.mu.Unlock()
}

func (t *turnUpdateTracker) WaitIdle(ctx context.Context, settleWindow, maxWait time.Duration) bool {
	if t == nil {
		return true
	}
	if settleWindow <= 0 {
		settleWindow = 50 * time.Millisecond
	}
	if maxWait <= 0 {
		maxWait = 2 * time.Second
	}
	deadline := time.Now().Add(maxWait)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		t.mu.Lock()
		inFlight := t.inFlight
		idleFor := time.Since(t.lastActivity)
		t.mu.Unlock()
		if inFlight == 0 && idleFor >= settleWindow {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}
