package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/internal/codexsync"
)

func TestScopeCodexAPIProvidersFollowsActiveProfile(t *testing.T) {
	paths := codexsync.Paths{SharedLLMux: filepath.Join(t.TempDir(), "llmux.config.toml")}
	if err := os.WriteFile(paths.SharedLLMux, []byte(codexSwitchSharedLLMux), 0o600); err != nil {
		t.Fatal(err)
	}
	providers := []agentAPIProvider{
		{ID: "api-llmux", Name: "llmux", BaseURL: "https://llmux.example/v1"},
		{ID: "api-llmux-nourl", Name: "llmux"},
		{ID: "api-custom", Name: "custom", BaseURL: "https://custom.example/v1"},
	}

	defaultScope, err := scopeCodexAPIProvidersAt(paths, providers, "default")
	if err != nil {
		t.Fatal(err)
	}
	if len(defaultScope) != 2 || defaultScope[0].ID != codexOfficialProviderID || defaultScope[0].Kind != agentAPIProviderKindOfficial {
		t.Fatalf("default scope = %+v, want official entry first", defaultScope)
	}
	for _, provider := range defaultScope {
		if strings.HasPrefix(provider.ID, "api-llmux") {
			t.Fatalf("default scope still exposes an llmux provider: %+v", defaultScope)
		}
	}
	if defaultScope[1].ID != "api-custom" {
		t.Fatalf("unmapped provider should stay manageable, got %+v", defaultScope)
	}

	llmuxScope, err := scopeCodexAPIProvidersAt(paths, providers, "llmux")
	if err != nil {
		t.Fatal(err)
	}
	llmuxCount := 0
	for _, provider := range llmuxScope {
		if strings.HasPrefix(provider.ID, "api-llmux") {
			llmuxCount++
		}
	}
	if llmuxCount != 2 {
		t.Fatalf("llmux scope = %+v, want both llmux entries", llmuxScope)
	}
}
