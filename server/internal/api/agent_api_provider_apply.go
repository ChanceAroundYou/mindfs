package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"mindfs/server/internal/agent"
	"mindfs/server/internal/apperr"
)

func applyAgentAPIProvider(agentName string, provider agentAPIProvider, app *AppContext) error {
	switch normalizedAPIProviderAgent(agentName) {
	case "codex":
		if err := applyCodexAPIProvider(provider); err != nil {
			return err
		}
	case "claude":
		env, err := replaceAgentConfiguredEnv(agentName, map[string]string{
			"ANTHROPIC_BASE_URL":   provider.BaseURL,
			"ANTHROPIC_AUTH_TOKEN": provider.APIKey,
		})
		if err != nil {
			return err
		}
		if app != nil && app.GetAgentPool() != nil {
			if err := app.GetAgentPool().SetAgentEnv(agentName, env); err != nil {
				return err
			}
		}
		if app != nil && app.GetProber() != nil {
			if err := app.GetProber().SetAgentEnv(agentName, env); err != nil {
				return err
			}
		}
	case "gemini":
		if err := applyGeminiAPIProvider(provider); err != nil {
			return err
		}
	case "copilot":
		if err := applyAgentProviderEnv(agentName, copilotAPIProviderEnv(provider), app); err != nil {
			return err
		}
	case "qwen":
		if err := applyQwenAPIProvider(provider); err != nil {
			return err
		}
	case "kimi":
		if err := applyKimiAPIProvider(provider); err != nil {
			return err
		}
	case "opencode":
		if err := applyOpenCodeAPIProvider(provider); err != nil {
			return err
		}
	case "openclaw":
		if err := applyOpenClawAPIProvider(provider); err != nil {
			return err
		}
	case "omp":
		if err := applyOMPAPIProvider(provider); err != nil {
			return err
		}
	case "pi":
		if err := applyPiAPIProvider(provider); err != nil {
			return err
		}
	case "hermes":
		if err := applyHermesAPIProvider(provider); err != nil {
			return err
		}
	default:
		if err := applyAgentProviderEnv(agentName, map[string]string{
			"OPENAI_BASE_URL": provider.BaseURL,
			"OPENAI_API_KEY":  provider.APIKey,
		}, app); err != nil {
			return err
		}
	}
	return nil
}

func applyAgentProviderEnv(agentName string, updates map[string]string, app *AppContext) error {
	env, err := mergeAgentEnvConfig(agentName, updates)
	if err != nil {
		return err
	}
	if app != nil && app.GetAgentPool() != nil {
		if err := app.GetAgentPool().SetAgentEnv(agentName, env); err != nil {
			return err
		}
	}
	if app != nil && app.GetProber() != nil {
		if err := app.GetProber().SetAgentEnv(agentName, env); err != nil {
			return err
		}
	}
	return nil
}

func copilotAPIProviderEnv(provider agentAPIProvider) map[string]string {
	env := map[string]string{
		"COPILOT_PROVIDER_BASE_URL": provider.BaseURL,
		"COPILOT_PROVIDER_API_KEY":  provider.APIKey,
		"COPILOT_MODEL":             firstModelOrDefault(provider.Models, ""),
	}
	switch agentAPIProviderActiveProtocol(provider) {
	case apiProviderProtocolAnthropicCompatible:
		env["COPILOT_PROVIDER_TYPE"] = "anthropic"
	default:
		env["COPILOT_PROVIDER_TYPE"] = "openai"
	}
	return env
}

func applyCodexAPIProvider(provider agentAPIProvider) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".codex")
	configPath := filepath.Join(dir, "config.toml")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return apperr.Wrap("mkdir", dir, err)
	}
	existing, _ := os.ReadFile(configPath)
	configText := mergeCodexAPIProviderConfig(string(existing), provider)
	return apperr.Wrap("write", configPath, os.WriteFile(configPath, []byte(configText), 0o600))
}

func mergeCodexAPIProviderConfig(existing string, provider agentAPIProvider) string {
	// Update provider settings while retaining the configured provider key.
	providerName := codexModelProviderFromConfig(existing)
	if providerName == "" {
		providerName = agentAPIProviderConfigName(provider)
	}
	providerTable := tomlDottedTable("model_providers", providerName)
	lines := strings.Split(strings.ReplaceAll(existing, "\r\n", "\n"), "\n")
	filtered := make([]string, 0, len(lines))
	skipTable := false
	currentTable := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		header := tomlTableHeader(line)
		if header != "" {
			currentTable = header
			tableProvider, isProviderTable := codexModelProviderTableKey(line)
			skipTable = isProviderTable && tableProvider == providerName
			if skipTable {
				continue
			}
		}
		if skipTable {
			continue
		}
		if strings.HasPrefix(trimmed, "model_provider") || strings.HasPrefix(trimmed, "base_url") || strings.HasPrefix(trimmed, "wire_api") || strings.HasPrefix(trimmed, "experimental_bearer_token") {
			continue
		}
		if currentTable == "[tui.model_availability_nux]" && strings.HasPrefix(trimmed, "model =") {
			continue
		}
		filtered = append(filtered, line)
	}
	// Retain the configured model only when the provider advertises it.
	model := codexModelForProvider(existing, provider.Models)
	base := insertCodexTopLevelProviderConfig(strings.Join(filtered, "\n"), model, providerName)
	addition := fmt.Sprintf(`%s
name = %q
base_url = %q
wire_api = "responses"
experimental_bearer_token = %q
`, providerTable, provider.Name, openAIModelsBaseURL(provider.BaseURL), provider.APIKey)
	if base == "" {
		return addition
	}
	return base + "\n\n" + addition
}

