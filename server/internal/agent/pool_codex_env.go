package agent

import (
	"os"
	"strings"

	"mindfs/internal/codexsync"
)

// codexChildEnv builds the environment for a Codex app-server process.
//
// codex-go-sdk replaces the whole child environment whenever Env is non-nil,
// so injecting the device-local LLMUX_API_KEY must start from os.Environ() or
// PATH (and everything else) disappears. The credential only goes into this
// child process: never log it, return it from an API, or write it into shared
// configuration. A nil result keeps the SDK's inherit-everything behavior.
func codexChildEnv(def Definition) map[string]string {
	env := cloneEnv(def.Env)
	if _, exists := env["LLMUX_API_KEY"]; exists {
		return env
	}
	value, ok := codexsync.LocalLLMuxCredentialValue()
	if !ok {
		return env
	}
	merged := make(map[string]string, len(env)+1)
	for _, entry := range os.Environ() {
		key, current, found := strings.Cut(entry, "=")
		if found {
			merged[key] = current
		}
	}
	for key, current := range env {
		merged[key] = current
	}
	merged["LLMUX_API_KEY"] = value
	return merged
}
