package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mindfs/server/internal/apperr"
)

func applyGeminiAPIProvider(provider agentAPIProvider) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".gemini")
	envPath := filepath.Join(dir, ".env")
	settingsPath := filepath.Join(dir, "settings.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return apperr.Wrap("mkdir", dir, err)
	}
	env := map[string]string{}
	if payload, err := os.ReadFile(envPath); err == nil {
		env = parseSimpleEnv(string(payload))
	}
	env["GEMINI_API_KEY"] = provider.APIKey
	env["GOOGLE_GEMINI_BASE_URL"] = provider.BaseURL
	if len(provider.Models) > 0 {
		env["GEMINI_MODEL"] = provider.Models[0]
	}
	if err := os.WriteFile(envPath, []byte(formatSimpleEnv(env)), 0o600); err != nil {
		return apperr.Wrap("write", envPath, err)
	}
	settings := map[string]any{}
	if payload, err := os.ReadFile(settingsPath); err == nil && len(strings.TrimSpace(string(payload))) > 0 {
		_ = json.Unmarshal(payload, &settings)
	}
	security, _ := settings["security"].(map[string]any)
	if security == nil {
		security = map[string]any{}
	}
	auth, _ := security["auth"].(map[string]any)
	if auth == nil {
		auth = map[string]any{}
	}
	auth["selectedType"] = "gemini-api-key"
	security["auth"] = auth
	settings["security"] = security
	payload, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return apperr.Wrap("write", settingsPath, os.WriteFile(settingsPath, append(payload, '\n'), 0o600))
}

func applyQwenAPIProvider(provider agentAPIProvider) error {
	path, err := homePath(".qwen", "settings.json")
	if err != nil {
		return err
	}
	settings, err := readJSONObject(path)
	if err != nil {
		return err
	}
	envKey := "OPENAI_API_KEY"
	providerKey := "openai"
	protocol := "openai"
	baseURL := openAIModelsBaseURL(provider.BaseURL)
	if agentAPIProviderActiveProtocol(provider) == apiProviderProtocolAnthropicCompatible {
		envKey = "ANTHROPIC_API_KEY"
		providerKey = "anthropic"
		protocol = "anthropic"
		baseURL = anthropicBaseURL(provider.BaseURL)
	}
	env := ensureJSONObject(settings, "env")
	env[envKey] = provider.APIKey
	modelProviders := ensureJSONObject(settings, "modelProviders")
	modelProviders[providerKey] = map[string]any{
		"protocol": protocol,
		"models":   qwenModelEntries(provider, envKey, baseURL),
	}
	model := ensureJSONObject(settings, "model")
	model["name"] = firstModelOrDefault(provider.Models, "")
	return writeJSONObject(path, settings, 0o600)
}

func applyKimiAPIProvider(provider agentAPIProvider) error {
	path, err := homePath(".kimi", "config.toml")
	if err != nil {
		return err
	}
	existing, _ := os.ReadFile(path)
	providerName := agentAPIProviderConfigName(provider)
	model := firstModelOrDefault(provider.Models, "")
	providerType := "openai_legacy"
	baseURL := openAIModelsBaseURL(provider.BaseURL)
	if agentAPIProviderActiveProtocol(provider) == apiProviderProtocolAnthropicCompatible {
		providerType = "anthropic"
		baseURL = anthropicBaseURL(provider.BaseURL)
	}
	block := fmt.Sprintf(`# BEGIN MINDFS API PROVIDER
default_model = %q

%s
type = %q
base_url = %q
api_key = %q

%s
provider = %q
model = %q
# END MINDFS API PROVIDER
`, providerName, tomlDottedTable("providers", providerName), providerType, baseURL, provider.APIKey, tomlDottedTable("models", providerName), providerName, model)
	return writeTextFile(path, replaceManagedBlock(string(existing), block, "# BEGIN MINDFS API PROVIDER", "# END MINDFS API PROVIDER"), 0o600)
}

func applyOpenCodeAPIProvider(provider agentAPIProvider) error {
	path, err := homePath(".config", "opencode", "opencode.json")
	if err != nil {
		return err
	}
	cfg, err := readJSONObject(path)
	if err != nil {
		return err
	}
	npm := "@ai-sdk/openai-compatible"
	baseURL := openAIModelsBaseURL(provider.BaseURL)
	if agentAPIProviderActiveProtocol(provider) == apiProviderProtocolAnthropicCompatible {
		npm = "@ai-sdk/anthropic"
		baseURL = anthropicBaseURL(provider.BaseURL)
	}
	providerName := agentAPIProviderConfigName(provider)
	providers := ensureJSONObject(cfg, "provider")
	providers[providerName] = map[string]any{
		"npm":  npm,
		"name": provider.Name,
		"options": map[string]any{
			"baseURL": baseURL,
			"apiKey":  provider.APIKey,
		},
		"models": modelObject(provider.Models),
	}
	if model := firstModelOrDefault(provider.Models, ""); model != "" {
		cfg["model"] = providerName + "/" + model
	}
	return writeJSONObject(path, cfg, 0o600)
}

