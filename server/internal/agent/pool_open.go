package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"mindfs/server/internal/agent/acp"
	"mindfs/server/internal/agent/claude"
	"mindfs/server/internal/agent/codex"
	"mindfs/server/internal/agent/proxy"
	agenttypes "mindfs/server/internal/agent/types"
)

// compactProxyBinding 是把一个 CLI 会话绑到 compact-strip 代理上所需的全部改动。
//
// settingsJSON 不是可选项：Claude Code 会把 `~/.claude/settings.json` 的 env
// 应用到进程环境之上，SDK 传下去的 ANTHROPIC_BASE_URL 会被它盖回上游地址
// （实测：进程环境指向不可达端口，CLI 依旧走 settings 的地址并成功回包）。
// 只有优先级更高的 `--settings` 能让 CLI 真正走代理。
type compactProxyBinding struct {
	env          map[string]string
	settingsJSON string
}

// compactProxyEnv builds the extra env vars that route this session through the
// compact-strip proxy.  Returns nil if the proxy is not running or the upstream
// base URL cannot be resolved (a session we cannot forward is worse than no
// proxy at all: it would fail every request).
// Also records the token in pendingProxyTokens so GetOrCreate can attach it to
// the sessionEntry for cleanup on close.
func (p *Pool) compactProxyEnv(sessionKey string, def Definition, configDir string) *compactProxyBinding {
	if p.compactProxy == nil {
		return nil
	}
	realBaseURL := resolveClaudeUpstreamBaseURL(def.Env, configDir)
	if realBaseURL == "" {
		return nil
	}
	token := make([]byte, 8)
	if _, err := rand.Read(token); err != nil {
		return nil
	}
	tok := hex.EncodeToString(token)

	port := p.compactProxy.Port()
	// 隔离 ConfigDir 的会话（Router/工作流/结果改写）拿不到用户归档目录的读取权限，
	// 折叠出的指针对它们等于丢失内容；只测量，不折叠。
	if strings.TrimSpace(configDir) == "" {
		p.compactProxy.RegisterSessionWithKey(tok, realBaseURL, sessionKey)
	} else {
		p.compactProxy.RegisterSessionObserveOnly(tok, realBaseURL, sessionKey)
	}

	p.mu.Lock()
	p.pendingProxyTokens[sessionKey] = tok
	p.mu.Unlock()

	proxyURL := fmt.Sprintf("http://127.0.0.1:%d/p/%s", port, tok)
	settingsJSON, err := json.Marshal(map[string]any{
		"env": map[string]string{"ANTHROPIC_BASE_URL": proxyURL},
	})
	if err != nil {
		p.compactProxy.UnregisterSession(tok)
		p.mu.Lock()
		if p.pendingProxyTokens[sessionKey] == tok {
			delete(p.pendingProxyTokens, sessionKey)
		}
		p.mu.Unlock()
		return nil
	}
	log.Printf("[proxy] session bound token=%s upstream=%s", proxy.ShortToken(tok), clampUpstreamForLog(realBaseURL))

	return &compactProxyBinding{
		env: map[string]string{
			"ANTHROPIC_BASE_URL":        proxyURL,
			"MINDFS_PROXY_TOKEN":        tok,
			"MINDFS_COMPACT_SIGNAL_URL": fmt.Sprintf("http://127.0.0.1:%d/compact-signal", port),
		},
		settingsJSON: string(settingsJSON),
	}
}

// mergeCompactProxyEnv 把代理绑定的 env 覆盖到会话 env 上。
//
// cloneEnv 在空输入时返回 nil，直接写会 panic —— agent 定义没有 env 是常态
// （本机 claude 的 provider 配置就在 ~/.claude/settings.json），所以这里必须
// 自己建 map。症状是 probe 直接 `assignment to entry in nil map`。
func mergeCompactProxyEnv(base map[string]string, binding *compactProxyBinding) map[string]string {
	if binding == nil {
		return base
	}
	if base == nil {
		base = make(map[string]string, len(binding.env))
	}
	for k, v := range binding.env {
		base[k] = v
	}
	return base
}

