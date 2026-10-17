package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime"
	"strings"
	"time"

)

// Start 启动定期探测
func (p *Prober) Start(ctx context.Context) {
	// 首次全量探测放到后台，避免阻塞服务启动和请求处理。
	// Windows 下深度探测会启动外部 Agent CLI；部分 SDK/CLI 无法由 MindFS
	// 注入 CREATE_NO_WINDOW，后台启动时会出现空白控制台窗口。
	if shouldRunBackgroundRuntimeProbe(runtime.GOOS) {
		go p.safeProbeAll(ctx)
	}

	// 启动定期探测：只重试未安装命令。运行时失败不做主动恢复探测，
	// 避免周期性打开 agent probe session。
	ticker := time.NewTicker(p.probeInterval)
	go func() {
		defer ticker.Stop()
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[agent/probe] ticker.panic recovered=%v", r)
			}
		}()
		for {
			select {
			case <-ticker.C:
				p.probeMissingCommands()
				p.probeChangedConfigAgents()
			case <-p.stopCh:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Stop 停止定期探测
func (p *Prober) Stop() {
	select {
	case <-p.stopCh:
		return
	default:
		close(p.stopCh)
	}
}

func (p *Prober) UpdateConfig(ctx context.Context, cfg *Config) {
	if p == nil || cfg == nil {
		return
	}
	now := time.Now().UTC()
	var installed []Definition
	p.mu.Lock()
	p.cfg = cfg
	for _, def := range cfg.Agents {
		if _, ok := p.statuses[def.Name]; ok {
			continue
		}
		status := normalizeStatus(probeInstallStatus(def.Name, def, now))
		p.statuses[def.Name] = status
		if status.Installed {
			installed = append(installed, def)
		}
	}
	p.mu.Unlock()
	if len(installed) > 0 && shouldRunBackgroundRuntimeProbe(runtime.GOOS) {
		go p.probeInstalledAgents(ctx, installed)
	}
}

func shouldRunBackgroundRuntimeProbe(goos string) bool {
	return goos != "windows"
}

// ProbeAll 探测所有配置的 Agent
func (p *Prober) ProbeAll(ctx context.Context) {
	defs := p.configuredDefinitions()
	if len(defs) == 0 {
		return
	}
	p.probeConfiguredAgents(ctx, defs)
}

func (p *Prober) safeProbeAll(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[agent/probe] probe_all.panic recovered=%v", r)
		}
	}()
	p.ProbeAll(ctx)
}

// ProbeOne probes a single configured agent with recovery-style timeout control.
func (p *Prober) ProbeOne(ctx context.Context, name string) Status {
	if p == nil {
		return unavailableStatus(strings.TrimSpace(name), false, "config not loaded", time.Now().UTC())
	}
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return unavailableStatus("", false, "agent required", time.Now().UTC())
	}
	def, ok := p.configuredDefinition(trimmed)
	if !ok {
		return unavailableStatus(trimmed, false, "agent not configured", time.Now().UTC())
	}
	status := safeProbeConfiguredAgentWithPool(ctx, trimmed, def, p.pool, p.probeSessions, probePhaseRecovery)
	p.setStatus(status)
	return status
}

