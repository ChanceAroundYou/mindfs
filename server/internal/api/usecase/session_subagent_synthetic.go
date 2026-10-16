package usecase

import (
	"context"
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
	OnCreated   func(*session.Session)
	OnUpdate    func(sessionKey string, update agenttypes.Event)
}
type claudeSubagentRouter struct {
	in       subagentSessionInput
	byRef    map[string]*claudeSyntheticSubagent
	children []*claudeSyntheticSubagent
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
	closed bool
}

// close 是合成子代理的**唯一**收尾点，幂等。
//
// 必须做的三件事缺一不可：走 done() 落盘并发出终帧（前端清灯的唯一来源）、掐断
// 订阅（此后不再有更新进来）、注销活跃轮次（否则下一轮取消会命中一个已死的子代理）。
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
	unregisterActiveTurn(c.rootID, c.key)
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
