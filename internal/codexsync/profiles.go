package codexsync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

const maxSharedConfigBytes = 1 << 20

// SharedProfileForProviderAt reports whether an API-provider entry describes the
// same endpoint as a shared Codex profile, so switching that provider is
// equivalent to switching the shared profile. Only llmux carries a provider
// table in the shared configuration.
func SharedProfileForProviderAt(paths Paths, name, baseURL string) (string, bool, error) {
	if !strings.EqualFold(strings.TrimSpace(name), "llmux") {
		return "", false, nil
	}
	payload, err := ReadSharedCodexProfileAt(paths, "llmux")
	if err != nil {
		return "", false, err
	}
	if payload == nil {
		return "", false, nil
	}
	root, err := decodeTOML(payload)
	if err != nil {
		return "", false, err
	}
	providers := tomlTable(root["model_providers"])
	llmux := tomlTable(providers["llmux"])
	profileBaseURL, _ := llmux["base_url"].(string)
	normalizedBaseURL := normalizeProviderBaseURL(baseURL)
	if normalizedBaseURL == "" {
		// 有的供应商元数据只带名字（历史条目或从未填 base URL）。名字就是
		// profile 的唯一标识，按名字认领，否则这类条目永远只能报 metadata-only，
		// 用户既切不过去也切不回来。
		return "llmux", true, nil
	}
	if normalizeProviderBaseURL(profileBaseURL) == "" || normalizeProviderBaseURL(profileBaseURL) != normalizedBaseURL {
		return "", false, nil
	}
	return "llmux", true, nil
}

func SharedProfileForProvider(name, baseURL string) (string, bool, error) {
	paths, err := ResolvePaths()
	if err != nil {
		return "", false, err
	}
	return SharedProfileForProviderAt(paths, name, baseURL)
}

func normalizeProviderBaseURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

func ProjectProfile(name string, snapshot []byte) ([]byte, error) {
	root, err := decodeTOML(snapshot)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	for _, key := range []string{"model", "model_reasoning_effort"} {
		if err := copyOptionalString(out, root, key); err != nil {
			return nil, err
		}
	}

	switch strings.ToLower(strings.TrimSpace(name)) {
	case "default":
		if features, ok := root["features"]; ok {
			featureTable, ok := features.(map[string]any)
			if !ok {
				return nil, errors.New("features must be a TOML table")
			}
			if goals, exists := featureTable["goals"]; exists {
				if _, ok := goals.(bool); !ok {
					return nil, errors.New("features.goals must be a boolean")
				}
				out["features"] = map[string]any{"goals": goals}
			}
		}
	case "llmux":
		providerID, ok := root["model_provider"].(string)
		if !ok || providerID != "llmux" {
			return nil, errors.New("llmux snapshot must select model_provider = llmux")
		}
		out["model_provider"] = "llmux"
		providers, ok := root["model_providers"].(map[string]any)
		if !ok {
			return nil, errors.New("llmux snapshot is missing model_providers.llmux")
		}
		llmux, ok := providers["llmux"].(map[string]any)
		if !ok {
			return nil, errors.New("llmux snapshot is missing model_providers.llmux")
		}
		profileProvider := map[string]any{}
		for _, key := range []string{"name", "base_url", "wire_api"} {
			value, ok := llmux[key].(string)
			if !ok || strings.TrimSpace(value) == "" {
				return nil, fmt.Errorf("model_providers.llmux.%s is required", key)
			}
			profileProvider[key] = value
		}
		profileProvider["env_key"] = "LLMUX_API_KEY"
		out["model_providers"] = map[string]any{"llmux": profileProvider}
	default:
		return nil, fmt.Errorf("unsupported Codex profile %q", name)
	}

	projected, err := toml.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("encode shared TOML: %w", err)
	}
	if err := ValidateSharedProfile(name, projected); err != nil {
		return nil, err
	}
	return projected, nil
}

func ExportSharedProfile(name string, snapshot []byte) error {
	paths, err := ResolvePaths()
	if err != nil {
		return err
	}
	return ExportSharedProfileAt(paths, name, snapshot)
}

