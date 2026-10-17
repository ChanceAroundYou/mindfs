package agent

import (
	"sort"
	"time"
)
func (p *Prober) GetStatus(name string) (Status, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	status, ok := p.statuses[name]
	return status, ok
}

// GetAllStatuses 获取所有缓存的 Agent 状态
func (p *Prober) GetAllStatuses() []Status {
	p.mu.RLock()
	defer p.mu.RUnlock()

	statuses := make([]Status, 0, len(p.statuses))
	seen := make(map[string]struct{}, len(p.statuses))

	if p.cfg != nil {
		for _, def := range p.cfg.Agents {
			if st, ok := p.statuses[def.Name]; ok {
				statuses = append(statuses, st)
			}
			seen[def.Name] = struct{}{}
		}
	}

	extra := make([]Status, 0)
	for name, st := range p.statuses {
		if _, ok := seen[name]; ok {
			continue
		}
		extra = append(extra, st)
	}
	sort.Slice(extra, func(i, j int) bool {
		return extra[i].Name < extra[j].Name
	})
	statuses = append(statuses, extra...)

	return statuses
}

// GetConfiguredStatuses returns statuses for agents declared in agents.json.
func (p *Prober) GetConfiguredStatuses() []Status {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.cfg == nil {
		return nil
	}
	statuses := make([]Status, 0, len(p.cfg.Agents))
	now := time.Now().UTC()
	for _, def := range p.cfg.Agents {
		if st, ok := p.statuses[def.Name]; ok {
			statuses = append(statuses, st)
			continue
		}
		statuses = append(statuses, normalizeStatus(probeInstallStatus(def.Name, def, now)))
	}
	return statuses
}

// GetInstalledStatuses returns configured statuses filtered to installed agents.
func (p *Prober) GetInstalledStatuses() []Status {
	all := p.GetAllStatuses()
	filtered := make([]Status, 0, len(all))
	for _, st := range all {
		if !st.Installed {
			continue
		}
		filtered = append(filtered, st)
	}
	return filtered
}

// IsAvailable 检查 Agent 是否可用
func (p *Prober) IsAvailable(name string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	status, ok := p.statuses[name]
	return ok && status.Available
}

// AddListener registers a callback invoked when an agent status changes.
func (p *Prober) AddListener(listener func(Status)) {
	if listener == nil {
		return
	}
	p.mu.Lock()
	p.listeners = append(p.listeners, listener)
	p.mu.Unlock()
}

func statusChanged(prev Status, next Status) bool {
	if prev.Name != next.Name {
		return true
	}
	if prev.Installed != next.Installed {
		return true
	}
	if prev.Available != next.Available {
		return true
	}
	if prev.Version != next.Version {
		return true
	}
	if prev.Error != next.Error {
		return true
	}
	if prev.RuntimeError != next.RuntimeError {
		return true
	}
	if prev.ProbeError != next.ProbeError {
		return true
	}
	if prev.CurrentModelID != next.CurrentModelID {
		return true
	}
	if prev.CurrentModeID != next.CurrentModeID {
		return true
	}
	if prev.DefaultModelID != next.DefaultModelID {
		return true
	}
	if prev.DefaultEffort != next.DefaultEffort {
		return true
	}
	if prev.DefaultFastService != next.DefaultFastService {
		return true
	}
	if prev.SupportsFastService != next.SupportsFastService {
		return true
	}
	if len(prev.Efforts) != len(next.Efforts) {
		return true
	}
	for i := range prev.Efforts {
		if prev.Efforts[i] != next.Efforts[i] {
			return true
		}
	}
	if prev.ModelsError != next.ModelsError {
		return true
	}
	if prev.ModesError != next.ModesError {
		return true
	}
	if prev.CommandsError != next.CommandsError {
		return true
	}
	if prev.Brief != next.Brief {
		return true
	}
	if len(prev.InstallCommands) != len(next.InstallCommands) {
		return true
	}
	for i := range prev.InstallCommands {
		if prev.InstallCommands[i] != next.InstallCommands[i] {
			return true
		}
	}
	if len(prev.UpdateCommands) != len(next.UpdateCommands) {
		return true
	}
	for i := range prev.UpdateCommands {
		if prev.UpdateCommands[i] != next.UpdateCommands[i] {
			return true
		}
	}
	if len(prev.Models) != len(next.Models) {
		return true
	}
	for i := range prev.Models {
		if prev.Models[i].ID != next.Models[i].ID ||
			prev.Models[i].Name != next.Models[i].Name ||
			prev.Models[i].Description != next.Models[i].Description ||
			prev.Models[i].Hidden != next.Models[i].Hidden ||
			prev.Models[i].SupportEffort != next.Models[i].SupportEffort {
			return true
		}
	}
	if len(prev.Modes) != len(next.Modes) {
		return true
	}
	for i := range prev.Modes {
		if prev.Modes[i] != next.Modes[i] {
			return true
		}
	}
	if len(prev.Commands) != len(next.Commands) {
		return true
	}
	for i := range prev.Commands {
		if prev.Commands[i] != next.Commands[i] {
			return true
		}
	}
	return false
}

func (p *Prober) setStatus(status Status) {
	p.mu.Lock()
	prev, hadPrev := p.statuses[status.Name]
	if hadPrev {
		status = preserveKnownCapabilities(prev, status)
	}
	status = normalizeStatus(status)
	p.statuses[status.Name] = status
	listeners := append([]func(Status){}, p.listeners...)
	p.mu.Unlock()

	if hadPrev && !statusChanged(prev, status) {
		return
	}
	for _, listener := range listeners {
		listener(status)
	}
}

func unavailableStatus(name string, installed bool, errMsg string, ts time.Time) Status {
	return Status{
		Name:       name,
		Installed:  installed,
		Available:  false,
		Error:      errMsg,
		ProbeError: errMsg,
		LastProbe:  ts,
	}
}
