package scheduled

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/session"
)

func (s *Service) store(rootID string) (*Store, error) {
	if s == nil || s.registry == nil {
		return nil, errors.New("scheduled service not configured")
	}
	root, err := s.registry.GetRoot(strings.TrimSpace(rootID))
	if err != nil {
		return nil, err
	}
	return NewStore(root), nil
}

func (s *Service) validateInput(in SaveInput) error {
	if strings.TrimSpace(in.RootID) == "" {
		return errors.New("root id required")
	}
	if strings.TrimSpace(in.Name) == "" {
		return errors.New("task name required")
	}
	if strings.TrimSpace(in.TaskCron) == "" {
		return errors.New("task crontab required")
	}
	if _, err := s.parser.Parse(strings.TrimSpace(in.TaskCron)); err != nil {
		return fmt.Errorf("invalid task crontab: %w", err)
	}
	if strings.TrimSpace(in.NewSessionCron) != "" {
		if _, err := s.parser.Parse(strings.TrimSpace(in.NewSessionCron)); err != nil {
			return fmt.Errorf("invalid new session crontab: %w", err)
		}
	}
	if strings.TrimSpace(in.Agent) == "" {
		return errors.New("agent required")
	}
	if strings.TrimSpace(in.Prompt) == "" {
		return errors.New("prompt required")
	}
	return nil
}

func (s *Service) findTask(rootID, id string) (Task, error) {
	store, err := s.store(rootID)
	if err != nil {
		return Task{}, err
	}
	tasks, err := store.List()
	if err != nil {
		return Task{}, err
	}
	id = strings.TrimSpace(id)
	for _, task := range tasks {
		if task.ID == id {
			return task, nil
		}
	}
	return Task{}, errors.New("task not found")
}

