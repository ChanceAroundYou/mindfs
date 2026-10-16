package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"mindfs/server/internal/apperr"
	configpkg "mindfs/server/internal/config"
)

const (
	apiProviderProtocolOpenAICompatible    = "openai-compatible"
	apiProviderProtocolAnthropicCompatible = "anthropic-compatible"
	apiProviderProtocolGeminiCompatible    = "gemini-compatible"
)

type agentAPIProvider struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	BaseURL        string   `json:"baseUrl"`
	APIKey         string   `json:"apiKey,omitempty"`
	Protocols      []string `json:"protocols,omitempty"`
	ModelFamilies  []string `json:"modelFamilies"`
	Models         []string `json:"models"`
	CreatedAt      string   `json:"createdAt"`
	UpdatedAt      string   `json:"updatedAt"`
	activeProtocol string
}

type agentAPIProviderPublic struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	BaseURL       string   `json:"baseUrl"`
	Protocols     []string `json:"protocols,omitempty"`
	ModelFamilies []string `json:"modelFamilies"`
	Models        []string `json:"models,omitempty"`
	CreatedAt     string   `json:"createdAt"`
	UpdatedAt     string   `json:"updatedAt"`
}

type agentAPIProviderCreateRequest struct {
	Name    string `json:"name"`
	BaseURL string `json:"baseUrl"`
	APIKey  string `json:"apiKey"`
}

type agentAPIProviderSyncRequest struct {
	Providers []agentAPIProviderCreateRequest `json:"providers"`
}

type agentAPIProviderSwitchRequest struct {
	Agent      string `json:"agent"`
	ProviderID string `json:"provider_id"`
}

type agentAPIProviderSyncAgentResult struct {
	Agent   string `json:"agent"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

type agentAPIProviderSyncAllResult struct {
	ID         string                            `json:"id"`
	Name       string                            `json:"name"`
	Success    bool                              `json:"success"`
	Error      string                            `json:"error,omitempty"`
	ModelCount int                               `json:"modelCount,omitempty"`
	Applied    []agentAPIProviderSyncAgentResult `json:"applied,omitempty"`
}

func readAgentAPIProviders() ([]agentAPIProvider, error) {
	path, err := agentAPIProvidersPath()
	if err != nil {
		return nil, err
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []agentAPIProvider{}, nil
		}
		return nil, apperr.Wrap("read", path, err)
	}
	if len(strings.TrimSpace(string(payload))) == 0 {
		return []agentAPIProvider{}, nil
	}
	var providers []agentAPIProvider
	if err := json.Unmarshal(payload, &providers); err != nil {
		return nil, err
	}
	for i := range providers {
		providers[i].Protocols = agentAPIProviderProtocols(providers[i])
	}
	return providers, nil
}

func writeAgentAPIProviders(providers []agentAPIProvider) error {
	path, err := agentAPIProvidersPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return apperr.Wrap("mkdir", filepath.Dir(path), err)
	}
	payload, err := json.MarshalIndent(providers, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o600); err != nil {
		return apperr.Wrap("write", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(path)
		if retryErr := os.Rename(tmp, path); retryErr != nil {
			return apperr.Wrap("rename", path, err)
		}
	}
	return nil
}

func agentAPIProvidersPath() (string, error) {
	configDir, err := configpkg.MindFSConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "api-providers.json"), nil
}

func publicAgentAPIProvider(provider agentAPIProvider) agentAPIProviderPublic {
	return agentAPIProviderPublic{
		ID:            provider.ID,
		Name:          provider.Name,
		BaseURL:       provider.BaseURL,
		Protocols:     agentAPIProviderProtocols(provider),
		ModelFamilies: append([]string(nil), provider.ModelFamilies...),
		Models:        append([]string(nil), provider.Models...),
		CreatedAt:     provider.CreatedAt,
		UpdatedAt:     provider.UpdatedAt,
	}
}

func normalizedAPIProviderAgent(agentName string) string {
	name := strings.ToLower(strings.TrimSpace(agentName))
	switch name {
	case "claude", "claudecode", "claude-code":
		return "claude"
	case "github-copilot", "copilot-cli":
		return "copilot"
	case "pi", "pi-acp", "pi-coding-agent":
		return "pi"
	case "qwen-code", "qwencode":
		return "qwen"
	case "kimi-code":
		return "kimi"
	default:
		return name
	}
}

func slugifyAPIProviderName(name string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '.' || r == '_' || r == '-' || r == ' ':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func normalizeModelIDs(input []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(input))
	for _, item := range input {
		id := strings.TrimSpace(item)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func inferModelFamilies(models []string) []string {
	seen := map[string]bool{}
	var families []string
	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			families = append(families, name)
		}
	}
	for _, model := range models {
		lower := strings.ToLower(model)
		switch {
		case strings.Contains(lower, "deepseek"):
			add("deepseek")
		case strings.Contains(lower, "claude"):
			add("anthropic")
		case strings.Contains(lower, "gemini"):
			add("gemini")
		case strings.Contains(lower, "qwen"):
			add("qwen")
		case strings.Contains(lower, "glm") || strings.Contains(lower, "zhipu") || strings.Contains(lower, "bigmodel"):
			add("glm")
		case strings.Contains(lower, "kimi") || strings.Contains(lower, "moonshot"):
			add("kimi")
		case strings.Contains(lower, "doubao") || strings.Contains(lower, "seed"):
			add("doubao")
		case strings.HasPrefix(lower, "gpt-") || strings.HasPrefix(lower, "o1") || strings.HasPrefix(lower, "o3") || strings.HasPrefix(lower, "o4"):
			add("openai")
		}
	}
	if len(families) == 0 {
		families = append(families, "unknown")
	}
	sort.Strings(families)
	return families
}
