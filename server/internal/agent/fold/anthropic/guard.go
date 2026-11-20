package anthropic

import (
	"strings"

	"mindfs/server/internal/agent/fold"
)

// The preserved-thinking prefix guard (design 2026-11-20 §4).
//
// Anthropic's preserved-thinking models carry a fingerprint over the request
// prefix. The documented rule is that the check "compares what you sent, not
// what Claude read": threshold compaction and server-side context editing never
// count, but anything the *client* changed does. A proxy-side rewrite is a
// client-side edit, so folding a request to one of these models invalidates
// every thinking block after the fold boundary — a 400 on the newest families,
// and at best stale thinking reaching the model (and being billed) on the
// previous one.
//
// The guard therefore refuses to fold those models outright. It fails closed on
// what it can recognise and open on what it cannot: the llmux alias in
// production ("ok") names no model this process can classify, and blocking it
// would silently turn folding off for the whole node. Aliases are handled by
// configuration (prefix_unsafe_models) plus a one-line-per-session warning the
// caller emits from GuardVerdict.UnknownModel, so the operator can see the
// alias in the log and decide.

// preservedThinkingFamilies are the normalized model families that run the
// prefix check (down to the minor version: 5.5 and 5.1/5.2 are affected, plain
// 5.0 or 4.x are not).
var preservedThinkingFamilies = []string{
	"fable-5-2",
	"mythos-5-2",
	"fable-5-1",
	"mythos-5-1",
	"opus-5-5",
	"sonnet-5-5",
	"haiku-5-5",
}

// knownModelPrefixes are the prefixes of model ids this process can classify.
// Anything else is an alias and gets the unknown warning.
var knownModelPrefixes = []string{"opus-", "sonnet-", "haiku-", "fable-", "mythos-"}

// Guard refuses to rewrite requests aimed at preserved-thinking models.
func (Adapter) Guard(model string, cfg fold.Config) fold.GuardVerdict {
	if !cfg.PrefixGuardOn() {
		return fold.GuardVerdict{}
	}
	family := modelFamily(model)
	if family == "" {
		return fold.GuardVerdict{}
	}
	if isPreservedThinkingFamily(family) {
		return fold.GuardVerdict{Block: true}
	}
	for _, unsafe := range cfg.PrefixUnsafeModels {
		if familyMatches(family, modelFamily(unsafe)) {
			return fold.GuardVerdict{Block: true}
		}
	}
	if !isKnownModelFamily(family) {
		return fold.GuardVerdict{UnknownModel: true}
	}
	return fold.GuardVerdict{}
}

// modelFamily normalizes a request's model id to a comparable family:
// lowercase, no "claude-" prefix, no "[1m]" context suffix, no "-YYYYMMDD"
// snapshot date. "claude-sonnet-5-5-20260101" and "sonnet-5-5" both become
// "sonnet-5-5"; "opus-5" stays "opus-5" and therefore does not match "opus-5-5".
func modelFamily(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if index := strings.IndexByte(m, '['); index >= 0 {
		m = m[:index]
	}
	m = strings.TrimPrefix(m, "claude-")
	if index := strings.LastIndexByte(m, '-'); index > 0 {
		suffix := m[index+1:]
		if len(suffix) == 8 && isAllDigits(suffix) {
			m = m[:index]
		}
	}
	return m
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// familyMatches reports whether a model family is the given family or a
// derivative of it. Prefix matching stops at a dash so "opus-5" cannot match
// "opus-5-5".
func familyMatches(family, want string) bool {
	return family == want || strings.HasPrefix(family, want+"-")
}

func isPreservedThinkingFamily(family string) bool {
	for _, want := range preservedThinkingFamilies {
		if familyMatches(family, want) {
			return true
		}
	}
	return false
}

func isKnownModelFamily(family string) bool {
	for _, prefix := range knownModelPrefixes {
		if strings.HasPrefix(family, prefix) {
			return true
		}
	}
	return false
}