// codexModelProviderFromConfig returns the top-level model_provider value.
func codexModelProviderFromConfig(existing string) string {
	lines := strings.Split(strings.ReplaceAll(existing, "\r\n", "\n"), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(tomlLineWithoutComment(line))
		if tomlTableHeader(line) != "" {
			break
		}
		if !strings.HasPrefix(trimmed, "model_provider") {
			continue
		}
		key, value, ok := strings.Cut(trimmed, "=")
		if !ok || strings.TrimSpace(key) != "model_provider" {
			continue
		}
		return tomlStringValue(value)
	}
	return ""
}

func codexModelForProvider(existing string, models []string) string {
	model := codexTopLevelStringValue(existing, "model")
	if model == "" {
		return ""
	}
	for _, candidate := range models {
		if strings.TrimSpace(candidate) == model {
			return model
		}
	}
	return ""
}

func codexTopLevelStringValue(existing, wantedKey string) string {
	lines := strings.Split(strings.ReplaceAll(existing, "\r\n", "\n"), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(tomlLineWithoutComment(line))
		if tomlTableHeader(line) != "" {
			break
		}
		key, value, ok := strings.Cut(trimmed, "=")
		if !ok || strings.TrimSpace(key) != wantedKey {
			continue
		}
		return tomlStringValue(value)
	}
	return ""
}

// tomlLineWithoutComment removes comments outside quoted strings.
func tomlLineWithoutComment(line string) string {
	inBasic, inLiteral, escaped := false, false, false
	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch {
		case inBasic:
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inBasic = false
			}
		case inLiteral:
			if ch == '\'' {
				inLiteral = false
			}
		case ch == '"':
			inBasic = true
		case ch == '\'':
			inLiteral = true
		case ch == '#':
			return line[:i]
		}
	}
	return line
}

func tomlTableHeader(line string) string {
	trimmed := strings.TrimSpace(tomlLineWithoutComment(line))
	if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
		return trimmed
	}
	return ""
}

func codexModelProviderTableKey(line string) (string, bool) {
	header := tomlTableHeader(line)
	if header == "" || strings.HasPrefix(header, "[[") {
		return "", false
	}
	parent, key, ok := strings.Cut(strings.TrimSpace(header[1:len(header)-1]), ".")
	if !ok || strings.TrimSpace(parent) != "model_providers" {
		return "", false
	}
	key = strings.TrimSpace(key)
	if isTOMLBareKey(key) {
		return key, true
	}
	if len(key) >= 2 && key[0] == '"' && key[len(key)-1] == '"' {
		parsed, err := strconv.Unquote(key)
		return parsed, err == nil
	}
	if len(key) >= 2 && key[0] == '\'' && key[len(key)-1] == '\'' && !strings.Contains(key[1:len(key)-1], "'") {
		return key[1 : len(key)-1], true
	}
	return "", false
}

func tomlStringValue(value string) string {
	value = strings.TrimSpace(tomlLineWithoutComment(value))
	if len(value) >= 2 && value[0] == '"' {
		if parsed, err := strconv.Unquote(value); err == nil {
			return strings.TrimSpace(parsed)
		}
	}
	if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
		return strings.TrimSpace(value[1 : len(value)-1])
	}
	return strings.TrimSpace(value)
}

