package kanban

import (
	"sync"
	"time"
)

// terminatedTurnSummary 是用户点了「终止」时写给回复行的说明。与重启中断的
// restartInterruptedReplySummary 分开：一个是被动丢失现场，一个是用户主动叫停，
// 后续动作也不一样（后者可以直接重开）。
const terminatedTurnSummary = "已被用户终止；可以直接发消息重开一轮。"

// terminatedTurnTTL 是终止标记的保留时长。正常情况下回合收尾就会消费掉；会话
// 已经不在跑、没人消费的那种到点清掉即可 —— 标记只决定「收尾写 cancelled 还是
// failed」，过期后再读到也没有副作用。
const terminatedTurnTTL = 5 * time.Minute

// TurnTerminations 记「这些回复是被用户终止的」。
//
// 为什么不是 ActivityService 的普通字段：ActivityServiceForRoot 每次现建实例，
// 处理 HTTP「终止」的那个实例与跑回合的实例并不是同一个对象，标记必须挂在两者
// 共享的登记簿上（Service.turnTerminations）才读得到。
type TurnTerminations struct {
	mu  sync.Mutex
	at  map[string]time.Time
	now func() time.Time // 测试注入时钟；nil 用 time.Now
}

func newTurnTerminations() *TurnTerminations {
	return &TurnTerminations{at: map[string]time.Time{}}
}

func (t *TurnTerminations) nowFn() time.Time {
	if t != nil && t.now != nil {
		return t.now()
	}
	return time.Now()
}

// Mark 记住这些活动是被终止的。顺带清掉过期条目，避免登记簿随着长期运行一直涨。
func (t *TurnTerminations) Mark(ids ...string) {
	if t == nil || len(ids) == 0 {
		return
	}
	now := t.nowFn()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.at == nil {
		t.at = map[string]time.Time{}
	}
	for _, id := range ids {
		if id == "" {
			continue
		}
		t.at[id] = now
	}
	for id, at := range t.at {
		if now.Sub(at) > terminatedTurnTTL {
			delete(t.at, id)
		}
	}
}

// Consume 取走标记并报告它是否仍然有效（存在且未过期）。
func (t *TurnTerminations) Consume(id string) bool {
	if t == nil || id == "" {
		return false
	}
	now := t.nowFn()
	t.mu.Lock()
	defer t.mu.Unlock()
	at, ok := t.at[id]
	if !ok {
		return false
	}
	delete(t.at, id)
	return now.Sub(at) <= terminatedTurnTTL
}
