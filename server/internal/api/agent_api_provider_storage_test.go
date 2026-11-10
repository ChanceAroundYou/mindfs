package api

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/internal/codexsync"
	"mindfs/server/internal/testutil"
)

func TestProviderMetadataAndSecretsAreStoredSeparately(t *testing.T) {
	root := t.TempDir()
	testutil.IsolateUserDirs(t, root)
	t.Setenv("AGENTS_ROOT", filepath.Join(root, ".agents"))
	provider := agentAPIProvider{
		ID: "api-llmux", Name: "LLMUX", BaseURL: "https://llmux.example/v1", APIKey: "local-secret",
		Protocols: []string{apiProviderProtocolOpenAICompatible}, ModelFamilies: []string{"deepseek"},
		Models: []string{"deepseek-v4"}, CreatedAt: "created", UpdatedAt: "updated",
	}
	if err := writeAgentAPIProviders([]agentAPIProvider{provider}); err != nil {
		t.Fatalf("writeAgentAPIProviders: %v", err)
	}
	paths, err := codexsync.ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	shared, err := os.ReadFile(paths.SharedProviders)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(shared), "apiKey") || strings.Contains(string(shared), "local-secret") {
		t.Fatalf("shared provider metadata contains a credential: %s", shared)
	}
	info, err := os.Stat(paths.LocalSecrets)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("local provider secrets mode = %o, want 600", info.Mode().Perm())
	}
	providers, err := readAgentAPIProviders()
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 1 || providers[0].APIKey != "local-secret" {
		t.Fatalf("provider key was not merged locally: %+v", providers)
	}
	public, err := json.Marshal(publicAgentAPIProvider(providers[0]))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(public), "apiKey") || strings.Contains(string(public), "local-secret") {
		t.Fatalf("public provider response contains key material: %s", public)
	}
	if !strings.Contains(string(public), `"localCredentialAvailable":true`) {
		t.Fatalf("public response does not indicate local key availability: %s", public)
	}
}

func TestProviderWriterHonorsCodexSyncLock(t *testing.T) {
	root := t.TempDir()
	testutil.IsolateUserDirs(t, root)
	t.Setenv("AGENTS_ROOT", filepath.Join(root, ".agents"))
	paths, err := codexsync.ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	var writeErr error
	if err := codexsync.WithSyncLock(paths, func() error {
		writeErr = writeAgentAPIProviders([]agentAPIProvider{{
			ID: "api-provider", Name: "Provider", BaseURL: "https://provider.example/v1", APIKey: "local-key",
		}})
		return nil
	}); err != nil {
		t.Fatalf("hold sync lock: %v", err)
	}
	if writeErr == nil {
		t.Fatal("provider writer succeeded while the sync lock was held")
	}
	if _, err := os.Stat(paths.SharedProviders); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("provider writer changed shared metadata while locked: stat err=%v", err)
	}
	if _, err := os.Stat(paths.LocalSecrets); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("provider writer changed local secrets while locked: stat err=%v", err)
	}
}

func TestProviderBaseURLRejectsEmbeddedCredentials(t *testing.T) {
	for _, baseURL := range []string{
		"https://user:password@provider.example/v1",
		"https://provider.example/v1?api_key=secret",
	} {
		if _, err := normalizeAPIProviderBaseURL(baseURL); err == nil {
			t.Errorf("normalizeAPIProviderBaseURL accepted %q", baseURL)
		}
	}
}

