package api

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/session"

	"github.com/gorilla/websocket"
)

type sessionMessageJob struct {
	RootID          string
	RuntimeRootPath string
	Key             string
	RequestID       string
	SessionType     string
	SessionName     string
	Shell           string
	TerminalCols    int
	User            PendingUserMessage
	ClientCtx       usecase.ClientContext
	ExcludeClientID string
	Queued          bool
}

func (h *WSHandler) handleSessionMessage(ctx context.Context, conn *websocket.Conn, clientID string, req WSRequest) {
	rootID := getString(req.Payload, "root_id")
	key := getString(req.Payload, "session_key")
	requestID := strings.TrimSpace(req.ID)
	content := getString(req.Payload, "content")
	planRequested, strippedContent := parsePlanMessage(content)
	if planRequested {
		content = strippedContent
	}
	planMode := planRequested
	sessionType := getString(req.Payload, "type")
	agentName := getString(req.Payload, "agent")
	model := getString(req.Payload, "model")
	agentMode := getString(req.Payload, "agent_mode")
	effort := getString(req.Payload, "effort")
	fastService := normalizeFastServiceValue(getString(req.Payload, "fast_service"))
	shell := getString(req.Payload, "shell")
	terminalCols := getInt(req.Payload, "terminal_cols")
	createWorktree := getBool(req.Payload, "create_worktree")
	worktreeBranchMode := getString(req.Payload, "worktree_branch_mode")
	worktreeBranch := getString(req.Payload, "worktree_branch")
	if content == "" || sessionType == "" || (agentName == "" && sessionType != session.TypeCommand) {
		h.sendWSError(conn, clientID, req.ID, "invalid_request", "content, type and agent required")
		return
	}

	uc := &usecase.Service{Registry: h.AppContext}
	streamHub := h.AppContext.GetSessionStreamHub()
	userTimestamp := time.Now().UTC()
	if requestID != "" {
		reservedAt, reserved := h.reserveClientRequest(requestID)
		userTimestamp = reservedAt
		if !reserved {
			h.sendWSAcceptedAt(conn, clientID, requestID, rootID, key, userTimestamp, "")
			return
		}
	}
	sessionName := ""
	runtimeRootPath := ""
	if key == "" {
		sessionName = usecase.BuildFallbackSessionName(content)
		sessionSource := ""
		if createWorktree && sessionType != session.TypeCommand {
			sessionSource = "worktree"
		}
		created, err := uc.CreateSession(ctx, usecase.CreateSessionInput{
			RootID: rootID,
			Input: session.CreateInput{
				Type:     sessionType,
				Source:   sessionSource,
				Agent:    agentName,
				Model:    model,
				Shell:    shell,
				PlanMode: planRequested,
				Name:     sessionName,
			},
		})
		if err != nil {
			h.sendWSError(conn, clientID, req.ID, "session.create_failed", err.Error())
			return
		}
		key = created.Key
		if createWorktree && sessionType != session.TypeCommand {
			wt, worktreeErr := h.AppContext.CreateSessionWorktree(ctx, rootID, worktreeBranchMode, worktreeBranch)
			if worktreeErr != nil {
				if manager, managerErr := h.AppContext.GetSessionManager(rootID); managerErr == nil {
					_ = manager.Delete(ctx, created.Key)
				}
				h.sendWSError(conn, clientID, req.ID, "session.create_failed", worktreeErr.Error())
				return
			}
			runtimeRootPath = wt.Path
			if manager, managerErr := h.AppContext.GetSessionManager(rootID); managerErr == nil {
				branch := worktreeBranch
				head := ""
				if root, rootErr := h.AppContext.GetRoot(rootID); rootErr == nil {
					if match, ok := resolveRelatedWorktree(ctx, root, wt.Path); ok {
						branch = match.Branch
						head = match.Head
					}
				}
				if _, recordErr := manager.RecordRelatedWorktree(ctx, created.Key, rootID, wt.Path, branch, head); recordErr != nil {
					h.sendWSError(conn, clientID, req.ID, "session.create_failed", recordErr.Error())
					return
				}
				if updated, getErr := manager.Get(ctx, created.Key, 0); getErr == nil && updated != nil {
					created = updated
				}
			}
		}
		h.broadcastSessionMetaUpdated(rootID, created)
		if sessionType != session.TypeCommand {
			go func(rootID, sessionKey, agentName, firstMessage string) {
				updated, err := uc.SuggestSessionName(context.Background(), usecase.SuggestSessionNameInput{
					RootID:       rootID,
					SessionKey:   sessionKey,
					Agent:        agentName,
					FirstMessage: firstMessage,
				})
				if err != nil {
					log.Printf("[session-name] async.error root=%s session=%s agent=%s err=%v", rootID, sessionKey, agentName, err)
					return
				}
				if updated == nil {
					return
				}
				if h.AppContext == nil {
					return
				}
				log.Printf("[session-name] async.broadcast root=%s session=%s name=%q", rootID, sessionKey, updated.Name)
				h.broadcastSessionMetaUpdated(rootID, updated)
			}(rootID, key, agentName, content)
		}
	} else if current, _, err := uc.GetSession(ctx, usecase.GetSessionInput{RootID: rootID, Key: key}); err == nil && current != nil {
		sessionName = current.Name
		planMode = current.PlanMode
		runtimeRootPath = sessionRuntimeRootPath(current)
		if planRequested && !current.PlanMode {
			if manager, managerErr := h.AppContext.GetSessionManager(rootID); managerErr == nil {
				if updateErr := manager.UpdatePlanMode(ctx, current, true); updateErr != nil {
					h.sendWSError(conn, clientID, req.ID, "session.plan_mode_failed", updateErr.Error())
					return
				}
				h.switchSessionRuntimePlanMode(ctx, key, current, true)
				updated, getErr := manager.Get(ctx, key, 0)
				if getErr == nil && updated != nil {
					current = updated
					planMode = updated.PlanMode
					h.broadcastSessionMetaUpdated(rootID, updated)
				}
			} else {
				h.sendWSError(conn, clientID, req.ID, "session.plan_mode_failed", managerErr.Error())
				return
			}
		}
	}
	modelDisplayName := ""
	if uc != nil {
		modelDisplayName = uc.ResolveModelDisplayName(agentName, model, runtimeRootPath)
	}
	if requestID != "" {
		h.sendWSAcceptedAt(conn, clientID, requestID, rootID, key, userTimestamp, modelDisplayName)
	}
	if h.AppContext != nil {
		streamHub.BindSessionClient(key, clientID)
	}
	clientCtx := parseClientContext(req.Payload, rootID)
	userMessage := PendingUserMessage{
		Agent:            agentName,
		Model:            model,
		ModelDisplayName: modelDisplayName,
		Mode:             agentMode,
		Effort:           effort,
		FastService:      fastService,
		PlanMode:         planMode,
		Content:          content,
		Timestamp:        userTimestamp,
	}
	job := sessionMessageJob{
		RootID:          rootID,
		RuntimeRootPath: runtimeRootPath,
		Key:             key,
		RequestID:       requestID,
		SessionType:     sessionType,
		SessionName:     sessionName,
		Shell:           shell,
		TerminalCols:    terminalCols,
		User:            userMessage,
		ClientCtx:       clientCtx,
		// 有意不排除发送方：session.user_message 现在携带用户消息的持久化 seq，
		// 发送方需要它把本地乐观条目（无 seq）收敛为已持久化条目，否则该条目会
		// 以「瞬时项」身份与窗口取回的同一条消息重复渲染。客户端按 role+content
		// 合并，收到自己的回显是幂等的。
		ExcludeClientID: "",
	}
	h.submitSessionMessage(job)
}

