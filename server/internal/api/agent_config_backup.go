package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mindfs/server/internal/agent"
	"mindfs/server/internal/apperr"
)

func (h *HTTPHandler) handleAgentConfigBackupsList(w http.ResponseWriter, r *http.Request) {
	agentName := strings.TrimSpace(r.URL.Query().Get("agent"))
	manifest, err := readAgentConfigManifest()
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, err)
		return
	}
	if agentName == "" {
		respondJSON(w, http.StatusOK, manifest)
		return
	}
	filtered := make([]agentConfigManifestEntry, 0, len(manifest))
	for _, item := range manifest {
		if item.Agent == agentName {
			filtered = append(filtered, item)
		}
	}
	respondJSON(w, http.StatusOK, filtered)
}

func (h *HTTPHandler) handleAgentConfigBackupCreate(w http.ResponseWriter, r *http.Request) {
	var req agentConfigBackupRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxUploadRequestBytes)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid request body"))
		return
	}
	entry, err := createAgentConfigBackup(req)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errAgentConfigConflict) {
			status = http.StatusConflict
		}
		respondError(w, status, err)
		return
	}
	respondJSON(w, http.StatusOK, entry)
}

func (h *HTTPHandler) handleAgentConfigBackupDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("backup id required"))
		return
	}
	manifest, err := deleteAgentConfigBackup(id)
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id, "backups": manifest})
}

func createAgentConfigBackup(req agentConfigBackupRequest) (agentConfigManifestEntry, error) {
	agentName, backupName, id, err := normalizeAgentConfigRequest(req.Agent, req.Name)
	if err != nil {
		return agentConfigManifestEntry{}, err
	}
	cfg, err := agent.LoadConfig("")
	if err != nil {
		return agentConfigManifestEntry{}, err
	}
	if _, ok := cfg.GetAgent(agentName); !ok {
		return agentConfigManifestEntry{}, fmt.Errorf("agent not configured: %s", agentName)
	}
	configRoot, err := agentConfigRootDir()
	if err != nil {
		return agentConfigManifestEntry{}, err
	}
	manifest, err := readAgentConfigManifest()
	if err != nil {
		return agentConfigManifestEntry{}, err
	}
	existingIndex := -1
	var createdAt string
	for index, item := range manifest {
		if item.ID == id {
			if !req.Overwrite {
				return agentConfigManifestEntry{}, errAgentConfigConflict
			}
			existingIndex = index
			createdAt = item.CreatedAt
			break
		}
	}
	now := time.Now().Format(time.RFC3339)
	if createdAt == "" {
		createdAt = now
	}
	entry := agentConfigManifestEntry{
		ID:        id,
		Agent:     agentName,
		Name:      backupName,
		CreatedAt: createdAt,
		UpdatedAt: now,
	}
	sources, err := normalizeFileSources(req.FileSources)
	if err != nil {
		return agentConfigManifestEntry{}, err
	}
	envLines, envKeys, err := normalizeEnvLines(req.EnvLines)
	if err != nil {
		return agentConfigManifestEntry{}, err
	}
	if len(sources) == 0 && len(envLines) == 0 {
		return agentConfigManifestEntry{}, errors.New("config source or environment variables required")
	}
	if err := os.RemoveAll(filepath.Join(configRoot, id)); err != nil {
		return agentConfigManifestEntry{}, apperr.Wrap("remove", filepath.Join(configRoot, id), err)
	}
	for index, source := range sources {
		name := fmt.Sprintf("%03d-%s", index+1, filepath.Base(source))
		rel := filepath.Join(id, name)
		dst := filepath.Join(configRoot, rel)
		if err := copyFile(source, dst); err != nil {
			return agentConfigManifestEntry{}, err
		}
		entry.Sources = append(entry.Sources, agentConfigSource{
			SourcePath: source,
			BackupPath: filepath.ToSlash(rel),
		})
	}
	envMap, err := readAgentEnvBackups()
	if err != nil {
		return agentConfigManifestEntry{}, err
	}
	if len(envLines) > 0 {
		envMap[id] = envLines
		entry.EnvKeys = envKeys
	} else {
		delete(envMap, id)
	}
	if err := writeAgentEnvBackups(envMap); err != nil {
		return agentConfigManifestEntry{}, err
	}
	if err := updateAgentConfigDefaults(agentName, sources, envKeys); err != nil {
		return agentConfigManifestEntry{}, err
	}
	if existingIndex >= 0 {
		manifest[existingIndex] = entry
	} else {
		manifest = append(manifest, entry)
	}
	if err := writeAgentConfigManifest(manifest); err != nil {
		return agentConfigManifestEntry{}, err
	}
	return entry, nil
}

func deleteAgentConfigBackup(id string) ([]agentConfigManifestEntry, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("backup id required")
	}
	manifest, err := readAgentConfigManifest()
	if err != nil {
		return nil, err
	}
	next := make([]agentConfigManifestEntry, 0, len(manifest))
	found := false
	for _, item := range manifest {
		if item.ID == id {
			found = true
			continue
		}
		next = append(next, item)
	}
	if !found {
		return nil, errors.New("backup not found")
	}
	if err := writeAgentConfigManifest(next); err != nil {
		return nil, err
	}
	configRoot, err := agentConfigRootDir()
	if err != nil {
		return nil, err
	}
	if err := os.RemoveAll(filepath.Join(configRoot, id)); err != nil {
		return nil, apperr.Wrap("remove", filepath.Join(configRoot, id), err)
	}
	envBackups, err := readAgentEnvBackups()
	if err != nil {
		return nil, err
	}
	if _, ok := envBackups[id]; ok {
		delete(envBackups, id)
		if err := writeAgentEnvBackups(envBackups); err != nil {
			return nil, err
		}
	}
	return next, nil
}

func readAgentEnvBackups() (map[string][]string, error) {
	path, err := agentEnvPath()
	if err != nil {
		return nil, err
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string][]string{}, nil
		}
		return nil, apperr.Wrap("read", path, err)
	}
	if len(strings.TrimSpace(string(payload))) == 0 {
		return map[string][]string{}, nil
	}
	var out map[string][]string
	if err := json.Unmarshal(payload, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string][]string{}
	}
	return out, nil
}

func writeAgentEnvBackups(env map[string][]string) error {
	path, err := agentEnvPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return apperr.Wrap("mkdir", filepath.Dir(path), err)
	}
	payload, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return apperr.Wrap("write", path, os.WriteFile(path, payload, 0o644))
}
