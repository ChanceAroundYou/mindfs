package update

import (
	"strconv"
	"strings"
)

func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	return v
}

func normalizeTag(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if v == "" {
		return ""
	}
	return "v" + v
}

func parseReleaseNotesVersion(text string) string {
	firstLine := strings.TrimSpace(strings.TrimSuffix(strings.SplitN(text, "\n", 2)[0], "\r"))
	const prefix = "# MindFS "
	if !strings.HasPrefix(firstLine, prefix) {
		return ""
	}
	fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(firstLine, prefix)))
	if len(fields) == 0 {
		return ""
	}
	version := fields[0]
	if _, ok := parseVersionParts(version); ok {
		return version
	}
	return ""
}

func latestReleaseNotesBody(text string) string {
	lines := strings.SplitAfter(text, "\n")
	for i := 1; i < len(lines); i++ {
		line := strings.TrimSpace(strings.TrimSuffix(lines[i], "\r"))
		if strings.HasPrefix(line, "# MindFS ") && parseReleaseNotesVersion(line) != "" {
			return strings.TrimSpace(strings.Join(lines[:i], ""))
		}
	}
	return strings.TrimSpace(text)
}

func isNewerVersion(latest, current string) bool {
	latestParts, latestOK := parseVersionParts(latest)
	if !latestOK {
		return false
	}
	currentParts, currentOK := parseVersionParts(current)
	if !currentOK {
		return true
	}
	maxLen := len(latestParts)
	if len(currentParts) > maxLen {
		maxLen = len(currentParts)
	}
	for i := 0; i < maxLen; i++ {
		var latestPart, currentPart int
		if i < len(latestParts) {
			latestPart = latestParts[i]
		}
		if i < len(currentParts) {
			currentPart = currentParts[i]
		}
		if latestPart > currentPart {
			return true
		}
		if latestPart < currentPart {
			return false
		}
	}
	return false
}

func parseVersionParts(v string) ([]int, bool) {
	v = normalizeVersion(v)
	if v == "" {
		return nil, false
	}
	end := 0
	for end < len(v) {
		ch := v[end]
		if (ch >= '0' && ch <= '9') || ch == '.' {
			end++
			continue
		}
		break
	}
	if end == 0 {
		return nil, false
	}
	core := strings.Trim(v[:end], ".")
	if core == "" {
		return nil, false
	}
	segments := strings.Split(core, ".")
	parts := make([]int, 0, len(segments))
	for _, segment := range segments {
		if segment == "" {
			return nil, false
		}
		value, err := strconv.Atoi(segment)
		if err != nil {
			return nil, false
		}
		parts = append(parts, value)
	}
	return parts, len(parts) > 0
}
