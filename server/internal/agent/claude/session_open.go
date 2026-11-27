package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"mindfs/server/internal/agent/logs"
	types "mindfs/server/internal/agent/types"

	claudeagent "github.com/roasbeef/claude-agent-sdk-go"
)

// ErrResumeTranscriptMissing 表示要 resume 的转录在本机已经不存在。
// 调用方据此走「开新会话并重绑」的回退，而不是把 CLI 直接交给 --resume 去撞死。
var ErrResumeTranscriptMissing = errors.New("claude resume transcript missing")

type OpenOptions struct {
	AgentName             string
	SessionKey            string
	Model                 string
	Effort                string
	PlanMode              bool
	RootPath              string
	Command               string
	Args                  []string
	Env                   map[string]string
	DeveloperInstructions string
	ResumeSessionID       string
	ForkSessionID         string
	ResumeMessageID       string
	// ConfigDir 隔离 CLI 的 settings/hooks/sessions（Router 专用）。
	ConfigDir string
	// Tools/ToolPolicy 只在 Router 会话里非空；普通会话保持 nil。
	Tools      []types.ToolSpec
	ToolPolicy *types.ToolPolicy
	// SettingsJSON 作为 `--settings` 传给 CLI（最高优先级的 settings 来源）。
	// 目前只用于把 ANTHROPIC_BASE_URL 指到 per-session 代理：用户 settings 的
	// env 会覆盖进程环境，不这样改道 CLI 根本不会经过代理。
	SettingsJSON string
}

type Runtime struct{}

func NewRuntime() *Runtime {
	return &Runtime{}
}

func (r *Runtime) OpenSession(ctx context.Context, opts OpenOptions) (types.Session, error) {
	if opts.SessionKey == "" {
		return nil, errors.New("session key required")
	}

	s := &session{
		sessionKey:      opts.SessionKey,
		model:           canonicalClaudeModel(opts.Model),
		planMode:        opts.PlanMode,
		agentDebugLog:   logs.NewAgentLogger(opts.RootPath, opts.SessionKey, opts.AgentName),
		questionWaits:   make(map[string]chan askUserAnswerResult),
		emittedAskUsers: make(map[string]struct{}),
		rootPath:        opts.RootPath,
		baseEnv:         cloneEnv(opts.Env),
		toolPolicy:      opts.ToolPolicy,
	}

	optionList := []claudeagent.Option{
		claudeagent.WithCwd(opts.RootPath),
		claudeagent.WithEnv(opts.Env),
		claudeagent.WithVerbose(true),
		claudeagent.WithIncludePartialMessages(true),
		claudeagent.WithAgentProgressSummaries(true),
		claudeagent.WithForwardSubagentText(true),
		claudeagent.WithCanUseTool(s.handleCanUseTool),
	}
	optionList = appendClaudeExtraArgs(optionList, opts.DeveloperInstructions, opts.SettingsJSON, builtinToolsOf(opts.ToolPolicy))
	if strings.TrimSpace(opts.ConfigDir) != "" {
		// Router 会话必须看不见用户的 ~/.claude（hooks/settings/会话转录）。
		// 没有 SettingSources 时，ConfigDir 决定 CLI 的配置根。
		optionList = append(optionList, claudeagent.WithConfigDir(strings.TrimSpace(opts.ConfigDir)))
	}
	if len(opts.Tools) > 0 {
		optionList = append(optionList, claudeRouterMcpOptions(opts.Tools)...)
	}
	if opts.ToolPolicy != nil && len(opts.ToolPolicy.DisallowedTools) > 0 {
		optionList = append(optionList, claudeagent.WithDisallowedTools(opts.ToolPolicy.DisallowedTools))
	}
	if strings.TrimSpace(opts.Command) != "" {
		optionList = append(optionList, claudeagent.WithCLIPath(opts.Command))
	}
	forkSessionID := strings.TrimSpace(opts.ForkSessionID)
	resumeMessageID := strings.TrimSpace(opts.ResumeMessageID)
	// 转录已被清掉时绝不能把 id 交给 --resume：CLI 会立即退出且不回答 initialize，
	// SDK 于是永久等待（事故形态）。这里提前判定，让调用方走「开新会话」回退。
	if forkSessionID == "" {
		if resumeID := strings.TrimSpace(opts.ResumeSessionID); resumeID != "" {
			if !hasTranscriptFor(resumeID, opts.ConfigDir) {
				return nil, fmt.Errorf("%w: %s", ErrResumeTranscriptMissing, resumeID)
			}
		}
	}
	explicitSessionID := ""
	if forkSessionID != "" && resumeMessageID != "" {
		forked, err := claudeagent.ForkSession(forkSessionID, &claudeagent.ForkSessionOptions{
			UpToMessageID: resumeMessageID,
		})
		if err != nil {
			return nil, fmt.Errorf("fork claude session at message: %w", err)
		}
		if forked == nil || strings.TrimSpace(forked.SessionID) == "" {
			return nil, errors.New("fork claude session did not return session id")
		}
		explicitSessionID = strings.TrimSpace(forked.SessionID)
		optionList = append(optionList, claudeagent.WithResume(explicitSessionID))
	} else if forkSessionID != "" {
		optionList = append(optionList, claudeagent.WithForkSession(forkSessionID))
	} else if strings.TrimSpace(opts.ResumeSessionID) != "" {
		optionList = append(optionList, claudeagent.WithResume(strings.TrimSpace(opts.ResumeSessionID)))
	}
	if resumeMessageID != "" && forkSessionID == "" {
		optionList = append(optionList, claudeagent.WithResumeSessionAt(resumeMessageID))
	}
	if strings.TrimSpace(opts.Model) != "" {
		optionList = append(optionList, claudeagent.WithModel(s.resolveClaudeModelArg(opts.Model)))
	}
	if strings.TrimSpace(opts.Effort) != "" {
		optionList = append(optionList, claudeagent.WithEffort(claudeagent.EffortLevel(strings.TrimSpace(opts.Effort))))
	}
	if opts.PlanMode {
		optionList = append(optionList, claudeagent.WithPermissionMode(claudeagent.PermissionModePlan))
	}

	client, err := claudeagent.NewClient(optionList...)
	if err != nil {
		return nil, err
	}
	stream, cancelStream, err := awaitInitialized(ctx, claudeInitTimeout,
		func(initCtx context.Context) (*claudeagent.Stream, error) {
			return client.Stream(initCtx)
		},
		func() { _ = client.Close() },
	)
	if err != nil {
		return nil, err
	}
	s.cancelStream = cancelStream

	selectedModel := canonicalClaudeModel(opts.Model)
	if selectedModel == "" && opts.ResumeSessionID == "" {
		if candidate, ok := claudeFirstAvailableModel(client); ok {
			selectedModel = canonicalClaudeModel(candidate)
		}
	}
	// WithModel already selects an explicitly requested model at CLI startup.
	// A control-plane SetModel here would repeat the same switch immediately and
	// force an unnecessary provider confirmation. Keep SetModel only for the
	// first-available fallback, where no model was passed to the CLI.
	if selectedModel != "" && strings.TrimSpace(opts.Model) == "" {
		if err := stream.SetModel(ctx, s.resolveClaudeModelArg(selectedModel)); err != nil {
			client.Close()
			return nil, err
		}
	}
	if opts.PlanMode {
		if err := stream.SetPermissionMode(ctx, claudeagent.PermissionModePlan); err != nil {
			client.Close()
			return nil, err
		}
	}

	s.client = client
	s.stream = stream
	s.sessionID = stream.SessionID()
	if strings.TrimSpace(s.sessionID) == "" {
		s.sessionID = explicitSessionID
	}
	s.model = selectedModel
	go s.consumeMessages()
	return s, nil
}

