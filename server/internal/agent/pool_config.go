package agent

import (
	"context"
	"errors"
	"strings"

	"mindfs/server/internal/agent/codex"
	agenttypes "mindfs/server/internal/agent/types"
)

// Config returns the pool configuration.
func (p *Pool) Config() Config {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cfg
}

func (p *Pool) CodexRateLimits(ctx context.Context, agentName string) (codex.RateLimitStatus, error) {
	opts, err := p.codexRuntimeOptions(agentName)
	if err != nil {
		return codex.RateLimitStatus{}, err
	}
	return p.codex.ReadRateLimits(ctx, opts)
}

func (p *Pool) ConsumeCodexRateLimitReset(ctx context.Context, agentName, idempotencyKey, creditID string) (codex.ConsumeRateLimitResetResult, error) {
	opts, err := p.codexRuntimeOptions(agentName)
	if err != nil {
		return codex.ConsumeRateLimitResetResult{}, err
	}
	return p.codex.ConsumeRateLimitReset(ctx, opts, idempotencyKey, creditID)
}

func (p *Pool) codexRuntimeOptions(agentName string) (codex.OpenOptions, error) {
	agentName = strings.TrimSpace(agentName)
	if agentName == "" {
		return codex.OpenOptions{}, errors.New("agent required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return codex.OpenOptions{}, errors.New("agent pool closed")
	}
	def, ok := p.cfg.GetAgent(agentName)
	if !ok {
		return codex.OpenOptions{}, errors.New("agent not configured: " + agentName)
	}
	protocol := def.Protocol
	if protocol == "" {
		protocol = DefaultProtocol(agentName)
	}
	if protocol != ProtocolCodexSDK {
		return codex.OpenOptions{}, errors.New("agent does not use codex-sdk: " + agentName)
	}
	return codex.OpenOptions{
		AgentName: agentName,
		Command:   def.Command,
		Args:      append([]string(nil), def.Args...),
		Env:       cloneEnv(def.Env),
	}, nil
}

func (p *Pool) UpdateConfig(cfg Config) Config {
	p.mu.Lock()
	defer p.mu.Unlock()
	cfg = p.applyRuntimeEnvOverridesLocked(cfg)
	p.cfg = cfg
	return p.cfg
}

func (p *Pool) SetAgentEnv(agentName string, env map[string]string) error {
	if agentName == "" {
		return errors.New("agent required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.cfg.Agents {
		if p.cfg.Agents[i].Name != agentName {
			continue
		}
		p.runtimeEnv[agentName] = cloneEnv(env)
		p.cfg.Agents[i].Env = cloneEnv(env)
		return nil
	}
	return errors.New("agent not configured: " + agentName)
}

func (p *Pool) applyRuntimeEnvOverridesLocked(cfg Config) Config {
	if len(p.runtimeEnv) == 0 {
		return cfg
	}
	for i := range cfg.Agents {
		env, ok := p.runtimeEnv[cfg.Agents[i].Name]
		if !ok {
			continue
		}
		cfg.Agents[i].Env = cloneEnv(env)
	}
	return cfg
}

// Get returns an existing session handle if present.
func (p *Pool) Get(sessionKey string) (agenttypes.Session, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry, ok := p.sessions[sessionKey]
	if !ok || entry == nil || entry.session == nil {
		return nil, false
	}
	return entry.session, true
}

// Context returns the pool lifecycle context (read-only).
func (p *Pool) Context() context.Context {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.processCtx != nil {
		return p.processCtx
	}
	return context.Background()
}
