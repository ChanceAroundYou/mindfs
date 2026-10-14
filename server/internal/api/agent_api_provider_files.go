package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"mindfs/server/internal/apperr"

	"gopkg.in/yaml.v3"
)

func firstModelOrDefault(models []string, fallback string) string {
	for _, model := range models {
		if trimmed := strings.TrimSpace(model); trimmed != "" {
			return trimmed
		}
	}
	return fallback
}

func homePath(parts ...string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{home}, parts...)...), nil
}

func writeTextFile(path, text string, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return apperr.Wrap("mkdir", filepath.Dir(path), err)
	}
	return apperr.Wrap("write", path, os.WriteFile(path, []byte(text), perm))
}

func readJSONObject(path string) (map[string]any, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, apperr.Wrap("read", path, err)
	}
	if len(strings.TrimSpace(string(payload))) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(payload, &out); err != nil {
		normalized := normalizeJSONLikeConfig(string(payload))
		if normalized == string(payload) {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		if fallbackErr := json.Unmarshal([]byte(normalized), &out); fallbackErr != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func writeJSONObject(path string, value map[string]any, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return apperr.Wrap("mkdir", filepath.Dir(path), err)
	}
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return apperr.Wrap("write", path, os.WriteFile(path, append(payload, '\n'), perm))
}

func normalizeJSONLikeConfig(input string) string {
	text := stripJSONLikeComments(input)
	text = replaceSingleQuotedStrings(text)
	text = quoteJSONLikeKeys(text)
	text = removeJSONTrailingCommas(text)
	return text
}

func stripJSONLikeComments(input string) string {
	var b strings.Builder
	inString := false
	quote := rune(0)
	escaped := false
	for i := 0; i < len(input); i++ {
		ch := rune(input[i])
		if inString {
			b.WriteByte(input[i])
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == quote {
				inString = false
			}
			continue
		}
		if ch == '"' || ch == '\'' {
			inString = true
			quote = ch
			b.WriteByte(input[i])
			continue
		}
		if ch == '/' && i+1 < len(input) && input[i+1] == '/' {
			for i < len(input) && input[i] != '\n' {
				i++
			}
			if i < len(input) {
				b.WriteByte(input[i])
			}
			continue
		}
		if ch == '/' && i+1 < len(input) && input[i+1] == '*' {
			i += 2
			for i+1 < len(input) && !(input[i] == '*' && input[i+1] == '/') {
				i++
			}
			i++
			continue
		}
		b.WriteByte(input[i])
	}
	return b.String()
}

func replaceSingleQuotedStrings(input string) string {
	var b strings.Builder
	inSingle := false
	inDouble := false
	escaped := false
	for i := 0; i < len(input); i++ {
		ch := input[i]
		if inDouble {
			b.WriteByte(ch)
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inDouble = false
			}
			continue
		}
		if !inSingle {
			if ch == '"' {
				inDouble = true
				b.WriteByte(ch)
				continue
			}
			if ch == '\'' {
				inSingle = true
				b.WriteByte('"')
				continue
			}
			b.WriteByte(ch)
			continue
		}
		if escaped {
			if ch == '\'' {
				b.WriteByte('\'')
			} else {
				b.WriteByte('\\')
				b.WriteByte(ch)
			}
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == '"' {
			b.WriteString(`\"`)
			continue
		}
		if ch == '\'' {
			inSingle = false
			b.WriteByte('"')
			continue
		}
		b.WriteByte(ch)
	}
	return b.String()
}

func quoteJSONLikeKeys(input string) string {
	re := regexp.MustCompile(`([,{]\s*)([A-Za-z_$][A-Za-z0-9_$-]*)(\s*:)`)
	prev := ""
	out := input
	for out != prev {
		prev = out
		out = re.ReplaceAllString(out, `$1"$2"$3`)
	}
	return out
}

func removeJSONTrailingCommas(input string) string {
	re := regexp.MustCompile(`,\s*([}\]])`)
	prev := ""
	out := input
	for out != prev {
		prev = out
		out = re.ReplaceAllString(out, `$1`)
	}
	return out
}

func ensureJSONObject(parent map[string]any, key string) map[string]any {
	if child, ok := parent[key].(map[string]any); ok {
		return child
	}
	child := map[string]any{}
	parent[key] = child
	return child
}

func readYAMLMap(path string) (map[string]any, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, apperr.Wrap("read", path, err)
	}
	if len(strings.TrimSpace(string(payload))) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := yaml.Unmarshal(payload, &out); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func writeYAMLMap(path string, value map[string]any, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return apperr.Wrap("mkdir", filepath.Dir(path), err)
	}
	payload, err := yaml.Marshal(value)
	if err != nil {
		return err
	}
	return apperr.Wrap("write", path, os.WriteFile(path, payload, perm))
}

