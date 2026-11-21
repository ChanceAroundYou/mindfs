package api

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/kanban"
	"mindfs/server/internal/session"
)

// workflowTurnRequest is one prompt turn for a workflow Agent node. It is the
// test seam boundary: production fills it from the node profile and a real
// session, tests capture it and answer with a fake assistant text.
type workflowTurnRequest struct {
	RootID          string
	RuntimeRootPath string
	SessionKey      string
	SessionName     string
	Agent           string
	Model           string
	Mode            string
	Effort          string
	FastService     string
	PlanMode        bool
	Prompt          string
	// OnToolCall 让调用方观察本轮流式上报的工具调用（含状态变化）。结果转换
	// 用它数写操作，核对「声称完成」是否有事实支撑；可为 nil。
	OnToolCall func(agenttypes.ToolCall)
}

// EnsureWorkflowNodeSession resolves D2 session reuse for one workflow node.
// It never synthesizes a StageRun; the session is the execution medium only.
func (s *AppContext) EnsureWorkflowNodeSession(ctx context.Context, exec kanban.WorkflowNodeExecution) (string, error) {
	if strings.TrimSpace(exec.RootID) == "" {
		return "", errors.New("root_id required")
	}
	if strings.TrimSpace(exec.Task.ID) == "" {
		return "", errors.New("task id required")
	}
	uc := &usecase.Service{Registry: s}
	reusable := func(key string) bool {
		key = strings.TrimSpace(key)
		if key == "" {
			return false
		}
		existing, _, err := uc.GetSession(ctx, usecase.GetSessionInput{RootID: exec.RootID, Key: key})
		return err == nil && existing != nil
	}
	policy := strings.TrimSpace(exec.SessionPolicy)
	if policy == "" {
		policy = kanban.WorkflowNodeSessionPolicy(exec.Node)
	}
	switch policy {
	case kanban.SessionReuseTaskMain:
		// D2: task_main is the only policy that deliberately joins the task's
		// main conversation; everything else must stay node-scoped.
		if reusable(exec.Task.MainSessionKey) {
			return strings.TrimSpace(exec.Task.MainSessionKey), nil
		}
	case kanban.SessionReuseSameStage:
		if reusable(exec.ReuseSessionKey) {
			return strings.TrimSpace(exec.ReuseSessionKey), nil
		}
		if reusable(exec.NodeRun.SessionKey) {
			return strings.TrimSpace(exec.NodeRun.SessionKey), nil
		}
	case kanban.SessionReuseAlwaysNew:
		// Explicitly never reuse.
	case kanban.SessionReuseRun:
		// 2026-11-20 起的默认：一轮 Run 一条会话。Run 已绑定的会话优先；没有
		// 时回落到这个 NodeRun 自己已绑定的会话 —— 升级前创建的 Run 没有共享
		// 会话，恢复中的节点必须继续它原来那段对话，而不是被切到新对话。
		if reusable(exec.RunSessionKey) {
			return strings.TrimSpace(exec.RunSessionKey), nil
		}
		if reusable(exec.NodeRun.SessionKey) {
			return strings.TrimSpace(exec.NodeRun.SessionKey), nil
		}
	default: // per_node
		// A persisted NodeRun session means this is a resume/re-claim, not a
		// fresh attempt; continue the conversation it already started.
		if reusable(exec.NodeRun.SessionKey) {
			return strings.TrimSpace(exec.NodeRun.SessionKey), nil
		}
	}
	profile := kanban.WorkflowNodeAgentProfileOf(exec.Node)
	base := kanban.TaskSessionName(strings.TrimSpace(exec.Task.Name), exec.Task.TaskNumber)
	if base == "" {
		base = strings.TrimSpace(exec.Task.TaskTemplateName)
	}
	if base == "" {
		base = strings.TrimSpace(exec.Node.Name)
	}
	if base == "" {
		base = strings.TrimSpace(exec.Node.ID)
	}
	if base == "" {
		base = usecase.BuildFallbackSessionName(exec.Prompt)
	}
	// 同一条任务每轮都会新建 worker 会话，名字只取任务名+编号会让它们在会话列表
	// 里完全一样。把节点名、重试/循环标记与触发这轮的用户留言拼进后缀，轮次之间
	// 才分得开。
	name := kanban.WorkflowNodeSessionName(base, exec.Node.Name, exec.UserInput, exec.NodeRun)
	if policy == kanban.SessionReuseRun {
		// 共享会话不属于某个节点：名字里不出现节点名，只带这轮留言的片段。
		name = kanban.WorkflowRunSessionName(base, exec.UserInput)
	}
	created, err := uc.CreateSession(ctx, usecase.CreateSessionInput{
		RootID: exec.RootID,
		Input: session.CreateInput{
			Type:     session.TypeChat,
			Agent:    profile.Agent,
			Model:    profile.Model,
			PlanMode: profile.PlanMode,
			Name:     name,
			TaskID:   exec.Task.ID,
		},
	})
	if err != nil {
		return "", err
	}
	s.BroadcastSessionMetaUpdated(exec.RootID, created)
	s.BroadcastSessionCreated(exec.RootID, created)
	return created.Key, nil
}

