package codexsync

import (
	"os"
	"path/filepath"
	"testing"
)

const providerMatchLLMuxProfile = `model = "gpt-6-luna"
model_provider = "llmux"

[model_providers.llmux]
name = "llmux"
base_url = "https://llmux.example/v1"
wire_api = "responses"
env_key = "LLMUX_API_KEY"
`

func TestSharedProfileForProviderMatchesByProfileNameWithoutBaseURL(t *testing.T) {
	paths := Paths{SharedLLMux: filepath.Join(t.TempDir(), "llmux.config.toml")}
	if err := os.WriteFile(paths.SharedLLMux, []byte(providerMatchLLMuxProfile), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		baseURL string
		want    bool
	}{
		{name: "llmux", baseURL: "", want: true},
		{name: "LLMUX", baseURL: "", want: true},
		{name: "llmux", baseURL: "https://llmux.example/v1/", want: true},
		{name: "llmux", baseURL: "https://other.example/v1", want: false},
		{name: "other", baseURL: "", want: false},
	}
	for _, tc := range cases {
		profile, matched, err := SharedProfileForProviderAt(paths, tc.name, tc.baseURL)
		if err != nil {
			t.Fatalf("%s/%s: %v", tc.name, tc.baseURL, err)
		}
		if matched != tc.want {
			t.Fatalf("%s/%s matched=%v, want %v", tc.name, tc.baseURL, matched, tc.want)
		}
		if matched && profile != "llmux" {
			t.Fatalf("%s/%s profile=%q, want llmux", tc.name, tc.baseURL, profile)
		}
	}
}
