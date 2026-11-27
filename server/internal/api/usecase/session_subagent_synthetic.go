package usecase

import (
	"context"
	"fmt"
	"mindfs/server/internal/agent"
	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/session"
	"sync"
)

type subagentSessionInput struct {
	// Ctx 是子代理订阅的父上下文（取消它 = 掐断这个子代理的更新）。零值时按
	// context.Background() 处理，保持调用方不传的旧行为。
	Ctx         context.Context
	RootID      string
	Parent      *session.Session
	Agent       string
	Model       string
	Mode        string
	Effort      string
	FastService string
	RootAbs     string
	Pool        *agent.Pool
	Manager     *session.Manager
	ToolCall    agenttypes.ToolCall
	// OnCreated 只在「新建」子会话时触发一次，语义是「把它加进列表」。
	OnCreated func(*session.Session)
	// OnSubAgentAttached 在**每一条** attach 路径（新建 + 认领已有 binding）上触发，
	// 返回不对 —— 是**交出**这条子会话本轮的身份（turn mindfs_id）。它必须由 attach()
	// 统一发放：认领分支此前从不调 OnCreated，于是那条子会话没有任何代码持有它的
	// 回合身份，defer drain 扫不到它、close() 也不终结它 —— pending 永久残留，
	// 父会话被前端 descendantRunning() 判成 delegating（线上 1792108831 的症状）。
	OnSubAgentAttached func(child *session.Session, turnID string)
	// OnSubAgentTurnFinished 由 close() 自己调用，交还同一枚 turn id。是谁收尾都行
	// （正常 done / 取消 / 父轮 defer drain / FinishAll），终结都汇聚到这一处。
	OnSubAgentTurnFinished func(sessionKey, turnID string)
	OnUpdate               func(sessionKey string, update agenttypes.Event)
}

// subagentTurnID 铸一条子会话的回合身份。
//
// attachID 是 registerActiveTurn 发放的本次 attach 专属 id：同一个子会话可能被
// 重复认领，若只按 MaxExchangeSeq+1 铸造，内容序号没变时会铸出同一个 id，旧的
// close 就会通过身份校验、把新 attach 的 pending 提前清掉。把 attachID 拼进去，
// 每次 attach 的身份都唯一，旧 close 只能被拒，不可能误清新灯。
func subagentTurnID(child *session.Session, attachID uint64) string {
	if child == nil {
		return ""
	}
	payload := fmt.Sprintf("%s|agent|%d", child.Key, session.MaxExchangeSeq(child.Exchanges)+1)
	if attachID != 0 {
		payload = fmt.Sprintf("%s|attach|%d", payload, attachID)
	}
	return session.NewMindFSID("", payload)
}

type claudeSubagentRouter struct {
	in        subagentSessionInput
	byRef     map[string]*claudeSyntheticSubagent
	bySession map[string]*claudeSyntheticSubagent
	children  []*claudeSyntheticSubagent
}

type claudeSyntheticSubagent struct {
	rootID  string
	key     string
	child   *session.Session
	runtime *syntheticAgentSession
	done    func()
	// cancel 掐断本子代理的订阅上下文。它同时是登记进 activeTurns 的取消函数，
	// 因此「停止按钮」能命中这个子代理（见 attach）。
	cancel context.CancelFunc
	// activeTurnID 是本次 attach 在 activeTurns 里的登记 id。close() 必须原样
	// 交回：重叠 attach 时旧 close 只按 key 注销会删掉新 attach 的登记，之后
	// ActiveTurnID 恒为 0，停止按钮就只剩静默 no-op。
	activeTurnID uint64
	// turnID 是 attach() 时发放的本轮身份。close() 必须原样交还给终结器 ——
	// EndSessionTurn 按它比对（见 PendingTurnIDMatches），缺了它清理会退化成
	// 无身份的「保守清」，把下一轮的灯误抹。
	turnID string
	in     subagentSessionInput
	closed bool
}

// close 是合成子代理的**唯一**收尾点，幂等。
//
// 必须做的四件事缺一不可：走 done() 落盘并发出终帧（前端清灯的来源之一）、掐断
// 订阅（此后不再有更新进来）、注销活跃轮次（否则下一轮取消会命中一个已死的子代理）、
// **终结本子会话自己的回合**（否则它的 pending 永久残留，父会话被前端
// descendantRunning() 判成 delegating —— 用户停了子代理，主会话依旧显示在跑）。
// 顺序上 done() 必须早于 cancel()：done() 用订阅上下文落盘，先掐上下文会让这条
// 子代理的正文写不下去 —— 灯是灭了，字也丢了。
func (c *claudeSyntheticSubagent) close() {
	if c == nil || c.closed {
		return
	}
	c.closed = true
	if c.done != nil {
		c.done()
	}
	if c.cancel != nil {
		c.cancel()
	}
	// 只注销本次 attach 的那一轮。按 key 强删会把后来者的登记一起带走，
	// 之后停止按钮拿不到 target_turn，只剩静默 no-op。
	unregisterActiveTurn(c.rootID, c.key, c.activeTurnID)
	// 终结放在最后：done() 会同步发出终帧（OnUpdate → EventTypeMessageDone），
	// 那条路径自己也会走一次终结。放前面会让它拿到已清理后的状态。
	if c.turnID != "" && c.in.OnSubAgentTurnFinished != nil {
		c.in.OnSubAgentTurnFinished(c.key, c.turnID)
	}
}

var activeSubagentSubscriptions sync.Map

func (s *syntheticAgentSession) SendMessage(context.Context, string) error { return nil }
func (s *syntheticAgentSession) AnswerQuestion(context.Context, agenttypes.AskUserAnswer) error {
	return nil
}
func (s *syntheticAgentSession) CurrentModel() string                   { return "" }
func (s *syntheticAgentSession) SetModel(context.Context, string) error { return nil }
func (s *syntheticAgentSession) ListModels(context.Context) (agenttypes.ModelList, error) {
	return agenttypes.ModelList{}, nil
}
func (s *syntheticAgentSession) SetMode(context.Context, string) error   { return nil }
func (s *syntheticAgentSession) SetPlanMode(context.Context, bool) error { return nil }
func (s *syntheticAgentSession) ListModes(context.Context) (agenttypes.ModeList, error) {
	return agenttypes.ModeList{}, nil
}
func (s *syntheticAgentSession) ListCommands(context.Context) (agenttypes.CommandList, error) {
	return agenttypes.CommandList{}, nil
}

// CancelCurrentTurn 关掉这个合成子代理。
//
// 早先这里无条件 return nil 是个"静默成功"的空操作：Service.CancelSessionTurn
// 调用它之后认为已经掐断，实际订阅还在、子会话还开着、pending 还在 —— 用户看到
// 的就是「停止按钮点了没反应、灯一直亮」。合成子代理没有可中断的进程（它的输出
// 来自父进程的 CLI 流），能掐的就是订阅本身。
func (s *syntheticAgentSession) CancelCurrentTurn() error {
	if s.cancel == nil {
		return nil
	}
	return s.cancel()
}
func (s *syntheticAgentSession) OnUpdate(onUpdate func(agenttypes.Event)) {
	s.onUpdate = onUpdate
}
func (s *syntheticAgentSession) SessionID() string { return s.id }

func (s *syntheticAgentSession) Close() error { return nil }
func (s *syntheticAgentSession) emit(event agenttypes.Event) {
	if s.onUpdate != nil {
		s.onUpdate(event)
	}
}
