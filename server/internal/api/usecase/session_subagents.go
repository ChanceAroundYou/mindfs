package usecase

import (
	"context"
	"log"
	"strings"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/session"
)

func newClaudeSubagentRouter(in subagentSessionInput) *claudeSubagentRouter {
	return &claudeSubagentRouter{
		in:    in,
		byRef: make(map[string]*claudeSyntheticSubagent),
	}
}

func (r *claudeSubagentRouter) Handle(ctx context.Context, update agenttypes.Event) bool {
	if r == nil || r.in.Parent == nil || r.in.Manager == nil {
		return false
	}
	if r.handleSubagentTerminalSummary(ctx, update) {
		return false
	}
	ref := claudeSubagentRefFromUpdate(update)
	if !ref.hasRef() {
		return false
	}
	if isClaudeParentTaskLifecycle(update) {
		return false
	}
	if r.find(ref) == nil {
		// 新建只认「真正的子代理身份」（Task 工具调用带 taskId/subagentType/
		// taskDescription），或者带 parentToolUseId 的子代理正文（MessageChunk /
		// ThoughtChunk / MessageDone 走认领分支）。Bash 等普通工具的心跳
		// （ToolProgressMessage → ToolUpdate）也带 parentToolUseId，但它不是新的
		// 子代理 —— 早前按 parentToolUseId 非空就放行，于是每次心跳都会建出一个
		// 名为 "Bash"、正文全空的幽灵子会话（2026-10-25 实测一次会话上百个）。
		if update.Type == agenttypes.EventTypeToolCall || update.Type == agenttypes.EventTypeToolUpdate {
			return false
		}
		if strings.TrimSpace(ref.ParentToolUseID) == "" {
			return false
		}
	}
	child, err := r.ensure(ctx, ref, toolCallFromUpdate(update))
	if err != nil {
		log.Printf("[subagent/claude] session.ensure.error root=%s parent=%s ref=%s err=%v", r.in.RootID, r.in.Parent.Key, ref.key(), err)
		return false
	}
	if child == nil {
		return false
	}
	if child.closed {
		// 已关闭的子代理，更新仍属于它 —— 必须吃掉而不是落回父会话：取消或收尾之后
		// 父进程可能还在吐这个子代理的残余输出，放行会让它串进父会话正文。
		return true
	}
	child.runtime.emit(update)
	if update.Type == agenttypes.EventTypeMessageDone {
		child.close()
	}
	return true
}

// FinishAll 收尾本轮派出去的所有子代理。close() 幂等，重复调用安全。
func (r *claudeSubagentRouter) FinishAll() {
	if r == nil {
		return
	}
	for _, child := range r.children {
		if child == nil {
			continue
		}
		child.close()
	}
}

func (r *claudeSubagentRouter) handleSubagentTerminalSummary(ctx context.Context, update agenttypes.Event) bool {
	if update.Type != agenttypes.EventTypeToolUpdate {
		return false
	}
	toolCall, ok := update.Data.(agenttypes.ToolCall)
	if !ok {
		return false
	}
	summary := ""
	if toolCall.RawType == "subagent_result" {
		for _, item := range toolCall.Content {
			if strings.TrimSpace(item.Text) != "" {
				summary = item.Text
				break
			}
		}
	} else if toolCall.RawType == "claude_task" && strings.TrimSpace(stringMeta(toolCall.Meta, "subtype")) == "task_notification" {
		summary = stringMeta(toolCall.Meta, "summary")
	} else {
		return false
	}
	child := r.find(claudeSubagentRefFromUpdate(update))
	if child == nil && toolCall.RawType == "subagent_result" {
		child = r.onlyOpenChild()
	}
	if child == nil {
		return false
	}
	if strings.TrimSpace(summary) != "" {
		child.runtime.emit(agenttypes.Event{
			Type:      agenttypes.EventTypeMessageChunk,
			SessionID: update.SessionID,
			Data:      agenttypes.MessageChunk{Content: summary},
		})
	}
	child.runtime.emit(agenttypes.Event{Type: agenttypes.EventTypeMessageDone, SessionID: update.SessionID, Data: agenttypes.MessageDone{}})
	child.close()
	_ = ctx
	return true
}

func (r *claudeSubagentRouter) onlyOpenChild() *claudeSyntheticSubagent {
	var found *claudeSyntheticSubagent
	for _, child := range r.children {
		if child == nil || child.closed {
			continue
		}
		if found != nil {
			return nil
		}
		found = child
	}
	return found
}