// withClaudeProviderEnv 给「隔离 ConfigDir」的 Claude 会话补上用户 settings 里的
// provider 环境变量。
//
// Router / workflow 推荐器 / 结果转换器都跑在 <account>/router/claude 下，刻意
// 不读用户的 settings：用户 hooks、plugins、tool 权限不能漏进来。代价是 CLI 也
// 读不到 settings.env 里的 provider 凭据，于是落到官方登录并报
// "Not logged in · Please run /login" —— 表现为 Router 每轮失败、工作流自动选择
// 永远回退到第一个可用 workflow（按 id 排序即 workflow-blank / 「空白执行」）。
//
// 这里只继承 provider 相关的键（ANTHROPIC_* 与 Bedrock/Vertex 开关），hooks 与
// 进程级开关（BASH_ENV/OMC_SKIP_HOOKS/...）一律不继承。configDir 为空时是普通
// 会话，CLI 自己会读用户 settings，行为保持不变。显式 agent env 优先。
func withClaudeProviderEnv(env map[string]string, configDir string) map[string]string {
	if strings.TrimSpace(configDir) == "" {
		return env
	}
	userEnv := claude.ClaudeUserSettingsEnv()
	if len(userEnv) == 0 {
		return env
	}
	inherited := map[string]string{}
	for key, value := range userEnv {
		if !isClaudeProviderEnvKey(key) {
			continue
		}
		if _, ok := env[key]; ok {
			continue
		}
		inherited[key] = value
	}
	if len(inherited) == 0 {
		return env
	}
	merged := make(map[string]string, len(env)+len(inherited))
	for key, value := range env {
		merged[key] = value
	}
	for key, value := range inherited {
		merged[key] = value
	}
	return merged
}

func isClaudeProviderEnvKey(key string) bool {
	switch key {
	case "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX":
		return true
	}
	return strings.HasPrefix(key, "ANTHROPIC_")
}

func (p *Pool) openSession(ctx context.Context, protocol Protocol, def Definition, in agenttypes.OpenSessionInput) (agenttypes.Session, error) {
	// Router 工具只存在于 claude 适配器；codex/acp 拿到工具说明装配错了，
	// 必须显式失败而不是静默丢掉工具（那会让 Router 以为工具可用）。
	if protocol != ProtocolClaudeSDK && (len(in.Tools) > 0 || in.ToolPolicy != nil) {
		return nil, errors.New("router tools require claude protocol")
	}
	switch protocol {
	case ProtocolClaudeSDK:
		env := withClaudeProviderEnv(cloneEnv(def.Env), in.ConfigDir)
		settingsJSON := ""
		if binding := p.compactProxyEnv(in.SessionKey, def, in.ConfigDir); binding != nil {
			env = mergeCompactProxyEnv(env, binding)
			settingsJSON = binding.settingsJSON
		}
		return p.claude.OpenSession(ctx, claude.OpenOptions{
			AgentName:             in.AgentName,
			SessionKey:            in.SessionKey,
			Model:                 in.Model,
			Effort:                in.Effort,
			PlanMode:              in.PlanMode,
			RootPath:              in.RootPath,
			Command:               def.Command,
			Args:                  append([]string{}, def.Args...),
			Env:                   env,
			DeveloperInstructions: in.DeveloperInstructions,
			ResumeSessionID:       in.AgentSessionID,
			ForkSessionID:         in.ForkPoint.AgentSessionID,
			ResumeMessageID:       in.ForkPoint.ClaudeMessageUUID,
			ConfigDir:             in.ConfigDir,
			Tools:                 in.Tools,
			ToolPolicy:            in.ToolPolicy,
			SettingsJSON:          settingsJSON,
		})
	case ProtocolCodexSDK:
		var codexUserOrdinal *int
		if in.ForkPoint.Kind == agenttypes.ForkPointCodexUserOrdinal {
			value := in.ForkPoint.CodexUserOrdinal
			codexUserOrdinal = &value
		}
		args := append([]string{}, def.Args...)
		var onCompact func(string)
		var onThreadStarted func(string)
		if p.compactProxy != nil {
			// Codex compacts by itself; tell the proxy the next request's cache
			// is cold so that fold is free.
			onCompact = p.compactProxy.SignalCompact
			// A new thread has no id at open time; the adapter reports it once
			// the app server assigns one, and that is when the proxy can start
			// attributing this session's provider traffic.
			onThreadStarted = func(threadID string) {
				p.RegisterThread(in.SessionKey, threadID)
			}
			// Codex reads its providers from config.toml. Point each of them at
			// the proxy with a command-line override rather than editing that
			// file: the user's configuration stays untouched, and a profile
			// without [model_providers] (ChatGPT login) is left exactly as it is.
			providers, err := codexProviderBaseURLs()
			if err != nil {
				// A malformed config is Codex's problem to report, not a reason
				// to refuse the session.
				log.Printf("[agent/pool] codex providers: %v", err)
			}
			if len(providers) > 0 {
				for name, base := range providers {
					p.compactProxy.RegisterProvider(name, base)
				}
				args = append(args, codexProxyArgs(providers, p.compactProxy.ProviderProxyURL)...)
			}
		}
		return p.codex.OpenSession(ctx, codex.OpenOptions{
			AgentName:             in.AgentName,
			SessionKey:            in.SessionKey,
			Model:                 in.Model,
			Effort:                in.Effort,
			FastService:           in.FastService,
			PlanMode:              in.PlanMode,
			Probe:                 in.Probe,
			RootPath:              in.RootPath,
			Command:               def.Command,
			Args:                  args,
			Env:                   codexChildEnv(def),
			DeveloperInstructions: in.DeveloperInstructions,
			ResumeSessionID:       in.AgentSessionID,
			ForkSessionID:         in.ForkPoint.AgentSessionID,
			CodexUserOrdinal:      codexUserOrdinal,
			OnCompact:             onCompact,
			OnThreadStarted:       onThreadStarted,
		})
	case ProtocolACP:
		fallthrough
	default:
		return p.acp.OpenSession(ctx, acp.OpenOptions{
			AgentName:       in.AgentName,
			SessionKey:      in.SessionKey,
			Model:           in.Model,
			Mode:            in.Mode,
			Effort:          in.Effort,
			RootPath:        in.RootPath,
			Command:         def.Command,
			Args:            def.BuildArgs(in.RootPath),
			Env:             cloneEnv(def.Env),
			Cwd:             def.ResolveCwd(in.RootPath),
			ResumeSessionID: in.AgentSessionID,
		})
	}
}

