package agent

import (
	"context"
	"log"
	"time"

	"mindfs/server/internal/agent/acp"
	"mindfs/server/internal/agent/claude"
	"mindfs/server/internal/agent/codex"
	agenttypes "mindfs/server/internal/agent/types"
)

func (p *Pool) openSession(ctx context.Context, protocol Protocol, def Definition, in agenttypes.OpenSessionInput) (agenttypes.Session, error) {
	switch protocol {
	case ProtocolClaudeSDK:
		return p.claude.OpenSession(ctx, claude.OpenOptions{
			AgentName:             in.AgentName,
			SessionKey:            in.SessionKey,
			Model:                 in.Model,
			Effort:                in.Effort,
			PlanMode:              in.PlanMode,
			RootPath:              in.RootPath,
			Command:               def.Command,
			Args:                  append([]string{}, def.Args...),
			Env:                   cloneEnv(def.Env),
			DeveloperInstructions: in.DeveloperInstructions,
			ResumeSessionID:       in.AgentSessionID,
			ForkSessionID:         in.ForkPoint.AgentSessionID,
			ResumeMessageID:       in.ForkPoint.ClaudeMessageUUID,
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
			Env:                   cloneEnv(def.Env),
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
