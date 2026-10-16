package api

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"mindfs/server/internal/apperr"
)

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
