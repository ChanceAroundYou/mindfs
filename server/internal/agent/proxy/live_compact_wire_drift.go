package proxy

import (
	"bytes"
	"hash/fnv"
	"strings"
)

// messageHashes fingerprints each message by its content so Phase A can detect
// how often the CLI itself rewrites an earlier part of the history.
//
// Two encoding-level rewrites are normalized away first, because neither
// changes what the model sees but both changed the raw bytes on real traffic
// (2026-11-18):
//
//   - cache_control relocation: Claude Code moves its ephemeral breakpoint to
//     the newest message every turn.
//   - text-shape flip: the system message arrives as [{"type":"text",...}]
//     on turn 1 and as a plain string on later turns; the tool_result at the
//     end of an assistant turn flips the other way.
//
// Anything still different after normalization is real content drift.
func messageHashes(req *wireRequest, limit int) []uint64 {
	count := len(req.messages)
	if limit > 0 && count > limit {
		count = limit
	}
	hashes := make([]uint64, 0, count)
	for index := 0; index < count; index++ {
		hash := fnv.New64a()
		_, _ = hash.Write(normalizeMessageHash(req.messages[index]))
		hashes = append(hashes, hash.Sum64())
	}
	return hashes
}

// normalizeMessageHash renders role plus block content in a canonical form:
// text-shape differences collapse, cache_control is dropped, and tool blocks
// keep their identifiers so a real edit still changes the hash.
func normalizeMessageHash(msg wireMessage) []byte {
	var builder bytes.Buffer
	builder.WriteString(msg.role)
	builder.WriteByte('\n')
	for _, block := range msg.blocks {
		switch block.typ {
		case "text":
			builder.WriteString("t:")
			builder.WriteString(block.text)
		case "tool_use":
			builder.WriteString("u:")
			builder.WriteString(block.toolUseID)
			builder.WriteByte(':')
			builder.WriteString(block.toolName)
		case "tool_result":
			builder.WriteString("r:")
			builder.WriteString(block.toolUseID)
			if block.isError {
				builder.WriteString(":err")
			}
			builder.WriteString(":")
			builder.WriteString(block.text)
		default:
			// thinking / image / document: the raw bytes are stable and any
			// change is real, so hash them as-is minus cache_control.
			builder.WriteByte('x')
			builder.Write(stripCacheControl(block.raw))
		}
		builder.WriteByte('\n')
	}
	return builder.Bytes()
}

// stripCacheControl removes `"cache_control": {...}` members from raw JSON
// before hashing. The output does not have to be valid JSON; it only has to be
// deterministic and content-equivalent.
func stripCacheControl(raw []byte) []byte {
	const key = `"cache_control"`
	out := make([]byte, 0, len(raw))
	for {
		index := bytes.Index(raw, []byte(key))
		if index < 0 {
			out = append(out, raw...)
			return out
		}
		out = append(out, raw[:index]...)
		rest := skipSpace(raw[index+len(key):])
		if len(rest) == 0 || rest[0] != ':' {
			// Not an object member (inside a string, or a different shape).
			// Keep the key verbatim and continue after it.
			out = append(out, raw[index:index+len(key)]...)
			raw = raw[index+len(key):]
			continue
		}
		value := skipSpace(rest[1:])
		if len(value) == 0 || value[0] != '{' {
			out = append(out, raw[index:index+len(key)]...)
			raw = raw[index+len(key):]
			continue
		}
		skip := skipJSONObject(value)
		after := rest[1:]
		after = after[len(after)-len(value)+skip:]
		// Drop one adjacent comma so the result stays close to canonical.
		if trimmed := bytes.TrimRight(out, " \t\r\n"); len(trimmed) > 0 && trimmed[len(trimmed)-1] == ',' {
			out = trimmed[:len(trimmed)-1]
		} else {
			after = bytes.TrimLeft(after, " \t\r\n")
			if len(after) > 0 && after[0] == ',' {
				after = after[1:]
			}
		}
		raw = after
	}
}

func skipSpace(in []byte) []byte {
	for len(in) > 0 && (in[0] == ' ' || in[0] == '\t' || in[0] == '\r' || in[0] == '\n') {
		in = in[1:]
	}
	return in
}

// skipJSONObject returns the byte length of the object starting at in[0] (or
// len(in) when unterminated), respecting strings and escapes.
func skipJSONObject(in []byte) int {
	depth := 0
	inString := false
	escaped := false
	for index := 0; index < len(in); index++ {
		ch := in[index]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return index + 1
			}
		}
	}
	return len(in)
}

// firstDrift returns the first message index whose fingerprint changed
// compared with the previous request, or -1 when the old prefix is intact.
// A pure append is not drift.
func firstDrift(previous, current []uint64) int {
	limit := len(previous)
	if len(current) < limit {
		limit = len(current)
	}
	for index := 0; index < limit; index++ {
		if previous[index] != current[index] {
			return index
		}
	}
	if len(current) < len(previous) {
		return len(current)
	}
	return -1
}

// looksLikeErrorText is a heuristic for the is_error=false-but-it-clearly-failed
// case: some tools report failure as normal output. It only widens the pointer
// excerpt, so a false positive costs a few lines, not correctness.
//
// Markers must start a line (after trimming). Searching anywhere matched Go
// source that merely contains fmt.Errorf / "failed" in string literals: real
// traffic flagged 2 of 3 file reads as error output (2026-11-18).
func looksLikeErrorText(text string) bool {
	const headLines = 20
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) > 2*headLines {
		tail := lines[len(lines)-headLines:]
		lines = append(append([]string{}, lines[:headLines]...), tail...)
	}
	for _, line := range lines {
		lower := strings.ToLower(strings.TrimSpace(line))
		for _, marker := range []string{
			"traceback (most recent call last)",
			"panic:",
			"fatal:",
			"exception:",
			"exit status",
			"permission denied",
			"no such file or directory",
			"command not found",
			"error:",
			"failed",
		} {
			if strings.HasPrefix(lower, marker) {
				return true
			}
		}
	}
	return false
}
