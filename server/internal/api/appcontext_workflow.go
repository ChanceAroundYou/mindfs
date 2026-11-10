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
	default: // per_node
		// A persisted NodeRun session means this is a resume/re-claim, not a
		// fresh attempt; continue the conversation it already started.
		if reusable(exec.NodeRun.SessionKey) {
			return strings.TrimSpace(exec.NodeRun.SessionKey), nil
		}
	}
	profile := kanban.WorkflowNodeAgentProfileOf(exec.Node)
	name := kanban.TaskSessionName(strings.TrimSpace(exec.Task.Name), exec.Task.TaskNumber)
	if name == "" {
		name = strings.TrimSpace(exec.Task.TaskTemplateName)
	}
	if name == "" {
		name = strings.TrimSpace(exec.Node.Name)
	}
	if name == "" {
		name = strings.TrimSpace(exec.Node.ID)
	}
	if name == "" {
		name = usecase.BuildFallbackSessionName(exec.Prompt)
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

// RunWorkflowNode runs the node's turn and parses D5's strict JSON envelope.
//
// A transport error (send failed) is returned as error. A completed turn whose
// answer is not a valid envelope returns Outcome=Unknown with a reason and nil
// error: D5 requires the NodeRun to become waiting_user, not fail, and the
// driver must never guess an outcome from prose.
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
		Prompt:          exec.Prompt + kanban.BuildWorkflowNodeOutputContract(exec.Node.ID),
	}
	text, err := s.runWorkflowTurn(ctx, req)
	finished := time.Now().UTC()
	result := kanban.WorkflowNodeResult{
		Outcome:    kanban.WorkflowNodeUnknown,
		SessionKey: req.SessionKey,
		StartedAt:  started,
		FinishedAt: finished,
	}
	if err != nil {
		result.Reason = err.Error()
		return result, err
	}
	parsed, perr := kanban.ParseWorkflowNodeEnvelope(text)
	if perr != nil {
		result.Reason = perr.Error()
		return result, nil
	}
	parsed.SessionKey = req.SessionKey
	parsed.StartedAt = started
	parsed.FinishedAt = finished
	return parsed, nil
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

	var assistantText strings.Builder
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
					assistantText.WriteString(chunk.Content)
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
	return assistantText.String(), nil
}
