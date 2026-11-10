package codexsync

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

func TestProjectProfileKeepsOnlyDefaultSharedSettings(t *testing.T) {
	snapshot := []byte(`model = "gpt-5-codex"
model_reasoning_effort = "high"

[features]
goals = true

[tui]
screen_reader_detection_done = true

[projects."/home/alice/project"]
trust_level = "trusted"

[hooks.state."/home/alice/.codex/hooks.json:session_start:0:0"]
trusted_hash = "device-hook-hash"
`)

	got, err := ProjectProfile("default", snapshot)
	if err != nil {
		t.Fatalf("ProjectProfile(default): %v", err)
	}
	var projected map[string]any
	if err := toml.Unmarshal(got, &projected); err != nil {
		t.Fatalf("projected default is invalid TOML: %v", err)
	}
	if projected["model"] != "gpt-5-codex" || projected["model_reasoning_effort"] != "high" {
		t.Fatalf("default portable settings missing: %#v", projected)
	}
	features, ok := projected["features"].(map[string]any)
	if !ok || features["goals"] != true {
		t.Fatalf("default goals setting missing: %#v", projected["features"])
	}
	for _, localKey := range []string{"tui", "projects", "hooks", "model_providers"} {
		if _, exists := projected[localKey]; exists {
			t.Errorf("default projection retained local key %q", localKey)
		}
	}
}

func TestProjectProfileReplacesLLMuxBearerWithLocalEnvKey(t *testing.T) {
	snapshot := []byte(`model = "deepseek-v4"
model_reasoning_effort = "high"
model_provider = "llmux"

[model_providers.llmux]
name = "LLMUX"
base_url = "https://llmux.example/v1"
wire_api = "responses"
experimental_bearer_token = "never-share-this-token"
`)

	got, err := ProjectProfile("llmux", snapshot)
	if err != nil {
		t.Fatalf("ProjectProfile(llmux): %v", err)
	}
	var projected map[string]any
	if err := toml.Unmarshal(got, &projected); err != nil {
		t.Fatalf("projected llmux profile is invalid TOML: %v", err)
	}
	if projected["model"] != "deepseek-v4" || projected["model_provider"] != "llmux" || projected["model_reasoning_effort"] != "high" {
		t.Fatalf("llmux profile settings missing: %#v", projected)
	}
	providers, ok := projected["model_providers"].(map[string]any)
	if !ok {
		t.Fatalf("model_providers missing: %#v", projected)
	}
	provider, ok := providers["llmux"].(map[string]any)
	if !ok || provider["env_key"] != "LLMUX_API_KEY" || provider["base_url"] != "https://llmux.example/v1" {
		t.Fatalf("llmux provider settings missing: %#v", projected["model_providers"])
	}
	if strings.Contains(string(got), "never-share-this-token") || strings.Contains(string(got), "experimental_bearer_token") {
		t.Fatalf("llmux projection retained bearer material: %s", got)
	}
}

func TestValidateProviderMetadataRejectsCredentialFields(t *testing.T) {
	good := []byte(`[{"id":"llmux","name":"LLMUX","baseUrl":"https://llmux.example/v1","protocols":["openai-compatible"],"modelFamilies":["deepseek"],"models":["deepseek-v4"]}]`)
	if err := ValidateProviderMetadata(good); err != nil {
		t.Fatalf("valid metadata rejected: %v", err)
	}
	bad := []byte(`[{"id":"llmux","name":"LLMUX","apiKey":"never-share-this-token"}]`)
	if err := ValidateProviderMetadata(bad); err == nil {
		t.Fatal("metadata with apiKey was accepted")
	}
}

func TestValidateProviderMetadataRequiresArrayAndUniqueIDs(t *testing.T) {
	for _, payload := range [][]byte{
		[]byte("null"),
		[]byte(`[{"id":"same","name":"One","baseUrl":"https://one.example"},{"id":"same","name":"Two","baseUrl":"https://two.example"}]`),
	} {
		if err := ValidateProviderMetadata(payload); err == nil {
			t.Errorf("invalid metadata accepted: %s", payload)
		}
	}
}

func TestValidateProviderMetadataRejectsCredentialBearingURLs(t *testing.T) {
	for _, baseURL := range []string{
		"https://user:password@provider.example/v1",
		"https://provider.example/v1?api_key=secret",
		"https://provider.example/v1#token",
	} {
		payload := []byte(`[{"id":"provider","name":"Provider","baseUrl":"` + baseURL + `"}]`)
		if err := ValidateProviderMetadata(payload); err == nil {
			t.Errorf("credential-bearing baseUrl accepted: %s", baseURL)
		}
	}
}

