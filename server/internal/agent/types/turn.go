package types

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
)

type TurnCanceler struct {
	mu     sync.RWMutex
	cancel context.CancelFunc
	turnID uint64
}

func (t *TurnCanceler) Begin(parent context.Context) (context.Context, uint64) {
	turnCtx, cancel := context.WithCancel(parent)
	turnID := atomic.AddUint64(&t.turnID, 1)

	t.mu.Lock()
	t.cancel = cancel
	t.turnID = turnID
	t.mu.Unlock()

	return turnCtx, turnID
}

func (t *TurnCanceler) Cancel() {
	t.mu.RLock()
	cancel := t.cancel
	t.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
}

func (t *TurnCanceler) End(turnID uint64) {
	t.mu.Lock()
	if t.turnID == turnID {
		t.cancel = nil
	}
	t.mu.Unlock()
}

// promptTurnIDContextKey 是「本轮要递给 ACP 的 prompt messageId」的 context 键。
// 用私有类型，避免与别的 context 值撞键。
type promptTurnIDContextKey struct{}

// WithPromptTurnID 把本轮的 MindFS mindfs_id 放进 context，供 ACP 适配器当作
// `PromptRequest.MessageId` 递出去（D3：一个逻辑 id 一种表示）。
//
// 为什么走 context 而不是给 `Session.SendMessage` 加参数：`SendMessage` 是三个
// agent 共用的接口，加参数要动三处实现 + 所有调用方；而 mindfs_id 本来就是
// 「这一轮」的上下文，与 `WithExchangeSource` / `WithExchangeIdentity` 同一层。
func WithPromptTurnID(ctx context.Context, turnID string) context.Context {
	return context.WithValue(ctx, promptTurnIDContextKey{}, turnID)
}

// PromptTurnIDFromContext 取出 WithPromptTurnID 放的 mindfs_id；没有则空串。
func PromptTurnIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	turnID, _ := ctx.Value(promptTurnIDContextKey{}).(string)
	return strings.TrimSpace(turnID)
}
