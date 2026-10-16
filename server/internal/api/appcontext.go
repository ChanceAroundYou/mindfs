package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"mindfs/server/internal/agent"
	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/auth"
	"mindfs/server/internal/fs"
	"mindfs/server/internal/githubimport"
	"mindfs/server/internal/kanban"
	"mindfs/server/internal/nodes"
	"mindfs/server/internal/notifyscript"
	"mindfs/server/internal/pins"
	"mindfs/server/internal/preferences"
	"mindfs/server/internal/scheduled"
	"mindfs/server/internal/session"
	"mindfs/server/internal/update"
	"mindfs/server/internal/webpush"
)

type AppContext struct {
	Dirs   *fs.Registry
	Agents *agent.Pool
	Prober *agent.Prober
	Update *update.Service
	GitHub *githubimport.Service
	Auth   *auth.Store
	// AccountDir 是本账户私有的配置目录（registry/preferences/nodes/prompts/看板模板）。
	// 主账户为空串时，各 store 会落回进程默认目录。
	AccountDir string
	WebPush    *webpush.Service
	Notify     *notifyscript.Service
	Prefs      *preferences.Store
	Nodes      *nodes.Store
	// Pins 是本账户的置顶表（项目置顶 + 会话置顶），**每账户一份**，与 Prefs 相反。
	//
	// 置顶必须按账户分开：你的置顶不该出现在别人的会话栏里。项目置顶原本住在
	// 共享偏好里，那正是它跨账户泄漏的原因。
	// 权威只在主节点 —— 运行节点上这份是空的，控制面端点一律 403。
	Pins      *pins.Store
	Scheduled *scheduled.Service
	Kanban    *kanban.Service

	mu                       sync.RWMutex
	sessionWorktreeMu        sync.Mutex
	roots                    map[string]*RootContext // root id -> root context
	fileChangeListeners      []func(fs.FileChangeEvent)
	fileChangeBatchListeners []func(fs.FileChangeBatchEvent)
	relatedFileListeners     []func(fs.RelatedFileEvent)
	streamHub                *StreamHub
	candidateRegistry        *usecase.CandidateRegistry
	externalImporters        map[string]agenttypes.ExternalSessionImporter
}