func (r *claudeSubagentRouter) ensure(ctx context.Context, ref claudeSubagentRef, source *agenttypes.ToolCall) (*claudeSyntheticSubagent, error) {
	keys := ref.keys()
	if child := r.find(ref); child != nil {
		r.bindKeys(child, keys)
		return child, nil
	}
	primary := ref.key()
	if primary == "" {
		return nil, nil
	}
	if existing, err := r.in.Manager.FindAgentBindingByAgentSession(ctx, r.in.Agent, "claude-subagent:"+primary); err != nil {
		return nil, err
	} else if existing != nil {
		loaded, err := r.in.Manager.Get(ctx, existing.SessionKey, 0)
		if err != nil {
			return nil, err
		}
		child := r.attach(loaded, ref)
		r.bindKeys(child, keys)
		return child, nil
	}
	parentToolCallID := firstNonEmptyString(ref.ParentToolUseID, sourceCallID(source), ref.TaskID)
	child, err := r.in.Manager.Create(ctx, session.CreateInput{
		Type:             session.TypeChat,
		ParentSessionKey: r.in.Parent.Key,
		ParentToolCallID: parentToolCallID,
		Agent:            r.in.Agent,
		Model:            firstNonEmptyString(sourceModel(source), r.in.Model),
		PlanMode:         false,
		Name:             claudeSubagentSessionName(ref, source),
	})
	if err != nil {
		return nil, err
	}
	if err := r.in.Manager.UpsertAgentBinding(ctx, session.AgentBinding{
		SessionKey:     child.Key,
		Agent:          r.in.Agent,
		AgentSessionID: "claude-subagent:" + primary,
	}); err != nil {
		return nil, err
	}
	attached := r.attach(child, ref)
	r.bindKeys(attached, keys)
	if r.in.OnCreated != nil {
		r.in.OnCreated(child)
	}
	return attached, nil
}

func (r *claudeSubagentRouter) find(ref claudeSubagentRef) *claudeSyntheticSubagent {
	for _, key := range ref.keys() {
		if child := r.byRef[key]; child != nil {
			return child
		}
	}
	return nil
}

func (r *claudeSubagentRouter) attach(child *session.Session, ref claudeSubagentRef) *claudeSyntheticSubagent {
	runtime := &syntheticAgentSession{id: firstNonEmptyString(ref.ParentToolUseID, ref.TaskID, child.Key)}
	in := r.in
	parentCtx := in.Ctx
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	ctx, cancel := context.WithCancel(parentCtx)
	done := attachBackgroundSessionUpdates(ctx, in, child, runtime)
	// 回合身份在**每一条** attach 路径上统一发放（新建 + 认领已有 binding）。
	// 认领分支此前只 attach 不发身份，于是那条子会话没有任何代码持有它的回合：
	// defer drain 扫不到、close() 也不终结它，pending 永久残留。
	turnID := subagentTurnID(child)
	attached := &claudeSyntheticSubagent{
		rootID:  in.RootID,
		key:     child.Key,
		child:   child,
		runtime: runtime,
		done:    done,
		cancel:  cancel,
		turnID:  turnID,
		in:      in,
	}
	// 合成子代理也是一轮"在跑"：不登记的话 Service.CancelSessionTurn 会走
	// active==nil 那条路静默返回 —— 表现就是停止按钮点了没反应。
	runtime.cancel = func() error {
		attached.close()
		return nil
	}
	registerActiveTurn(in.RootID, child.Key, cancel)
	setActiveTurnSession(in.RootID, child.Key, runtime)
	// 由调用方在这里置 pending（拿到同一枚 turn id）并登记进自己的 drain map。
	// 认领分支此前不走这里，是这条子会话身份缺失的直接原因。
	if in.OnSubAgentAttached != nil {
		in.OnSubAgentAttached(child, attached.turnID)
	}
	r.children = append(r.children, attached)
	return attached
}

func (r *claudeSubagentRouter) bindKeys(child *claudeSyntheticSubagent, keys []string) {
	for _, key := range keys {
		if key != "" {
			r.byRef[key] = child
		}
	}
}

type claudeSubagentRef struct {
	ParentToolUseID string
	TaskID          string
	SubagentType    string
	TaskDescription string
}
