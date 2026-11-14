package api

import (
	"context"
	"errors"
	"strings"

	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/kanban"
	"mindfs/server/internal/session"
)

// RunDirectActivity 是纯对话模式（workflow_choice=disabled）的一轮：把用户
// 这句话作为普通回合发到任务的主会话，返回 Agent 的回复正文。
//
// Router 只负责这一轮的生命周期；回复内容归会话本身，不经过工作流节点。
func (s *AppContext) RunDirectActivity(ctx context.Context, task kanban.Task, body string) (string, error) {
	if strings.TrimSpace(task.ID) == "" || strings.TrimSpace(task.RootID) == "" {
		return "", errors.New("task required")
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return "", errors.New("message body required")
	}
	key, err := s.ensureDirectSession(ctx, task)
	if err != nil {
		return "", err
	}
	req := workflowTurnRequest{
		RootID:          task.RootID,
		RuntimeRootPath: strings.TrimSpace(task.WorktreePath),
		SessionKey:      key,
		SessionName:     s.sessionTitle(task.RootID, key),
		Prompt:          body,
	}
	return s.runWorkflowTurn(ctx, req)
}

// ensureDirectSession 复用任务主会话；没有（或已失效）就建一个普通 chat 会话
// 并记回任务。会话建不出来时这一轮显式失败，绝不静默吞掉用户的留言。
func (s *AppContext) ensureDirectSession(ctx context.Context, task kanban.Task) (string, error) {
	uc := &usecase.Service{Registry: s}
	key := strings.TrimSpace(task.MainSessionKey)
	if key != "" {
		if existing, _, err := uc.GetSession(ctx, usecase.GetSessionInput{RootID: task.RootID, Key: key}); err == nil && existing != nil {
			return key, nil
		}
	}
	name := strings.TrimSpace(task.Name)
	if name == "" {
		name = kanban.TaskSessionName("", task.TaskNumber)
	}
	created, err := uc.CreateSession(ctx, usecase.CreateSessionInput{
		RootID: task.RootID,
		Input: session.CreateInput{
			Type:   session.TypeChat,
			Name:   name,
			TaskID: task.ID,
		},
	})
	if err != nil {
		return "", err
	}
	s.BroadcastSessionMetaUpdated(task.RootID, created)
	s.BroadcastSessionCreated(task.RootID, created)
	if err := s.Kanban.SetTaskMainSessionKey(ctx, task.RootID, task.ID, created.Key); err != nil {
		return "", err
	}
	return created.Key, nil
}