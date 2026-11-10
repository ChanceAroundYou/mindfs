package codexsync

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const maxLLMuxEnvFileBytes = 64 << 10

// LocalLLMuxCredentialAvailable checks the env used by codex-llmux without sourcing
// or logging the file, which would execute shell code and expose the key.
func LocalLLMuxCredentialAvailable() bool {
	_, ok := LocalLLMuxCredentialValue()
	return ok
}

// LocalLLMuxCredentialValue returns the literal LLMUX_API_KEY this device can use.
// Callers must pass it straight to the Codex child process environment: never log
// it, return it from an API, or write it into shared configuration.
func LocalLLMuxCredentialValue() (string, bool) {
	environmentAvailable := strings.TrimSpace(os.Getenv("LLMUX_API_KEY"))
	configDir, err := os.UserConfigDir()
	if err != nil {
		return environmentAvailable, environmentAvailable != ""
	}
	path := filepath.Join(configDir, "codex", "llmux.env")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return environmentAvailable, environmentAvailable != ""
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxLLMuxEnvFileBytes {
		return "", false
	}
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), maxLLMuxEnvFileBytes)
	assigned := false
	current := environmentAvailable
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export") && len(line) > len("export") && (line[len("export")] == ' ' || line[len("export")] == '\t') {
			line = strings.TrimSpace(line[len("export"):])
		}
		key, value, found := strings.Cut(line, "=")
		if !found || key != "LLMUX_API_KEY" {
			continue
		}
		assigned = true
		literal, valid := parseLiteralShellValue(value)
		if !valid || strings.TrimSpace(literal) == "" {
			current = ""
			continue
		}
		current = literal
	}
	if scanner.Err() != nil {
		return "", false
	}
	if assigned {
		return current, current != ""
	}
	return environmentAvailable, environmentAvailable != ""
}

func parseLiteralShellValue(raw string) (string, bool) {
	if raw == "" {
		return "", true
	}
	// In shell assignment words, whitespace immediately after '=' ends the
	// assignment. The variable is set to an empty string; following text is a
	// separate command or comment.
	if raw[0] == ' ' || raw[0] == '\t' {
		return "", true
	}
	value := strings.TrimRight(raw, " \t")
	if value[0] == '\'' {
		end := strings.IndexByte(value[1:], '\'')
		if end < 0 {
			return "", false
		}
		literal := value[1 : end+1]
		return literal, onlyShellComment(value[end+2:])
	}
	if value[0] == '"' {
		end := -1
		for i := 1; i < len(value); i++ {
			if value[i] == '\\' {
				return "", false
			}
			if value[i] == '"' {
				end = i
				break
			}
		}
		if end < 0 || strings.ContainsAny(value[1:end], "$`\\") || !onlyShellComment(value[end+1:]) {
			return "", false
		}
		return value[1:end], true
	}
	if strings.ContainsAny(value, "$`\\;|&()<>\n\r\t") {
		return "", false
	}
	if index := strings.IndexAny(value, " \t"); index >= 0 {
		if !onlyShellComment(value[index:]) {
			return "", false
		}
		value = value[:index]
	}
	return value, true
}

func onlyShellComment(suffix string) bool {
	suffix = strings.TrimLeft(suffix, " \t")
	return suffix == "" || strings.HasPrefix(suffix, "#")
}
