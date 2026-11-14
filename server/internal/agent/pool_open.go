package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"time"

	"mindfs/server/internal/agent/acp"
	"mindfs/server/internal/agent/claude"
	"mindfs/server/internal/agent/codex"
	agenttypes "mindfs/server/internal/agent/types"
)

// compactProxyEnv builds the extra env vars that route this session through the
// compact-strip proxy.  Returns nil if the proxy is not running.
// Also records the token in pendingProxyTokens so GetOrCreate can attach it to
// the sessionEntry for cleanup on close.
func (p *Pool) compactProxyEnv(sessionKey string, def Definition) map[string]string {
	if p.compactProxy == nil {
		return nil
	}
	token := make([]byte, 8)
	if _, err := rand.Read(token); err != nil {
		return nil
	}
	tok := hex.EncodeToString(token)

	// Determine the real upstream base URL from the agent definition.
	realBaseURL := def.Env["ANTHROPIC_BASE_URL"]
	if realBaseURL == "" {
		return nil
	}

	port := p.compactProxy.Port()
	p.compactProxy.RegisterSession(tok, realBaseURL)

	p.mu.Lock()
	p.pendingProxyTokens[sessionKey] = tok
	p.mu.Unlock()

	return map[string]string{
		"ANTHROPIC_BASE_URL":        fmt.Sprintf("http://127.0.0.1:%d/p/%s", port, tok),
		"MINDFS_PROXY_TOKEN":        tok,
		"MINDFS_COMPACT_SIGNAL_URL": fmt.Sprintf("http://127.0.0.1:%d/compact-signal", port),
	}
}

func (p *Pool) openSession(ctx context.Context, protocol Protocol, def Definition, in agenttypes.OpenSessionInput) (agenttypes.Session, error) {
	// Router 工具只存在于 claude 适配器；codex/acp 拿到工具说明装配错了，
	// 必须显式失败而不是静默丢掉工具（那会让 Router 以为工具可用）。
	if protocol != ProtocolClaudeSDK && (len(in.Tools) > 0 || in.ToolPolicy != nil) {
		return nil, errors.New("router tools require claude protocol")
	}
	switch protocol {
	case ProtocolClaudeSDK:
		env := cloneEnv(def.Env)
		if extra := p.compactProxyEnv(in.SessionKey, def); extra != nil {
			for k, v := range extra {
				env[k] = v
			}
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
		})
	case ProtocolCodexSDK:
		var codexUserOrdinal *int
		if in.ForkPoint.Kind == agenttypes.ForkPointCodexUserOrdinal {
			value := in.ForkPoint.CodexUserOrdinal
			codexUserOrdinal = &value
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
			Args:                  append([]string{}, def.Args...),
			Env:                   codexChildEnv(def),
			DeveloperInstructions: in.DeveloperInstructions,
			ResumeSessionID:       in.AgentSessionID,
			ForkSessionID:         in.ForkPoint.AgentSessionID,
			CodexUserOrdinal:      codexUserOrdinal,
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
