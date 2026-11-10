package api

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/internal/codexsync"
	"mindfs/server/internal/testutil"
)

func TestSharedCodexProfilesAreListedSeparatelyFromLocalManifest(t *testing.T) {
	home := t.TempDir()
	testutil.IsolateUserDirs(t, home)
	agentsRoot := filepath.Join(home, ".agents")
	t.Setenv("AGENTS_ROOT", agentsRoot)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("LLMUX_API_KEY", "")
	paths, err := codexsync.ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.SharedLLMux), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.SharedDefault, []byte("model = \"gpt-5-codex\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	llmux := "model = \"deepseek-v4\"\nmodel_provider = \"llmux\"\n\n[model_providers.llmux]\nname = \"LLMUX\"\nbase_url = \"https://llmux.example/v1\"\nwire_api = \"responses\"\nenv_key = \"LLMUX_API_KEY\"\n"
	if err := os.WriteFile(paths.SharedLLMux, []byte(llmux), 0o644); err != nil {
		t.Fatal(err)
	}
	local := agentConfigManifestEntry{ID: "codex-personal", Agent: "codex", Name: "personal", Source: "local"}
	if err := writeAgentConfigManifest([]agentConfigManifestEntry{local}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(mustManifestPath(t))
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/api/agent-config/backups?agent=codex", nil)
	(&HTTPHandler{}).handleAgentConfigBackupsList(recorder, request)
	if recorder.Code != 200 {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var entries []agentConfigManifestEntry
	if err := json.Unmarshal(recorder.Body.Bytes(), &entries); err != nil {
		t.Fatal(err)
	}
	byID := map[string]agentConfigManifestEntry{}
	for _, entry := range entries {
		byID[entry.ID] = entry
	}
	if byID[local.ID].Source != "local" || byID["shared:codex-default"].Source != "shared" || byID["shared:codex-llmux"].Source != "shared" {
		t.Fatalf("unexpected backup sources: %+v", byID)
	}
	if byID["shared:codex-llmux"].LocalCredentialAvailable == nil || *byID["shared:codex-llmux"].LocalCredentialAvailable {
		t.Fatalf("llmux profile did not report missing local key: %+v", byID["shared:codex-llmux"])
	}
	after, err := os.ReadFile(mustManifestPath(t))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) || strings.Contains(string(after), "shared:codex-") {
		t.Fatalf("listing wrote synthesized profiles into local manifest: %s", after)
	}
}

func TestSwitchSharedCodexProfilePreservesDeviceAuthAndSnapshots(t *testing.T) {
	home := t.TempDir()
	testutil.IsolateUserDirs(t, home)
	t.Setenv("AGENTS_ROOT", filepath.Join(home, ".agents"))
	codexHome := filepath.Join(home, ".codex")
	t.Setenv("CODEX_HOME", codexHome)
	paths, err := codexsync.ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.SharedDefault), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.SharedDefault, []byte("model = \"gpt-5-codex\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.RuntimeConfig, []byte(`model = "device-old"

[tui]
screen_reader_detection_done = true

[projects."/device/project"]
trust_level = "trusted"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	const auth = `{"access_token":"device-auth-secret"}`
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), []byte(auth), 0o600); err != nil {
		t.Fatal(err)
	}
	configRoot, err := agentConfigRootDir()
	if err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(configRoot, "codex-local", "002-config.toml")
	if err := os.MkdirAll(filepath.Dir(snapshot), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(snapshot, []byte("device snapshot must stay local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := []agentConfigManifestEntry{{ID: "codex-local", Agent: "codex", Name: "personal", Source: "local"}}
	if err := writeAgentConfigManifest(manifest); err != nil {
		t.Fatal(err)
	}
	manifestBefore, err := os.ReadFile(mustManifestPath(t))
	if err != nil {
		t.Fatal(err)
	}
	entry, needsConfirm, err := switchAgentConfig(agentConfigSwitchRequest{ID: "shared:codex-default"}, nil)
	if err != nil || needsConfirm {
		t.Fatalf("switch shared profile: needsConfirm=%t err=%v", needsConfirm, err)
	}
	if entry.Source != "shared" || entry.Name != "default" {
		t.Fatalf("switched entry = %+v", entry)
	}
	rendered, err := os.ReadFile(paths.RuntimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"model = \"gpt-5-codex\"", "sync-codex local", "[tui]", "/device/project"} {
		if !strings.Contains(string(rendered), expected) {
			t.Errorf("shared switch lost %q: %s", expected, rendered)
		}
	}
	gotAuth, err := os.ReadFile(filepath.Join(codexHome, "auth.json"))
	if err != nil || string(gotAuth) != auth {
		t.Fatalf("auth changed: %q, %v", gotAuth, err)
	}
	gotSnapshot, err := os.ReadFile(snapshot)
	if err != nil || string(gotSnapshot) != "device snapshot must stay local\n" {
		t.Fatalf("local snapshot changed: %q, %v", gotSnapshot, err)
	}
	manifestAfter, err := os.ReadFile(mustManifestPath(t))
	if err != nil || string(manifestAfter) != string(manifestBefore) {
		t.Fatalf("local manifest changed after shared switch: %v\n%s", err, manifestAfter)
	}
}

func mustManifestPath(t *testing.T) string {
	t.Helper()
	path, err := agentConfigManifestPath()
	if err != nil {
		t.Fatal(err)
	}
	return path
}