func TestValidateProviderMetadataRejectsMalformedAndOversizedJSON(t *testing.T) {
	if err := ValidateProviderMetadata([]byte(`[{`)); err == nil {
		t.Fatal("malformed provider JSON was accepted")
	}
	tooLarge := []byte(`[]` + strings.Repeat(" ", 1<<20))
	if err := ValidateProviderMetadata(tooLarge); err == nil {
		t.Fatal("provider JSON larger than 1 MiB was accepted")
	}
}

func TestProjectProfileRejectsWrongSharedFieldTypes(t *testing.T) {
	for _, snapshot := range []string{
		"model = true\n",
		"model_reasoning_effort = 5\n",
		"[features]\ngoals = \"yes\"\n",
	} {
		if _, err := ProjectProfile("default", []byte(snapshot)); err == nil {
			t.Errorf("snapshot with wrong shared field type accepted: %s", snapshot)
		}
	}
}

func TestReadOptionalRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "outside.toml")
	if err := os.WriteFile(target, []byte("model = \"private\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "config.toml")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := readOptional(link); err == nil {
		t.Fatal("readOptional accepted a symlink")
	}
}

func TestValidateSharedProfileRejectsBearerField(t *testing.T) {
	bad := []byte(`[model_providers.llmux]
name = "LLMUX"
base_url = "https://llmux.example/v1"
wire_api = "responses"
experimental_bearer_token = "never-share-this-token"
`)
	if err := ValidateSharedProfile("llmux", bad); err == nil {
		t.Fatal("shared profile with experimental_bearer_token was accepted")
	}
}

func TestProjectProfileRejectsMalformedAndOversizedTOML(t *testing.T) {
	if _, err := ProjectProfile("default", []byte("model = [")); err == nil {
		t.Fatal("malformed TOML was accepted")
	}
	tooLarge := []byte("model = \"" + strings.Repeat("x", 1<<20) + "\"\n")
	if _, err := ProjectProfile("default", tooLarge); err == nil {
		t.Fatal("TOML larger than 1 MiB was accepted")
	}
}

func TestIsAllowedRepoPathRestrictsGitWrites(t *testing.T) {
	allowed := []string{
		"codex/config.toml",
		"codex/profiles/llmux.config.toml",
		"codex/providers.json",
	}
	for _, path := range allowed {
		if !IsAllowedRepoPath(path) {
			t.Errorf("IsAllowedRepoPath(%q) = false", path)
		}
	}
	for _, path := range []string{
		"codex/AGENTS.md",
		"codex/profiles/default.config.toml",
		"codex/providers.json.tmp",
		"../secrets.json",
		"",
	} {
		if IsAllowedRepoPath(path) {
			t.Errorf("IsAllowedRepoPath(%q) = true", path)
		}
	}
}