// preserveCodexModelProviderKey aligns the selected provider key and table.
func preserveCodexModelProviderKey(existing, stableProvider string) string {
	stableProvider = strings.TrimSpace(stableProvider)
	if stableProvider == "" {
		return existing
	}
	currentProvider := codexModelProviderFromConfig(existing)
	if currentProvider == stableProvider {
		return existing
	}
	if currentProvider == "" {
		// Add the provider selector before the first table.
		return addCodexModelProviderKey(existing, stableProvider)
	}
	stableTable := tomlDottedTable("model_providers", stableProvider)
	lines := strings.Split(strings.ReplaceAll(existing, "\r\n", "\n"), "\n")
	currentTableFound, stableTableFound := false, false
	for _, line := range lines {
		tableProvider, ok := codexModelProviderTableKey(line)
		if ok && tableProvider == currentProvider {
			currentTableFound = true
		}
		if ok && tableProvider == stableProvider {
			stableTableFound = true
		}
	}
	// Keep the configuration unchanged when neither provider table exists.
	if !currentTableFound && !stableTableFound {
		return existing
	}
	out := make([]string, 0, len(lines))
	inTable := false
	skipStableTable := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		header := tomlTableHeader(line)
		if header != "" {
			inTable = true
			tableProvider, isProviderTable := codexModelProviderTableKey(line)
			skipStableTable = currentTableFound && isProviderTable && tableProvider == stableProvider
			if skipStableTable {
				continue
			}
			if isProviderTable && tableProvider == currentProvider {
				line = stableTable
			}
		}
		if skipStableTable {
			continue
		}
		if !inTable && strings.HasPrefix(trimmed, "model_provider") {
			if key, _, ok := strings.Cut(trimmed, "="); ok && strings.TrimSpace(key) == "model_provider" {
				line = fmt.Sprintf("model_provider = %q", stableProvider)
			}
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n")) + "\n"
}

func addCodexModelProviderKey(existing, providerName string) string {
	lines := strings.Split(strings.TrimRight(strings.ReplaceAll(existing, "\r\n", "\n"), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	insertAt := len(lines)
	for i, line := range lines {
		if tomlTableHeader(line) != "" {
			insertAt = i
			break
		}
	}
	top := append([]string(nil), lines[:insertAt]...)
	top = append(top, fmt.Sprintf("model_provider = %q", providerName))
	if insertAt < len(lines) {
		if len(top) > 0 && strings.TrimSpace(top[len(top)-1]) != "" {
			top = append(top, "")
		}
		top = append(top, lines[insertAt:]...)
	}
	return strings.TrimSpace(strings.Join(top, "\n")) + "\n"
}

func insertCodexTopLevelProviderConfig(existing, model, providerName string) string {
	lines := strings.Split(strings.TrimRight(existing, "\n"), "\n")
	insertAt := len(lines)
	for i, line := range lines {
		if tomlTableHeader(line) != "" {
			insertAt = i
			break
		}
	}
	top := make([]string, 0, insertAt+2)
	for _, line := range lines[:insertAt] {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "model =") || strings.HasPrefix(trimmed, "model_provider =") {
			continue
		}
		top = append(top, line)
	}
	if model != "" {
		top = append(top, fmt.Sprintf("model = %q", model))
	}
	top = append(top, fmt.Sprintf("model_provider = %q", providerName))
	out := append([]string{}, top...)
	if insertAt < len(lines) {
		if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
			out = append(out, "")
		}
		out = append(out, lines[insertAt:]...)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

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

func mergeAgentEnvConfig(agentName string, updates map[string]string) (map[string]string, error) {
	return writeAgentEnvConfig(agentName, updates, false)
}

func replaceAgentConfiguredEnv(agentName string, updates map[string]string) (map[string]string, error) {
	return writeAgentEnvConfig(agentName, updates, true)
}

func writeAgentEnvConfig(agentName string, updates map[string]string, clearConfigured bool) (map[string]string, error) {
	// clearConfigured 要删的是「配置档记过账的 key」，那份记录在 ConfigBackup 里。
	// 这里读生效配置（安装自带 ⊕ 用户层）只是**只读查表**，不会再被写回用户层。
	clearKeys, err := configuredEnvKeys(agentName)
	if err != nil {
		return nil, err
	}

	var merged map[string]string
	err = mutateUserAgent(agentName, func(def *agent.Definition) {
		merged = cloneStringMap(def.Env)
		if merged == nil {
			merged = map[string]string{}
		}
		if clearConfigured {
			for _, key := range clearKeys {
				delete(merged, key)
			}
		}
		for key, value := range updates {
			if strings.TrimSpace(value) == "" {
				delete(merged, key)
				continue
			}
			merged[key] = value
		}
		def.Env = cloneStringMap(merged)
	})
	if err != nil {
		return nil, err
	}

	// 调用方拿这个 env 去 SetAgentEnv，而它是**整份替换**：必须是生效 env，
	// 用户层没写的键还得靠安装自带那份供给，只回传用户层会当场把它们抹掉。
	effective, err := agent.LoadConfig("")
	if err != nil {
		return nil, err
	}
	if def, ok := effective.GetAgent(agentName); ok && len(def.Env) > 0 {
		return cloneStringMap(def.Env), nil
	}
	return merged, nil
}

// configuredEnvKeys 取配置档记过账的 env key。用户层没记就回退到上游那份。
func configuredEnvKeys(agentName string) ([]string, error) {
	cfg, err := agent.LoadConfig("")
	if err != nil {
		return nil, err
	}
	if def, ok := cfg.GetAgent(agentName); ok {
		return def.ConfigBackup.EnvKeys, nil
	}
	return nil, nil
}
