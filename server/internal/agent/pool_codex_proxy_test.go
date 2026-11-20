package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCodexProviderBaseURLsReadsTOML(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	config := `[model_providers.llmux]
base_url = "https://up.example/llmux/v1"
wire_api = "responses"

[model_providers.broken]
name = "no base url"

[model_providers."dot.ted"]
base_url = "https://up.example/dotted/v1"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := codexProviderBaseURLs()
	if err != nil {
		t.Fatal(err)
	}
	if got["llmux"] != "https://up.example/llmux/v1" {
		t.Fatalf("got %+v", got)
	}
	if _, ok := got["broken"]; ok {
		t.Fatal("provider without base_url must be skipped")
	}
	// A name that is not a plain TOML key would need its own escaping in the
	// override; skip it rather than emit an override Codex cannot parse.
	if _, ok := got["dot.ted"]; ok {
		t.Fatal("non-plain provider name must be skipped")
	}
}

// ChatGPT-login setups have no config.toml providers at all; that is not an
// error and must leave every session untouched.
func TestCodexProviderBaseURLsMissingConfig(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	got, err := codexProviderBaseURLs()
	if err != nil || len(got) != 0 {
		t.Fatalf("got %+v err=%v", got, err)
	}
}

func TestCodexProxyArgsRedirectBaseURL(t *testing.T) {
	providers := map[string]string{
		"zeta":  "https://up.example/zeta/v1",
		"llmux": "https://up.example/llmux/v1",
	}
	args := codexProxyArgs(providers, func(name string) string {
		return "http://127.0.0.1:9999/c/" + name + "/v1"
	})
	want := []string{
		"-c", `model_providers.llmux.base_url="http://127.0.0.1:9999/c/llmux/v1"`,
		"-c", `model_providers.zeta.base_url="http://127.0.0.1:9999/c/zeta/v1"`,
	}
	if len(args) != len(want) {
		t.Fatalf("args = %v", args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args[%d] = %q want %q", i, args[i], want[i])
		}
	}
}
