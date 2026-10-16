package codex

import (
	"encoding/json"
	"strings"
)

func importedCodexToolCallIndex(script string) int {
	var quote byte
	escaped := false
	for index := 0; index+len("tools.") <= len(script); index++ {
		char := script[index]
		if quote != 0 {
			if escaped {
				escaped = false
			} else if char == '\\' {
				escaped = true
			} else if char == quote {
				quote = 0
			}
			continue
		}
		if char == '"' || char == '\'' || char == '`' {
			quote = char
			continue
		}
		if strings.HasPrefix(script[index:], "tools.") {
			return index
		}
	}
	return -1
}

func importedCodexBalancedJavaScriptValue(script string, start int, open, close byte) (string, bool) {
	depth := 0
	var quote byte
	escaped := false
	for index := start; index < len(script); index++ {
		char := script[index]
		if quote != 0 {
			if escaped {
				escaped = false
			} else if char == '\\' {
				escaped = true
			} else if char == quote {
				quote = 0
			}
			continue
		}
		switch char {
		case '"', '\'', '`':
			quote = char
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return script[start : index+1], true
			}
		}
	}
	return "", false
}

func quoteImportedJavaScriptObjectKeys(literal string) string {
	var out strings.Builder
	out.Grow(len(literal) + 16)
	var quote byte
	escaped := false
	for index := 0; index < len(literal); {
		char := literal[index]
		if quote != 0 {
			out.WriteByte(char)
			index++
			if escaped {
				escaped = false
			} else if char == '\\' {
				escaped = true
			} else if char == quote {
				quote = 0
			}
			continue
		}
		if char == '"' || char == '\'' || char == '`' {
			quote = char
			out.WriteByte(char)
			index++
			continue
		}
		if isImportedJavaScriptIdentifierStart(char) {
			end := index + 1
			for end < len(literal) && isImportedJavaScriptIdentifierByte(literal[end]) {
				end++
			}
			next := end
			for next < len(literal) && (literal[next] == ' ' || literal[next] == '\t' || literal[next] == '\r' || literal[next] == '\n') {
				next++
			}
			previous := index - 1
			for previous >= 0 && (literal[previous] == ' ' || literal[previous] == '\t' || literal[previous] == '\r' || literal[previous] == '\n') {
				previous--
			}
			if next < len(literal) && literal[next] == ':' && previous >= 0 && (literal[previous] == '{' || literal[previous] == ',') {
				out.WriteByte('"')
				out.WriteString(literal[index:end])
				out.WriteByte('"')
			} else {
				out.WriteString(literal[index:end])
			}
			index = end
			continue
		}
		out.WriteByte(char)
		index++
	}
	return out.String()
}

func importedCodexJavaScriptVariable(prefix, name string) (string, bool) {
	for _, declaration := range []string{"const ", "let ", "var "} {
		marker := declaration + name
		index := strings.LastIndex(prefix, marker)
		if index < 0 {
			continue
		}
		index += len(marker)
		for index < len(prefix) && (prefix[index] == ' ' || prefix[index] == '\t' || prefix[index] == '\r' || prefix[index] == '\n') {
			index++
		}
		if index >= len(prefix) || prefix[index] != '=' {
			continue
		}
		index++
		for index < len(prefix) && (prefix[index] == ' ' || prefix[index] == '\t' || prefix[index] == '\r' || prefix[index] == '\n') {
			index++
		}
		if value, _, ok := importedCodexJavaScriptString(prefix, index); ok {
			return value, true
		}
	}
	return "", false
}

func importedCodexJavaScriptString(script string, start int) (string, int, bool) {
	if start >= len(script) || (script[start] != '"' && script[start] != '\'' && script[start] != '`') {
		return "", start, false
	}
	quote := script[start]
	escaped := false
	for index := start + 1; index < len(script); index++ {
		char := script[index]
		if escaped {
			escaped = false
			continue
		}
		if char == '\\' {
			escaped = true
			continue
		}
		if char != quote {
			continue
		}
		literal := script[start : index+1]
		if quote == '"' {
			var decoded string
			if json.Unmarshal([]byte(literal), &decoded) == nil {
				return decoded, index + 1, true
			}
		}
		return literal[1 : len(literal)-1], index + 1, true
	}
	return "", start, false
}

func isImportedJavaScriptIdentifierStart(char byte) bool {
	return char == '_' || char == '$' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z'
}

func isImportedJavaScriptIdentifierByte(char byte) bool {
	return isImportedJavaScriptIdentifierStart(char) || char >= '0' && char <= '9'
}
