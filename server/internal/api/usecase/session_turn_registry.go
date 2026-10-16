package usecase

import (
	"context"
	"strings"
	"sync"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
)

const (
	switchContextTailLines   = 20
	sessionNameTimeout       = 30 * time.Second
	sessionNameMinMessageLen = 12
	sessionRecoveryAttempts  = 3
	sessionRecoveryDelay     = 30 * time.Second
)

var (
	sessionSendLocksMu sync.Mutex
	sessionSendLocks   = make(map[string]*sync.Mutex)
	activeTurnsMu      sync.Mutex
	activeTurns        = make(map[string]*activeTurnState)
	activeTurnSeq      uint64
)

type activeTurnState struct {
	// id 标识「具体是哪一轮」。取消是异步落地的（ws.go 异步派发，因为 Interrupt
	// 可能阻塞），从用户点到执行可以隔上几百毫秒；这期间同一会话可能已经开了新的
	// 一轮（用户点完停止紧接着发消息）。调用方在收到请求的那一刻就记下当时的 id，
	// 落地时 id 对不上就说明它要停的那一轮已经结束了 —— 直接放弃，别误伤新轮。
	id      uint64
	cancel  context.CancelFunc
	session agenttypes.Session
}

func getSessionSendLock(sessionKey string) *sync.Mutex {
	sessionSendLocksMu.Lock()
	defer sessionSendLocksMu.Unlock()
	lock := sessionSendLocks[sessionKey]
	if lock == nil {
		lock = &sync.Mutex{}
		sessionSendLocks[sessionKey] = lock
	}
	return lock
}

func activeTurnKey(rootID, sessionKey string) string {
	return rootID + "::" + sessionKey
}

func registerActiveTurn(rootID, sessionKey string, cancel context.CancelFunc) {
	if strings.TrimSpace(rootID) == "" || strings.TrimSpace(sessionKey) == "" || cancel == nil {
		return
	}
	activeTurnsMu.Lock()
	activeTurnSeq++
	activeTurns[activeTurnKey(rootID, sessionKey)] = &activeTurnState{id: activeTurnSeq, cancel: cancel}
	activeTurnsMu.Unlock()
}

// ActiveTurnID 返回此刻在跑的那一轮的 id，没有在跑的一轮时返回 0。
//
// 调用方必须在**决定取消的那一刻**同步取证（ws.go 的读循环里），再拿着这个 id
// 去做异步的取消：取消落地时 id 不一致就说明目标轮次已经结束，必须放弃。
func ActiveTurnID(rootID, sessionKey string) uint64 {
	activeTurnsMu.Lock()
	defer activeTurnsMu.Unlock()
	if state := activeTurns[activeTurnKey(rootID, sessionKey)]; state != nil {
		return state.id
	}
	return 0
}

func setActiveTurnSession(rootID, sessionKey string, sess agenttypes.Session) {
	if strings.TrimSpace(rootID) == "" || strings.TrimSpace(sessionKey) == "" || sess == nil {
		return
	}
	activeTurnsMu.Lock()
	state := activeTurns[activeTurnKey(rootID, sessionKey)]
	if state != nil {
		state.session = sess
	}
	activeTurnsMu.Unlock()
}

func unregisterActiveTurn(rootID, sessionKey string) {
	if strings.TrimSpace(rootID) == "" || strings.TrimSpace(sessionKey) == "" {
		return
	}
	activeTurnsMu.Lock()
	delete(activeTurns, activeTurnKey(rootID, sessionKey))
	activeTurnsMu.Unlock()
}

func getActiveTurn(rootID, sessionKey string) *activeTurnState {
	activeTurnsMu.Lock()
	defer activeTurnsMu.Unlock()
	return activeTurns[activeTurnKey(rootID, sessionKey)]
}

func activeTurnIDOrNil(active *activeTurnState) any {
	if active == nil {
		return "none"
	}
	return active.id
}

// SessionTurnActive lets background orchestration defer while the user is chatting.
// SendMessage's existing per-session lock remains the final serialization guard.
func SessionTurnActive(rootID, sessionKey string) bool {
	return getActiveTurn(rootID, sessionKey) != nil
}