func TestSyncRepositoryPushesToConfiguredUpstreamBranch(t *testing.T) {
	remote, local := newSyncRepository(t)
	configPath := filepath.Join(local, "codex", "config.toml")
	if err := os.WriteFile(configPath, []byte("model = \"gpt-5-codex\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := syncRepository(context.Background(), local); err != nil {
		t.Fatalf("syncRepository: %v", err)
	}
	got := gitOutput(t, local, "--git-dir", remote, "show", "main:codex/config.toml")
	if got != "model = \"gpt-5-codex\"" {
		t.Fatalf("remote main config = %q", got)
	}
	if got := gitOutput(t, local, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); got != "origin/main" {
		t.Fatalf("tracking ref = %q, want origin/main", got)
	}
}

func TestSyncRepositoryRetriesWhenUpstreamMovesDuringPush(t *testing.T) {
	remote, local := newSyncRepository(t)
	configPath := filepath.Join(local, "codex", "config.toml")
	if err := os.WriteFile(configPath, []byte("model = \"local\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	peer := filepath.Join(filepath.Dir(local), "peer-race")
	gitRun(t, "", "clone", remote, peer)
	gitRun(t, peer, "config", "user.name", "Peer")
	gitRun(t, peer, "config", "user.email", "peer@example.invalid")

	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	marker := filepath.Join(binDir, "pushed-once")
	wrapper := filepath.Join(binDir, "git")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = push ] && [ ! -f " + shellQuote(marker) + " ]; then\n" +
		"  touch " + shellQuote(marker) + "\n" +
		"  printf '[]\\n' > " + shellQuote(filepath.Join(peer, "codex", "providers.json")) + "\n" +
		"  " + shellQuote(realGit) + " -C " + shellQuote(peer) + " add codex/providers.json\n" +
		"  " + shellQuote(realGit) + " -C " + shellQuote(peer) + " commit -m peer-race --author='Peer <peer@example.invalid>'\n" +
		"  " + shellQuote(realGit) + " -C " + shellQuote(peer) + " push origin HEAD:main\n" +
		"fi\nexec " + shellQuote(realGit) + " \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	result, err := syncRepository(context.Background(), local)
	if err != nil || result.State != "synced" {
		t.Fatalf("syncRepository after remote race = %+v, %v", result, err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("push race was not injected: %v", err)
	}
	remoteConfig := gitOutput(t, local, "--git-dir", remote, "show", "main:codex/config.toml")
	if remoteConfig != "model = \"local\"" {
		t.Fatalf("local edit was not pushed after retry: %q", remoteConfig)
	}
	remoteProviders := gitOutput(t, local, "--git-dir", remote, "show", "main:codex/providers.json")
	if remoteProviders != "[]" {
		t.Fatalf("peer edit was lost during retry: %q", remoteProviders)
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func TestSyncRepositoryRefusesUnrelatedDirtyPath(t *testing.T) {
	_, local := newSyncRepository(t)
	configPath := filepath.Join(local, "codex", "config.toml")
	if err := os.WriteFile(configPath, []byte("model = \"local\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(local, "README.md")
	if err := os.WriteFile(unrelated, []byte("keep local work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	headBefore := gitOutput(t, local, "rev-parse", "HEAD")
	if _, err := syncRepository(context.Background(), local); err == nil {
		t.Fatal("sync accepted an unrelated dirty path")
	}
	if got := gitOutput(t, local, "rev-parse", "HEAD"); got != headBefore {
		t.Fatalf("HEAD changed after refusal: %s != %s", got, headBefore)
	}
	if got, err := os.ReadFile(unrelated); err != nil || string(got) != "keep local work\n" {
		t.Fatalf("unrelated work changed: %q, %v", got, err)
	}
}

func TestSyncRepositoryRefusesUnrelatedLocalCommit(t *testing.T) {
	remote, local := newSyncRepository(t)
	readme := filepath.Join(local, "README.md")
	if err := os.WriteFile(readme, []byte("local-only change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, local, "add", "README.md")
	gitRun(t, local, "commit", "-m", "local unrelated change")

	result, err := syncRepository(context.Background(), local)
	if err == nil {
		t.Fatalf("sync unexpectedly accepted unrelated commit: %+v", result)
	}
	if got := gitOutput(t, local, "--git-dir", remote, "rev-parse", "main"); got == gitOutput(t, local, "rev-parse", "HEAD") {
		t.Fatal("unrelated commit was pushed to the remote")
	}
	if got := gitOutput(t, local, "status", "--porcelain"); got != "" {
		t.Fatalf("refused local commit changed worktree: %q", got)
	}
}

func TestSyncRepositoryPreservesBothSidesOfRebaseConflict(t *testing.T) {
	remote, local := newSyncRepository(t)
	peer := filepath.Join(filepath.Dir(local), "peer")
	gitRun(t, "", "clone", remote, peer)
	gitRun(t, peer, "config", "user.name", "Peer")
	gitRun(t, peer, "config", "user.email", "peer@example.invalid")
	peerConfig := filepath.Join(peer, "codex", "config.toml")
	if err := os.WriteFile(peerConfig, []byte("model = \"remote\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, peer, "add", "codex/config.toml")
	gitRun(t, peer, "commit", "-m", "remote edit")
	gitRun(t, peer, "push", "origin", "HEAD:main")

	localConfig := filepath.Join(local, "codex", "config.toml")
	if err := os.WriteFile(localConfig, []byte("model = \"local\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := syncRepository(context.Background(), local); err == nil {
		t.Fatal("sync silently resolved a same-file conflict")
	}
	conflicted, err := os.ReadFile(localConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(conflicted), "local") || !strings.Contains(string(conflicted), "remote") {
		t.Fatalf("conflict did not preserve both edits: %s", conflicted)
	}
	status := gitOutput(t, local, "status", "--porcelain")
	if !strings.Contains(status, "UU codex/config.toml") {
		t.Fatalf("rebase conflict state missing: %q", status)
	}
}

func newSyncRepository(t *testing.T) (remote, local string) {
	t.Helper()
	root := t.TempDir()
	remote = filepath.Join(root, "remote.git")
	local = filepath.Join(root, "local")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, "", "init", "--bare", "--initial-branch=main", remote)
	gitRun(t, local, "init", "--initial-branch=master")
	gitRun(t, local, "config", "user.name", "Sync test")
	gitRun(t, local, "config", "user.email", "sync@example.invalid")
	if err := os.MkdirAll(filepath.Join(local, "codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "codex", "config.toml"), []byte("model = \"initial\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, local, "add", "codex/config.toml")
	gitRun(t, local, "commit", "-m", "initial")
	gitRun(t, local, "remote", "add", "origin", remote)
	gitRun(t, local, "push", "--set-upstream", "origin", "HEAD:main")
	return remote, local
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, output)
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v in %s: %v", args, dir, err)
	}
	return strings.TrimSpace(string(output))
}
