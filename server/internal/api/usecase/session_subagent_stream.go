package usecase

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/session"
)

func (s *Service) ensureSubagentSessions(ctx context.Context, in subagentSessionInput) {
	if in.Parent == nil || in.Pool == nil || in.Manager == nil || in.ToolCall.RawType != "collabToolCall" {
		return
	}
	for _, receiverThreadID := range stringSliceMeta(in.ToolCall.Meta, "receiverThreadIds") {
		receiverThreadID = strings.TrimSpace(receiverThreadID)
		if receiverThreadID == "" {
			continue
		}
		child, err := s.ensureSubagentSession(ctx, in, receiverThreadID)
		if err != nil {
			log.Printf("[subagent] session.ensure.error root=%s parent=%s receiver=%s err=%v", in.RootID, in.Parent.Key, receiverThreadID, err)
			continue
		}
		if child != nil {
			s.startSubagentSubscription(in, child, receiverThreadID)
		}
	}
}

func (s *Service) ensureSubagentSession(ctx context.Context, in subagentSessionInput, receiverThreadID string) (*session.Session, error) {
	if existing, err := in.Manager.FindAgentBindingByAgentSession(ctx, in.Agent, receiverThreadID); err != nil {
		return nil, err
	} else if existing != nil {
		return in.Manager.Get(ctx, existing.SessionKey, 0)
	}
	child, err := in.Manager.Create(ctx, session.CreateInput{
		Type:             session.TypeChat,
		ParentSessionKey: in.Parent.Key,
		ParentToolCallID: in.ToolCall.CallID,
		Agent:            in.Agent,
		Model:            firstNonEmptyString(stringMeta(in.ToolCall.Meta, "model"), in.Model),
		PlanMode:         false,
		Name:             subagentSessionName(in.ToolCall, receiverThreadID),
	})
	if err != nil {
		return nil, err
	}
	if err := in.Manager.UpsertAgentBinding(ctx, session.AgentBinding{
		SessionKey:     child.Key,
		Agent:          in.Agent,
		AgentSessionID: receiverThreadID,
	}); err != nil {
		return nil, err
	}
	if in.OnCreated != nil {
		in.OnCreated(child)
	}
	return child, nil
}

func (s *Service) startSubagentSubscription(in subagentSessionInput, child *session.Session, receiverThreadID string) {
	key := in.RootID + ":" + child.Key + ":" + receiverThreadID
	if _, loaded := activeSubagentSubscriptions.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	go func() {
		defer activeSubagentSubscriptions.Delete(key)
		ctx, cancel := context.WithCancel(in.Pool.Context())
		registerActiveTurn(in.RootID, child.Key, cancel)
		defer unregisterActiveTurn(in.RootID, child.Key)
		runtime, err := in.Pool.GetOrCreate(ctx, agenttypes.OpenSessionInput{
			SessionKey:     agentPoolSessionKey(child.Key, in.Agent),
			AgentName:      in.Agent,
			Model:          firstNonEmptyString(stringMeta(in.ToolCall.Meta, "model"), in.Model),
			Mode:           in.Mode,
			Effort:         firstNonEmptyString(stringMeta(in.ToolCall.Meta, "reasoningEffort"), in.Effort),
			FastService:    in.FastService,
			PlanMode:       false,
			RootPath:       in.RootAbs,
			AgentSessionID: receiverThreadID,
			AgentCtxSeq:    child.AgentCtxSeq[in.Agent],
		})
		if err != nil {
			log.Printf("[subagent] subscription.open.error root=%s session=%s receiver=%s err=%v", in.RootID, child.Key, receiverThreadID, err)
			return
		}
		finishUse := in.Pool.BeginSessionUse(agentPoolSessionKey(child.Key, in.Agent))
		defer finishUse()
		setActiveTurnSession(in.RootID, child.Key, runtime)
		subscriber, ok := runtime.(agenttypes.ThreadEventSubscriber)
		if !ok {
			log.Printf("[subagent] subscription.unsupported root=%s session=%s receiver=%s", in.RootID, child.Key, receiverThreadID)
			return
		}
		markDone := attachBackgroundSessionUpdates(ctx, in, child, runtime)
		if err := subscriber.SubscribeThreadEvents(ctx); err != nil && ctx.Err() == nil {
			log.Printf("[subagent] subscription.error root=%s session=%s receiver=%s err=%v", in.RootID, child.Key, receiverThreadID, err)
		}
		markDone()
	}()
}

