package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"mindfs/server/internal/apperr"
)

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
