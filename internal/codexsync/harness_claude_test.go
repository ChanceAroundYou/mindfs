package codexsync

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func claudeFixture(t *testing.T) Paths {
	t.Helper()
	paths := testHarnessPaths(t)
	writeTestFile(t, paths.SharedClaudeSettings, `{
  "env": {"ANTHROPIC_BASE_URL": "https://llmux.example/v1", "SHARED_ONLY": "yes"},
  "model": "sonnet",
  "permissions": {"defaultMode": "bypassPermissions", "deny": ["Bash(dd:*)", "Bash(rm -rf /:*)"]},
  "hooks": {"PostToolUse": [{"matcher": "Bash"}]}
}`)
	writeTestFile(t, paths.SharedClaudeMCP, `{"mcpServers":{"shared":{"command":"${HOME}/bin/shared"},"disabled-here":{"command":"x"}}}`)
	writeTestFile(t, paths.ClaudeLocalSettings, `{
  "env": {"SHARED_ONLY": "overridden", "LOCAL_ONLY": "yes"},
  "permissions": {"deny": ["Bash(rm -rf /:*)", "Bash(mkfs:*)"]},
  "mcpServers": {"disabled-here": null, "local":{"url":"${HOME}/local"}}
}`)
	writeTestFile(t, paths.ClaudeJSON, `{"mcpServers":{"old":{"command":"old"},"disabled-here":{"command":"y"}},"projects":{"/keep":{}}}`)
	return paths
}

func readJSONTest(t *testing.T, path string) map[string]any {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var value map[string]any
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatalf("decode %s: %v (%s)", path, err, payload)
	}
	return value
}

func TestRenderClaudeRuntimeMergesLayers(t *testing.T) {
	paths := claudeFixture(t)
	if err := renderClaudeRuntime(paths); err != nil {
		t.Fatalf("render: %v", err)
	}

	settings := readJSONTest(t, paths.ClaudeRuntimeSettings)
	env := settings["env"].(map[string]any)
	if env["SHARED_ONLY"] != "overridden" || env["LOCAL_ONLY"] != "yes" || env["ANTHROPIC_BASE_URL"] != "https://llmux.example/v1" {
		t.Fatalf("env merge wrong: %+v", env)
	}
	deny := settings["permissions"].(map[string]any)["deny"].([]any)
	gotDeny := make([]string, len(deny))
	for i, item := range deny {
		gotDeny[i] = item.(string)
	}
	wantDeny := []string{"Bash(dd:*)", "Bash(mkfs:*)", "Bash(rm -rf /:*)"}
	if !reflect.DeepEqual(gotDeny, wantDeny) {
		t.Fatalf("deny union = %v, want %v", gotDeny, wantDeny)
	}
	if settings["permissions"].(map[string]any)["defaultMode"] != "bypassPermissions" {
		t.Fatal("permissions.defaultMode should survive the merge")
	}

	mcp := readJSONTest(t, paths.ClaudeRuntimeMCP)
	servers := mcp["mcpServers"].(map[string]any)
	if _, exists := servers["disabled-here"]; exists {
		t.Fatal("null local MCP entry must disable the shared server")
	}
	home, _ := os.UserHomeDir()
	if servers["shared"].(map[string]any)["command"] != home+"/bin/shared" {
		t.Fatalf("${HOME} was not expanded: %+v", servers["shared"])
	}
	if servers["local"].(map[string]any)["url"] != home+"/local" {
		t.Fatalf("local MCP ${HOME} was not expanded: %+v", servers["local"])
	}

	claudeJSON := readJSONTest(t, paths.ClaudeJSON)
	rootServers := claudeJSON["mcpServers"].(map[string]any)
	if _, exists := rootServers["disabled-here"]; exists {
		t.Fatal("~/.claude.json must drop locally disabled servers")
	}
	if _, exists := rootServers["old"]; !exists {
		t.Fatal("~/.claude.json mcpServers must be merged, not replaced")
	}
	if _, exists := claudeJSON["projects"]; !exists {
		t.Fatal("~/.claude.json device-local keys must be preserved")
	}

	if runtime.GOOS != "windows" {
		for _, path := range []string{paths.ClaudeRuntimeSettings, paths.ClaudeRuntimeMCP, paths.ClaudeJSON} {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o600 {
				t.Fatalf("%s mode = %o, want 600", path, info.Mode().Perm())
			}
		}
	}
}

