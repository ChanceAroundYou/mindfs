package api

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/internal/codexsync"
	"mindfs/server/internal/agent"
	"mindfs/server/internal/testutil"
)

func TestExportCodexBackupProjectsOnlyPortableSettings(t *testing.T) {
	home := t.TempDir()
	testutil.IsolateUserDirs(t, home)
	agentsRoot := filepath.Join(home, ".agents")
	t.Setenv("AGENTS_ROOT", agentsRoot)
	codexHome := filepath.Join(home, ".codex")
	t.Setenv("CODEX_HOME", codexHome)
	initTestGitRepo(t, agentsRoot)
	configPath := filepath.Join(home, "mindfs-agents.json")
	t.Setenv("MINDFS_AGENTS_CONFIG", configPath)
	writeJSON(t, configPath, agent.Config{Agents: []agent.Definition{{Name: "codex", Command: "codex", Protocol: agent.ProtocolCodexSDK}}})
	paths, err := codexsync.ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	snapshot := `model = "gpt-5-codex"
model_reasoning_effort = "high"

[features]
goals = true

[tui]
screen_reader_detection_done = true

[projects."/device/private-project"]
trust_level = "trusted"

[hooks.state."/device/hook.json:session_start:0:0"]
trusted_hash = "private-hook-hash"
`
	snapshotPath := filepath.Join(paths.SnapshotsRoot, "codex-default", "002-config.toml")
	if err := os.MkdirAll(filepath.Dir(snapshotPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(snapshotPath, []byte(snapshot), 0o600); err != nil {
		t.Fatal(err)
	}
	entry := agentConfigManifestEntry{
		ID: "codex-default", Agent: "codex", Name: "default", Source: "local",
		Sources: []agentConfigSource{{SourcePath: paths.RuntimeConfig, BackupPath: "codex-default/002-config.toml"}},
	}
	if err := exportCodexBackupSnapshot(entry); err != nil {
		t.Fatalf("exportCodexBackupSnapshot: %v", err)
	}
	shared, err := os.ReadFile(paths.SharedDefault)
	if err != nil {
		t.Fatal(err)
	}
	for _, excluded := range []string{"tui", "projects", "hooks", "private-project", "private-hook-hash"} {
		if strings.Contains(string(shared), excluded) {
			t.Errorf("shared default retained local field %q: %s", excluded, shared)
		}
	}
	for _, included := range []string{"gpt-5-codex", "high", "goals"} {
		if !strings.Contains(string(shared), included) {
			t.Errorf("shared default omitted portable field %q: %s", included, shared)
		}
	}
	full, err := os.ReadFile(snapshotPath)
	if err != nil || !strings.Contains(string(full), "private-hook-hash") {
		t.Fatalf("complete recovery snapshot was changed: %v", err)
	}
}

func TestExportCodexLLMuxBackupRemovesBearerAndKeepsSnapshot(t *testing.T) {
	home := t.TempDir()
	testutil.IsolateUserDirs(t, home)
	agentsRoot := filepath.Join(home, ".agents")
	t.Setenv("AGENTS_ROOT", agentsRoot)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	initTestGitRepo(t, agentsRoot)
	configPath := filepath.Join(home, "mindfs-agents.json")
	t.Setenv("MINDFS_AGENTS_CONFIG", configPath)
	writeJSON(t, configPath, agent.Config{Agents: []agent.Definition{{Name: "codex", Command: "codex", Protocol: agent.ProtocolCodexSDK}}})
	paths, err := codexsync.ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	snapshot := `model = "deepseek-v4"
model_reasoning_effort = "high"
model_provider = "llmux"

[model_providers.llmux]
name = "LLMUX"
base_url = "https://llmux.example/v1"
wire_api = "responses"
experimental_bearer_token = "never-share-this-token"
`
	snapshotPath := filepath.Join(paths.SnapshotsRoot, "codex-llmux", "002-config.toml")
	if err := os.MkdirAll(filepath.Dir(snapshotPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(snapshotPath, []byte(snapshot), 0o600); err != nil {
		t.Fatal(err)
	}
	entry := agentConfigManifestEntry{
		ID: "codex-llmux", Agent: "codex", Name: "llmux", Source: "local",
		Sources: []agentConfigSource{{SourcePath: paths.RuntimeConfig, BackupPath: "codex-llmux/002-config.toml"}},
	}
	if err := exportCodexBackupSnapshot(entry); err != nil {
		t.Fatalf("exportCodexBackupSnapshot: %v", err)
	}
	shared, err := os.ReadFile(paths.SharedLLMux)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"model = 'deepseek-v4'", "env_key = 'LLMUX_API_KEY'"} {
		if !strings.Contains(string(shared), expected) {
			t.Errorf("shared llmux profile omitted %q: %s", expected, shared)
		}
	}
	for _, forbidden := range []string{"experimental_bearer_token", "never-share-this-token"} {
		if strings.Contains(string(shared), forbidden) {
			t.Errorf("shared llmux profile retained %q: %s", forbidden, shared)
		}
	}
	full, err := os.ReadFile(snapshotPath)
	if err != nil || !strings.Contains(string(full), "never-share-this-token") {
		t.Fatalf("complete local recovery snapshot changed: %v", err)
	}
}

func TestSharedExportRefusesExistingUncommittedEdit(t *testing.T) {
	root := t.TempDir()
	paths := codexsync.Paths{
		AgentsRoot:      root,
		MindFSConfig:    filepath.Join(root, "config"),
		SharedDefault:   filepath.Join(root, "codex", "config.toml"),
		SharedLLMux:     filepath.Join(root, "codex", "profiles", "llmux.config.toml"),
		SharedProviders: filepath.Join(root, "codex", "providers.json"),
		RuntimeConfig:   filepath.Join(root, "codex-home", "config.toml"),
		RuntimeLLMux:    filepath.Join(root, "codex-home", "llmux.config.toml"),
		SnapshotsRoot:   filepath.Join(root, "config", "agents-config"),
		DefaultSnapshot: filepath.Join(root, "config", "agents-config", "codex-default", "002-config.toml"),
		LLMuxSnapshot:   filepath.Join(root, "config", "agents-config", "codex-llmux", "002-config.toml"),
		LegacyProviders: filepath.Join(root, "config", "api-providers.json"),
		LocalSecrets:    filepath.Join(root, "config", "api-provider-secrets.json"),
		StateDir:        filepath.Join(root, "config", "sync"),
	}
	initTestGitRepo(t, root)
	if err := os.MkdirAll(filepath.Dir(paths.SharedDefault), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.SharedDefault, []byte("model = \"old\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTest(t, root, "add", "codex/config.toml")
	gitTest(t, root, "commit", "-m", "initial shared config")
	if err := os.WriteFile(paths.SharedDefault, []byte("model = \"uncommitted\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := codexsync.ExportSharedProfileAt(paths, "default", []byte("model = \"new\"\n"))
	if err == nil {
		t.Fatal("export overwrote a dirty shared file")
	}
	got, err := os.ReadFile(paths.SharedDefault)
	if err != nil || string(got) != "model = \"uncommitted\"\n" {
		t.Fatalf("dirty source was changed: %q, %v", got, err)
	}
}

func initTestGitRepo(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	gitTest(t, root, "init", "--initial-branch=main")
	gitTest(t, root, "config", "user.name", "MindFS test")
	gitTest(t, root, "config", "user.email", "mindfs@example.invalid")
}

func gitTest(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
