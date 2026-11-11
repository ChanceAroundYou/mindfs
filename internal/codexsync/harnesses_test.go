package codexsync

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func testHarnessPaths(t *testing.T) Paths {
	t.Helper()
	root := t.TempDir()
	return Paths{
		AgentsRoot:            filepath.Join(root, ".agents"),
		CodexHome:             filepath.Join(root, ".codex"),
		MindFSConfig:          filepath.Join(root, ".config", "mindfs"),
		SharedDefault:         filepath.Join(root, ".agents", "codex", "config.toml"),
		SharedLLMux:           filepath.Join(root, ".agents", "codex", "profiles", "llmux.config.toml"),
		SharedProviders:       filepath.Join(root, ".agents", "codex", "providers.json"),
		RuntimeConfig:         filepath.Join(root, ".codex", "config.toml"),
		RuntimeLLMux:          filepath.Join(root, ".codex", "llmux.config.toml"),
		SnapshotsRoot:         filepath.Join(root, ".config", "mindfs", "agents-config"),
		DefaultSnapshot:       filepath.Join(root, ".config", "mindfs", "agents-config", "codex-default", "002-config.toml"),
		LLMuxSnapshot:         filepath.Join(root, ".config", "mindfs", "agents-config", "codex-llmux", "002-config.toml"),
		LegacyProviders:       filepath.Join(root, ".config", "mindfs", "api-providers.json"),
		LocalSecrets:          filepath.Join(root, ".config", "mindfs", "api-provider-secrets.json"),
		StateDir:              filepath.Join(root, ".config", "mindfs", "agents-config-sync"),
		ClaudeHome:            filepath.Join(root, ".claude"),
		SharedClaude:          filepath.Join(root, ".agents", "claude"),
		SharedClaudeSettings:  filepath.Join(root, ".agents", "claude", "settings.shared.json"),
		SharedClaudeMCP:       filepath.Join(root, ".agents", "claude", "mcp.shared.json"),
		ClaudeLocalSettings:   filepath.Join(root, ".claude", "settings.local.json"),
		ClaudeRuntimeSettings: filepath.Join(root, ".claude", "settings.json"),
		ClaudeRuntimeMCP:      filepath.Join(root, ".claude", ".mcp.json"),
		ClaudeJSON:            filepath.Join(root, ".claude.json"),
	}
}

func TestHarnessStatusesAtReportsAvailability(t *testing.T) {
	paths := testHarnessPaths(t)
	writeTestFile(t, paths.SharedDefault, "model = \"gpt\"\n")
	writeTestFile(t, paths.SharedClaudeSettings, `{"model":"sonnet"}`)

	statuses := HarnessStatusesAt(paths)
	if len(statuses) != 2 {
		t.Fatalf("harnesses = %d, want 2", len(statuses))
	}
	byName := map[string]HarnessInfo{}
	for _, info := range statuses {
		byName[info.Name] = info
	}
	if !byName["codex"].Available || !byName["claude"].Available {
		t.Fatalf("expected codex and claude available: %+v", statuses)
	}
	if byName["claude"].SharedDir != "claude" {
		t.Fatalf("claude shared dir = %q", byName["claude"].SharedDir)
	}

	if err := os.Remove(paths.SharedClaudeSettings); err != nil {
		t.Fatal(err)
	}
	for _, info := range HarnessStatusesAt(paths) {
		if info.Name == "claude" {
			if info.Available || !strings.Contains(info.Message, "settings.shared.json") {
				t.Fatalf("claude should be unavailable with reason: %+v", info)
			}
		}
	}
}

func TestIsAllowedRepoPathCoversSharedHarnessDirs(t *testing.T) {
	allowed := []string{
		"codex/config.toml",
		"codex/profiles/llmux.config.toml",
		"codex/providers.json",
		"claude/settings.shared.json",
		"claude/rules/common/engineering.md",
		"dsh/README.md",
		"skills/brainstorming/SKILL.md",
		"scripts/render.sh",
	}
	for _, path := range allowed {
		if !IsAllowedRepoPath(path) {
			t.Fatalf("IsAllowedRepoPath(%q) = false, want true", path)
		}
	}
	rejected := []string{
		"",
		"../escape",
		"/etc/passwd",
		`codex\config.toml`,
		"./codex/config.toml",
		"codex/AGENTS.md",
		"codex/providers.json.tmp",
		"secrets/key.txt",
		"README.md",
		"internal/codexsync/run.go",
	}
	for _, path := range rejected {
		if IsAllowedRepoPath(path) {
			t.Fatalf("IsAllowedRepoPath(%q) = true, want false", path)
		}
	}
}

func TestValidateAllSharedSourcesIncludesClaude(t *testing.T) {
	paths := testHarnessPaths(t)
	writeTestFile(t, paths.SharedClaudeSettings, `{"env":{"ANTHROPIC_API_KEY":"sk-live-0123456789abcdef"}}`)
	if err := validateAllSharedSources(paths); err == nil || !strings.Contains(err.Error(), "claude") {
		t.Fatalf("expected claude validation error, got %v", err)
	}

	writeTestFile(t, paths.SharedClaudeSettings, `{"env":{"ANTHROPIC_API_KEY":"${ANTHROPIC_API_KEY}"}}`)
	if err := validateAllSharedSources(paths); err != nil {
		t.Fatalf("placeholder credential must be allowed: %v", err)
	}
}

func TestResultSerializesHarnessAvailability(t *testing.T) {
	paths := testHarnessPaths(t)
	writeTestFile(t, paths.SharedDefault, "model = \"gpt\"\n")
	writeTestFile(t, paths.SharedClaudeSettings, `{"model":"sonnet"}`)
	payload, err := json.Marshal(Result{State: "idle", Harnesses: HarnessStatusesAt(paths)})
	if err != nil {
		t.Fatal(err)
	}
	body := string(payload)
	for _, expected := range []string{`"harnesses"`, `"name":"codex"`, `"name":"claude"`, `"available":true`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("serialized result missing %s: %s", expected, body)
		}
	}
	if strings.Contains(body, "message") {
		t.Fatalf("available harnesses should not carry an unavailable reason: %s", body)
	}
}

func TestRenderSharedHarnessRuntimesSkipsUnavailableHarness(t *testing.T) {
	paths := testHarnessPaths(t)
	if err := renderSharedHarnessRuntimes(paths); err != nil {
		t.Fatalf("render without shared claude dir: %v", err)
	}
	if _, err := os.Stat(paths.ClaudeRuntimeSettings); !os.IsNotExist(err) {
		t.Fatalf("settings.json should not be created when claude is unavailable: %v", err)
	}
}