// RunWorkflowNode runs the node's turn and hands its natural-language reply to
// the Router's result normalizer.
//
// The worker is not asked to emit a machine-readable envelope. A transport
// error (send failed) is returned as error. A completed turn is normalized to
// outcome/summary by the Router layer; an empty reply becomes silent so the
// driver parks as waiting_user instead of guessing success.
func (s *AppContext) RunWorkflowNode(ctx context.Context, exec kanban.WorkflowNodeExecution) (kanban.WorkflowNodeResult, error) {
	if strings.TrimSpace(exec.RootID) == "" {
		return kanban.WorkflowNodeResult{}, errors.New("root_id required")
	}
	if strings.TrimSpace(exec.NodeRun.SessionKey) == "" {
		return kanban.WorkflowNodeResult{}, errors.New("session_key required")
	}
	if strings.TrimSpace(exec.Prompt) == "" {
		return kanban.WorkflowNodeResult{}, errors.New("agent prompt required")
	}
	profile := kanban.WorkflowNodeAgentProfileOf(exec.Node)
	started := time.Now().UTC()
	req := workflowTurnRequest{
		RootID:          exec.RootID,
		RuntimeRootPath: exec.RuntimeRootPath,
		SessionKey:      strings.TrimSpace(exec.NodeRun.SessionKey),
		SessionName:     s.sessionTitle(exec.RootID, exec.NodeRun.SessionKey),
		Agent:           profile.Agent,
		Model:           profile.Model,
		Mode:            profile.Mode,
		Effort:          profile.Effort,
		FastService:     normalizeFastServiceValue(profile.FastService),
		PlanMode:        profile.PlanMode,
		Prompt:          exec.Prompt,
	}
	// 写操作证据只从本轮流式工具事件里数（不看转录正文）：修复轮产出的 done
	// 要靠它核对「说做完了」与「真写过」是否一致。
	var writes workflowWriteCounter
	req.OnToolCall = writes.observe
	text, err := s.runWorkflowTurn(ctx, req)
	finished := time.Now().UTC()
	if err != nil {
		return kanban.WorkflowNodeResult{
			Outcome:    kanban.WorkflowNodeUnknown,
			SessionKey: req.SessionKey,
			Reason:     err.Error(),
			StartedAt:  started,
			FinishedAt: finished,
		}, err
	}
	result := s.normalizeWorkflowNodeOutput(ctx, exec, text, writes.writes())
	result.SessionKey = req.SessionKey
	result.StartedAt = started
	result.FinishedAt = finished
	return result, nil
}

// SendWorkflowComment delivers a queued user comment as a normal continuation
// turn. Unlike RunWorkflowNode it does not normalize the assistant text into a
// workflow result: the user is talking to the session, not asking the node for
// a work outcome.
func (s *AppContext) SendWorkflowComment(ctx context.Context, exec kanban.WorkflowNodeExecution, message string) error {
	if strings.TrimSpace(exec.RootID) == "" {
		return errors.New("root_id required")
	}
	if strings.TrimSpace(exec.NodeRun.SessionKey) == "" {
		return errors.New("session_key required")
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return errors.New("comment message required")
	}
	// The comment is a continuation turn in the node's session, so it must run
	// with the same agent profile as the node. Omitting it makes the turn layer
	// reject the send as "agent required" and the comment silently degrades into
	// a delivery-failed line.
	profile := kanban.WorkflowNodeAgentProfileOf(exec.Node)
	req := workflowTurnRequest{
		RootID:          exec.RootID,
		RuntimeRootPath: exec.RuntimeRootPath,
		SessionKey:      strings.TrimSpace(exec.NodeRun.SessionKey),
		SessionName:     s.sessionTitle(exec.RootID, exec.NodeRun.SessionKey),
		Agent:           profile.Agent,
		Model:           profile.Model,
		Mode:            profile.Mode,
		Effort:          profile.Effort,
		FastService:     normalizeFastServiceValue(profile.FastService),
		PlanMode:        profile.PlanMode,
		Prompt:          message,
	}
	_, err := s.runWorkflowTurn(ctx, req)
	return err
}

// CancelWorkflowNode is the bounded-cancel seam used by CancelWorkflowRun. A
// NodeRun that never bound a session has not sent anything, so there is no turn
// to cancel; every other case asks the session layer to cancel the active turn.
// The workflow driver owns the timeout; this adapter must not block forever.
func (s *AppContext) CancelWorkflowNode(ctx context.Context, rootID string, nodeRun kanban.NodeRun) error {
	key := strings.TrimSpace(nodeRun.SessionKey)
	if key == "" {
		return nil
	}
	uc := &usecase.Service{Registry: s}
	return uc.CancelSessionTurn(ctx, usecase.CancelSessionTurnInput{
		RootID: strings.TrimSpace(rootID),
		Key:    key,
	})
}

func (s *AppContext) runWorkflowTurn(ctx context.Context, req workflowTurnRequest) (string, error) {
	if s.workflowTurnOverride != nil {
		return s.workflowTurnOverride(ctx, req)
	}
	return s.executeWorkflowTurn(ctx, req)
}