func (p *Pool) KillAgentProcess(agentName string, wait time.Duration) (string, bool) {
	_ = wait
	def, ok := p.cfg.GetAgent(agentName)
	if !ok {
		return "", false
	}

	protocol := def.Protocol
	if protocol == "" {
		protocol = DefaultProtocol(agentName)
	}
	unlockRuntime := p.lockRuntimeGroups([]runtimeGroup{{agentName: agentName, protocol: protocol}})
	defer unlockRuntime()
	switch protocol {
	case ProtocolClaudeSDK:
		p.closeSessionsForAgent(agentName, ProtocolClaudeSDK)
		log.Printf("[agent/pool] kill_agent_process.claude_closed agent=%s", agentName)
		return "", true
	case ProtocolCodexSDK:
		p.closeSessionsForAgent(agentName, ProtocolCodexSDK)
		_ = p.codex.Close(agentName)
		log.Printf("[agent/pool] kill_agent_process.codex_closed agent=%s", agentName)
		return "", true
	case ProtocolACP:
		p.closeSessionsForAgent(agentName, ProtocolACP)
		_ = p.acp.Close(agentName)
		if hint, ok := p.acp.RecentCloseHint(agentName); ok {
			log.Printf("[agent/pool] kill_agent_process.hint agent=%s hint=%q", agentName, hint)
			return hint, true
		}
		log.Printf("[agent/pool] kill_agent_process.no_hint agent=%s", agentName)
		return "", false
	default:
		return "", false
	}
}

func (p *Pool) closeSessionsForAgent(agentName string, protocol Protocol) {
	p.closeSessions(
		p.takeSessions(func(entry *sessionEntry) bool {
			return entry.agentName == agentName && entry.protocol == protocol
		}),
	)
}

func cloneEnv(env map[string]string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	out := make(map[string]string, len(env))
	for key, value := range env {
		out[key] = value
	}
	return out
}