func (s *AppContext) EnsureAgentSession(ctx context.Context, exec kanban.AgentStageExecution) (string, error) {
	if strings.TrimSpace(exec.RootID) == "" {
		return "", errors.New("root_id required")
	}
	uc := &usecase.Service{Registry: s}
	reusable := func(key string) bool {
		if strings.TrimSpace(key) == "" {
			return false
		}
		existing, _, err := uc.GetSession(ctx, usecase.GetSessionInput{RootID: exec.RootID, Key: key})
		return err == nil && existing != nil
	}
	if reusable(exec.Run.SessionKey) && exec.Stage.SessionReusePolicy != kanban.SessionReuseAlwaysNew {
		return strings.TrimSpace(exec.Run.SessionKey), nil
	}
	switch strings.TrimSpace(exec.Stage.SessionReusePolicy) {
	case kanban.SessionReuseTaskMain, "":
		if reusable(exec.Task.MainSessionKey) {
			return strings.TrimSpace(exec.Task.MainSessionKey), nil
		}
	case kanban.SessionReuseSameStage:
		if reusable(exec.Run.SessionKey) {
			return strings.TrimSpace(exec.Run.SessionKey), nil
		}
	}
	// 任务名优先：任务名在建会话之前就定了，而「任务改名 → 同步会话名」只在
	// 会话已经存在时才跑得到（bindTaskSessionNames），首段建会话这步是唯一的兜底，
	// 不兜底会话就一直挂着模板名。任务没名字才退回模板名 / #编号 / prompt。
	// #编号由 kanban.TaskSessionName 统一拼，和改名那条路径共用一处派生。
	name := strings.TrimSpace(exec.Task.Name)
	if name == "" {
		name = strings.TrimSpace(exec.Task.TaskTemplateName)
	}
	name = kanban.TaskSessionName(name, exec.Task.TaskNumber)
	if strings.TrimSpace(name) == "" {
		name = usecase.BuildFallbackSessionName(exec.Prompt)
	}
	created, err := uc.CreateSession(ctx, usecase.CreateSessionInput{
		RootID: exec.RootID,
		Input: session.CreateInput{
			Type:     session.TypeChat,
			Agent:    exec.Stage.Agent,
			Model:    exec.Stage.Model,
			PlanMode: exec.Stage.PlanMode,
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

func (s *AppContext) RunAgentStage(ctx context.Context, exec kanban.AgentStageExecution) (kanban.StageResult, error) {
	if strings.TrimSpace(exec.RootID) == "" {
		return kanban.StageResult{}, errors.New("root_id required")
	}
	if strings.TrimSpace(exec.Run.SessionKey) == "" {
		return kanban.StageResult{}, errors.New("session_key required")
	}
	if strings.TrimSpace(exec.Prompt) == "" {
		return kanban.StageResult{}, errors.New("agent prompt required")
	}
	uc := &usecase.Service{Registry: s}
	sessionKey := strings.TrimSpace(exec.Run.SessionKey)
	sessionName := s.sessionTitle(exec.RootID, sessionKey)
	updateTracker := newTurnUpdateTracker()
	planMode := exec.Stage.PlanMode
	userTimestamp := time.Now().UTC()
	// 本轮 assistant 正文：只用主线的 message_chunk（子代理的带 ParentToolUseID/TaskID，
	// 不能算进主线）。kanban 要靠它匹配阶段完成标记。
	var assistantText strings.Builder
	// 本轮创建的子会话 key：父轮收尾时要把「没等到 MessageDone」的子会话一并终结，
	// 否则它们置的 pending 会永久残留（症状「完成回答但还显示正在思考」）。
	var subSessionKeys []string
	var subMu sync.Mutex
	// 回合身份：主会话与各子会话的 mindfs_id 都记下来，收尾时原样交给终结器。
	// 与 ws.go / scheduled/tasks.go 同一条纪律（G-BE）—— 缺了它，done 以无身份走
	// 「保守清」，把此后新起一轮的灯误抹（症状「灯不亮」）。
	var turnMu sync.Mutex
	turnIDs := map[string]string{}
	// 终结器用 defer 交出必达性：成功 / 出错 / 提前 return / panic 都走到。
	defer func() {
		turnMu.Lock()
		mainID := turnIDs[sessionKey]
		subs := append([]string(nil), subSessionKeys...)
		subIDs := make(map[string]string, len(subs))
		for _, k := range subs {
			subIDs[k] = turnIDs[k]
		}
		turnMu.Unlock()
		s.EndSessionTurn(exec.RootID, sessionKey, "", mainID)
		for _, subKey := range subs {
			s.EndSessionTurn(exec.RootID, subKey, "", subIDs[subKey])
		}
	}()
	err := uc.SendMessage(ctx, usecase.SendMessageInput{
		RootID:          exec.RootID,
		RuntimeRootPath: exec.RuntimeRootPath,
		Key:             sessionKey,
		Agent:           exec.Stage.Agent,
		Model:           exec.Stage.Model,
		Mode:            exec.Stage.Mode,
		Effort:          exec.Stage.Effort,
		FastService:     normalizeFastServiceValue(exec.Stage.FastService),
		PlanMode:        &planMode,
		Content:         exec.Prompt,
		UserTimestamp:   userTimestamp,
		OnStart: func(start usecase.MessageStart) {
			id := s.BroadcastSessionUserMessageAt(exec.RootID, sessionKey, session.TypeChat, sessionName, exec.Stage.Agent, start.Model, start.ModelDisplayName, start.Mode, start.Effort, start.FastService, planMode, exec.Prompt, userTimestamp, start.UserExchangeSeq, start.MindFSID)
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
			s.BroadcastSessionUpdate(exec.RootID, sessionKey, update)
		},
		OnAgentDefaultsChanged: func(agentName string) {
			s.BroadcastAgentStatusChanged(agentName)
		},
		OnSubSessionCreated: func(created *session.Session) {
			s.BroadcastSessionMetaUpdated(exec.RootID, created)
		},
		// 每条 attach 路径都发一枚 turn id。置 pending 并登记进 drain map —— 认领分支
		// 此前不走 OnCreated，那条子会话没有任何代码持有它的回合，defer drain 扫不到、
		// close() 也不终结它，pending 永久残留（线上 1792108831 的症状）。
		OnSubAgentAttached: func(child *session.Session, turnID string) {
			if child == nil || turnID == "" {
				return
			}
			id := s.SetSessionPendingReply(exec.RootID, child.Key, child.Name, turnID)
			turnMu.Lock()
			turnIDs[child.Key] = id
			subMu.Lock()
			subSessionKeys = append(subSessionKeys, child.Key)
			subMu.Unlock()
			turnMu.Unlock()
		},
		// close() 自己终结自己那一轮（正常 done / 取消 / FinishAll 都汇聚到这里），
		// 因此漏掉 defer 快照之后才注册进来的子会话也不会永久残留。
		OnSubAgentTurnFinished: func(sessionKey, id string) {
			if sessionKey == "" || id == "" {
				return
			}
			s.EndSessionTurn(exec.RootID, sessionKey, "", id)
		},
		OnSubSessionUpdate: func(sessionKey string, update agenttypes.Event) {
			updateTracker.Begin()
			defer updateTracker.End()
			s.BroadcastSessionUpdate(exec.RootID, sessionKey, update)
			if update.Type == agenttypes.EventTypeMessageDone {
				turnMu.Lock()
				id := turnIDs[sessionKey]
				turnMu.Unlock()
				s.EndSessionTurn(exec.RootID, sessionKey, "", id)
			}
		},
	})
	if err != nil {
		s.BroadcastSessionError(exec.RootID, sessionKey, err.Error())
	}
	if ok := updateTracker.WaitIdle(ctx, sessionDoneSettleWindow, sessionDoneMaxWait); !ok {
		log.Printf("[kanban] session.done.wait_timeout root=%s session=%s task=%s", exec.RootID, sessionKey, exec.Task.ID)
	}
	if err != nil {
		return kanban.StageResult{}, err
	}
	// error == nil 只说明消息投递成功，不代表活干完了。判完成与否看 agent 有没有
	// 显式回报（见 kanban.MatchStageOutcome）。收尾广播由上面的 defer 终结器负责。
	return kanban.MatchStageOutcome(assistantText.String(), exec.Task.CurrentStageIndex), nil
}

func (s *AppContext) TaskUpdated(rootID string, detail kanban.TaskDetail) {
	s.GetSessionStreamHub().BroadcastAll(WSResponse{
		Type: "task.updated",
		Payload: map[string]any{
			"root_id": rootID,
			"task":    detail.Task,
			"detail":  detail,
		},
	})
}

func (s *AppContext) ReleaseFileWatcher(rootID, sessionKey string) {
	rootCtx, err := s.GetRootContext(rootID)
	if err != nil {
		return
	}

	s.mu.Lock()
	watcher := rootCtx.Watcher
	s.mu.Unlock()
	if watcher == nil {
		return
	}

	watcher.UnregisterSession(sessionKey)
	if watcher.SessionCount() > 0 {
		return
	}

	s.mu.Lock()
	if rootCtx.Watcher == watcher {
		rootCtx.Watcher = nil
	}
	s.mu.Unlock()
	watcher.Close()
}

// StopTaskExecution requests cancellation without releasing the scheduler slot.
// The kanban runner releases it only after SendMessage returns.
func (s *AppContext) StopTaskExecution(ctx context.Context, task kanban.Task) error {
	if task.MainSessionKey == "" {
		return nil
	}
	uc := &usecase.Service{Registry: s}
	return uc.CancelSessionTurn(ctx, usecase.CancelSessionTurnInput{RootID: task.RootID, Key: task.MainSessionKey})
}

func (s *AppContext) ValidateTaskAgent(stage kanban.StageTemplate) error {
	if s.GetProber() == nil {
		return nil
	}
	for _, status := range s.GetProber().GetConfiguredStatuses() {
		if status.Name == stage.Agent {
			return nil
		}
	}
	return fmt.Errorf("unknown agent %q; query available agents first", stage.Agent)
}

func (s *AppContext) TaskDeleted(root, id string) {
	s.GetSessionStreamHub().BroadcastAll(WSResponse{Type: "task.deleted", Payload: map[string]any{"root_id": root, "task_id": id}})
}
