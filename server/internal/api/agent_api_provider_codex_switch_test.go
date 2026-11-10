package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"

	"mindfs/internal/codexsync"
	"mindfs/server/internal/agent"
)

const codexSwitchSharedDefault = `model = "gpt-6-luna"
model_reasoning_effort = "medium"

[features]
goals = true
`

const codexSwitchSharedLLMux = `model = "gpt-6-luna"
model_provider = "llmux"
model_reasoning_effort = "medium"

[model_providers]

[model_providers.llmux]
name = "llmux"
base_url = "https://llmux.example/v1"
wire_api = "responses"
env_key = "LLMUX_API_KEY"
`

func codexSwitchPaths(t *testing.T, baseURL string) (codexsync.Paths, string) {
	t.Helper()
	root := writeAPIProvidersFile(t, []agentAPIProvider{
		{ID: "api-llmux", Name: "llmux", BaseURL: baseURL, APIKey: "local-key"},
	})
	t.Setenv("CODEX_HOME", filepath.Join(root, ".codex"))
	configPath := filepath.Join(root, "agents.json")
	t.Setenv("MINDFS_AGENTS_CONFIG", configPath)
	writeJSON(t, configPath, agent.Config{Agents: []agent.Definition{
		{Name: "codex", Command: "codex", Protocol: agent.ProtocolCodexSDK},
	}})
	paths, err := codexsync.ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.SharedLLMux), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.SharedDefault, []byte(codexSwitchSharedDefault), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.SharedLLMux, []byte(codexSwitchSharedLLMux), 0o644); err != nil {
		t.Fatal(err)
	}
	return paths, root
}

func TestCodexProviderSwitchDelegatesToMatchingSharedProfile(t *testing.T) {
	paths, _ := codexSwitchPaths(t, "https://llmux.example/v1")

	provider, err := switchAgentAPIProvider(agentAPIProviderSwitchRequest{
		Agent:      "codex",
		ProviderID: "api-llmux",
	}, nil)
	if err != nil {
		t.Fatalf("switchAgentAPIProvider: %v", err)
	}
	if provider.ID != "api-llmux" {
		t.Fatalf("switch returned provider %+v", provider)
	}
	rendered, err := os.ReadFile(paths.RuntimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := toml.Unmarshal(rendered, &root); err != nil {
		t.Fatalf("rendered config is invalid TOML: %v\n%s", err, rendered)
	}
	if root["model"] != "gpt-6-luna" || root["model_provider"] != "llmux" {
		t.Fatalf("shared llmux keys are not at the root: %#v\n%s", root, rendered)
	}
	features, ok := root["features"].(map[string]any)
	if !ok || len(features) != 1 || features["goals"] != true {
		t.Fatalf("features table was polluted by root keys: %#v", features)
	}
}

func TestCodexProviderSwitchRejectsMismatchedSharedProfile(t *testing.T) {
	paths, _ := codexSwitchPaths(t, "https://other.example/v1")

	if _, err := switchAgentAPIProvider(agentAPIProviderSwitchRequest{
		Agent:      "codex",
		ProviderID: "api-llmux",
	}, nil); err == nil || !strings.Contains(err.Error(), "metadata only") {
		t.Fatalf("mismatched provider switch err = %v", err)
	}
	if _, err := os.Stat(paths.RuntimeConfig); !os.IsNotExist(err) {
		t.Fatalf("mismatched provider switch wrote runtime config, err=%v", err)
	}
}