func ExportSharedProfileAt(paths Paths, name string, snapshot []byte) error {
	return WithSyncLock(paths, func() error {
		return exportSharedProfileAtLocked(paths, name, snapshot)
	})
}

func exportSharedProfileAtLocked(paths Paths, name string, snapshot []byte) error {
	projected, err := ProjectProfile(name, snapshot)
	if err != nil {
		return err
	}
	path := paths.SharedDefault
	if strings.EqualFold(strings.TrimSpace(name), "llmux") {
		path = paths.SharedLLMux
	}
	repoPath, err := filepath.Rel(paths.AgentsRoot, path)
	if err != nil {
		return err
	}
	repoPath = filepath.ToSlash(repoPath)
	changed, err := worktreePaths(context.Background(), paths.AgentsRoot)
	if err != nil {
		return err
	}
	for _, dirty := range changed {
		if dirty == repoPath {
			return fmt.Errorf("shared profile %s already has local edits; export stopped to preserve them", name)
		}
	}
	if filesEqual(path, projected) {
		return nil
	}
	if err := saveRollbackCopy(paths.StateDir, path); err != nil {
		return err
	}
	return atomicWriteFile(path, projected, 0o644)
}

func ValidateSharedProfile(kind string, payload []byte) error {
	root, err := decodeTOML(payload)
	if err != nil {
		return err
	}
	if err := rejectCredentialKeys(root); err != nil {
		return err
	}
	allowedRoot := map[string]bool{"model": true, "model_reasoning_effort": true}
	for _, key := range []string{"model", "model_reasoning_effort"} {
		if value, exists := root[key]; exists {
			text, ok := value.(string)
			if !ok || strings.TrimSpace(text) == "" {
				return fmt.Errorf("%s must be a non-empty string", key)
			}
		}
	}
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "default":
		allowedRoot["features"] = true
		for key := range root {
			if !allowedRoot[key] {
				return fmt.Errorf("unsupported shared default key %q", key)
			}
		}
		if features, ok := root["features"]; ok {
			featureTable, ok := features.(map[string]any)
			if !ok {
				return errors.New("features must be a TOML table")
			}
			for key, value := range featureTable {
				if key != "goals" {
					return fmt.Errorf("unsupported shared feature key %q", key)
				}
				if _, ok := value.(bool); !ok {
					return errors.New("features.goals must be a boolean")
				}
			}
		}
	case "llmux":
		allowedRoot["model_provider"] = true
		allowedRoot["model_providers"] = true
		for key := range root {
			if !allowedRoot[key] {
				return fmt.Errorf("unsupported shared llmux key %q", key)
			}
		}
		if root["model_provider"] != "llmux" {
			return errors.New("shared llmux profile must select model_provider = llmux")
		}
		providers, ok := root["model_providers"].(map[string]any)
		if !ok || len(providers) != 1 {
			return errors.New("shared llmux profile must contain only model_providers.llmux")
		}
		provider, ok := providers["llmux"].(map[string]any)
		if !ok {
			return errors.New("shared llmux provider table is missing")
		}
		allowedProvider := map[string]bool{"name": true, "base_url": true, "wire_api": true, "env_key": true}
		for key, value := range provider {
			if !allowedProvider[key] {
				return fmt.Errorf("unsupported shared llmux provider key %q", key)
			}
			if _, ok := value.(string); !ok {
				return fmt.Errorf("model_providers.llmux.%s must be a string", key)
			}
		}
		if provider["env_key"] != "LLMUX_API_KEY" {
			return errors.New("shared llmux profile must use env_key = LLMUX_API_KEY")
		}
		for _, key := range []string{"name", "base_url", "wire_api"} {
			value, ok := provider[key].(string)
			if !ok || strings.TrimSpace(value) == "" {
				return fmt.Errorf("model_providers.llmux.%s is required", key)
			}
		}
	default:
		return fmt.Errorf("unsupported Codex profile %q", kind)
	}
	return nil
}