// builtinToolsOf 取出策略里的 `--tools` 值；nil 表示调用方没限制，保持 CLI 默认。
func builtinToolsOf(policy *types.ToolPolicy) *string {
	if policy == nil {
		return nil
	}
	return policy.BuiltinTools
}

// appendClaudeExtraArgs 汇总所有 `--flag value` 形态的参数。
//
// SDK 的 WithExtraArgs 是**赋值**不是合并，两处各自调用会互相覆盖；
// 所以这里一次建好 map 再交给 SDK。
func appendClaudeExtraArgs(options []claudeagent.Option, developerInstructions, settingsJSON string, builtinTools *string) []claudeagent.Option {
	extraArgs := map[string]*string{}
	if instructions := strings.TrimSpace(developerInstructions); instructions != "" {
		extraArgs["append-system-prompt"] = &instructions
	}
	if settings := strings.TrimSpace(settingsJSON); settings != "" {
		extraArgs["settings"] = &settings
	}
	if builtinTools != nil {
		// 空串是 CLI 约定的「关闭全部内置工具」。
		extraArgs["tools"] = builtinTools
	}
	if len(extraArgs) == 0 {
		return options
	}
	return append(options, claudeagent.WithExtraArgs(extraArgs))
}

// claudeRouterMcpOptions 把一个进程内 MCP server 注册给 CLI。
//
// WithStrictMCPConfig(true) 让 CLI 只认这里显式注册的 server，不加载用户级
// mcp.json；这是「Router 不污染用户配置」的另一半（另一半是 ConfigDir）。
// WithAllowedTools 只写精确工具名：CLI 不保证支持通配，而真正的闸门是
// handleCanUseTool 里的 ToolPolicy。
func claudeRouterMcpOptions(tools []types.ToolSpec) []claudeagent.Option {
	server := claudeagent.CreateMcpServer(claudeagent.McpServerOptions{
		Name:    types.RouterMCPServerName,
		Version: "1.0.0",
	})
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" || tool.Handler == nil {
			continue
		}
		handler := tool.Handler
		claudeagent.AddToolUntyped(server,
			claudeagent.ToolDef{Name: name, Description: tool.Description, InputSchema: tool.InputSchema},
			func(ctx context.Context, args json.RawMessage) (claudeagent.ToolResult, error) {
				text, err := handler(ctx, args)
				if err != nil {
					// 工具错误回给模型，而不是结束整个回合：Router 需要看到
					// 「参数不对/目标不存在」并自己改口。
					return claudeagent.ErrorResult(err.Error()), nil
				}
				return claudeagent.TextResult(text), nil
			})
		names = append(names, types.RouterToolPrefix+name)
	}
	options := []claudeagent.Option{
		claudeagent.WithMcpServer(types.RouterMCPServerName, server),
		claudeagent.WithStrictMCPConfig(true),
	}
	if len(names) > 0 {
		options = append(options, claudeagent.WithAllowedTools(names))
	}
	return options
}

func (r *Runtime) CloseAll() {}

func claudeFirstAvailableModel(client *claudeagent.Client) (string, bool) {
	if client == nil {
		return "", false
	}
	for _, item := range client.SupportedModelsFromInit() {
		candidate := strings.TrimSpace(item.Value)
		if candidate == "" || strings.EqualFold(candidate, "default") {
			continue
		}
		return candidate, true
	}
	return "", false
}