func (p *Prober) SetAgentEnv(agentName string, env map[string]string) error {
	if p == nil {
		return errors.New("prober not configured")
	}
	agentName = strings.TrimSpace(agentName)
	if agentName == "" {
		return errors.New("agent required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cfg == nil {
		return errors.New("config not loaded")
	}
	for i := range p.cfg.Agents {
		if p.cfg.Agents[i].Name != agentName {
			continue
		}
		p.cfg.Agents[i].Env = cloneEnv(env)
		return nil
	}
	return errors.New("agent not configured: " + agentName)
}

func (p *Prober) ClearProbeSession(agentName string) error {
	if p == nil {
		return nil
	}
	return clearProbeSessionBinding(p.probeSessions, agentName)
}

// ReportRuntimeFailure marks an agent as unavailable due to a real user-facing runtime failure.
func (p *Prober) ReportRuntimeFailure(name string, err error) {
	msg := "unknown failure"
	if err != nil {
		msg = err.Error()
	}
	installed := true
	current, ok := p.GetStatus(name)
	if ok {
		installed = current.Installed
	}
	status := current
	if !ok {
		status = unavailableStatus(name, installed, "", time.Now().UTC())
	}
	status.Name = name
	status.Installed = installed
	status.Available = false
	status.LastProbe = time.Now().UTC()
	status.RuntimeError = msg
	p.setStatus(status)
}

// ReportProbeFailure marks an agent as unavailable due to background probe failure.
func (p *Prober) ReportProbeFailure(name string, err error) {
	msg := "unknown failure"
	if err != nil {
		msg = err.Error()
	}
	installed := true
	current, ok := p.GetStatus(name)
	if ok {
		installed = current.Installed
	}
	status := current
	if !ok {
		status = unavailableStatus(name, installed, msg, time.Now().UTC())
	}
	status.Name = name
	status.Installed = installed
	status.Available = false
	status.ProbeError = msg
	status.LastProbe = time.Now().UTC()
	p.setStatus(status)
}

// ReportSuccess marks an agent as available due to successful runtime interaction.
func (p *Prober) ReportSuccess(name string) {
	st, _ := p.GetStatus(name)
	st.Name = name
	st.Installed = true
	st.Available = true
	st.Error = ""
	st.RuntimeError = ""
	st.ProbeError = ""
	st.LastProbe = time.Now().UTC()
	p.setStatus(st)
}

func (p *Prober) probeMissingCommands() {
	if p.cfg == nil {
		return
	}
	defs := p.collectDefinitions(func(st Status, ok bool) bool {
		return !ok || !st.Installed
	})
	log.Printf("[agent/probe] probe_missing_commands count=%d agents=%s", len(defs), definitionNames(defs))
	p.probeInstallOnly(defs)
}

func resolveProbePool(def Definition, shared *Pool) (*Pool, bool) {
	if shared != nil {
		return shared, false
	}
	return NewPool(Config{Agents: []Definition{def}}), true
}

func (p *Prober) collectDefinitions(include func(Status, bool) bool) []Definition {
	all := p.configuredDefinitions()
	defs := make([]Definition, 0, len(all))
	for _, def := range all {
		status, ok := p.GetStatus(def.Name)
		if !include(status, ok) {
			continue
		}
		defs = append(defs, def)
	}
	return defs
}

func (p *Prober) configuredDefinition(name string) (Definition, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.cfg == nil {
		return Definition{}, false
	}
	return p.cfg.GetAgent(name)
}

func (p *Prober) configuredDefinitions() []Definition {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.cfg == nil {
		return nil
	}
	defs := make([]Definition, len(p.cfg.Agents))
	copy(defs, p.cfg.Agents)
	return defs
}

func (p *Prober) probeConfiguredAgents(ctx context.Context, defs []Definition) {
	if len(defs) == 0 {
		return
	}
	p.runDefinitionsConcurrently(defs, func(_ int, def Definition) {
		status := safeProbeConfiguredAgentWithPool(ctx, def.Name, def, p.pool, p.probeSessions, probePhaseInitial)
		p.setStatus(status)
	})
}

func (p *Prober) probeInstallOnly(defs []Definition) {
	if len(defs) == 0 {
		return
	}

	p.runDefinitionsConcurrently(defs, func(_ int, def Definition) {
		status := probeInstallStatus(def.Name, def, time.Now().UTC())
		p.setStatus(status)
	})
}

func (p *Prober) probeInstalledAgents(ctx context.Context, defs []Definition) {
	if len(defs) == 0 {
		return
	}

	p.runDefinitionsConcurrently(defs, func(_ int, def Definition) {
		status := unavailableStatus(def.Name, true, "probe pending", time.Now().UTC())
		status.Protocol = agentDefinitionProtocol(def.Name, def)
		status = safeProbeInstalledAgentWithPool(ctx, def.Name, def, p.pool, p.probeSessions, status, probePhaseBackground)
		p.setStatus(status)
	})
}

func (p *Prober) runDefinitionsConcurrently(defs []Definition, fn func(i int, def Definition)) {
	for i, def := range defs {
		p.mu.Lock()
		if _, running := p.inFlight[def.Name]; running {
			p.mu.Unlock()
			continue
		}
		p.inFlight[def.Name] = struct{}{}
		p.mu.Unlock()

		go func(i int, def Definition) {
			defer func() {
				if r := recover(); r != nil {
					status := unavailableStatus(def.Name, true, fmt.Sprintf("probe panic: %v", r), time.Now().UTC())
					p.setStatus(status)
					log.Printf("[agent/probe] worker.panic agent=%s recovered=%v", def.Name, r)
				}
				p.mu.Lock()
				delete(p.inFlight, def.Name)
				p.mu.Unlock()
			}()
			fn(i, def)
		}(i, def)
	}
}

func definitionNames(defs []Definition) string {
	if len(defs) == 0 {
		return "-"
	}
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		names = append(names, def.Name)
	}
	return strings.Join(names, ",")
}