func ValidateProviderMetadata(payload []byte) error {
	if len(payload) > maxSharedConfigBytes {
		return errors.New("provider metadata exceeds 1 MiB")
	}
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return errors.New("provider metadata must be one JSON array")
	}
	type providerMetadata struct {
		ID            string   `json:"id"`
		Name          string   `json:"name"`
		BaseURL       string   `json:"baseUrl"`
		Protocols     []string `json:"protocols"`
		ModelFamilies []string `json:"modelFamilies"`
		Models        []string `json:"models"`
		CreatedAt     string   `json:"createdAt"`
		UpdatedAt     string   `json:"updatedAt"`
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var providers []providerMetadata
	if err := decoder.Decode(&providers); err != nil {
		return fmt.Errorf("decode provider metadata: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("provider metadata must contain one JSON array")
	}
	seen := make(map[string]struct{}, len(providers))
	for i, provider := range providers {
		if strings.TrimSpace(provider.ID) == "" || strings.TrimSpace(provider.Name) == "" || strings.TrimSpace(provider.BaseURL) == "" {
			return fmt.Errorf("provider metadata entry %d is missing an id, name, or baseUrl", i)
		}
		parsedURL, err := url.Parse(provider.BaseURL)
		if err != nil || parsedURL.Host == "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.User != nil || parsedURL.RawQuery != "" || parsedURL.Fragment != "" {
			return fmt.Errorf("provider metadata entry %d has an unsafe baseUrl", i)
		}
		if _, exists := seen[provider.ID]; exists {
			return fmt.Errorf("provider metadata contains duplicate id %q", provider.ID)
		}
		seen[provider.ID] = struct{}{}
	}
	return nil
}

// allowedRepoPaths / allowedRepoPrefixes 是共享仓库里允许 MindFS 提交/应用的
// 路径。仓库本身就是「跨设备共享配置」专用，任何未登记的路径都视为无关改动并
// 阻止同步。Codex 只允许三个受管文件（其余快照、缓存不得进 Git）；新增 harness
// 时在前缀表里登记它的共享目录（并补 harness adapter）。
var allowedRepoPaths = map[string]bool{
	"codex/config.toml":                true,
	"codex/profiles/llmux.config.toml": true,
	"codex/providers.json":             true,
}

var allowedRepoPrefixes = []string{
	"claude/",
	"dsh/",
	"skills/",
	"scripts/",
}

func IsAllowedRepoPath(repoPath string) bool {
	repoPath, ok := normalizeRepoPath(repoPath)
	if !ok {
		return false
	}
	if allowedRepoPaths[repoPath] {
		return true
	}
	base := filepath.Base(repoPath)
	if strings.HasSuffix(base, ".tmp") || strings.HasSuffix(base, ".replace-backup") {
		return false
	}
	for _, prefix := range allowedRepoPrefixes {
		if strings.HasPrefix(repoPath, prefix) {
			return true
		}
	}
	return false
}

func decodeTOML(payload []byte) (map[string]any, error) {
	if len(payload) > maxSharedConfigBytes {
		return nil, errors.New("TOML exceeds 1 MiB")
	}
	var root map[string]any
	if err := toml.Unmarshal(payload, &root); err != nil {
		return nil, fmt.Errorf("decode TOML: %w", err)
	}
	if root == nil {
		root = map[string]any{}
	}
	return root, nil
}

func copyOptionalString(target, source map[string]any, key string) error {
	value, exists := source[key]
	if !exists {
		return nil
	}
	text, ok := value.(string)
	if !ok {
		return fmt.Errorf("%s must be a string", key)
	}
	if strings.TrimSpace(text) != "" {
		target[key] = text
	}
	return nil
}

func rejectCredentialKeys(value any) error {
	for key, child := range tomlTable(value) {
		normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
		switch normalized {
		case "apikey", "accesstoken", "refreshtoken", "bearertoken", "experimentalbearertoken", "clientsecret", "password":
			return fmt.Errorf("shared TOML contains forbidden credential field %q", key)
		}
		if err := rejectCredentialKeys(child); err != nil {
			return err
		}
	}
	return nil
}

func tomlTable(value any) map[string]any {
	if table, ok := value.(map[string]any); ok {
		return table
	}
	return nil
}
