package api

import (
	"fmt"
	"os"
	"path/filepath"
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