func (s *Service) runTask(ctx context.Context, task Task, force bool) error {
	runKey := task.RootID + ":" + task.ID
	s.mu.Lock()
	if s.running[runKey] {
		s.mu.Unlock()
		_ = s.updateTask(task.RootID, task.ID, func(t *Task) {
			now := time.Now().UTC()
			t.LastRunAt = &now
			t.LastError = "previous run still active"
			t.UpdatedAt = now
		})
		return errors.New("previous run still active")
	}
	s.running[runKey] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.running, runKey)
		s.mu.Unlock()
	}()

	current, err := s.findTask(task.RootID, task.ID)
	if err != nil {
		return err
	}
	if !force && !current.Enabled {
		return nil
	}
	broadcaster := s.broadcaster
	if broadcaster == nil {
		err := errors.New("session activity broadcaster not configured")
		_ = s.recordRunError(current, err)
		return err
	}
	manager, err := s.registry.GetSessionManager(current.RootID)
	if err != nil {
		_ = s.recordRunError(current, err)
		broadcaster.BroadcastScheduledTaskFailed(current.RootID, current.ID, current.Name, "", err.Error())
		return err
	}
	sessionKey := strings.TrimSpace(current.SessionKey)
	if sessionKey != "" {
		if _, err := manager.Get(ctx, sessionKey, 0); err != nil {
			sessionKey = ""
		}
	}
	if sessionKey == "" || s.shouldCreateNewSession(current, time.Now().UTC()) {
		created, err := s.usecase.CreateSession(ctx, usecase.CreateSessionInput{
			RootID: current.RootID,
			Input: session.CreateInput{
				Type:  session.TypeChat,
				Agent: current.Agent,
				Model: current.Model,
				Name:  current.Name,
			},
		})
		if err != nil {
			_ = s.recordRunError(current, err)
			broadcaster.BroadcastScheduledTaskFailed(current.RootID, current.ID, current.Name, "", err.Error())
			return err
		}
		broadcaster.BroadcastSessionMetaUpdated(current.RootID, created)
		broadcaster.BroadcastSessionCreated(current.RootID, created)
		sessionKey = created.Key
		resetAt := time.Now().UTC()
		current.SessionKey = sessionKey
		current.LastSessionResetAt = &resetAt
		if err := s.updateTask(current.RootID, current.ID, func(t *Task) {
			t.SessionKey = sessionKey
			t.LastSessionResetAt = &resetAt
			t.UpdatedAt = resetAt
		}); err != nil {
			return err
		}
	}
	sessionName := current.Name
	userTimestamp := time.Now().UTC()
	// 回合身份：主会话与各子会话的 pending mindfs_id 都记下来，收尾时原样交给终结器。
	// 没有它，done 会以无身份走「保守清」，把此后新起一轮的灯误抹（症状「灯不亮」）。
	var turnMu sync.Mutex
	turnIDs := map[string]string{}
	// 子会话 pending 的父轮收尾：本轮创建的子会话若没等到 MessageDone（出错/取消/
	// 父轮先结束），pending 会永久残留。与 ws.go 同一条纪律：用 defer 把一个终结器
	// 覆盖全部终态边（成功与失败两条 return 都走到）。
	var subSessionKeys []string
	defer func() {
		turnMu.Lock()
		subs := append([]string(nil), subSessionKeys...)
		ids := make(map[string]string, len(subs))
		for _, k := range subs {
			ids[k] = turnIDs[k]
		}
		turnMu.Unlock()
		for _, subKey := range subs {
			broadcaster.EndSessionTurn(current.RootID, subKey, "", ids[subKey])
		}
	}()
	err = s.usecase.SendMessage(ctx, usecase.SendMessageInput{
		RootID:        current.RootID,
		Key:           sessionKey,
		Agent:         current.Agent,
		Model:         current.Model,
		Mode:          current.Mode,
		Effort:        current.Effort,
		FastService:   current.FastService,
		Content:       current.Prompt,
		UserTimestamp: userTimestamp,
		ClientCtx: usecase.ClientContext{
			CurrentRoot: current.RootID,
		},
		OnStart: func(start usecase.MessageStart) {
			id := broadcaster.BroadcastSessionUserMessageAt(current.RootID, sessionKey, session.TypeChat, sessionName, current.Agent, start.Model, start.ModelDisplayName, start.Mode, start.Effort, start.FastService, false, current.Prompt, userTimestamp, start.UserExchangeSeq, start.MindFSID)
			turnMu.Lock()
			turnIDs[sessionKey] = id
			turnMu.Unlock()
		},
		OnUpdate: func(update agenttypes.Event) {
			broadcaster.BroadcastSessionUpdate(current.RootID, sessionKey, update)
		},
		OnAgentDefaultsChanged: func(agentName string) {
			broadcaster.BroadcastAgentStatusChanged(agentName)
		},
		OnSubSessionCreated: func(created *session.Session) {
			broadcaster.BroadcastSessionMetaUpdated(current.RootID, created)
			// 子会话同样是「新会话」：列表里要把它挂到父会话下面。发 created 才触发
			// 前端重拉，meta.updated 不会（前端 handler 只写已缓存条目）。
			broadcaster.BroadcastSessionCreated(current.RootID, created)
		},
		// 每条 attach 路径都发一枚 turn id。置 pending 并登记进 drain map —— 认领分支
		// 此前不走 OnCreated，那条子会话没有任何代码持有它的回合，defer drain 扫不到、
		// close() 也不终结它，pending 永久残留（线上 1792108831 的症状）。
		OnSubAgentAttached: func(child *session.Session, turnID string) {
			if child == nil || turnID == "" {
				return
			}
			broadcaster.BroadcastSessionMetaUpdated(current.RootID, child)
			broadcaster.BroadcastSessionCreated(current.RootID, child)
			id := broadcaster.SetSessionPendingReply(current.RootID, child.Key, child.Name, turnID)
			turnMu.Lock()
			turnIDs[child.Key] = id
			subSessionKeys = append(subSessionKeys, child.Key)
			turnMu.Unlock()
		},
		// close() 自己终结自己那一轮（正常 done / 取消 / FinishAll 都汇聚到这里），
		// 因此漏掉 defer 快照之后才注册进来的子会话也不会永久残留。
		OnSubAgentTurnFinished: func(sessionKey, id string) {
			if sessionKey == "" || id == "" {
				return
			}
			broadcaster.EndSessionTurn(current.RootID, sessionKey, "", id)
		},
		OnSubSessionUpdate: func(sessionKey string, update agenttypes.Event) {
			broadcaster.BroadcastSessionUpdate(current.RootID, sessionKey, update)
			if update.Type == agenttypes.EventTypeMessageDone {
				turnMu.Lock()
				id := turnIDs[sessionKey]
				turnMu.Unlock()
				broadcaster.EndSessionTurn(current.RootID, sessionKey, "", id)
			}
		},
	})
	now := time.Now().UTC()
	turnMu.Lock()
	mainID := turnIDs[sessionKey]
	turnMu.Unlock()
	if err != nil {
		broadcaster.BroadcastSessionError(current.RootID, sessionKey, err.Error())
		broadcaster.EndSessionTurn(current.RootID, sessionKey, "scheduled:"+current.ID, mainID)
		broadcaster.BroadcastScheduledTaskFailed(current.RootID, current.ID, current.Name, sessionKey, err.Error())
		_ = s.updateTask(current.RootID, current.ID, func(t *Task) {
			t.LastRunAt = &now
			t.LastError = err.Error()
			t.UpdatedAt = now
		})
		return err
	}
	broadcaster.EndSessionTurn(current.RootID, sessionKey, "scheduled:"+current.ID, mainID)
	broadcaster.BroadcastScheduledTaskDone(current.RootID, current.ID, current.Name, sessionKey, "")
	return s.updateTask(current.RootID, current.ID, func(t *Task) {
		t.SessionKey = sessionKey
		t.LastRunAt = &now
		t.LastSuccessAt = &now
		t.LastError = ""
		t.UpdatedAt = now
	})
}

