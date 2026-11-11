package codexsync

import (
	"os"
	"path/filepath"
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

// pc-win 的真实形态：共享 default 以 [features] 表结尾，本地 Codex 配置里
// 还有根级键（service_tier / notify）。旧的拼接方式会把根级键塞进 [features]，
// Codex 启动时报 `invalid type: string "default", expected a boolean`。
const runtimeLocalOverlayWithRootKeys = `model = "gpt-6-luna"
model_reasoning_effort = "medium"

[features]
goals = true
# >>> sync-codex local (not shared) >>>
service_tier = "default"

notify = [ "C:\\node\\codex-computer-use.exe", "turn-ended" ]

[tui]
screen_reader_detection_done = true

[plugins."browser@openai-bundled"]
enabled = true
# <<< sync-codex local (not shared) <<<
`

func TestBuildRuntimeConfigKeepsLocalRootKeysBeforeSharedTables(t *testing.T) {
	paths := testPaths(t)
	if err := os.MkdirAll(filepath.Dir(paths.SharedDefault), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.SharedDefault, []byte(mergeTestDefault), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.RuntimeConfig, []byte(runtimeLocalOverlayWithRootKeys), 0o600); err != nil {
		t.Fatal(err)
	}

	payload, _, err := buildRuntimeConfig(paths, false, true)
	if err != nil {
		t.Fatalf("buildRuntimeConfig: %v", err)
	}
	var root map[string]any
	if err := toml.Unmarshal(payload, &root); err != nil {
		t.Fatalf("rendered config is invalid TOML: %v\n%s", err, payload)
	}
	if root["service_tier"] != "default" {
		t.Fatalf("local root key service_tier was absorbed by a table: %#v\n%s", root, payload)
	}
	if _, ok := root["notify"].([]any); !ok {
		t.Fatalf("local root key notify was absorbed by a table: %#v\n%s", root, payload)
	}
	features, ok := root["features"].(map[string]any)
	if !ok || len(features) != 1 || features["goals"] != true {
		t.Fatalf("features table was polluted by local root keys: %#v\n%s", features, payload)
	}
	if _, ok := root["tui"].(map[string]any); !ok {
		t.Fatalf("local table [tui] missing: %#v", root)
	}
	if begin, table := strings.Index(string(payload), localConfigBegin), strings.Index(string(payload), "[features]"); begin < 0 || table < 0 || table < begin {
		t.Fatalf("shared tables must follow the local sentinel block:\n%s", payload)
	}

	// 渲染结果再次作为输入时必须稳定（避免每次启动都改文件）。
	if err := os.WriteFile(paths.RuntimeConfig, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	again, _, err := buildRuntimeConfig(paths, false, true)
	if err != nil {
		t.Fatalf("second buildRuntimeConfig: %v", err)
	}
	if string(again) != string(payload) {
		t.Fatalf("render is not idempotent:\nfirst:\n%s\nsecond:\n%s", payload, again)
	}
}

func TestValidateLocalOverlayRootKeysRejectsNestedKeys(t *testing.T) {
	nested := strings.Replace(runtimeLocalOverlayWithRootKeys, "goals = true", "goals = true\nservice_tier = \"default\"", 1)
	overlay := `service_tier = "default"` + "\n"
	if err := validateLocalOverlayRootKeys([]byte(nested), []byte(overlay)); err == nil {
		t.Fatal("validator accepted a local root key nested inside [features]")
	}
}
