package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mindfs/server/internal/agent"
	"mindfs/server/internal/apperr"
	"mindfs/server/internal/preferences"
)

func (h *HTTPHandler) handleAgentConfigSwitch(w http.ResponseWriter, r *http.Request) {
	var req agentConfigSwitchRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxUploadRequestBytes)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid request body"))
		return
	}
	entry, needsConfirm, err := switchAgentConfig(req, h.AppContext)
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	if needsConfirm {
		respondJSON(w, http.StatusOK, map[string]any{
			"needs_confirm": true,
			"message":       "目标配置文件已存在，请确保已备份",
			"backup":        entry,
		})
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"needs_confirm": false,
		"backup":        entry,
	})
}

func (h *HTTPHandler) handleAgentRestart(w http.ResponseWriter, r *http.Request) {
	var req agentRestartRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxUploadRequestBytes)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid request body"))
		return
	}
	if err := restartAgent(req.Agent, h.AppContext); err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"restarting": true,
		"agent":      strings.TrimSpace(req.Agent),
	})
}

func switchAgentConfig(req agentConfigSwitchRequest, app *AppContext) (agentConfigManifestEntry, bool, error) {
	id := strings.TrimSpace(req.ID)
	if id == "" {
		return agentConfigManifestEntry{}, false, errors.New("backup id required")
	}
	manifest, err := readAgentConfigManifest()
	if err != nil {
		return agentConfigManifestEntry{}, false, err
	}
	var entry agentConfigManifestEntry
	for _, item := range manifest {
		if item.ID == id {
			entry = item
			break
		}
	}
	if entry.ID == "" {
		return agentConfigManifestEntry{}, false, errors.New("backup not found")
	}
	if len(entry.Sources) == 0 && len(entry.EnvKeys) == 0 {
		return agentConfigManifestEntry{}, false, errors.New("backup has no config content")
	}
	exists := false
	for _, source := range entry.Sources {
		sourcePath, err := expandUserPath(source.SourcePath)
		if err != nil {
			return agentConfigManifestEntry{}, false, err
		}
		if _, err := os.Stat(sourcePath); err == nil {
			exists = true
			break
		} else if err != nil && !os.IsNotExist(err) {
			return agentConfigManifestEntry{}, false, apperr.Wrap("stat", sourcePath, err)
		}
	}
	if exists && !req.ConfirmOverwrite {
		return entry, true, nil
	}
	configRoot, err := agentConfigRootDir()
	if err != nil {
		return agentConfigManifestEntry{}, false, err
	}
	// Preserve the selected Codex provider key while restoring config.toml.
	var codexStableProvider string
	var codexConfigPath string
	cfg, err := agent.LoadConfig("")
	if err != nil {
		return agentConfigManifestEntry{}, false, err
	}
	def, ok := cfg.GetAgent(entry.Agent)
	if !ok {
		return agentConfigManifestEntry{}, false, fmt.Errorf("agent not configured: %s", entry.Agent)
	}
	isCodex := def.Protocol == agent.ProtocolCodexSDK
	if isCodex {
		if home, homeErr := os.UserHomeDir(); homeErr == nil {
			codexConfigPath = filepath.Clean(filepath.Join(home, ".codex", "config.toml"))
		}
		for _, source := range entry.Sources {
			sourcePath, pathErr := expandUserPath(source.SourcePath)
			if pathErr != nil || codexConfigPath == "" || filepath.Clean(sourcePath) != codexConfigPath {
				continue
			}
			if payload, readErr := os.ReadFile(sourcePath); readErr == nil {
				codexStableProvider = codexModelProviderFromConfig(string(payload))
			}
			break
		}
	}
	for _, source := range entry.Sources {
		sourcePath, err := expandUserPath(source.SourcePath)
		if err != nil {
			return agentConfigManifestEntry{}, false, err
		}
		backupPath := filepath.Join(configRoot, filepath.FromSlash(source.BackupPath))
		if err := copyFile(backupPath, sourcePath); err != nil {
			return agentConfigManifestEntry{}, false, err
		}
		if codexStableProvider != "" && isCodex && codexConfigPath != "" && filepath.Clean(sourcePath) == codexConfigPath {
			payload, readErr := os.ReadFile(sourcePath)
			if readErr != nil {
				return agentConfigManifestEntry{}, false, apperr.Wrap("read", sourcePath, readErr)
			}
			normalized := preserveCodexModelProviderKey(string(payload), codexStableProvider)
			if err := os.WriteFile(sourcePath, []byte(normalized), 0o600); err != nil {
				return agentConfigManifestEntry{}, false, apperr.Wrap("write", sourcePath, err)
			}
		}
	}
	var env map[string]string
	if len(entry.EnvKeys) > 0 {
		envBackups, err := readAgentEnvBackups()
		if err != nil {
			return agentConfigManifestEntry{}, false, err
		}
		lines, ok := envBackups[entry.ID]
		if !ok {
			return agentConfigManifestEntry{}, false, errors.New("environment backup not found")
		}
		parsedEnv, _, err := envLinesToMap(lines)
		if err != nil {
			return agentConfigManifestEntry{}, false, err
		}
		env = parsedEnv
	}
	if err := updateAgentEnvConfig(entry.Agent, env); err != nil {
		return agentConfigManifestEntry{}, false, err
	}
	if app != nil && app.GetAgentPool() != nil {
		if err := app.GetAgentPool().SetAgentEnv(entry.Agent, env); err != nil {
			return agentConfigManifestEntry{}, false, err
		}
	}
	if app != nil && app.GetProber() != nil {
		if err := app.GetProber().SetAgentEnv(entry.Agent, env); err != nil {
			return agentConfigManifestEntry{}, false, err
		}
	}
	if app != nil && app.GetAgentPool() != nil {
		app.GetAgentPool().KillAgentProcess(entry.Agent, 0)
	}
	if app != nil && app.GetPreferences() != nil {
		if err := app.GetPreferences().UpdateAgentLastConfigSelection(entry.Agent, preferences.LastConfigSelection{
			Type: "backup",
			ID:   entry.ID,
			Name: entry.Name,
		}); err != nil {
			return agentConfigManifestEntry{}, false, err
		}
	}
	triggerAgentConfigSwitchProbe(app, entry.Agent)
	return entry, false, nil
}

func restartAgent(agentName string, app *AppContext) error {
	agentName = strings.TrimSpace(agentName)
	if agentName == "" {
		return errors.New("agent required")
	}
	if app == nil || app.GetAgentPool() == nil {
		return errors.New("agent pool not configured")
	}
	if _, ok := app.GetAgentPool().Config().GetAgent(agentName); !ok {
		return fmt.Errorf("agent not configured: %s", agentName)
	}
	app.GetAgentPool().KillAgentProcess(agentName, 0)
	triggerAgentConfigSwitchProbe(app, agentName)
	return nil
}

func triggerAgentConfigSwitchProbe(app *AppContext, agentName string) {
	if app == nil || app.GetProber() == nil {
		return
	}
	prober := app.GetProber()
	if err := prober.ClearProbeSession(agentName); err != nil {
		log.Printf("[agent-config] clear_probe_session.error agent=%s err=%v", agentName, err)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		status := prober.ProbeOne(ctx, agentName)
		if status.Error != "" {
			log.Printf("[agent-config] switch_probe.completed agent=%s available=%t err=%q", agentName, status.Available, status.Error)
			return
		}
		log.Printf("[agent-config] switch_probe.completed agent=%s available=%t", agentName, status.Available)
	}()
}
