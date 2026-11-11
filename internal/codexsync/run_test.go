package codexsync

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckIsReadOnly(t *testing.T) {
	paths := testPaths(t)
	initSyncRepo(t, paths.AgentsRoot)
	if err := os.MkdirAll(filepath.Dir(paths.SharedDefault), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.SharedDefault, []byte("model = \"gpt-5-codex\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, paths.AgentsRoot, "add", "codex/config.toml")
	gitTestRun(t, paths.AgentsRoot, "commit", "-m", "shared default")
	gitTestRun(t, paths.AgentsRoot, "push")
	if err := os.WriteFile(paths.RuntimeConfig, []byte("model = \"local\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(paths.RuntimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	result, err := RunAt(context.Background(), ModeCheck, paths)
	if err != nil {
		t.Fatalf("RunAt(Check): %v", err)
	}
	if result.State == "" {
		t.Fatal("Check returned no sync state")
	}
	after, err := os.Stat(paths.RuntimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("Check modified runtime config mtime")
	}
	if got := gitTestOutput(t, paths.AgentsRoot, "status", "--porcelain"); got != "" {
		t.Fatalf("Check modified Git worktree: %q", got)
	}
}

func TestCheckReturnsErrorWhenSharedWorktreeIsPending(t *testing.T) {
	_, local := newSyncRepository(t)
	paths := testPaths(t)
	paths.AgentsRoot = local
	paths.SharedDefault = filepath.Join(local, "codex", "config.toml")
	paths.SharedLLMux = filepath.Join(local, "codex", "profiles", "llmux.config.toml")
	paths.SharedProviders = filepath.Join(local, "codex", "providers.json")
	if err := os.WriteFile(paths.SharedDefault, []byte("model = \"local-edit\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := RunAt(context.Background(), ModeCheck, paths)
	if err == nil || result.State != "pending" {
		t.Fatalf("RunAt(Check) = %+v, %v; want pending state and non-nil error", result, err)
	}
}

func TestApplyMigratesSanitizedSnapshotAndIsIdempotent(t *testing.T) {
	paths := testPaths(t)
	initSyncRepo(t, paths.AgentsRoot)
	if err := os.WriteFile(paths.SharedDefault, []byte("model = \"gpt-5-codex\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, paths.AgentsRoot, "add", "codex/config.toml")
	gitTestRun(t, paths.AgentsRoot, "commit", "-m", "current shared default")
	gitTestRun(t, paths.AgentsRoot, "push")
	if err := os.MkdirAll(filepath.Dir(paths.DefaultSnapshot), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.LLMuxSnapshot), 0o755); err != nil {
		t.Fatal(err)
	}
	snapshot := `model = "gpt-5-codex"
model_reasoning_effort = "high"
model_provider = "llmux"

[features]
goals = true

[tui]
screen_reader_detection_done = true

[projects."/home/alice/project"]
trust_level = "trusted"

[model_providers.llmux]
name = "LLMUX"
base_url = "https://llmux.example/v1"
wire_api = "responses"
experimental_bearer_token = "never-share-this-token"
`
	if err := os.WriteFile(paths.DefaultSnapshot, []byte(snapshot), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.LLMuxSnapshot, []byte(snapshot), 0o600); err != nil {
		t.Fatal(err)
	}
	active := `model = "old-local"
model_provider = "llmux"

[features]
goals = false

[tui]
screen_reader_detection_done = true

[projects."/home/bob/project"]
trust_level = "trusted"

[model_providers.llmux]
name = "LLMUX"
base_url = "https://llmux.example/v1"
wire_api = "responses"
experimental_bearer_token = "local-token"
`
	if err := os.WriteFile(paths.RuntimeConfig, []byte(active), 0o600); err != nil {
		t.Fatal(err)
	}

	first, err := RunAt(context.Background(), ModeApply, paths)
	if err != nil {
		t.Fatalf("first Apply: %v (%+v)", err, first)
	}
	shared, err := os.ReadFile(paths.SharedDefault)
	if err != nil {
		t.Fatal(err)
	}
	llmux, err := os.ReadFile(paths.SharedLLMux)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(shared)+string(llmux), "never-share-this-token") || strings.Contains(string(shared)+string(llmux), "local-token") {
		t.Fatal("credential was written to shared sources")
	}
	if !strings.Contains(string(shared), "model = 'gpt-5-codex'") || !strings.Contains(string(llmux), "env_key = 'LLMUX_API_KEY'") {
		t.Fatalf("shared projection missing expected settings: default=%s llmux=%s", shared, llmux)
	}
	rendered, err := os.ReadFile(paths.RuntimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	for _, keep := range []string{`model = 'gpt-5-codex'`, `[tui]`, `/home/bob/project`} {
		if !strings.Contains(string(rendered), keep) {
			t.Errorf("render lost setting %q: %s", keep, rendered)
		}
	}
	for _, remove := range []string{"experimental_bearer_token", "local-token", "/home/alice/project"} {
		if strings.Contains(string(rendered), remove) {
			t.Errorf("render retained %q: %s", remove, rendered)
		}
	}
	firstContent := string(rendered)
	second, err := RunAt(context.Background(), ModeApply, paths)
	if err != nil {
		t.Fatalf("second Apply: %v (%+v)", err, second)
	}
	rendered, err = os.ReadFile(paths.RuntimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	if string(rendered) != firstContent {
		t.Fatalf("second Apply changed rendered config:\nfirst %s\nsecond %s", firstContent, rendered)
	}
	if matches, _ := filepath.Glob(filepath.Join(paths.StateDir, "rollback", "config.toml.*.bak")); len(matches) == 0 {
		t.Fatal("Apply did not preserve runtime rollback copy")
	}
}

func TestApplyTightensLocalProviderSecretPermissions(t *testing.T) {
	paths := testPaths(t)
	if err := os.WriteFile(paths.LocalSecrets, []byte("{\n  \"api-provider\": \"private-key\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := migrate(paths); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	info, err := os.Stat(paths.LocalSecrets)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("local secret mode = %#o, want 0600", got)
	}
	secrets, err := ReadLocalProviderSecrets(paths.LocalSecrets)
	if err != nil || secrets["api-provider"] != "private-key" {
		t.Fatalf("secret was not preserved: %#v, %v", secrets, err)
	}
}

func TestApplyDoesNotReseedDeletedLegacyProviderAfterMigration(t *testing.T) {
	_, local := newSyncRepository(t)
	paths := testPaths(t)
	paths.AgentsRoot = local
	paths.SharedDefault = filepath.Join(local, "codex", "config.toml")
	paths.SharedLLMux = filepath.Join(local, "codex", "profiles", "llmux.config.toml")
	paths.SharedProviders = filepath.Join(local, "codex", "providers.json")
	legacy := `[{"id":"api-provider","name":"Provider","baseUrl":"https://provider.example/v1","modelFamilies":[],"models":[],"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z","apiKey":"local-secret"}]`
	if err := os.WriteFile(paths.LegacyProviders, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RunAt(context.Background(), ModeApply, paths); err != nil {
		t.Fatalf("initial Apply: %v", err)
	}
	if err := WriteProviderMetadata(paths.SharedProviders, []ProviderMetadata{}); err != nil {
		t.Fatal(err)
	}
	if _, err := RunAt(context.Background(), ModeApply, paths); err != nil {
		t.Fatalf("repeated Apply: %v", err)
	}
	providers, err := ReadProviderMetadata(paths.SharedProviders)
	if err != nil || len(providers) != 0 {
		t.Fatalf("deleted provider was reseeded: %#v, %v", providers, err)
	}
	secrets, err := ReadLocalProviderSecrets(paths.LocalSecrets)
	if err != nil || secrets["api-provider"] != "local-secret" {
		t.Fatalf("local API key was not preserved: %#v, %v", secrets, err)
	}
}

func TestSyncPullsRemoteDefaultAndKeepsDeviceOverlay(t *testing.T) {
	remote, local := newSyncRepository(t)
	paths := testPaths(t)
	paths.AgentsRoot = local
	paths.SharedDefault = filepath.Join(local, "codex", "config.toml")
	paths.SharedLLMux = filepath.Join(local, "codex", "profiles", "llmux.config.toml")
	paths.SharedProviders = filepath.Join(local, "codex", "providers.json")
	active := `model = "device-old"

[tui]
screen_reader_detection_done = true

[projects."/device/project"]
trust_level = "trusted"
`
	if err := os.WriteFile(paths.RuntimeConfig, []byte(active), 0o600); err != nil {
		t.Fatal(err)
	}
	peer := filepath.Join(filepath.Dir(local), "peer-sync")
	gitRun(t, "", "clone", remote, peer)
	gitRun(t, peer, "config", "user.name", "Peer")
	gitRun(t, peer, "config", "user.email", "peer@example.invalid")
	if err := os.WriteFile(filepath.Join(peer, "codex", "config.toml"), []byte("model = \"remote-model\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, peer, "add", "codex/config.toml")
	gitRun(t, peer, "commit", "-m", "remote model update")
	gitRun(t, peer, "push", "origin", "HEAD:main")

	result, err := RunAt(context.Background(), ModeSync, paths)
	if err != nil || result.State != "synced" {
		t.Fatalf("RunAt(Sync) = %+v, %v", result, err)
	}
	rendered, err := os.ReadFile(paths.RuntimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"model = \"remote-model\"", localConfigBegin, "[tui]", "/device/project"} {
		if !strings.Contains(string(rendered), expected) {
			t.Errorf("rendered config lost %q: %s", expected, rendered)
		}
	}
}

func TestRenderRuntimeCheckIsReadOnly(t *testing.T) {
	paths := testPaths(t)
	if err := os.MkdirAll(filepath.Dir(paths.SharedDefault), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.SharedDefault, []byte("model = \"expected\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.RuntimeConfig, []byte("model = \"actual\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(paths.RuntimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckRuntimeAt(paths); err == nil {
		t.Fatal("CheckRuntimeAt accepted a drifted runtime config")
	}
	after, err := os.Stat(paths.RuntimeConfig)
	if err != nil || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("check-runtime changed runtime mtime: %v", err)
	}
}

func TestRenderModeRequiresSharedDefaultProfile(t *testing.T) {
	paths := testPaths(t)
	const previous = "model = \"local-runtime\"\n"
	if err := os.WriteFile(paths.RuntimeConfig, []byte(previous), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := RunAt(context.Background(), ModeRender, paths)
	if err == nil || result.State != "invalid" {
		t.Fatalf("RunAt(Render) = %+v, %v; want invalid state and missing-profile error", result, err)
	}
	got, readErr := os.ReadFile(paths.RuntimeConfig)
	if readErr != nil || string(got) != previous {
		t.Fatalf("render changed runtime without shared default: %q, %v", got, readErr)
	}
	if _, err := os.Stat(activeProfilePath(paths)); !os.IsNotExist(err) {
		t.Fatalf("render created active-profile marker without shared default: %v", err)
	}
}

func TestRenderRuntimeAtRequiresSharedDefaultProfile(t *testing.T) {
	paths := testPaths(t)
	const previous = "model = \"local-runtime\"\n"
	if err := os.WriteFile(paths.RuntimeConfig, []byte(previous), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RenderRuntimeAt(paths); err == nil {
		t.Fatal("RenderRuntimeAt succeeded without a shared default profile")
	}
	got, readErr := os.ReadFile(paths.RuntimeConfig)
	if readErr != nil || string(got) != previous {
		t.Fatalf("RenderRuntimeAt changed runtime without shared default: %q, %v", got, readErr)
	}
}

func TestSyncImportsWindowsStyleLocalLLMuxEdit(t *testing.T) {
	remote, local := newSyncRepository(t)
	paths := testPaths(t)
	paths.AgentsRoot = local
	paths.SharedDefault = filepath.Join(local, "codex", "config.toml")
	paths.SharedLLMux = filepath.Join(local, "codex", "profiles", "llmux.config.toml")
	paths.SharedProviders = filepath.Join(local, "codex", "providers.json")
	if err := os.MkdirAll(filepath.Dir(paths.SharedLLMux), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.SharedLLMux, []byte(testLLMuxProfile), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, local, "add", "codex/profiles/llmux.config.toml")
	gitRun(t, local, "commit", "-m", "shared llmux profile")
	gitRun(t, local, "push", "origin", "HEAD:main")
	noSymlink := func(string, string) error { return errors.New("symlinks unavailable") }
	if err := ensureLLMuxProfileWith(paths, noSymlink); err != nil {
		t.Fatalf("initial copy fallback: %v", err)
	}
	localEdit := strings.Replace(testLLMuxProfile, "deepseek-v4", "deepseek-v5", 1)
	if err := os.WriteFile(paths.RuntimeLLMux, []byte(localEdit), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := RunAt(context.Background(), ModeSync, paths)
	if err != nil || result.State != "synced" {
		t.Fatalf("RunAt(Sync) = %+v, %v", result, err)
	}
	remoteProfile := gitOutput(t, local, "--git-dir", remote, "show", "main:codex/profiles/llmux.config.toml")
	if !strings.Contains(remoteProfile, "deepseek-v5") {
		t.Fatalf("local copy edit was not published: %s", remoteProfile)
	}
	copy, err := os.ReadFile(paths.RuntimeLLMux)
	if err != nil || !strings.Contains(string(copy), "deepseek-v5") {
		t.Fatalf("local copy lost the imported edit: %s, %v", copy, err)
	}
}

func TestSyncConflictDoesNotReplaceRuntimeConfig(t *testing.T) {
	remote, local := newSyncRepository(t)
	paths := testPaths(t)
	paths.AgentsRoot = local
	paths.SharedDefault = filepath.Join(local, "codex", "config.toml")
	paths.SharedLLMux = filepath.Join(local, "codex", "profiles", "llmux.config.toml")
	paths.SharedProviders = filepath.Join(local, "codex", "providers.json")
	if err := os.WriteFile(paths.RuntimeConfig, []byte("model = \"keep-runtime\"\n[tui]\nscreen_reader_detection_done = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(paths.RuntimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	runtimeInfo, err := os.Stat(paths.RuntimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.SharedDefault, []byte("model = \"local-edit\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	peer := filepath.Join(filepath.Dir(local), "peer-conflict")
	gitRun(t, "", "clone", remote, peer)
	gitRun(t, peer, "config", "user.name", "Peer")
	gitRun(t, peer, "config", "user.email", "peer@example.invalid")
	if err := os.WriteFile(filepath.Join(peer, "codex", "config.toml"), []byte("model = \"remote-edit\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, peer, "add", "codex/config.toml")
	gitRun(t, peer, "commit", "-m", "remote conflicting edit")
	gitRun(t, peer, "push", "origin", "HEAD:main")

	result, err := RunAt(context.Background(), ModeSync, paths)
	if err == nil || result.State != "conflict" {
		t.Fatalf("RunAt(Sync) = %+v, %v; want visible conflict", result, err)
	}
	after, err := os.ReadFile(paths.RuntimeConfig)
	if err != nil || string(after) != string(before) {
		t.Fatalf("runtime config was replaced after conflict: %q, %v", after, err)
	}
	afterInfo, err := os.Stat(paths.RuntimeConfig)
	if err != nil || !runtimeInfo.ModTime().Equal(afterInfo.ModTime()) {
		t.Fatalf("runtime config mtime changed after conflict: %v", err)
	}
	conflicted, err := os.ReadFile(paths.SharedDefault)
	if err != nil || !strings.Contains(string(conflicted), "local-edit") || !strings.Contains(string(conflicted), "remote-edit") {
		t.Fatalf("shared conflict did not preserve both edits: %s (%v)", conflicted, err)
	}
}

func TestFailedRuntimeRenderPreservesPreviousConfig(t *testing.T) {
	_, local := newSyncRepository(t)
	paths := testPaths(t)
	paths.AgentsRoot = local
	paths.SharedDefault = filepath.Join(local, "codex", "config.toml")
	paths.SharedLLMux = filepath.Join(local, "codex", "profiles", "llmux.config.toml")
	paths.SharedProviders = filepath.Join(local, "codex", "providers.json")
	previous := []byte("model = [\n")
	if err := os.WriteFile(paths.RuntimeConfig, previous, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := RunAt(context.Background(), ModeSync, paths)
	if err == nil || result.State != "invalid" {
		t.Fatalf("RunAt(Sync) = %+v, %v; want invalid render", result, err)
	}
	after, err := os.ReadFile(paths.RuntimeConfig)
	if err != nil || string(after) != string(previous) {
		t.Fatalf("failed render replaced previous runtime config: %q, %v", after, err)
	}
}

func testPaths(t *testing.T) Paths {
	t.Helper()
	root := t.TempDir()
	paths := Paths{
		AgentsRoot:      filepath.Join(root, "agents"),
		CodexHome:       filepath.Join(root, "codex"),
		MindFSConfig:    filepath.Join(root, "config", "mindfs"),
		SharedDefault:   filepath.Join(root, "agents", "codex", "config.toml"),
		SharedLLMux:     filepath.Join(root, "agents", "codex", "profiles", "llmux.config.toml"),
		SharedProviders: filepath.Join(root, "agents", "codex", "providers.json"),
		RuntimeConfig:   filepath.Join(root, "codex", "config.toml"),
		RuntimeLLMux:    filepath.Join(root, "codex", "llmux.config.toml"),
		SnapshotsRoot:   filepath.Join(root, "config", "mindfs", "agents-config"),
		DefaultSnapshot: filepath.Join(root, "config", "mindfs", "agents-config", "codex-default", "002-config.toml"),
		LLMuxSnapshot:   filepath.Join(root, "config", "mindfs", "agents-config", "codex-llmux", "002-config.toml"),
		LegacyProviders: filepath.Join(root, "config", "mindfs", "api-providers.json"),
		LocalSecrets:    filepath.Join(root, "config", "mindfs", "api-provider-secrets.json"),
		StateDir:        filepath.Join(root, "config", "mindfs", "agents-config-sync"),

		ClaudeHome:            filepath.Join(root, "claudehome"),
		SharedClaude:          filepath.Join(root, "agents", "claude"),
		SharedClaudeSettings:  filepath.Join(root, "agents", "claude", "settings.shared.json"),
		SharedClaudeMCP:       filepath.Join(root, "agents", "claude", "mcp.shared.json"),
		ClaudeLocalSettings:   filepath.Join(root, "claudehome", "settings.local.json"),
		ClaudeRuntimeSettings: filepath.Join(root, "claudehome", "settings.json"),
		ClaudeRuntimeMCP:      filepath.Join(root, "claudehome", ".mcp.json"),
		ClaudeJSON:            filepath.Join(root, ".claude.json"),
	}
	if err := os.MkdirAll(paths.CodexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.MindFSConfig, 0o700); err != nil {
		t.Fatal(err)
	}
	return paths
}

func initSyncRepo(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, root, "init", "--initial-branch=main")
	gitTestRun(t, root, "config", "user.name", "Sync test")
	gitTestRun(t, root, "config", "user.email", "sync@example.invalid")
	if err := os.MkdirAll(filepath.Join(root, "codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "codex", "config.toml"), []byte("model = \"initial\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, root, "add", "codex/config.toml")
	gitTestRun(t, root, "commit", "-m", "initial")
	remote := filepath.Join(filepath.Dir(root), "remote.git")
	gitTestRun(t, filepath.Dir(root), "init", "--bare", "--initial-branch=main", remote)
	gitTestRun(t, root, "remote", "add", "origin", remote)
	gitTestRun(t, root, "push", "--set-upstream", "origin", "HEAD:main")
}

func gitTestRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func gitTestOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(output))
}