func attachBackgroundSessionUpdates(ctx context.Context, in subagentSessionInput, child *session.Session, runtime agenttypes.Session) func() {
	var responseText string
	// 子会话是三条服务端发起的轮次路径之一（方案 C2）：它没有 user 行
	// （子会话的转录就是子代理自己的），所以 TurnSeq 留 0，只 mint mindfs_id。
	// 一个订阅 = 一条助手行 = 一轮；mindfs_id 在分配真实 seq 时 mint。
	auxBuffer := make([]session.ExchangeAux, 0, 8)
	var thoughtBuffer strings.Builder
	lastResponseUpdateType := ""
	var doneMu sync.Mutex
	doneSent := false
	currentThoughtID := ""
	flushThought := func() {
		thought := thoughtBuffer.String()
		if strings.TrimSpace(thought) == "" {
			thoughtBuffer.Reset()
			currentThoughtID = ""
			return
		}
		thoughtID := currentThoughtID
		thoughtBuffer.Reset()
		currentThoughtID = ""
		auxBuffer = append(auxBuffer, session.ExchangeAux{
			Line:      currentAssistantLine(responseText),
			Thought:   thought,
			ThoughtID: thoughtID,
		})
	}
	finish := func(emit bool) {
		doneMu.Lock()
		if doneSent {
			doneMu.Unlock()
			return
		}
		doneSent = true
		doneMu.Unlock()
		// 终帧先挂成 defer：它必须**无论落盘成败**都发出去。落盘失败时早退会把
		// 灯永久留在"在回复"上，而用户看到的是"卡死"——比丢一段正文更难查。
		if emit && in.OnUpdate != nil {
			defer in.OnUpdate(child.Key, agenttypes.Event{Type: agenttypes.EventTypeMessageDone, Data: agenttypes.MessageDone{}})
		}
		ctx = session.WithExchangeSource(ctx, session.ExchangeSourceLive)
		defer in.Manager.ClearPendingExchangeAux(context.Background(), child.Key)
		// 落盘一律走**脱离订阅取消**的上下文。子代理订阅上下文是从父回合 turnCtx
		// 派生的（session_subagents.go:189），父回合一结束它就被掐 —— 而 FinishAll
		// 正是 defer 在父回合尾部跑的。用订阅上下文落盘会整段撞上 context canceled：
		// 正文/aux/块表写不进去，而 finish 对这些失败只 log 不返回，丢失是静默的
		// （线上形态：[subagent] persist.blocks.error err=context canceled）。
		// ClearPendingExchangeAux 早就用 Background 了，这里补齐其余四处。
		// 只影响"写不写得进 DB"，不影响订阅本身什么时候断。
		persistCtx := session.WithExchangeSource(context.Background(), session.ExchangeSourceLive)
		flushThought()
		var subTurnID string
		subAgentSeq, err := in.Manager.AddExchangeForAgentAtWithIdentityFactory(
			persistCtx, child, "agent", responseText, in.Agent, in.Mode, in.Effort, in.FastService, time.Time{},
			func(seq int) session.ExchangeIdentity {
				subTurnID = newMindFSID("", fmt.Sprintf("%s|agent|%d", child.Key, seq))
				return session.ExchangeIdentity{MindFSID: subTurnID}
			},
		)
		if err != nil {
			log.Printf("[subagent] persist.agent.error root=%s session=%s err=%v", in.RootID, child.Key, err)
			return
		}
		subAux := dedupeExchangeAuxBuffer(auxBuffer)
		for i := range subAux {
			subAux[i].Seq = subAgentSeq
			subAux[i] = hydratePendingToolCallAux(persistCtx, in.Manager, child.Key, subAux[i])
			if err := in.Manager.AddExchangeAux(persistCtx, child.Key, subAux[i]); err != nil {
				log.Printf("[subagent] persist.aux.error root=%s session=%s err=%v", in.RootID, child.Key, err)
				return
			}
		}
		// 双写块流（方案 S2a）。子代理块带 parentToolUseId / taskId / subagentType（D16）由
		// aux 的既有归属字段承载，S2c 接线；这里先保证正文不丢。
		if err := persistTurnBlocks(persistCtx, in.Manager, child.Key, subAgentSeq, subTurnID, responseText, subAux); err != nil {
			log.Printf("[subagent] persist.blocks.error root=%s session=%s seq=%d err=%v", in.RootID, child.Key, subAgentSeq, err)
		}
		if err := in.Manager.UpdateAgentState(persistCtx, child, in.Agent, contextLineCount(child.Exchanges), runtime.SessionID()); err != nil {
			log.Printf("[subagent] persist.agent_state.error root=%s session=%s err=%v", in.RootID, child.Key, err)
		}
	}
	runtime.OnUpdate(func(update agenttypes.Event) {
		switch update.Type {
		case agenttypes.EventTypeThoughtChunk:
			if chunk, ok := update.Data.(agenttypes.ThoughtChunk); ok && chunk.Content != "" {
				if currentThoughtID == "" {
					currentThoughtID = "thought-" + randomHex(8)
				}
				chunk.ID = currentThoughtID
				update.Data = chunk
				thoughtBuffer.WriteString(chunk.Content)
			}
		case agenttypes.EventTypeToolCall, agenttypes.EventTypeToolUpdate, agenttypes.EventTypeTodoUpdate, agenttypes.EventTypePlanUpdate, agenttypes.EventTypeCompact, agenttypes.EventTypeMessageChunk, agenttypes.EventTypeMessageDone:
			flushThought()
		}
		clientUpdate := compactAgentUpdate(update)
		if update.Type == agenttypes.EventTypeToolCall || update.Type == agenttypes.EventTypeToolUpdate {
			if toolCall, ok := update.Data.(agenttypes.ToolCall); ok {
				if shouldPersistToolCallAux(toolCall) {
					toolCallCopy := toolCall
					auxBuffer = append(auxBuffer, session.ExchangeAux{
						Line:     currentAssistantLine(responseText),
						ToolCall: &toolCallCopy,
					})
					_ = in.Manager.UpsertPendingExchangeAux(context.Background(), child.Key, session.ExchangeAux{ToolCall: &toolCallCopy})
				}
			}
		}
		if update.Type == agenttypes.EventTypePlanUpdate {
			if plan, ok := update.Data.(agenttypes.PlanUpdate); ok && strings.TrimSpace(plan.Content) != "" {
				planCopy := plan
				auxBuffer = append(auxBuffer, session.ExchangeAux{
					Line: currentAssistantLine(responseText),
					Plan: &planCopy,
				})
			}
		}
		if update.Type == agenttypes.EventTypeTodoUpdate {
			if todo, ok := update.Data.(agenttypes.TodoUpdate); ok && len(todo.Items) > 0 {
				todoCopy := todo
				auxBuffer = append(auxBuffer, session.ExchangeAux{
					Line: currentAssistantLine(responseText),
					Todo: &todoCopy,
				})
			}
		}
		if update.Type == agenttypes.EventTypeCompact {
			if compact, ok := update.Data.(agenttypes.CompactNotice); ok {
				compactCopy := compact
				auxBuffer = append(auxBuffer, session.ExchangeAux{
					Line:    currentAssistantLine(responseText),
					Compact: &compactCopy,
				})
			}
		}
		if update.Type == agenttypes.EventTypeMessageChunk {
			if chunk, ok := update.Data.(agenttypes.MessageChunk); ok {
				responseText = appendResponseChunk(responseText, lastResponseUpdateType, chunk.Content)
				lastResponseUpdateType = string(update.Type)
			}
		} else if update.Type == agenttypes.EventTypeThoughtChunk ||
			update.Type == agenttypes.EventTypeToolCall ||
			update.Type == agenttypes.EventTypeToolUpdate ||
			update.Type == agenttypes.EventTypeTodoUpdate ||
			update.Type == agenttypes.EventTypePlanUpdate ||
			update.Type == agenttypes.EventTypeCompact {
			lastResponseUpdateType = string(update.Type)
		}
		if update.Type != agenttypes.EventTypeMessageDone {
			if in.OnUpdate != nil {
				in.OnUpdate(child.Key, clientUpdate)
			}
			return
		}
		finish(false)
		if in.OnUpdate != nil {
			in.OnUpdate(child.Key, clientUpdate)
		}
	})
	return func() { finish(true) }
}
