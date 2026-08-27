// Package deploy provides the compile-time deployment URL prefix shared by all
// local MindFS callers. It deliberately has no dependencies so API, CLI, and
// relay code cannot form an import cycle.
package deploy

import "strings"

// Prefix is injected at build time with:
// -ldflags "-X mindfs/internal/deploy.Prefix=/mindfs".
// Empty string means root deployment.
var Prefix string

// NormalizedPrefix returns the deployment prefix with one leading slash and no
// trailing slash. An empty string means root deployment.
func NormalizedPrefix() string {
	p := strings.TrimSpace(Prefix)
	p = strings.TrimRight(p, "/")
	if p == "" || p == "/" {
		return ""
	}
	if !strings.HasPrefix(p, "/") {
		return "/" + p
	}
	return p
}

// PrefixedPath joins a prefix-free app path with the deployment prefix.
func PrefixedPath(p string) string {
	prefix := NormalizedPrefix()
	if prefix == "" {
		return p
	}
	if strings.HasPrefix(p, "/") {
		return prefix + p
	}
	return prefix + "/" + p
}

// RelayAssetsAlias returns the absolute asset path used by relayed frontends.
func RelayAssetsAlias() string {
	prefix := strings.TrimLeft(NormalizedPrefix(), "/")
	if prefix == "" {
		return "/assets/"
	}
	return "/" + prefix + "-assets/"
}
