package usecase

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/session"
)

func (s *Service) SendMessage(ctx context.Context, in SendMessageInput) error {
	if err := s.ensureRegistry(); err != nil {
		return err
	}
	userTimestamp := sendMessageUserTimestamp(in, time.Now().UTC())
	sendLock := getSessionSendLock(in.Key)
	sendLock.Lock()
	defer sendLock.Unlock()
	turnCtx, turnCancel := context.WithCancel(ctx)
	registerActiveTurn(in.RootID, in.Key, turnCancel)
	defer unregisterActiveTurn(in.RootID, in.Key)
	s.applyMessageRuntimeDefaults(&in)
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return err
	}
	current, err := manager.Get(ctx, in.Key, 0)
	if err != nil {
		return err
	}
	if in.PlanMode != nil && current.PlanMode != *in.PlanMode {
		if err := manager.UpdatePlanMode(ctx, current, *in.PlanMode); err != nil {
			return err
		}
	}
	root := manager.Root()
	managedRootAbs, _ := root.RootDir()
	rootAbs := managedRootAbs
	if runtimeRootPath := strings.TrimSpace(in.RuntimeRootPath); runtimeRootPath != "" {
		rootAbs = filepath.Clean(runtimeRootPath)
	}
	resolvedMode := resolveRuntimeMode(current, in.Mode)
	resolvedFastService := resolveRuntimeFastService(in.Agent, current, in.FastService)
	// 可在任何东西落盘之前就知道它会失败的校验，必须先跑。模型不被支持时本轮压根不会
	// 开始，user 行也就没有存在过——否则会留下一条用户没等到任何回复的孤立消息。
	if current.Type != session.TypeCommand {
		if err := s.validateAgentModel(in.Agent, in.Model); err != nil {
			log.Printf("[session/model] validate.error root=%s session=%s agent=%s model=%q err=%v", in.RootID, in.Key, strings.TrimSpace(in.Agent), strings.TrimSpace(in.Model), err)
			return err
		}
		// The selected model is session metadata, not a completion result. Persist it
		// before the user exchange and OnStart so list/window refreshes during the
		// streaming turn cannot restore the previous model. The final runtime model
		// is reconciled below in case the agent canonicalizes the requested value.
		if requestedModel := strings.TrimSpace(in.Model); requestedModel != "" {
			if err := manager.UpdateModel(ctx, current, requestedModel); err != nil {
				return err
			}
		}
	}
	// 本轮用户消息**在回合开始前**落盘：广播出去的 seq 因此是「已落库的真值」而不是预测。
	// 晚落盘会留下一个窗口期——客户端已按 seq 认定该行持久化，而窗口 maxSeq 与读接口还
	// 看不到它，前端 overlay 会在任意一次重锚定/翻页时把这行丢掉（2026-09-28 实测：
	// agent 执行中自己的消息从列表消失，点同步也刷不回来，回合结束才自愈）。
	// 从这一刻起这个会话的持久化归实时路径（"谁驱动，谁落盘"），导入器不再常规增量
	// 同步——判据是推导的，见 session.SessionIsLiveOwned。
	userExchangeSeq, turnID, err := persistUserTurnExchange(ctx, manager, current, in.Content, in.Agent, resolvedMode, resolveRuntimeEffort(in.Agent, current, in.Effort), resolvedFastService, userTimestamp)
	if err != nil {
		log.Printf("[session] persist.user.error root=%s session=%s agent=%s err=%v", in.RootID, in.Key, in.Agent, err)
		return err
	}
	// persistUserTurnExchange refreshes the caller snapshot and either finds or
	// appends this turn's user row. Count by its actual seq so an importer duplicate
	// found before later rows still gets the correct prompt history boundary.
	linesBeforeThisTurn := 0
	if userExchangeSeq > 0 {
		for _, exchange := range current.Exchanges {
			if exchange.Seq > 0 && exchange.Seq < userExchangeSeq {
				linesBeforeThisTurn++
			}
		}
	} else {
		linesBeforeThisTurn = len(current.Exchanges)
	}
	isInitial := linesBeforeThisTurn == 0
	// 将 canonical turn id 放进上下文。ACP 会把它作为 PromptRequest.MessageId 发送；
	// 如果转录原样回显，导入判重会把它与 user 行 MindFSID 对齐。它仍是 MindFS 的
	// turn 身份，不写入 agent_id；协议不回显时，继续使用各自真实可用的原生身份判重。
	turnCtx = agenttypes.WithPromptTurnID(turnCtx, turnID)
	// 中途换 agent 时，新 agent 的绑定要等本轮跑完才由 UpdateAgentState 写——这中间徽标
	// 会落回「AI」占位。这里补一条，已存在绑定则跳过，不碰已有转录 id。
	if err := manager.EnsureAgentBinding(ctx, current.Key, in.Agent); err != nil {
		log.Printf("[session] ensure.binding.error root=%s session=%s agent=%s err=%v", in.RootID, in.Key, in.Agent, err)
	}
	if in.OnStart != nil {
		in.OnStart(MessageStart{
			Model:            in.Model,
			ModelDisplayName: s.resolveExchangeModelDisplayName(in.Agent, in.Model, rootAbs),
			Mode:             resolvedMode,
			Effort:           in.Effort,
			FastService:      resolvedFastService,
			MindFSID:         turnID,
			UserExchangeSeq:  userExchangeSeq,
		})
	}
	if current.Type == session.TypeCommand {
		return s.sendCommandMessage(turnCtx, in, manager, current, turnID, userExchangeSeq)
	}
	agentPool := s.Registry.GetAgentPool()
	if agentPool == nil {
		return nil
	}
	watcher, _ := s.Registry.GetFileWatcher(in.RootID, manager)
	if watcher != nil {
		watcher.RegisterSession(current.Key)
		watcher.MarkSessionActive(current.Key)
	}
	planMode := current != nil && current.PlanMode

	developerInstructions := ""
	includeReplyTipsInUserMessage := isInitial
	if isInitial && agentPool.SupportsDeveloperInstructions(in.Agent) {
		developerInstructions = replyTips
		includeReplyTipsInUserMessage = false
	}
	sess, agentCtxSeq, err := s.ensureAgentSession(turnCtx, agentPool, manager, current, in.Agent, in.Model, in.Mode, in.Effort, in.FastService, rootAbs, developerInstructions)
	if err != nil {
		return err
	}
	setActiveTurnSession(in.RootID, current.Key, sess)

	prompt := s.BuildPrompt(BuildPromptInput{
		Session:                       current,
		Manager:                       manager,
		Agent:                         in.Agent,
		Message:                       in.Content,
		ClientContext:                 in.ClientCtx,
		AgentCtxSeq:                   agentCtxSeq,
		IsInitial:                     isInitial,
		IncludeReplyTipsInUserMessage: includeReplyTipsInUserMessage,
		LinesBeforeThisTurn:           &linesBeforeThisTurn,
	})
	var responseText string
	sawAssistantChunk := false
	var lastContextWindow agenttypes.ContextWindow
	var turnTokenUsage *agenttypes.TokenUsage
	// turnAgentMessageID 是本轮最后一个 agent 原生消息 id（ACP `messageId`，
	// claude 是转录 uuid），回合结束时写进助手行的 `agent_id`。没有它，导入器重放
	// 同一轮时只能靠内容认领 —— 而导入器会把相邻同角色条目合并，内容必然不同
	// （实测 2026-10-07 某 live-owned 会话 Full 同步写出 9 对重复轮次）。
	var turnAgentMessageID string
	auxBuffer := make([]session.ExchangeAux, 0, 8)
	defer manager.ClearPendingExchangeAux(context.Background(), current.Key)
	var thoughtBuffer strings.Builder
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
	lastResponseUpdateType := ""
	claudeSubagents := newClaudeSubagentRouter(subagentSessionInput{
		Ctx:         turnCtx,
		RootID:      in.RootID,
		Parent:      current,
		Agent:       in.Agent,
		Model:       in.Model,
		Mode:        in.Mode,
		Effort:      in.Effort,
		FastService: in.FastService,
		RootAbs:     rootAbs,
		Manager:     manager,
		OnCreated:   in.OnSubSessionCreated,
		OnUpdate:    in.OnSubSessionUpdate,
	})
	// 收尾必须是 defer：本轮任何一条提前返回/panic 的路径漏掉它，都会留下一盏
	// 永远亮着的子代理灯（用户看到"卡死"）。
	defer claudeSubagents.FinishAll()
	attachSessionUpdates := func(runtime agenttypes.Session) {
		runtime.OnUpdate(func(update agenttypes.Event) {
			update = normalizeAgentUpdatePaths(root, update)
			if claudeSubagents.Handle(context.Background(), update) {
				return
			}
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
				if toolCall, ok := update.Data.(agenttypes.ToolCall); ok && toolCall.IsWriteOperation() {
					for _, path := range toolCall.GetAffectedPaths() {
						if watcher == nil {
							continue
						}
						recordPath := relatedFileRecordPath(managedRootAbs, rootAbs, path)
						if update.Type == agenttypes.EventTypeToolCall && toolCall.Status == "running" {
							watcher.RecordPendingWrite(current.Key, recordPath)
						}
						if update.Type == agenttypes.EventTypeToolUpdate || toolCall.Status == "complete" {
							watcher.RecordSessionFile(current.Key, recordPath)
						}
					}
				}
				if toolCall, ok := update.Data.(agenttypes.ToolCall); ok {
					s.ensureSubagentSessions(context.Background(), subagentSessionInput{
						RootID:      in.RootID,
						Parent:      current,
						Agent:       in.Agent,
						Model:       in.Model,
						Mode:        in.Mode,
						Effort:      in.Effort,
						FastService: in.FastService,
						RootAbs:     rootAbs,
						Pool:        agentPool,
						Manager:     manager,
						ToolCall:    toolCall,
						OnCreated:   in.OnSubSessionCreated,
						OnUpdate:    in.OnSubSessionUpdate,
					})
					if shouldPersistToolCallAux(toolCall) {
						toolCallCopy := toolCall
						auxBuffer = append(auxBuffer, session.ExchangeAux{
							Line:     currentAssistantLine(responseText),
							ToolCall: &toolCallCopy,
						})
						_ = manager.UpsertPendingExchangeAux(context.Background(), current.Key, session.ExchangeAux{ToolCall: &toolCallCopy})
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
					sawAssistantChunk = true
					// 记住**最后一个**带 messageId 的文本块的 id（方案 S2c）：一轮里
					// agent 会发多条原生消息，块流按它切 step；行级身份取**最后一条** ——
					// 与导入侧取法一致（ACP importer 的 append 同样在 messageId 变化时
					// 开新行）。两端取法必须一样，否则同一轮永远「对不上」。
					if strings.TrimSpace(chunk.MessageID) != "" {
						turnAgentMessageID = strings.TrimSpace(chunk.MessageID)
					}
					responseText = appendResponseChunk(responseText, lastResponseUpdateType, chunk.Content)
					lastResponseUpdateType = string(update.Type)
				}
			} else if update.Type == agenttypes.EventTypeMessageDone {
				if done, ok := update.Data.(agenttypes.MessageDone); ok {
					lastContextWindow = done.ContextWindow
					turnTokenUsage = done.TokenUsage
				}
			} else if update.Type == agenttypes.EventTypeThoughtChunk ||
				update.Type == agenttypes.EventTypeToolCall ||
				update.Type == agenttypes.EventTypeToolUpdate ||
				update.Type == agenttypes.EventTypeTodoUpdate ||
				update.Type == agenttypes.EventTypePlanUpdate ||
				update.Type == agenttypes.EventTypeCompact {
				lastResponseUpdateType = string(update.Type)
			}
			if watcher != nil {
				watcher.MarkSessionActive(current.Key)
			}
			if in.OnUpdate != nil {
				in.OnUpdate(clientUpdate)
			}
		})
	}
	sendWithAttachedUpdates := func(runtime agenttypes.Session, content string) error {
		attachSessionUpdates(runtime)
		finishUse := agentPool.BeginSessionUse(agentPoolSessionKey(current.Key, in.Agent))
		defer finishUse()
		return runtime.SendMessage(turnCtx, content)
	}
	sendErr := sendWithAttachedUpdates(sess, prompt)
	if sendErr != nil && !isCanceledTurnError(sendErr) {
		if isNonRecoverableAgentError(sendErr) {
			log.Printf("[session] turn.send.non_recoverable root=%s session=%s agent=%s action=fail_without_recovery err=%v", in.RootID, current.Key, in.Agent, sendErr)
			cancelRuntimeAfterNonRecoverableError(sess, agentPool, in.Agent, sendErr)
		} else if !sawAssistantChunk {
			log.Printf("[session] turn.send.no_response root=%s session=%s agent=%s action=fail_without_recovery", in.RootID, current.Key, in.Agent)
		} else {
			if in.OnUpdate != nil {
				in.OnUpdate(agenttypes.Event{
					Type: agenttypes.EventTypeRecovery,
					Data: agenttypes.RecoveryStatus{Message: "遇到错误，重试中..."},
				})
			}
			recoveredSess, recoveredErr := s.recoverAgentTurn(turnCtx, SendRecoveryInput{
				RootID:             in.RootID,
				SessionKey:         current.Key,
				Manager:            manager,
				Current:            current,
				AgentName:          in.Agent,
				Model:              in.Model,
				Mode:               in.Mode,
				Effort:             in.Effort,
				FastService:        in.FastService,
				PlanMode:           planMode,
				RootAbs:            rootAbs,
				CurrentSession:     sess,
				Prompt:             prompt,
				SawAssistantChunk:  sawAssistantChunk,
				SendWithAttachment: sendWithAttachedUpdates,
			})
			if recoveredErr != nil {
				sendErr = recoveredErr
			} else {
				sess = recoveredSess
				sendErr = nil
			}
		}
	}
	flushThought()
	if sendErr != nil && !isCanceledTurnError(sendErr) {
		log.Printf("[session] turn.send.error root=%s session=%s agent=%s err=%v", in.RootID, current.Key, in.Agent, sendErr)
	}
	resolvedModel := resolveRuntimeModel(current, sess, in.Model)
	resolvedEffort := resolveRuntimeEffort(in.Agent, current, in.Effort)
	resolvedFastService = resolveRuntimeFastService(in.Agent, current, in.FastService)
	if prefs := s.Registry.GetPreferences(); prefs != nil {
		if changed, err := prefs.UpdateAgentDefaultsIfChanged(in.Agent, resolvedModel, resolvedEffort, resolvedFastService); err != nil {
			log.Printf("[preferences] agent_defaults.update.error agent=%s err=%v", strings.TrimSpace(in.Agent), err)
		} else if changed {
			log.Printf("[preferences] agent_defaults.update.done agent=%s model=%q effort=%q fast_service=%q", strings.TrimSpace(in.Agent), resolvedModel, resolvedEffort, resolvedFastService)
			if in.OnAgentDefaultsChanged != nil {
				in.OnAgentDefaultsChanged(strings.TrimSpace(in.Agent))
			}
		}
	}
	modelDisplayName := s.resolveExchangeModelDisplayName(in.Agent, resolvedModel, rootAbs)
	exchangeCtx := session.WithExchangeModelDisplayName(ctx, modelDisplayName)
	exchangeCtx = session.WithExchangeSource(exchangeCtx, session.ExchangeSourceLive)
	// 助手行复用本轮 user 行 mint 的 mindfs_id（方案 §3.3「mindfs_id 落盘即定」）：
	// 两行必须同身份，块流才能把正文挂回轮次。
	exchangeCtx = session.WithExchangeIdentity(exchangeCtx, session.ExchangeIdentity{
		MindFSID: turnID,
		TurnSeq:  userExchangeSeq,
		// agent_id = 本轮最后一个 agent 原生消息 id。它是助手行在 agent 侧的桥梁键，
		// 导入器重放同一条消息时读回的就是它 —— 两个写入者由此对上（方案 S2c/D3）。
		// ACP 有 messageId；claude 侧的转录 uuid 由 handleAssistantMessage 补读后
		// 经 MessageChunk 带回（见 claude/session.go）。
		AgentID: turnAgentMessageID,
	})
	agentExchangeCtx := session.WithExchangeTokenUsage(exchangeCtx, turnTokenUsage)
	if strings.TrimSpace(current.Model) != resolvedModel {
		if err := manager.UpdateModel(ctx, current, resolvedModel); err != nil {
			return err
		}
	}
	// 用户行已在回合开始前落盘（persistUserTurnExchange），这里只补助手行。
	// S2d-2：主日志不要再带正文（块流是唯一载体，见下面 persistTurnBlocks）。
	agentSeq, err := manager.AddExchangeForAgentAndGetSeq(agentExchangeCtx, current, "agent", "", in.Agent, resolvedMode, resolvedEffort, resolvedFastService)
	if err != nil {
		log.Printf("[session] persist.agent.error root=%s session=%s agent=%s err=%v", in.RootID, current.Key, in.Agent, err)
		return err
	}
	if agentSeq <= 0 {
		return errors.New("agent exchange was not appended")
	}
	if err := manager.UpdateLastContextWindow(ctx, current, lastContextWindow); err != nil {
		return err
	}
	persistedAux := dedupeExchangeAuxBuffer(auxBuffer)
	for i := range persistedAux {
		persistedAux[i].Seq = agentSeq
		persistedAux[i] = hydratePendingToolCallAux(ctx, manager, current.Key, persistedAux[i])
		if err := manager.AddExchangeAux(ctx, current.Key, persistedAux[i]); err != nil {
			return err
		}
	}
	// 块流是正文的**唯一**载体（D1，S2d-2）。顺序仍是先主日志再块：块要 mindfs_id，
	// 而它随 AddExchangeForAgent 才定下来。失败**必须**返回 err —— 主日志那一行现在
	// 只剩元数据，块丢了就等于这一轮没有正文，而用户看得出来。
	if err := persistTurnBlocks(ctx, manager, current.Key, agentSeq, turnID, responseText, persistedAux); err != nil {
		return fmt.Errorf("persist turn blocks: %w", err)
	}
	// AgentCtxSeq 记的是「agent 已经看到多少行会话历史」，口径是**已落盘的总行数**
	// （与 external_sessions.go:415 的 len(latest.Exchanges)、
	// 子会话的 contextLineCount(child.Exchanges) 一致）。此刻 user 行（persistUserTurnExchange）
	// 与助手行（AddExchangeForAgent）都已落盘，所以是 linesBeforeThisTurn+2。
	// 早先只记 linesBeforeThisTurn，两行都漏：下一轮 prependSwitchHint 的
	// total=linesBeforeThisTurn、last=上一轮记的值，delta 恒为 2 > 0，切 agent 提示
	// 于是在每个正常回合都被重复注入（日志实测 12 小时 140 次），而不是只在真切换时出现。
	if err := manager.UpdateAgentState(ctx, current, in.Agent, linesBeforeThisTurn+2, sess.SessionID()); err != nil {
		return err
	}

	prober := s.Registry.GetProber()
	if sendErr != nil && !isCanceledTurnError(sendErr) {
		if prober != nil {
			prober.ReportRuntimeFailure(in.Agent, sendErr)
		}
		return sendErr
	} else if prober != nil {
		prober.ReportSuccess(in.Agent)
	}
	return nil
}
