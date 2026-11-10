package api

import (
	"os"
	"strings"
	"time"

	"mindfs/internal/codexsync"
	"mindfs/server/internal/agent"
	"mindfs/server/internal/preferences"
)

func configuredCodexSDKAgent(name string) (string, bool, error) {
	cfg, err := agent.LoadConfig("")
	if err != nil {
		return "", false, err
	}
	definition, ok := cfg.GetAgent(strings.TrimSpace(name))
	if !ok || definition.Protocol != agent.ProtocolCodexSDK {
		return "", false, nil
	}
	return definition.Name, true, nil
}

func sharedCodexConfigBackups() ([]agentConfigManifestEntry, error) {
	paths, err := codexsync.ResolvePaths()
	if err != nil {
		return nil, err
	}
	profiles := []struct {
		id   string
		name string
		path string
		kind string
	}{
		{"shared:codex-default", "default", paths.SharedDefault, "default"},
		{"shared:codex-llmux", "llmux", paths.SharedLLMux, "llmux"},
	}
	entries := make([]agentConfigManifestEntry, 0, len(profiles))
	for _, profile := range profiles {
		payload, err := codexsync.ReadSharedCodexProfileAt(paths, profile.kind)
		if err != nil {
			return nil, err
		}
		if payload == nil {
			continue
		}
		modified := time.Time{}
		if info, statErr := os.Stat(profile.path); statErr == nil {
			modified = info.ModTime()
		}
		updated := ""
		if !modified.IsZero() {
			updated = modified.UTC().Format(time.RFC3339)
		}
		entries = append(entries, agentConfigManifestEntry{
			ID:        profile.id,
			Agent:     "codex",
			Name:      profile.name,
			Source:    "shared",
			CreatedAt: updated,
			UpdatedAt: updated,
		})
		if profile.kind == "llmux" {
			available := codexsync.LocalLLMuxCredentialAvailable()
			entries[len(entries)-1].LocalCredentialAvailable = &available
		}
	}
	return entries, nil
}

func switchSharedCodexConfig(id, requestedAgent string, app *AppContext) (agentConfigManifestEntry, error) {
	agentName := strings.TrimSpace(requestedAgent)
	if agentName == "" {
		agentName = "codex"
	}
	configuredName, isCodexSDK, err := configuredCodexSDKAgent(agentName)
	if err != nil {
		return agentConfigManifestEntry{}, err
	}
	if !isCodexSDK {
		return agentConfigManifestEntry{}, apperrInvalid("共享 Codex 配置只能用于 Codex SDK Agent")
	}
	agentName = configuredName
	name := ""
	switch strings.TrimSpace(id) {
	case "shared:codex-default":
		name = "default"
	case "shared:codex-llmux":
		name = "llmux"
	default:
		return agentConfigManifestEntry{}, apperrInvalid("共享 Codex 配置不存在")
	}
	if err := codexsync.ApplySharedCodexProfile(name); err != nil {
		return agentConfigManifestEntry{}, err
	}
	entry := agentConfigManifestEntry{ID: id, Agent: agentName, Name: name, Source: "shared"}
	if name == "llmux" {
		available := codexsync.LocalLLMuxCredentialAvailable()
		entry.LocalCredentialAvailable = &available
	}
	if app != nil && app.GetAgentPool() != nil {
		app.GetAgentPool().KillAgentProcess(agentName, 0)
	}
	if app != nil && app.GetPreferences() != nil {
		if err := app.GetPreferences().UpdateAgentLastConfigSelection(agentName, preferences.LastConfigSelection{
			Type: "backup",
			ID:   entry.ID,
			Name: entry.Name,
		}); err != nil {
			return agentConfigManifestEntry{}, err
		}
	}
	triggerAgentConfigSwitchProbe(app, agentName)
	return entry, nil
}