func TestProviderWriteBeforeApplyMarksLegacyProviderMigration(t *testing.T) {
	root := t.TempDir()
	testutil.IsolateUserDirs(t, root)
	agentsRoot := filepath.Join(root, ".agents")
	t.Setenv("AGENTS_ROOT", agentsRoot)
	initTestGitRepo(t, agentsRoot)
	legacyPath, err := agentAPIProvidersPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := []byte(`[{"id":"provider","name":"Old name","baseUrl":"https://provider.example/v1","apiKey":"local-secret","models":["m1"]}]`)
	if err := os.WriteFile(legacyPath, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	providers, err := readAgentAPIProviders()
	if err != nil || len(providers) != 1 {
		t.Fatalf("read legacy provider: %+v, %v", providers, err)
	}
	providers[0].Name = "Updated name"
	providers[0].UpdatedAt = "updated"
	if err := writeAgentAPIProviders(providers); err != nil {
		t.Fatalf("writeAgentAPIProviders: %v", err)
	}
	paths, err := codexsync.ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := codexsync.RunAt(t.Context(), codexsync.ModeApply, paths); err != nil {
		t.Fatalf("Apply after provider UI write: %v", err)
	}
	metadata, err := codexsync.ReadProviderMetadata(paths.SharedProviders)
	if err != nil || len(metadata) != 1 || metadata[0].Name != "Updated name" {
		t.Fatalf("provider write was reseeded from stale legacy data: %+v, %v", metadata, err)
	}
	secrets, err := codexsync.ReadLocalProviderSecrets(paths.LocalSecrets)
	if err != nil || secrets["provider"] != "local-secret" {
		t.Fatalf("provider key was not retained locally: %+v, %v", secrets, err)
	}
	if err := writeAgentAPIProviders([]agentAPIProvider{}); err != nil {
		t.Fatalf("delete provider before Apply: %v", err)
	}
	if _, err := codexsync.RunAt(t.Context(), codexsync.ModeApply, paths); err != nil {
		t.Fatalf("Apply after provider delete: %v", err)
	}
	metadata, err = codexsync.ReadProviderMetadata(paths.SharedProviders)
	if err != nil || len(metadata) != 0 {
		t.Fatalf("deleted legacy provider was resurrected: %+v, %v", metadata, err)
	}
}

func TestProviderWriteFailureRestoresMetadataAndLocalKeys(t *testing.T) {
	for _, scenario := range []struct {
		name string
		next []agentAPIProvider
	}{
		{
			name: "delete",
			next: []agentAPIProvider{},
		},
		{
			name: "update",
			next: []agentAPIProvider{{ID: "provider", Name: "Updated", BaseURL: "https://provider.example/v2", APIKey: "new-key", Models: []string{"m2"}}},
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			root := t.TempDir()
			testutil.IsolateUserDirs(t, root)
			t.Setenv("AGENTS_ROOT", filepath.Join(root, ".agents"))
			paths, err := codexsync.ResolvePaths()
			if err != nil {
				t.Fatal(err)
			}
			oldMetadata := []codexsync.ProviderMetadata{{
				ID: "provider", Name: "Original", BaseURL: "https://provider.example/v1", Models: []string{"m1"},
			}}
			if err := codexsync.WriteProviderMetadata(paths.SharedProviders, oldMetadata); err != nil {
				t.Fatal(err)
			}
			if err := codexsync.WriteLocalProviderSecrets(paths.LocalSecrets, map[string]string{"provider": "old-key"}); err != nil {
				t.Fatal(err)
			}
			writeFailed := errors.New("injected metadata write failure")
			err = writeAgentAPIProvidersLockedWith(paths, scenario.next, func(string, []codexsync.ProviderMetadata) error {
				return writeFailed
			}, codexsync.MarkProviderMigrationComplete)
			if !errors.Is(err, writeFailed) {
				t.Fatalf("write error = %v, want injected failure", err)
			}
			metadata, err := codexsync.ReadProviderMetadata(paths.SharedProviders)
			if err != nil || len(metadata) != 1 || metadata[0].Name != "Original" {
				t.Fatalf("shared metadata was not restored: %+v, %v", metadata, err)
			}
			secrets, err := codexsync.ReadLocalProviderSecrets(paths.LocalSecrets)
			if err != nil || secrets["provider"] != "old-key" {
				t.Fatalf("local key was not restored: %+v, %v", secrets, err)
			}
		})
	}
}

func TestSharedProviderWithoutLocalKeyRemainsVisibleButUnavailable(t *testing.T) {
	root := t.TempDir()
	testutil.IsolateUserDirs(t, root)
	t.Setenv("AGENTS_ROOT", filepath.Join(root, ".agents"))
	paths, err := codexsync.ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := codexsync.WriteProviderMetadata(paths.SharedProviders, []codexsync.ProviderMetadata{{
		ID: "api-remote", Name: "Remote", BaseURL: "https://provider.invalid/v1", Models: []string{"m1"},
	}}); err != nil {
		t.Fatal(err)
	}
	providers, err := readAgentAPIProviders()
	if err != nil || len(providers) != 1 {
		t.Fatalf("shared provider was not visible: providers=%+v err=%v", providers, err)
	}
	public := publicAgentAPIProvider(providers[0])
	if public.LocalCredentialAvailable {
		t.Fatal("provider without a local key was reported usable")
	}
	result, err := testAgentAPIProviderModel(t.Context(), agentAPIProviderTestRequest{ProviderID: "api-remote"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || result.Error != "local API key is missing" {
		t.Fatalf("missing local key result = %+v", result)
	}
	_, results, err := syncAllAgentAPIProviders(t.Context(), nil)
	if err != nil || len(results) != 1 || results[0].Success || results[0].Error != "local API key is missing" {
		t.Fatalf("missing-key refresh = %+v, err=%v", results, err)
	}
}

func TestProviderReadUsesLegacyFileUntilSharedMetadataExists(t *testing.T) {
	root := t.TempDir()
	testutil.IsolateUserDirs(t, root)
	t.Setenv("AGENTS_ROOT", filepath.Join(root, ".agents"))
	legacyPath, err := agentAPIProvidersPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := []byte(`[{"id":"legacy","name":"Legacy","baseUrl":"https://legacy.invalid/v1","apiKey":"old-local-key","models":["m"]}]`)
	if err := os.WriteFile(legacyPath, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	providers, err := readAgentAPIProviders()
	if err != nil || len(providers) != 1 || providers[0].APIKey != "old-local-key" {
		t.Fatalf("legacy provider fallback failed: %+v, %v", providers, err)
	}
	paths, err := codexsync.ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := codexsync.WriteProviderMetadata(paths.SharedProviders, []codexsync.ProviderMetadata{}); err != nil {
		t.Fatal(err)
	}
	providers, err = readAgentAPIProviders()
	if err != nil || len(providers) != 0 {
		t.Fatalf("empty shared source did not suppress legacy fallback: %+v, %v", providers, err)
	}
}
