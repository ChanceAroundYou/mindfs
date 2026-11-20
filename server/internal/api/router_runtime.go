package api

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// routerToolBindings 把「本轮的工具实现」和「注册给 CLI 的工具句柄」分开。
//
// Router 会话在 agent pool 里长期存活（key `router-<taskID>`）：CLI 只在
// OpenSession 时注册一次进程内 MCP 工具，之后每轮复用同一个会话。如果把本轮
// routerToolEnv（collector + brief）直接闭包进注册的 handler，第二轮起的工具
// 调用仍然写进第一轮的 collector —— 本轮 collector 永远为空，synthesizeActions
// 只能从最终文本合成一条 say，deliver/run_workflow 从未被校验、从未被应用。
// 线上症状就是「转交后没有任何动作」，且决策里的 seq 跨轮累加（0；1,2；3,4,5）。
//
// 因此注册给 CLI 的 handler 只认工具名，调用时现查本轮的实现。
type routerToolBindings struct {
	mu       sync.RWMutex
	handlers map[string]func(ctx context.Context, args json.RawMessage) (string, error)
	specs    []kanban.RouterToolSpec
}

func newRouterToolBindings() *routerToolBindings {
	return &routerToolBindings{handlers: make(map[string]func(ctx context.Context, args json.RawMessage) (string, error))}
}

// Set 用本轮的工具集替换绑定。只换实现，不换注册给 CLI 的句柄。
func (b *routerToolBindings) Set(specs []kanban.RouterToolSpec) {
	if b == nil {
		return
	}
	handlers := make(map[string]func(ctx context.Context, args json.RawMessage) (string, error), len(specs))
	current := make([]kanban.RouterToolSpec, 0, len(specs))
	for _, spec := range specs {
		if spec.Handler == nil {
			continue
		}
		handlers[spec.Name] = spec.Handler
		current = append(current, spec)
	}
	b.mu.Lock()
	b.handlers = handlers
	b.specs = current
	b.mu.Unlock()
}

// Specs 返回本轮的元数据加上按名字解析的句柄。第一次 Set 之后交给
// OpenSession 的句柄可以一直复用：名称相同时它自动落到当前轮。
func (b *routerToolBindings) Specs() []kanban.RouterToolSpec {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	current := b.specs
	b.mu.RUnlock()
	out := make([]kanban.RouterToolSpec, 0, len(current))
	for _, spec := range current {
		name := spec.Name
		spec.Handler = func(ctx context.Context, args json.RawMessage) (string, error) {
			return b.call(name, ctx, args)
		}
		out = append(out, spec)
	}
	return out
}

func (b *routerToolBindings) call(name string, ctx context.Context, args json.RawMessage) (string, error) {
	if b == nil {
		return "", errors.New("router tool bindings are not configured")
	}
	b.mu.RLock()
	handler := b.handlers[name]
	b.mu.RUnlock()
	if handler == nil {
		return "", errors.New("router tool " + name + " is not available in this turn")
	}
	return handler(ctx, args)
}

// routerToolBindings 按任务缓存在 AppContext 上：pool 里的 Router 会话按
// `router-<taskID>` 复用，同一任务的句柄天然共享同一份绑定表；不同任务并行
// 各有一份，互不影响。root 一起进 key，避免不同项目的同名任务串表。
func (s *AppContext) routerToolBindings(rootID, taskID string) *routerToolBindings {
	key := routerToolBindingsKey(rootID, taskID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.routerTools == nil {
		s.routerTools = make(map[string]*routerToolBindings)
	}
	bindings := s.routerTools[key]
	if bindings == nil {
		bindings = newRouterToolBindings()
		s.routerTools[key] = bindings
	}
	return bindings
}

func routerToolBindingsKey(rootID, taskID string) string {
	return strings.TrimSpace(rootID) + "::" + routerSessionKey(taskID)
}

// dropRouterToolBindings 在 Router runtime 关闭时摘掉这张表，避免按任务无限增长。
func (s *AppContext) dropRouterToolBindings(rootID, sessionKey string) {
	rootID = strings.TrimSpace(rootID)
	sessionKey = strings.TrimSpace(sessionKey)
	if rootID == "" || sessionKey == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.routerTools, rootID+"::"+sessionKey)
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
	readOnly := req.ReadOnly
	var (
		key string
		err error
	)
	if readOnly {
		// 只读复核回合有自己的会话与工作目录：它要看任务工作区，而 Router 的
		// cwd 刻意是一个空目录（模型绕开策略也读不到业务代码）。两者共用会话
		// 会把 Router 的 brief 与复核的只读上下文搅在一起。
		key, err = s.ensureVerifierSession(ctx, rootID, taskID, req.SessionKey, agentName, req.Model)
	} else {
		key, err = s.ensureRouterSession(ctx, rootID, taskID, agentName, req.Model)
	}
	if err != nil {
		return kanban.RouterAgentResult{}, err
	}
	configDir, workDir, err := s.routerDirs(taskID)
	if err != nil {
		return kanban.RouterAgentResult{}, err
	}
	if readOnly {
		if cwd := strings.TrimSpace(req.Cwd); cwd != "" {
			if err := os.MkdirAll(cwd, 0o700); err != nil {
				return kanban.RouterAgentResult{}, err
			}
			workDir = cwd
		}
	}
	policy := routerAgentToolPolicy(req.ToolPolicy)
	var tools []agenttypes.ToolSpec
	if readOnly {
		// 复核只拿 Read/Grep/Glob：它要判断「做没做完」，不能顺手改代码。
		policy = routerVerifierToolPolicy()
	} else {
		// Router 会话跨轮复用，工具句柄只在首次 OpenSession 注册。先把本轮实现装进
		// 绑定表，再从中取句柄：新回合（含复用会话）的调用落到本轮 handler。
		bindings := s.routerToolBindings(rootID, taskID)
		bindings.Set(req.Tools)
		tools = routerAgentToolSpecs(bindings.Specs())
	}

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
	// runtime 关了，注册给 CLI 的句柄也随之作废；下一轮会重新 Open 并重新注册。
	s.dropRouterToolBindings(rootID, sessionKey)
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

// ensureVerifierSession 建（或复用）只读复核会话。key 由 kanban 层给：它同时是
// 结论复用的身份（同 Run、同缺口只复核一次），换 key 就等于换一件事。
func (s *AppContext) ensureVerifierSession(ctx context.Context, rootID, taskID, sessionKey, agentName, model string) (string, error) {
	key := strings.TrimSpace(sessionKey)
	if key == "" {
		return "", errors.New("verifier turn requires a session key")
	}
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
			Name:   "复核",
		},
	})
	if err != nil {
		if existing, _, getErr := uc.GetSession(ctx, usecase.GetSessionInput{RootID: rootID, Key: key}); getErr == nil && existing != nil {
			return key, nil
		}
		return "", err
	}
	return key, nil
}

// routerVerifierToolPolicy 是只读复核回合的工具集：Read/Grep/Glob，其余一律拒。
// 这一点只有 Claude 适配器能强制，所以复核回合只支持 Claude。
func routerVerifierToolPolicy() *agenttypes.ToolPolicy {
	return &agenttypes.ToolPolicy{
		AllowedTools:  []string{"Read", "Grep", "Glob"},
		DenyByDefault: true,
	}
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
