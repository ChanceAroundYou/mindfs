package api

import (
	"strconv"
	"strings"
)

func agentAPIProviderConfigName(provider agentAPIProvider) string {
	name := strings.TrimSpace(provider.Name)
	if name == "" {
		name = strings.TrimSpace(provider.ID)
	}
	if name == "" {
		return "api-provider"
	}
	return name
}

func tomlDottedTable(parent, key string) string {
	return "[" + parent + "." + tomlKey(key) + "]"
}

func tomlKey(key string) string {
	if isTOMLBareKey(key) {
		return key
	}
	return strconv.Quote(key)
}

func isTOMLBareKey(key string) bool {
	if key == "" {
		return false
	}
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_' || r == '-':
			continue
		default:
			return false
		}
	}
	return true
}