func applyOpenClawAPIProvider(provider agentAPIProvider) error {
	path, err := homePath(".openclaw", "openclaw.json")
	if err != nil {
		return err
	}
	cfg, err := readJSONObject(path)
	if err != nil {
		return err
	}
	activeProtocol := agentAPIProviderActiveProtocol(provider)
	apiMode := additiveAgentAPI(activeProtocol)
	baseURL := provider.BaseURL
	if activeProtocol == apiProviderProtocolAnthropicCompatible {
		baseURL = anthropicBaseURL(provider.BaseURL)
	}
	models := ensureJSONObject(cfg, "models")
	if _, ok := models["mode"]; !ok {
		models["mode"] = "merge"
	}
	providerName := agentAPIProviderConfigName(provider)
	providers := ensureJSONObject(models, "providers")
	providers[providerName] = map[string]any{
		"baseUrl": baseURL,
		"apiKey":  provider.APIKey,
		"api":     apiMode,
		"models":  modelArray(provider.Models),
	}
	agents := ensureJSONObject(cfg, "agents")
	defaults := ensureJSONObject(agents, "defaults")
	if model := firstModelOrDefault(provider.Models, ""); model != "" {
		defaults["model"] = map[string]any{
			"primary":   providerName + "/" + model,
			"fallbacks": []any{},
		}
		catalog := ensureJSONObject(defaults, "models")
		for _, item := range provider.Models {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			key := providerName + "/" + item
			if _, exists := catalog[key]; !exists {
				catalog[key] = map[string]any{"alias": item}
			}
		}
	}
	return writeJSONObject(path, cfg, 0o600)
}

func applyOMPAPIProvider(provider agentAPIProvider) error {
	path, err := homePath(".omp", "agent", "models.yml")
	if err != nil {
		return err
	}
	cfg, err := readYAMLMap(path)
	if err != nil {
		return err
	}
	activeProtocol := agentAPIProviderActiveProtocol(provider)
	baseURL := provider.BaseURL
	if activeProtocol == apiProviderProtocolAnthropicCompatible {
		baseURL = anthropicBaseURL(provider.BaseURL)
	}
	providerName := agentAPIProviderConfigName(provider)
	providers := ensureYAMLMap(cfg, "providers")
	providers[providerName] = map[string]any{
		"baseUrl":    baseURL,
		"apiKey":     provider.APIKey,
		"api":        additiveAgentAPI(activeProtocol),
		"authHeader": activeProtocol == apiProviderProtocolOpenAICompatible,
		"models":     modelArray(provider.Models),
	}
	if model := firstModelOrDefault(provider.Models, ""); model != "" {
		roles := ensureYAMLMap(cfg, "modelRoles")
		roles["main"] = providerName + "/" + model
	}
	return writeYAMLMap(path, cfg, 0o600)
}

func applyPiAPIProvider(provider agentAPIProvider) error {
	modelsPath, err := homePath(".pi", "agent", "models.json")
	if err != nil {
		return err
	}
	cfg, err := readJSONObject(modelsPath)
	if err != nil {
		return err
	}
	providerName := agentAPIProviderConfigName(provider)
	apiMode := piAgentAPI(agentAPIProviderActiveProtocol(provider))
	baseURL := piAgentBaseURL(provider)
	providers := ensureJSONObject(cfg, "providers")
	providers[providerName] = map[string]any{
		"baseUrl": baseURL,
		"apiKey":  provider.APIKey,
		"api":     apiMode,
		"models":  modelArray(provider.Models),
	}
	if err := writeJSONObject(modelsPath, cfg, 0o600); err != nil {
		return err
	}
	if model := firstModelOrDefault(provider.Models, ""); model != "" {
		settingsPath, err := homePath(".pi", "agent", "settings.json")
		if err != nil {
			return err
		}
		settings, err := readJSONObject(settingsPath)
		if err != nil {
			return err
		}
		settings["defaultProvider"] = providerName
		settings["defaultModel"] = model
		if err := writeJSONObject(settingsPath, settings, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func applyHermesAPIProvider(provider agentAPIProvider) error {
	path, err := homePath(".hermes", "config.yaml")
	if err != nil {
		return err
	}
	cfg, err := readYAMLMap(path)
	if err != nil {
		return err
	}
	providerName := agentAPIProviderConfigName(provider)
	model := firstModelOrDefault(provider.Models, "")
	entry := map[string]any{
		"name":     providerName,
		"base_url": provider.BaseURL,
		"api_key":  provider.APIKey,
		"api_mode": "chat_completions",
	}
	if model != "" {
		entry["model"] = model
	}
	modelCfg := ensureYAMLMap(cfg, "model")
	modelCfg["api_key"] = provider.APIKey
	modelCfg["api_mode"] = "chat_completions"
	modelCfg["base_url"] = provider.BaseURL
	modelCfg["provider"] = "custom"
	if model != "" {
		modelCfg["default"] = model
	}
	cfg["custom_providers"] = replaceNamedYAMLSequenceEntry(cfg["custom_providers"], providerName, entry)
	return writeYAMLMap(path, cfg, 0o600)
}
