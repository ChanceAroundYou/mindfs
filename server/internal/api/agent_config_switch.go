package api

import (
	"context"
	"encoding/json"
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
	respondJSON(w, http.StatusAccepted, map[string]any{
		"restarting": true,
		"agent":      strings.TrimSpace(req.Agent),
	})
}

func switchAgentConfig(req agentConfigSwitchRequest, app *AppContext) (agentConfigManifestEntry, bool, error) {
	id := strings.TrimSpace(req.ID)
	if id == "" {
		return agentConfigManifestEntry{}, false, apperrInvalid("请提供备份 ID")
	}
	if strings.HasPrefix(id, "shared:codex-") {
		entry, err := switchSharedCodexConfig(id, req.Agent, app)
		return entry, false, err
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
		return agentConfigManifestEntry{}, false, apperrInvalid("备份不存在")
	}
	if len(entry.Sources) == 0 && len(entry.EnvKeys) == 0 {
		return agentConfigManifestEntry{}, false, apperrInvalid("备份没有配置内容")
	}
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
		return agentConfigManifestEntry{}, false, apperrInvalid("Codex 本机恢复快照不能作为切换目标")
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
	for _, source := range entry.Sources {
		sourcePath, err := expandUserPath(source.SourcePath)
		if err != nil {
			return agentConfigManifestEntry{}, false, err
		}
		backupPath := filepath.Join(configRoot, filepath.FromSlash(source.BackupPath))
		if err := copyFile(backupPath, sourcePath); err != nil {
			return agentConfigManifestEntry{}, false, err
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
			return agentConfigManifestEntry{}, false, apperrInvalid("环境备份不存在")
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

// agentRestartHooks 把「关会话」和「重新探测」拆成可注入的两段，便于钉住
// 重启请求不等慢速的 KillAgentProcess。
type agentRestartHooks struct {
	kill  func(string)
	probe func(string)
}

// scheduleAgentRestart 让重启在后台串行执行：先关旧会话，再探测新进程。
//
// KillAgentProcess 要拿 agent 的运行锁；Windows 上 CLI 启动要 20s 以上，
// 连续点两次重启时第二次会一直等这把锁。前端跨节点请求只有 10s deadline
// （见 web/src/services/net/api.ts），同步等就会出现「重启提示超时」。
// 端点已经返回 restarting:true，真实动作异步完成正好符合这个语义。
func scheduleAgentRestart(agentName string, hooks agentRestartHooks) {
	go func() {
		if hooks.kill != nil {
			hooks.kill(agentName)
		}
		if hooks.probe != nil {
			hooks.probe(agentName)
		}
	}()
}

func restartAgent(agentName string, app *AppContext) error {
	agentName = strings.TrimSpace(agentName)
	if agentName == "" {
		return apperrInvalid("请指定 agent")
	}
	if app == nil || app.GetAgentPool() == nil {
		return apperrInvalid("agent pool 未配置")
	}
	if _, ok := app.GetAgentPool().Config().GetAgent(agentName); !ok {
		return fmt.Errorf("agent not configured: %s", agentName)
	}
	scheduleAgentRestart(agentName, agentRestartHooks{
		kill: func(name string) {
			app.GetAgentPool().KillAgentProcess(name, 0)
		},
		probe: func(name string) {
			triggerAgentConfigSwitchProbe(app, name)
		},
	})
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
