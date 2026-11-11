package app

import (
	"os"
	"path/filepath"
	"testing"

	toml "github.com/pelletier/go-toml/v2"

	"mindfs/internal/codexsync"
	"mindfs/server/internal/preferences"
	"mindfs/server/internal/testutil"
)

func TestInitializeCodexProfileMigratesLegacyLLMuxSelectionOnce(t *testing.T) {
	home := t.TempDir()
	testutil.IsolateUserDirs(t, home)
	t.Setenv("AGENTS_ROOT", filepath.Join(home, ".agents"))
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	paths, err := codexsync.ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.SharedLLMux), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.SharedDefault, []byte("model = \"default-model\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.SharedLLMux, []byte(`model = "llmux-model"
model_provider = "llmux"

[model_providers.llmux]
name = "LLMUX"
base_url = "https://llmux.example/v1"
wire_api = "responses"
env_key = "LLMUX_API_KEY"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.CodexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.RuntimeConfig, []byte("model = \"old\"\nmodel_provider = \"llmux\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	prefs, err := preferences.NewStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := prefs.UpdateAgentLastConfigSelection("codex", preferences.LastConfigSelection{Type: "api_provider", ID: "api-llmux", Name: "llmux"}); err != nil {
		t.Fatal(err)
	}

	if err := initializeCodexProfileSelection(prefs); err != nil {
		t.Fatalf("initializeCodexProfileSelection: %v", err)
	}
	active, err := codexsync.ActiveProfile()
	if err != nil || active != "llmux" {
		t.Fatalf("active profile = %q, %v; want migrated llmux", active, err)
	}
	selection := prefs.AgentLastConfigSelections()["codex"]
	if selection.Type != "backup" || selection.ID != "shared:codex-llmux" {
		t.Fatalf("legacy selection was not migrated: %+v", selection)
	}

	if err := codexsync.ApplySharedCodexProfile("default"); err != nil {
		t.Fatal(err)
	}
	if err := prefs.UpdateAgentLastConfigSelection("codex", preferences.LastConfigSelection{Type: "backup", ID: "shared:codex-default", Name: "default"}); err != nil {
		t.Fatal(err)
	}
	if err := initializeCodexProfileSelection(prefs); err != nil {
		t.Fatalf("second startup migration: %v", err)
	}
	active, err = codexsync.ActiveProfile()
	if err != nil || active != "default" {
		t.Fatalf("second startup overwrote active profile: %q, %v", active, err)
	}
	selection = prefs.AgentLastConfigSelections()["codex"]
	if selection.ID != "shared:codex-default" {
		t.Fatalf("second startup overwrote current selection: %+v", selection)
	}
}

func TestInitializeCodexProfileRendersRuntimeAtStartup(t *testing.T) {
	home := t.TempDir()
	testutil.IsolateUserDirs(t, home)
	t.Setenv("AGENTS_ROOT", filepath.Join(home, ".agents"))
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	paths, err := codexsync.ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.SharedDefault), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.SharedDefault, []byte(`model = "gpt-6-luna"
model_reasoning_effort = "medium"

[features]
goals = true
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.CodexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	// pc-win 的旧文件形态：本地根级键落在 [features] 之后。
	if err := os.WriteFile(paths.RuntimeConfig, []byte(`model = "gpt-6-luna"
model_reasoning_effort = "medium"

[features]
goals = true
# >>> sync-codex local (not shared) >>>
service_tier = "default"

[tui]
screen_reader_detection_done = true
# <<< sync-codex local (not shared) <<<
`), 0o600); err != nil {
		t.Fatal(err)
	}
	prefs, err := preferences.NewStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := prefs.UpdateAgentLastConfigSelection("codex", preferences.LastConfigSelection{Type: "backup", ID: "shared:codex-default", Name: "default"}); err != nil {
		t.Fatal(err)
	}

	if err := initializeCodexProfileSelection(prefs); err != nil {
		t.Fatalf("initializeCodexProfileSelection: %v", err)
	}
	rendered, err := os.ReadFile(paths.RuntimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := toml.Unmarshal(rendered, &root); err != nil {
		t.Fatalf("startup render produced invalid TOML: %v\n%s", err, rendered)
	}
	if root["service_tier"] != "default" {
		t.Fatalf("startup render left the local root key nested: %#v\n%s", root, rendered)
	}
	if features, ok := root["features"].(map[string]any); !ok || len(features) != 1 || features["goals"] != true {
		t.Fatalf("features table was polluted: %#v", root["features"])
	}
	if _, ok := root["tui"].(map[string]any); !ok {
		t.Fatalf("local table [tui] lost: %#v", root)
	}
}
