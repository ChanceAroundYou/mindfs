package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"mindfs/server/internal/agent"
	"mindfs/server/internal/apperr"
	configpkg "mindfs/server/internal/config"
)

// apperrInvalid 把校验失败包成 apperr.Error，使 respondError 走中文消息层，
// 而不是把英文原文直接给用户看（backend/TODO.md 2026-10-15 发现的那条真 TODO）。
func apperrInvalid(message string) error {
	return &apperr.Error{Code: "invalid_request", Message: message}
}

type agentConfigSource struct {
	SourcePath string `json:"sourcePath"`
	BackupPath string `json:"backupPath"`
}

type agentConfigManifestEntry struct {
	ID        string              `json:"id"`
	Agent     string              `json:"agent"`
	Name      string              `json:"name"`
	CreatedAt string              `json:"createdAt"`
	UpdatedAt string              `json:"updatedAt"`
	Sources   []agentConfigSource `json:"sources,omitempty"`
	EnvKeys   []string            `json:"envKeys,omitempty"`
}

type agentConfigBackupRequest struct {
	Agent       string   `json:"agent"`
	Name        string   `json:"name"`
	FileSources []string `json:"file_sources"`
	EnvLines    []string `json:"env_lines"`
	Overwrite   bool     `json:"overwrite"`
}

type agentConfigSwitchRequest struct {
	ID               string `json:"id"`
	ConfirmOverwrite bool   `json:"confirm_overwrite"`
}

type agentRestartRequest struct {
	Agent string `json:"agent"`
}

var agentConfigNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func (h *HTTPHandler) handleAgentConfigDefaults(w http.ResponseWriter, r *http.Request) {
	agentName := strings.TrimSpace(r.URL.Query().Get("agent"))
	if agentName == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("agent required"))
		return
	}
	cfg, err := agent.LoadConfig("")
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, err)
		return
	}
	def, ok := cfg.GetAgent(agentName)
	if !ok {
		respondError(w, http.StatusNotFound, errInvalidRequest("agent not configured"))
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"agent":        def.Name,
		"file_sources": existingDefaultFileSources(def.ConfigBackup.FileSources),
		"env_keys":     def.ConfigBackup.EnvKeys,
	})
}

var errAgentConfigConflict = errors.New("备份已存在")

func readAgentConfigManifest() ([]agentConfigManifestEntry, error) {
	path, err := agentConfigManifestPath()
	if err != nil {
		return nil, err
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []agentConfigManifestEntry{}, nil
		}
		return nil, apperr.Wrap("read", path, err)
	}
	var manifest []agentConfigManifestEntry
	if len(strings.TrimSpace(string(payload))) == 0 {
		return []agentConfigManifestEntry{}, nil
	}
	if err := json.Unmarshal(payload, &manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

func writeAgentConfigManifest(manifest []agentConfigManifestEntry) error {
	path, err := agentConfigManifestPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return apperr.Wrap("mkdir", filepath.Dir(path), err)
	}
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return apperr.Wrap("write", path, os.WriteFile(path, payload, 0o644))
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return apperr.Wrap("open", src, err)
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return apperr.Wrap("mkdir", filepath.Dir(dst), err)
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return apperr.Wrap("write", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return apperr.Wrap("copy", dst, err)
	}
	return apperr.Wrap("write", dst, out.Close())
}

func agentConfigRootDir() (string, error) {
	configDir, err := configpkg.MindFSConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "agents-config"), nil
}

func agentConfigManifestPath() (string, error) {
	root, err := agentConfigRootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "manifest.json"), nil
}

func agentEnvPath() (string, error) {
	configDir, err := configpkg.MindFSConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "agents-env.json"), nil
}