func ensureYAMLMap(parent map[string]any, key string) map[string]any {
	return ensureJSONObject(parent, key)
}

func qwenModelEntries(provider agentAPIProvider, envKey, baseURL string) []map[string]any {
	out := make([]map[string]any, 0, len(provider.Models))
	for _, model := range provider.Models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		out = append(out, map[string]any{
			"id":      model,
			"name":    model,
			"envKey":  envKey,
			"baseUrl": baseURL,
		})
	}
	return out
}

func modelObject(models []string) map[string]any {
	out := map[string]any{}
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		out[model] = map[string]any{"name": model}
	}
	return out
}

func modelArray(models []string) []map[string]any {
	out := make([]map[string]any, 0, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		out = append(out, map[string]any{"id": model, "name": model})
	}
	return out
}

func additiveAgentAPI(protocol string) string {
	if protocol == apiProviderProtocolAnthropicCompatible {
		return "anthropic-messages"
	}
	return "openai-completions"
}

func piAgentAPI(protocol string) string {
	switch protocol {
	case apiProviderProtocolAnthropicCompatible:
		return "anthropic-messages"
	case apiProviderProtocolGeminiCompatible:
		return "google-generative-ai"
	default:
		return "openai-completions"
	}
}

func piAgentBaseURL(provider agentAPIProvider) string {
	switch agentAPIProviderActiveProtocol(provider) {
	case apiProviderProtocolAnthropicCompatible:
		return anthropicBaseURL(provider.BaseURL)
	case apiProviderProtocolGeminiCompatible:
		return geminiModelsBaseURL(provider.BaseURL)
	default:
		return openAIModelsBaseURL(provider.BaseURL)
	}
}

func anthropicBaseURL(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	return strings.TrimSuffix(base, "/v1")
}

func geminiModelsBaseURL(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(base, "/v1beta") {
		return base
	}
	return base + "/v1beta"
}

func replaceManagedBlock(existing, block, startMarker, endMarker string) string {
	text := strings.TrimRight(strings.ReplaceAll(existing, "\r\n", "\n"), "\n")
	start := strings.Index(text, startMarker)
	if start >= 0 {
		end := strings.Index(text[start:], endMarker)
		if end >= 0 {
			end += start + len(endMarker)
			text = strings.TrimRight(text[:start], "\n") + "\n\n" + strings.TrimLeft(text[end:], "\n")
		}
	}
	block = strings.TrimRight(block, "\n")
	if strings.TrimSpace(text) == "" {
		return block + "\n"
	}
	return strings.TrimRight(text, "\n") + "\n\n" + block + "\n"
}

func upsertNamedYAMLSequence(existing any, name string, entry map[string]any) []any {
	var out []any
	if seq, ok := existing.([]any); ok {
		out = append(out, seq...)
	}
	for i, item := range out {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if objName, _ := obj["name"].(string); objName == name {
			merged := map[string]any{}
			for key, value := range obj {
				merged[key] = value
			}
			for key, value := range entry {
				merged[key] = value
			}
			out[i] = merged
			return out
		}
	}
	return append(out, entry)
}

func replaceNamedYAMLSequenceEntry(existing any, name string, entry map[string]any) []any {
	var out []any
	if seq, ok := existing.([]any); ok {
		out = append(out, seq...)
	}
	for i, item := range out {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if objName, _ := obj["name"].(string); objName == name {
			out[i] = entry
			return out
		}
	}
	return append(out, entry)
}

func parseSimpleEnv(input string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(input, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return out
}

func formatSimpleEnv(env map[string]string) string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		value := strings.TrimSpace(env[key])
		if value == "" {
			continue
		}
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(value)
		b.WriteByte('\n')
	}
	return b.String()
}
