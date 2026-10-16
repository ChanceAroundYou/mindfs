package agent

import (
	"path/filepath"
	"runtime"
	"strings"
)

func mergeShells(base []Shell, override []Shell) []Shell {
	if len(override) == 0 {
		return append([]Shell(nil), base...)
	}
	merged := append([]Shell(nil), override...)
	shellIndexes := make(map[string]int, len(merged))
	for i, shell := range merged {
		if key := shellMergeKey(shell); key != "" {
			shellIndexes[key] = i
		}
	}
	for _, shell := range base {
		key := shellMergeKey(shell)
		if key == "" {
			continue
		}
		if _, ok := shellIndexes[key]; ok {
			continue
		}
		shellIndexes[key] = len(merged)
		merged = append(merged, shell)
	}
	return merged
}

func shellMergeKey(shell Shell) string {
	return strings.ToLower(strings.TrimSpace(shell.Command))
}

func parseShellOS(raw interface{}) []string {
	switch value := raw.(type) {
	case nil:
		return nil
	case string:
		return []string{value}
	case []interface{}:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func normalizeShellOS(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.ToLower(strings.TrimSpace(value))
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func shellMatchesCurrentOS(shell Shell) bool {
	if len(shell.OS) == 0 {
		return true
	}
	for _, value := range shell.OS {
		if value == runtime.GOOS {
			return true
		}
	}
	return false
}

func lifecycleCommandMatchesCurrentOS(values []string) bool {
	if len(values) == 0 {
		return true
	}
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), runtime.GOOS) {
			return true
		}
	}
	return false
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA == nil && errB == nil {
		return absA == absB
	}
	return a == b
}
