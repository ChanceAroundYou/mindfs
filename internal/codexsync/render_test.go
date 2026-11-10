package codexsync

import (
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

const mergeTestDefault = `model = "gpt-6-luna"
model_reasoning_effort = "medium"

[features]
goals = true
`

const mergeTestLLMux = `model = "gpt-6-luna"
model_provider = "llmux"
model_reasoning_effort = "medium"

[model_providers]

[model_providers.llmux]
name = "llmux"
base_url = "https://llmux.example/v1"
wire_api = "responses"
env_key = "LLMUX_API_KEY"
`

func TestMergeSharedProfilesKeepsRootAssignmentsBeforeTables(t *testing.T) {
	merged := mergeSharedProfiles([]byte(mergeTestDefault), []byte(mergeTestLLMux))
	var root map[string]any
	if err := toml.Unmarshal([]byte(merged), &root); err != nil {
		t.Fatalf("merged config is invalid TOML: %v\n%s", err, merged)
	}
	if root["model"] != "gpt-6-luna" || root["model_provider"] != "llmux" || root["model_reasoning_effort"] != "medium" {
		t.Fatalf("llmux root keys were nested into a table: %#v\n%s", root, merged)
	}
	features, ok := root["features"].(map[string]any)
	if !ok || len(features) != 1 || features["goals"] != true {
		t.Fatalf("features table was polluted by root keys: %#v", features)
	}
	providers, ok := root["model_providers"].(map[string]any)
	if !ok {
		t.Fatalf("model_providers table missing: %#v", root["model_providers"])
	}
	if _, ok := providers["llmux"].(map[string]any); !ok {
		t.Fatalf("model_providers.llmux table missing: %#v", providers)
	}
	if err := validateMergedRootKeys([]byte(merged), []byte(mergeTestLLMux)); err != nil {
		t.Fatalf("validateMergedRootKeys: %v", err)
	}
}

func TestValidateMergedRootKeysRejectsNestedKeys(t *testing.T) {
	nested := strings.Replace(mergeTestDefault, "goals = true", "goals = true\nmodel_provider = \"llmux\"", 1)
	if err := validateMergedRootKeys([]byte(nested), []byte(mergeTestLLMux)); err == nil {
		t.Fatal("validator accepted a shared root key nested inside [features]")
	}
}
