package codexsync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

func TestInitializeActiveProfileOnlySetsMissingSelection(t *testing.T) {
	paths := testPaths(t)

	active, err := InitializeActiveProfileAt(paths, "llmux")
	if err != nil || active != "llmux" {
		t.Fatalf("InitializeActiveProfileAt(llmux) = %q, %v", active, err)
	}
	active, err = InitializeActiveProfileAt(paths, "default")
	if err != nil || active != "llmux" {
		t.Fatalf("second InitializeActiveProfileAt(default) = %q, %v; want persisted llmux", active, err)
	}
}

func TestRenderRuntimeUsesActiveLLMuxProfileAndKeepsDeviceOverlay(t *testing.T) {
	paths := testPaths(t)
	if err := os.MkdirAll(filepath.Dir(paths.SharedLLMux), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.SharedDefault, []byte("model = \"default-model\"\n\n[features]\ngoals = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	llmuxProfile := strings.Replace(testLLMuxProfile, "[model_providers.llmux]", "[model_providers]\n\n[model_providers.llmux]", 1)
	if err := os.WriteFile(paths.SharedLLMux, []byte(llmuxProfile), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InitializeActiveProfileAt(paths, "llmux"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.RuntimeConfig, []byte(`model = "previous-model"
model_provider = "llmux"

[model_providers]

[model_providers.llmux]
name = "old LLMUX"
base_url = "https://old-llmux.example/v1"
wire_api = "responses"
env_key = "LLMUX_API_KEY"

[model_providers.local]
name = "Local provider"
base_url = "https://local.example/v1"
wire_api = "responses"

[tui]
screen_reader_detection_done = true

[projects."/device/project"]
trust_level = "trusted"
`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := renderRuntime(paths); err != nil {
		t.Fatalf("renderRuntime: %v", err)
	}
	rendered, err := os.ReadFile(paths.RuntimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`model = "deepseek-v4"`, `model_provider = "llmux"`, `[model_providers.llmux]`, `env_key = "LLMUX_API_KEY"`, `[tui]`, `/device/project`} {
		if !strings.Contains(string(rendered), expected) {
			t.Errorf("rendered config lost %q: %s", expected, rendered)
		}
	}
	if strings.Contains(string(rendered), "experimental_bearer_token") {
		t.Fatalf("rendered config contains an inline credential: %s", rendered)
	}
	var renderedRoot map[string]any
	if err := toml.Unmarshal(rendered, &renderedRoot); err != nil {
		t.Fatalf("rendered config is invalid TOML: %v\n%s", err, rendered)
	}
	if renderedRoot["model"] != "deepseek-v4" || renderedRoot["model_provider"] != "llmux" {
		t.Fatalf("active llmux keys were nested into a table: %#v\n%s", renderedRoot, rendered)
	}
	if features, ok := renderedRoot["features"].(map[string]any); !ok || len(features) != 1 || features["goals"] != true {
		t.Fatalf("features table was polluted by root keys: %#v", renderedRoot["features"])
	}
	if err := ApplySharedCodexProfileAt(paths, "default"); err != nil {
		t.Fatalf("switch back to default: %v", err)
	}
	defaultConfig, err := os.ReadFile(paths.RuntimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(defaultConfig), "model_provider = \"llmux\"") || strings.Contains(string(defaultConfig), "[model_providers.llmux]") {
		t.Fatalf("default profile retained llmux provider tables: %s", defaultConfig)
	}
	if !strings.Contains(string(defaultConfig), "[model_providers.local]") {
		t.Fatalf("default profile dropped a device-local provider: %s", defaultConfig)
	}
}

func TestModeSyncKeepsActiveLLMuxProfile(t *testing.T) {
	_, local := newSyncRepository(t)
	paths := testPaths(t)
	paths.AgentsRoot = local
	paths.SharedDefault = filepath.Join(local, "codex", "config.toml")
	paths.SharedLLMux = filepath.Join(local, "codex", "profiles", "llmux.config.toml")
	paths.SharedProviders = filepath.Join(local, "codex", "providers.json")
	if err := os.MkdirAll(filepath.Dir(paths.SharedLLMux), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.SharedDefault, []byte("model = \"default-model\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.SharedLLMux, []byte(testLLMuxProfile), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, local, "add", "codex/config.toml", "codex/profiles/llmux.config.toml")
	gitTestRun(t, local, "commit", "-m", "add llmux profile")
	gitTestRun(t, local, "push", "origin", "HEAD:main")
	if _, err := InitializeActiveProfileAt(paths, "llmux"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.RuntimeConfig, []byte("model = \"old\"\nmodel_provider = \"llmux\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := RunAt(context.Background(), ModeSync, paths)
	if err != nil || result.State != "synced" {
		t.Fatalf("RunAt(Sync) = %+v, %v", result, err)
	}
	rendered, err := os.ReadFile(paths.RuntimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rendered), `model = "deepseek-v4"`) || !strings.Contains(string(rendered), `model_provider = "llmux"`) {
		t.Fatalf("sync replaced active llmux profile with default: %s", rendered)
	}
	active, err := ActiveProfileAt(paths)
	if err != nil || active != "llmux" {
		t.Fatalf("active profile after sync = %q, %v", active, err)
	}
}

func TestFailedSharedProfileSwitchRestoresActiveMarker(t *testing.T) {
	paths := testPaths(t)
	if err := os.MkdirAll(filepath.Dir(paths.SharedLLMux), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.SharedDefault, []byte("model = \"default-model\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.SharedLLMux, []byte(testLLMuxProfile), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InitializeActiveProfileAt(paths, "default"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.RuntimeConfig, []byte("previous runtime\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(paths.RuntimeConfig); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(paths.CodexHome, "missing-target"), paths.RuntimeConfig); err != nil {
		t.Fatal(err)
	}

	if err := ApplySharedCodexProfileAt(paths, "llmux"); err == nil {
		t.Fatal("profile switch unexpectedly rendered through a runtime symlink")
	}
	active, err := ActiveProfileAt(paths)
	if err != nil || active != "default" {
		t.Fatalf("failed switch left active profile %q, %v; want default", active, err)
	}
}
