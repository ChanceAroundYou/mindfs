package acp

import (
	"context"
	"strings"

	types "mindfs/server/internal/agent/types"

	acp "github.com/coder/acp-go-sdk"
)

func (p *Process) agentLabel() string {
	if p == nil || p.agentName == "" {
		return "unknown"
	}
	return p.agentName
}

// SessionID returns the ACP session ID for a MindFS session key.
func (p *Process) SessionID(sessionKey string) string {
	if sess := p.getSessionByKey(sessionKey); sess != nil {
		return string(sess.ID)
	}
	return ""
}

// ProcessID returns the PID of the shared ACP agent process.
func (p *Process) ProcessID() int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// Capability returns agent capabilities reported by initialize response.
func (p *Process) Capability() CapabilitySnapshot {
	return p.capability
}

func (p *Process) ConfigOptions() []acp.SessionConfigOption {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return cloneConfigOptions(p.configOptions)
}

func (p *Process) ModelState() *acp.SessionModelState {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.models
}

func (p *Process) ModeState() *acp.SessionModeState {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.modes
}

func (p *Process) SetModel(ctx context.Context, sessionKey, model string) error {
	sess := p.getSessionByKey(sessionKey)
	if sess == nil || strings.TrimSpace(model) == "" {
		return nil
	}
	options := sess.getConfigOptions()
	option, ok := findSelectConfigOption(options, acp.SessionConfigOptionCategoryModel)
	if !ok {
		if sess.getModels() == nil {
			return nil
		}
		_, err := p.conn.UnstableSetSessionModel(ctx, acp.UnstableSetSessionModelRequest{
			SessionId: sess.ID,
			ModelId:   acp.UnstableModelId(strings.TrimSpace(model)),
		})
		if err == nil {
			if state := sess.getModels(); state != nil {
				state.CurrentModelId = acp.ModelId(strings.TrimSpace(model))
				sess.setModels(state)
				p.mu.Lock()
				p.models = state
				p.mu.Unlock()
			}
		}
		return err
	}
	resp, err := p.conn.SetSessionConfigOption(ctx, acp.SetSessionConfigOptionRequest{
		ValueId: &acp.SetSessionConfigOptionValueId{
			ConfigId:  option.Id,
			SessionId: sess.ID,
			Value:     acp.SessionConfigValueId(strings.TrimSpace(model)),
		},
	})
	if err != nil {
		return err
	}
	sess.setConfigOptions(resp.ConfigOptions)
	p.mu.Lock()
	p.configOptions = cloneConfigOptions(resp.ConfigOptions)
	p.mu.Unlock()
	return nil
}

func (p *Process) SetMode(ctx context.Context, sessionKey, mode string) error {
	sess := p.getSessionByKey(sessionKey)
	if sess == nil || strings.TrimSpace(mode) == "" {
		return nil
	}
	if option, ok := findSelectConfigOption(sess.getConfigOptions(), acp.SessionConfigOptionCategoryMode); ok {
		resp, err := p.conn.SetSessionConfigOption(ctx, acp.SetSessionConfigOptionRequest{
			ValueId: &acp.SetSessionConfigOptionValueId{
				ConfigId:  option.Id,
				SessionId: sess.ID,
				Value:     acp.SessionConfigValueId(strings.TrimSpace(mode)),
			},
		})
		if err != nil {
			return err
		}
		sess.setConfigOptions(resp.ConfigOptions)
		p.mu.Lock()
		p.configOptions = cloneConfigOptions(resp.ConfigOptions)
		p.mu.Unlock()
		return nil
	}
	_, err := p.conn.SetSessionMode(ctx, acp.SetSessionModeRequest{
		SessionId: sess.ID,
		ModeId:    acp.SessionModeId(strings.TrimSpace(mode)),
	})
	if err == nil {
		if state := sess.getModes(); state != nil {
			state.CurrentModeId = acp.SessionModeId(strings.TrimSpace(mode))
			sess.setModes(state)
			p.mu.Lock()
			p.modes = state
			p.mu.Unlock()
		}
	}
	return err
}

func (p *Process) SetThoughtLevel(ctx context.Context, sessionKey, effort string) error {
	sess := p.getSessionByKey(sessionKey)
	if sess == nil || strings.TrimSpace(effort) == "" {
		return nil
	}
	option, ok := findSelectConfigOption(sess.getConfigOptions(), acp.SessionConfigOptionCategoryThoughtLevel)
	if !ok {
		return nil
	}
	resp, err := p.conn.SetSessionConfigOption(ctx, acp.SetSessionConfigOptionRequest{
		ValueId: &acp.SetSessionConfigOptionValueId{
			ConfigId:  option.Id,
			SessionId: sess.ID,
			Value:     acp.SessionConfigValueId(strings.TrimSpace(effort)),
		},
	})
	if err != nil {
		return err
	}
	sess.setConfigOptions(resp.ConfigOptions)
	p.mu.Lock()
	p.configOptions = cloneConfigOptions(resp.ConfigOptions)
	p.mu.Unlock()
	return nil
}

func (p *Process) SessionModelState(sessionKey string) *acp.SessionModelState {
	sess := p.getSessionByKey(sessionKey)
	if sess == nil {
		return nil
	}
	return sess.getModels()
}

func (p *Process) SessionConfigOptions(sessionKey string) []acp.SessionConfigOption {
	sess := p.getSessionByKey(sessionKey)
	if sess == nil {
		return nil
	}
	return sess.getConfigOptions()
}

func (p *Process) SessionModeState(sessionKey string) *acp.SessionModeState {
	sess := p.getSessionByKey(sessionKey)
	if sess == nil {
		return nil
	}
	return sess.getModes()
}

func (p *Process) SessionCommands(sessionKey string) []acp.AvailableCommand {
	sess := p.getSessionByKey(sessionKey)
	if sess == nil {
		return nil
	}
	return sess.getCommands()
}

func (p *Process) SessionContextWindow(sessionKey string) types.ContextWindow {
	sess := p.getSessionByKey(sessionKey)
	if sess == nil {
		return types.ContextWindow{}
	}
	return sess.getContextWindow()
}
