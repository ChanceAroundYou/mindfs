package api

import (
	"context"
	"errors"
	"strings"

	"mindfs/server/internal/agent"
	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/kanban"
	"mindfs/server/internal/session"
)

// errNoDirectAgent 是「一个可用 Agent 都没有」的显式失败。它必须在这里出现：
// SendMessage 收到空 agent 只会在绑定层抛 "agent required"，那是实现细节，
// 用户看不出该怎么办。
var errNoDirectAgent = errors.New("没有可用的 Agent，请先在「Agent 配置」里安装并启用一个")

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
	key, agentName, err := s.ensureDirectSession(ctx, task)
	if err != nil {
		return "", err
	}
	req := workflowTurnRequest{
		RootID:          task.RootID,
		RuntimeRootPath: strings.TrimSpace(task.WorktreePath),
		SessionKey:      key,
		SessionName:     s.sessionTitle(task.RootID, key),
		Agent:           agentName,
		Model:           s.taskModelFor(agentName),
		Prompt:          body,
	}
	return s.runWorkflowTurn(ctx, req)
}

// pickAgent selects the Agent for a direct task turn. An explicit preference
// wins whenever that Agent is actually usable (available or at least
// installed); this is what keeps task dispatch on the configured Agent instead
// of letting agents.json order decide. Without a usable preference it falls
// back to the historical heuristic (first available, else first installed,
// else first configured), so a machine without the preferred Agent keeps
// working. Callers pass GetAllStatuses, which keeps agents.json order for
// configured agents, so ties are deterministic.
func pickAgent(statuses []agent.Status, preferred string) string {
	preferred = strings.TrimSpace(preferred)
	if preferred != "" {
		for _, status := range statuses {
			if strings.TrimSpace(status.Name) != preferred {
				continue
			}
			// Available 是探针刚确认过；Installed 单独也算，因为启动/探针
			// 间隙里 Available 还是 false —— 那段窗口正是任务最容易派发
			// 到列表里第一个可用 Agent 的时候。
			if status.Available || status.Installed {
				return preferred
			}
			break
		}
	}
	var installed string
	var first string
	for _, status := range statuses {
		name := strings.TrimSpace(status.Name)
		if name == "" {
			continue
		}
		if first == "" {
			first = name
		}
		if status.Available {
			return name
		}
		if installed == "" && status.Installed {
			installed = name
		}
	}
	if installed != "" {
		return installed
	}
	return first
}

func (s *AppContext) defaultAgent() string {
	prober := s.GetProber()
	if prober == nil {
		return ""
	}
	return pickAgent(prober.GetAllStatuses(), s.TaskAgent)
}

// taskModelFor 只给「与任务默认 Agent 一致」的会话套默认模型；别的 Agent 的
// 会话（例如修复前建好的 codex 主会话）拿自己的模型，否则会把 claude 的模型名
// 发到不认它的 Agent 上，发送边界直接失败。
func (s *AppContext) taskModelFor(agentName string) string {
	agentName = strings.TrimSpace(agentName)
	if agentName == "" || agentName != strings.TrimSpace(s.TaskAgent) {
		return ""
	}
	return strings.TrimSpace(s.TaskModel)
}

// ensureDirectSession 复用任务主会话；没有（或已失效）就建一个普通 chat 会话
// 并记回任务。返回这一轮要用的 Agent：已有会话沿用会话自己的 Agent（换 Agent
// 会丢上下文连续性），新会话才按默认值选。
//
// 会话建不出来时这一轮显式失败，绝不静默吞掉用户的留言。
func (s *AppContext) ensureDirectSession(ctx context.Context, task kanban.Task) (string, string, error) {
	uc := &usecase.Service{Registry: s}
	key := strings.TrimSpace(task.MainSessionKey)
	if key != "" {
		if existing, _, err := uc.GetSession(ctx, usecase.GetSessionInput{RootID: task.RootID, Key: key}); err == nil && existing != nil {
			agentName := strings.TrimSpace(session.InferAgentFromSession(existing))
			if agentName == "" {
				// 旧任务的主会话可能是这次修复前建的（没有 Agent 记录）；
				// 补一个默认值，而不是让它永远发不出去。
				agentName = s.defaultAgent()
			}
			if agentName == "" {
				return "", "", errNoDirectAgent
			}
			return key, agentName, nil
		}
	}
	agentName := s.defaultAgent()
	if agentName == "" {
		return "", "", errNoDirectAgent
	}
	name := strings.TrimSpace(task.Name)
	if name == "" {
		name = kanban.TaskSessionName("", task.TaskNumber)
	}
	created, err := uc.CreateSession(ctx, usecase.CreateSessionInput{
		RootID: task.RootID,
		Input: session.CreateInput{
			Type:   session.TypeChat,
			Agent:  agentName,
			Model:  s.taskModelFor(agentName),
			Name:   name,
			TaskID: task.ID,
		},
	})
	if err != nil {
		return "", "", err
	}
	s.BroadcastSessionMetaUpdated(task.RootID, created)
	s.BroadcastSessionCreated(task.RootID, created)
	if err := s.Kanban.SetTaskMainSessionKey(ctx, task.RootID, task.ID, created.Key); err != nil {
		return "", "", err
	}
	return created.Key, agentName, nil
}
