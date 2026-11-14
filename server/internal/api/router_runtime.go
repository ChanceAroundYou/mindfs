package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/kanban"
	"mindfs/server/internal/session"
)

// Router 隐藏在侧栏之外（session.TypeRouter），但仍是一个真实的 Claude Code
// 会话：每任务一个，绑定固定 key "router-<taskID>"，可 resume、可查转录。
// 这里只负责「跑一轮」和「关 runtime」，决策记账在 kanban.RouterService。

const routerSessionKeyPrefix = "router-"

func routerSessionKey(taskID string) string {
	return routerSessionKeyPrefix + strings.TrimSpace(taskID)
}

// RunRouterTurn 在一个隔离的 Router 会话里跑一轮。rompt 是 Situation Brief
// 的 JSON；工具是进程内 MCP，权限由 deny-by-default policy 兜底。
func (s *AppContext) RunRouterTurn(ctx context.Context, req kanban.RouterAgentRequest) (kanban.RouterAgentResult, error) {
	rootID := strings.TrimSpace(req.RootID)
	taskID := strings.TrimSpace(req.TaskID)
	if rootID == "" || taskID == "" {
		return kanban.RouterAgentResult{}, errors.New("router turn requires root id and task id")
	}
	agentName := strings.TrimSpace(req.Agent)
	if agentName == "" {
		agentName = "claude"
	}
	key, err := s.ensureRouterSession(ctx, rootID, taskID, agentName, req.Model)
	if err != nil {
		return kanban.RouterAgentResult{}, err
	}
	configDir, workDir, err := s.routerDirs(taskID)
	if err != nil {
		return kanban.RouterAgentResult{}, err
	}
	tools := routerAgentToolSpecs(req.Tools)
	policy := routerAgentToolPolicy(req.ToolPolicy)

	var text strings.Builder
	var usage *agenttypes.TokenUsage
	uc := &usecase.Service{Registry: s}
	planMode := false
	err = uc.SendMessage(ctx, usecase.SendMessageInput{
		RootID:                rootID,
		RuntimeRootPath:       workDir,
		Key:                   key,
		Agent:                 agentName,
		Model:                 req.Model,
		Effort:                req.Effort,
		PlanMode:              &planMode,
		Content:               req.Prompt,
		UserTimestamp:         time.Now().UTC(),
		DeveloperInstructions: req.DeveloperInstructions,
		ConfigDir:             configDir,
		Tools:                 tools,
		ToolPolicy:            policy,
		OnUpdate: func(update agenttypes.Event) {
			switch update.Type {
			case agenttypes.EventTypeMessageChunk:
				if chunk, ok := update.Data.(agenttypes.MessageChunk); ok &&
					strings.TrimSpace(chunk.ParentToolUseID) == "" && strings.TrimSpace(chunk.TaskID) == "" {
					text.WriteString(chunk.Content)
				}
			case agenttypes.EventTypeMessageDone:
				if done, ok := update.Data.(agenttypes.MessageDone); ok && done.TokenUsage != nil {
					usage = done.TokenUsage
				}
			}
		},
	})
	if err != nil {
		return kanban.RouterAgentResult{}, err
	}
	result := kanban.RouterAgentResult{
		SessionKey: key,
		FinalText:  text.String(),
		ConfigDir:  configDir,
		Cwd:        workDir,
		Meta: kanban.RouterTurnMeta{
			Model:      req.Model,
			PromptHash: req.PromptHash,
			FinalText:  text.String(),
		},
	}
	if usage != nil {
		result.Meta.PromptTokens = usage.InputTokens
		result.Meta.CompletionTokens = usage.OutputTokens
	}
	return result, nil
}

// CloseRouterSession 只关进程内 runtime；manager 里的会话行保留，下一轮仍能
// resume 并读回历史。
func (s *AppContext) CloseRouterSession(ctx context.Context, rootID, sessionKey string) error {
	rootID = strings.TrimSpace(rootID)
	sessionKey = strings.TrimSpace(sessionKey)
	if rootID == "" || sessionKey == "" {
		return nil
	}
	uc := &usecase.Service{Registry: s}
	_, err := uc.CloseSession(ctx, usecase.CloseSessionInput{RootID: rootID, Key: sessionKey})
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "not found") {
		return nil
	}
	return err
}

func (s *AppContext) ensureRouterSession(ctx context.Context, rootID, taskID, agentName, model string) (string, error) {
	key := routerSessionKey(taskID)
	uc := &usecase.Service{Registry: s}
	if existing, _, err := uc.GetSession(ctx, usecase.GetSessionInput{RootID: rootID, Key: key}); err == nil && existing != nil {
		return key, nil
	}
	_, err := uc.CreateSession(ctx, usecase.CreateSessionInput{
		RootID: rootID,
		Input: session.CreateInput{
			Key:    key,
			Type:   session.TypeRouter,
			TaskID: taskID,
			Agent:  agentName,
			Model:  model,
			Name:   "调度",
		},
	})
	if err != nil {
		// 并发创建时另一条 lane 可能已经建好；再读一次，存在即复用。
		if existing, _, getErr := uc.GetSession(ctx, usecase.GetSessionInput{RootID: rootID, Key: key}); getErr == nil && existing != nil {
			return key, nil
		}
		return "", err
	}
	return key, nil
}

// routerDirs 给 Router 一个与用户 Claude 配置完全隔离的 config dir 和 cwd。
// cwd 里没有项目文件，即使模型绕过工具策略也读不到业务代码。
func (s *AppContext) routerDirs(taskID string) (configDir, workDir string, err error) {
	base := strings.TrimSpace(s.AccountDir)
	if base == "" {
		base = strings.TrimSpace(s.MetaRoot())
	}
	if base == "" {
		return "", "", errors.New("router requires an account config directory")
	}
	configDir = filepath.Join(base, "router", "claude")
	workDir = filepath.Join(base, "router", "tasks", strings.TrimSpace(taskID))
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		return "", "", err
	}
	return configDir, workDir, nil
}

func routerAgentToolSpecs(specs []kanban.RouterToolSpec) []agenttypes.ToolSpec {
	out := make([]agenttypes.ToolSpec, 0, len(specs))
	for _, spec := range specs {
		if spec.Handler == nil {
			continue
		}
		out = append(out, agenttypes.ToolSpec{
			Name:        spec.Name,
			Description: spec.Description,
			InputSchema: spec.InputSchema,
			Handler:     spec.Handler,
		})
	}
	return out
}

func routerAgentToolPolicy(policy kanban.RouterToolPolicy) *agenttypes.ToolPolicy {
	return &agenttypes.ToolPolicy{
		AllowedTools:    append([]string(nil), policy.AllowedTools...),
		DisallowedTools: append([]string(nil), policy.DisallowedTools...),
		DenyByDefault:   policy.DenyByDefault,
	}
}