// executeWorkflowTurn mirrors the turn lifecycle in RunAgentStage (same
// pending/turn-identity discipline) but has no legacy stage marker matching:
// workflow outcomes come from the JSON envelope, not stage tags.
func (s *AppContext) executeWorkflowTurn(ctx context.Context, req workflowTurnRequest) (string, error) {
	uc := &usecase.Service{Registry: s}
	sessionKey := req.SessionKey
	sessionName := req.SessionName
	updateTracker := newTurnUpdateTracker()
	userTimestamp := time.Now().UTC()
	planMode := req.PlanMode

	var assistantText, assistantTextLastType string
	var subSessionKeys []string
	var subMu sync.Mutex
	var turnMu sync.Mutex
	turnIDs := map[string]string{}

	defer func() {
		turnMu.Lock()
		mainID := turnIDs[sessionKey]
		subs := append([]string(nil), subSessionKeys...)
		subIDs := make(map[string]string, len(subs))
		for _, k := range subs {
			subIDs[k] = turnIDs[k]
		}
		turnMu.Unlock()
		s.EndSessionTurn(req.RootID, sessionKey, "", mainID)
		for _, subKey := range subs {
			s.EndSessionTurn(req.RootID, subKey, "", subIDs[subKey])
		}
	}()

	err := uc.SendMessage(ctx, usecase.SendMessageInput{
		RootID:          req.RootID,
		RuntimeRootPath: req.RuntimeRootPath,
		Key:             sessionKey,
		Agent:           req.Agent,
		Model:           req.Model,
		Mode:            req.Mode,
		Effort:          req.Effort,
		FastService:     req.FastService,
		PlanMode:        &planMode,
		Content:         req.Prompt,
		UserTimestamp:   userTimestamp,
		OnStart: func(start usecase.MessageStart) {
			id := s.BroadcastSessionUserMessageAt(req.RootID, sessionKey, session.TypeChat, sessionName, req.Agent, start.Model, start.ModelDisplayName, start.Mode, start.Effort, start.FastService, planMode, req.Prompt, userTimestamp, start.UserExchangeSeq, start.MindFSID)
			turnMu.Lock()
			turnIDs[sessionKey] = id
			turnMu.Unlock()
		},
		OnUpdate: func(update agenttypes.Event) {
			updateTracker.Begin()
			defer updateTracker.End()
			if update.Type == agenttypes.EventTypeMessageChunk {
				if chunk, ok := update.Data.(agenttypes.MessageChunk); ok &&
					strings.TrimSpace(chunk.ParentToolUseID) == "" && strings.TrimSpace(chunk.TaskID) == "" {
					assistantText, assistantTextLastType = appendTurnTextBlock(assistantText, assistantTextLastType, update.Type, chunk.Content)
				}
			} else {
				assistantText, assistantTextLastType = appendTurnTextBlock(assistantText, assistantTextLastType, update.Type, "")
			}
			if req.OnToolCall != nil &&
				(update.Type == agenttypes.EventTypeToolCall || update.Type == agenttypes.EventTypeToolUpdate) {
				if call, ok := update.Data.(agenttypes.ToolCall); ok {
					req.OnToolCall(call)
				}
			}
			s.BroadcastSessionUpdate(req.RootID, sessionKey, update)
		},
		OnAgentDefaultsChanged: func(agentName string) {
			s.BroadcastAgentStatusChanged(agentName)
		},
		OnSubSessionCreated: func(created *session.Session) {
			s.BroadcastSessionMetaUpdated(req.RootID, created)
		},
		OnSubAgentAttached: func(child *session.Session, turnID string) {
			if child == nil || turnID == "" {
				return
			}
			id := s.SetSessionPendingReply(req.RootID, child.Key, child.Name, turnID)
			turnMu.Lock()
			turnIDs[child.Key] = id
			subMu.Lock()
			subSessionKeys = append(subSessionKeys, child.Key)
			subMu.Unlock()
			turnMu.Unlock()
		},
		OnSubAgentTurnFinished: func(sessionKey, id string) {
			if sessionKey == "" || id == "" {
				return
			}
			s.EndSessionTurn(req.RootID, sessionKey, "", id)
		},
		OnSubSessionUpdate: func(sessionKey string, update agenttypes.Event) {
			updateTracker.Begin()
			defer updateTracker.End()
			s.BroadcastSessionUpdate(req.RootID, sessionKey, update)
			if update.Type == agenttypes.EventTypeMessageDone {
				turnMu.Lock()
				id := turnIDs[sessionKey]
				turnMu.Unlock()
				s.EndSessionTurn(req.RootID, sessionKey, "", id)
			}
		},
	})
	if err != nil {
		s.BroadcastSessionError(req.RootID, sessionKey, err.Error())
	}
	if ok := updateTracker.WaitIdle(ctx, sessionDoneSettleWindow, sessionDoneMaxWait); !ok {
		log.Printf("[workflow] session.done.wait_timeout root=%s session=%s node=%s", req.RootID, sessionKey, req.SessionName)
	}
	if err != nil {
		return "", err
	}
	return assistantText, nil
}