func TestRenderClaudeRuntimeSkipsSemanticallyEqualWrites(t *testing.T) {
	paths := claudeFixture(t)
	if err := renderClaudeRuntime(paths); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(paths.ClaudeRuntimeSettings)
	if err != nil {
		t.Fatal(err)
	}
	// 改写键顺序但语义相同：不应重新落盘。
	settings := readJSONTest(t, paths.ClaudeRuntimeSettings)
	reordered, err := json.MarshalIndent(settings, "", "    ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.ClaudeRuntimeSettings, reordered, 0o600); err != nil {
		t.Fatal(err)
	}
	reorderedInfo, _ := os.Stat(paths.ClaudeRuntimeSettings)
	if err := renderClaudeRuntime(paths); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(paths.ClaudeRuntimeSettings)
	if after.ModTime() != reorderedInfo.ModTime() || after.Size() != reorderedInfo.Size() {
		t.Fatalf("semantically equal settings.json was rewritten (%v -> %v)", reorderedInfo.ModTime(), after.ModTime())
	}
	if before.Mode() != after.Mode() {
		t.Fatalf("mode changed: %v -> %v", before.Mode(), after.Mode())
	}
}

func TestRenderClaudeRuntimeRefusesInvalidExistingSettings(t *testing.T) {
	paths := claudeFixture(t)
	writeTestFile(t, paths.ClaudeRuntimeSettings, `not json`)
	err := renderClaudeRuntime(paths)
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("expected refusal, got %v", err)
	}
	payload, _ := os.ReadFile(paths.ClaudeRuntimeSettings)
	if string(payload) != "not json" {
		t.Fatalf("invalid existing file was modified: %q", payload)
	}
}

func TestValidateClaudeSharedRejectsCredentials(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		wantErr bool
	}{
		{name: "placeholder api key", payload: `{"env":{"ANTHROPIC_API_KEY":"${ANTHROPIC_API_KEY}"}}`},
		{name: "empty secret", payload: `{"env":{"CLIENT_SECRET":""}}`},
		{name: "placeholder in header", payload: `{"mcpServers":{"x":{"headers":{"Authorization":"Bearer ${MCP_TOKEN}"}}}}`},
		{name: "non-secret token name", payload: `{"env":{"CLAUDE_CODE_TOTAL_TOKENS_REMINDER":"off"}}`},
		{name: "literal api key", payload: `{"env":{"ANTHROPIC_API_KEY":"sk-live-0123456789abcdef"}}`, wantErr: true},
		{name: "literal token value", payload: `{"mcpServers":{"x":{"env":{"SOME_VALUE":"ghp_0123456789abcdefghijklmnop"}}}}`, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			paths := testHarnessPaths(t)
			writeTestFile(t, paths.SharedClaudeSettings, tc.payload)
			err := validateClaudeShared(paths)
			if tc.wantErr && err == nil {
				t.Fatal("expected validation error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestClaudeWriteTargetPreservesSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink layout is a Unix dotfiles concern")
	}
	paths := claudeFixture(t)
	real := filepath.Join(t.TempDir(), "rendered-settings.json")
	if err := os.WriteFile(real, []byte(`{"stale":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.ClaudeHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, paths.ClaudeRuntimeSettings); err != nil {
		t.Fatal(err)
	}
	if err := renderClaudeRuntime(paths); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(paths.ClaudeRuntimeSettings)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("settings.json symlink was replaced by a regular file")
	}
	rendered := readJSONTest(t, real)
	if rendered["model"] != "sonnet" {
		t.Fatalf("symlink target was not rendered: %+v", rendered)
	}
}