// Both transports use the same queue and execution lifecycle. The HTTP request
// context must not cancel the agent turn when the accepted response is sent.
func (h *WSHandler) submitSessionMessage(job sessionMessageJob) bool {
	streamHub := h.AppContext.GetSessionStreamHub()
	if job.SessionType != session.TypeCommand {
		queue, queued := streamHub.reserveOrQueueSessionMessage(job.RootID, job.Key, job.SessionName, QueuedUserMessage{
			ID: job.RequestID, PendingUserMessage: job.User, ClientCtx: job.ClientCtx,
		})
		if queued {
			streamHub.BroadcastSessionQueueUpdated(job.RootID, job.Key, queue)
			return true
		}
	}
	if queue, changed := streamHub.UnfreezeQueuedSessionMessages(job.Key); changed {
		streamHub.BroadcastSessionQueueUpdated(job.RootID, job.Key, queue)
	}
	go h.runSessionMessage(job)
	return false
}

func (h *WSHandler) runSessionMessage(job sessionMessageJob) {
	rootID := job.RootID
	key := job.Key
	requestID := strings.TrimSpace(job.RequestID)
	uc := &usecase.Service{Registry: h.AppContext}
	streamHub := h.AppContext.GetSessionStreamHub()
	msgCtx, cancel := h.sessionMessageContext()
	defer cancel()
	updateTracker := newTurnUpdateTracker()
	subTrackers := map[string]*turnUpdateTracker{}
	var subTrackersMu sync.Mutex
	subTrackerFor := func(sessionKey string) *turnUpdateTracker {
		subTrackersMu.Lock()
		defer subTrackersMu.Unlock()
		tracker := subTrackers[sessionKey]
		if tracker == nil {
			tracker = newTurnUpdateTracker()
			subTrackers[sessionKey] = tracker
		}
		return tracker
	}

	// 回合身份与终结器。
	// turnID：本轮的 pending 回合身份（OnStart 置 pending 时拿到），结束时按它比对清理。
	// subIDs：本轮创建的子会话各自的回合身份，父轮收尾时一并终结。
	// defer 保证成功 / 出错 / 提前 return / panic 都走到终结器 —— 这是「灯不灭」的根治点
	// （原先只有函数尾一处显式广播，panic 或 SendMessage 挂死就永久残留）。
	var turnID string
	subIDs := map[string]string{}
	var subIDsMu sync.Mutex
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[ws] session.message.panic root=%s session=%s request=%s panic=%v", rootID, key, requestID, r)
		}
		if ok := updateTracker.WaitIdle(msgCtx, sessionDoneSettleWindow, sessionDoneMaxWait); !ok {
			log.Printf("[ws] session.done.wait_timeout root=%s session=%s request=%s", rootID, key, requestID)
		}
		log.Printf("[ws] session.done root=%s session=%s request=%s", rootID, key, requestID)
		h.AppContext.EndSessionTurn(rootID, key, requestID, turnID)
		subIDsMu.Lock()
		subs := make(map[string]string, len(subIDs))
		for k, v := range subIDs {
			subs[k] = v
		}
		subIDsMu.Unlock()
		for subKey, id := range subs {
			h.AppContext.EndSessionTurn(rootID, subKey, "", id)
		}
		// 取队列必须排在收尾**之后**：EndSessionTurn → ClearSessionPending 才把 pending
		// 的 Active 置回 false，而 PopQueuedSessionMessage 的闸门正是「Active 就不许再起
		// 一轮」。原先这一步写在函数尾，defer 要等 return 才跑，于是取队列时 Active 仍是
		// true —— 排队消息永远取不出来（症状：回答结束后排队消息不动，得手动点「立即发送」）。
		h.startNextQueuedSessionMessage(rootID, key)
	}()

	err := uc.SendMessage(msgCtx, usecase.SendMessageInput{
		RootID:          rootID,
		RuntimeRootPath: job.RuntimeRootPath,
		Key:             key,
		Agent:           job.User.Agent,
		Model:           job.User.Model,
		Mode:            job.User.Mode,
		Effort:          job.User.Effort,
		FastService:     job.User.FastService,
		PlanMode:        &job.User.PlanMode,
		Shell:           job.Shell,
		TerminalCols:    job.TerminalCols,
		Content:         job.User.Content,
		UserTimestamp:   job.User.Timestamp,
		ClientCtx:       job.ClientCtx,
		OnStart: func(start usecase.MessageStart) {
			h.AppContext.ClearTaskAuxFlagsForSession(rootID, key)
			turnID = streamHub.BroadcastSessionUserMessageAt(rootID, key, job.SessionType, job.SessionName, job.User.Agent, start.Model, start.ModelDisplayName, start.Mode, start.Effort, start.FastService, job.User.PlanMode, job.User.Content, job.User.Timestamp, start.UserExchangeSeq, start.MindFSID, job.ExcludeClientID, job.Queued)
		},
		OnUpdate: func(update agenttypes.Event) {
			updateTracker.Begin()
			defer updateTracker.End()
			if updateToEvent(update) == nil {
				return
			}
			h.AppContext.BroadcastSessionUpdate(rootID, key, update)
		},
		OnAgentDefaultsChanged: func(agentName string) {
			h.AppContext.BroadcastAgentStatusChanged(agentName)
		},
		OnSubSessionCreated: func(created *session.Session) {
			h.broadcastSessionMetaUpdated(rootID, created)
		},
		// 每条 attach 路径（新建 + 认领已有 binding）都发一枚 turn id。这里置 pending
		// 并登记进 subIDs —— 认领分支此前不走 OnCreated，那条子会话因此没有任何代码
		// 持有它的回合，defer drain 扫不到、close() 也不终结它，pending 永久残留。
		OnSubAgentAttached: func(child *session.Session, turnID string) {
			if child == nil || turnID == "" {
				return
			}
			h.broadcastSessionMetaUpdated(rootID, child)
			id := streamHub.SetPendingReply(rootID, child.Key, child.Name, turnID)
			subIDsMu.Lock()
			subIDs[child.Key] = id
			subIDsMu.Unlock()
		},
		// close() 自己终结自己那一轮（正常 done / 取消 / FinishAll 都汇聚到这里），
		// 因此漏掉 defer 快照之后才注册进来的子会话也不会永久残留。
		OnSubAgentTurnFinished: func(sessionKey, id string) {
			if sessionKey == "" || id == "" {
				return
			}
			h.AppContext.EndSessionTurn(rootID, sessionKey, "", id)
		},
		OnSubSessionUpdate: func(sessionKey string, update agenttypes.Event) {
			tracker := subTrackerFor(sessionKey)
			tracker.Begin()
			if updateToEvent(update) == nil {
				tracker.End()
				return
			}
			h.AppContext.BroadcastSessionUpdate(rootID, sessionKey, update)
			if update.Type == agenttypes.EventTypeMessageDone {
				tracker.End()
				if ok := tracker.WaitIdle(msgCtx, sessionDoneSettleWindow, sessionDoneMaxWait); !ok {
					log.Printf("[ws] sub-session.done.wait_timeout root=%s session=%s", rootID, sessionKey)
				}
				subIDsMu.Lock()
				id := subIDs[sessionKey]
				subIDsMu.Unlock()
				h.AppContext.EndSessionTurn(rootID, sessionKey, "", id)
				return
			}
			tracker.End()
		},
	})
	if err != nil {
		log.Printf("[ws] session.message.error root=%s session=%s request=%s err=%v", rootID, key, requestID, err)
		h.AppContext.BroadcastSessionError(rootID, key, err.Error())
	}
}

func (h *WSHandler) handleSessionReady(clientID string, req WSRequest) {
	if h.AppContext == nil {
		return
	}
	rootID := getString(req.Payload, "root_id")
	key := getString(req.Payload, "session_key")
	if rootID == "" || key == "" {
		return
	}
	streamHub := h.AppContext.GetSessionStreamHub()
	streamHub.BindSessionClient(key, clientID)
	// 不接受 event_cursor：挂载会话一律是「快照重建」，不是「按序号续传」。
	// 客户端不需要、也不该告诉服务端它见过多少 —— 见 ReplayPending。
	streamHub.ReplayPending(rootID, clientID, key)
}

