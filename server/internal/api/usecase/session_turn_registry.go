package usecase

import (
	"context"
	"strings"
	"sync"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
)

const (
	switchContextTailLines = 20
	// 块流一行一块，一轮的主体回复能散成十几个块；给一个约等于 20 条交换行
	// 的块行上限，避免首次导入（agent 一条都没看过）时要求它读整份文件。
	switchContextTailBlocks  = 200
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
	// continuationTails 记「这一轮的边界之后，CLI 还在这条流上继续输出」。
	//
	// 起因：Claude CLI 在回合 result 之后会因后台任务回注（task-notification）自己再跑
	// 一整段循环，输出的行实时可见，但实时路径只在 SendMessage 返回时落一次盘，那之后
	// 的续写没有任何写入者。补录走导入器，而导入器每拍都读转录太贵（live-owned 的字节
	// 游标是冻结的，读一次就是从游标到文件尾），所以用这个标记当触发边沿：只有真的
	// 见到「回合已结束却还有事件进来」才去读一次转录。
	continuationTails = make(map[string]bool)
	activeTurnSeq     uint64
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

// markSessionContinuationTail 记下「回合边界之后这条流还在输出」，等下一次同步补录。
func markSessionContinuationTail(rootID, sessionKey string) {
	if strings.TrimSpace(rootID) == "" || strings.TrimSpace(sessionKey) == "" {
		return
	}
	activeTurnsMu.Lock()
	continuationTails[activeTurnKey(rootID, sessionKey)] = true
	activeTurnsMu.Unlock()
}

// takeSessionContinuationTail 取走标记（取即清除）：补录失败或转录尾部还没收尾时，
// 调用方负责用 markSessionContinuationTail 放回去。
func takeSessionContinuationTail(rootID, sessionKey string) bool {
	if strings.TrimSpace(rootID) == "" || strings.TrimSpace(sessionKey) == "" {
		return false
	}
	activeTurnsMu.Lock()
	defer activeTurnsMu.Unlock()
	key := activeTurnKey(rootID, sessionKey)
	if !continuationTails[key] {
		return false
	}
	delete(continuationTails, key)
	return true
}