func (s *Service) recordRunError(task Task, err error) error {
	now := time.Now().UTC()
	return s.updateTask(task.RootID, task.ID, func(t *Task) {
		t.LastRunAt = &now
		t.LastError = err.Error()
		t.UpdatedAt = now
	})
}

func (s *Service) updateTask(rootID, id string, update func(*Task)) error {
	store, err := s.store(rootID)
	if err != nil {
		return err
	}
	tasks, err := store.List()
	if err != nil {
		return err
	}
	for i := range tasks {
		if tasks[i].ID != id {
			continue
		}
		update(&tasks[i])
		return store.Save(tasks)
	}
	return errors.New("task not found")
}

func (s *Service) shouldCreateNewSession(task Task, now time.Time) bool {
	if strings.TrimSpace(task.NewSessionCron) == "" {
		return false
	}
	schedule, err := s.parser.Parse(strings.TrimSpace(task.NewSessionCron))
	if err != nil {
		return false
	}
	if task.LastSessionResetAt == nil || task.LastSessionResetAt.IsZero() {
		return true
	}
	next := schedule.Next(task.LastSessionResetAt.UTC())
	return !next.After(now)
}

func (s *Service) decorateTask(task *Task) {
	if task == nil {
		return
	}
	if schedule, err := s.parser.Parse(strings.TrimSpace(task.TaskCron)); err == nil {
		next := schedule.Next(time.Now())
		task.NextRunAt = &next
	}
	if strings.TrimSpace(task.NewSessionCron) != "" {
		if schedule, err := s.parser.Parse(strings.TrimSpace(task.NewSessionCron)); err == nil {
			base := time.Now()
			if task.LastSessionResetAt != nil && !task.LastSessionResetAt.IsZero() {
				base = task.LastSessionResetAt.UTC()
			}
			next := schedule.Next(base)
			task.NextNewSessionAt = &next
		}
	}
	s.mu.Lock()
	task.Running = s.running[task.RootID+":"+task.ID]
	s.mu.Unlock()
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
