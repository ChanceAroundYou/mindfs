package agent

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"mindfs/server/internal/agent/proxy"
)

func TestCodexProviderConfigReadsProvidersAndModelProvider(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	config := `model = "gpt-6-luna"
model_provider = "llmux"

[model_providers.llmux]
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
	got, err := readCodexProviderConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.ModelProvider != "llmux" {
		t.Fatalf("model_provider = %q", got.ModelProvider)
	}
	if got.Providers["llmux"] != "https://up.example/llmux/v1" {
		t.Fatalf("providers = %+v", got.Providers)
	}
	if _, ok := got.Providers["broken"]; ok {
		t.Fatal("provider without base_url must be skipped")
	}
	// A name that is not a plain TOML key would need its own escaping in the
	// override; skip it rather than emit an override Codex cannot parse.
	if _, ok := got.Providers["dot.ted"]; ok {
		t.Fatal("non-plain provider name must be skipped")
	}
}

// ChatGPT-login setups have no config.toml providers at all; that is not an
// error and must leave the file-derived routing empty.
func TestCodexProviderConfigMissingFile(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	got, err := readCodexProviderConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Providers) != 0 || got.ModelProvider != "" {
		t.Fatalf("got %+v", got)
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

// writeCodexAuth installs an auth.json under a temporary CODEX_HOME. An empty
// body writes no file at all, which is the "never logged in" shape.
func writeCodexAuth(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	if body == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCodexChatGPTLoginDetection(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"chatgpt login", `{"OPENAI_API_KEY":null,"tokens":{"access_token":"tok"}}`, true},
		{"api key only", `{"OPENAI_API_KEY":"sk-test","tokens":{}}`, false},
		{"api key wins over tokens", `{"OPENAI_API_KEY":"sk-test","tokens":{"access_token":"tok"}}`, false},
		{"no credentials", `{"tokens":{}}`, false},
		{"no file", ``, false},
		{"malformed", `{`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writeCodexAuth(t, tc.body)
			if got := codexChatGPTLogin(); got != tc.want {
				t.Fatalf("codexChatGPTLogin() = %v want %v", got, tc.want)
			}
		})
	}
}

// The ChatGPT backend lives at /backend-api/codex and has no /v1 segment, so
// the proxy address handed to Codex for it must not grow one.
func TestPlanCodexRoutingInjectsOfficialProviderForChatGPTLogin(t *testing.T) {
	proxyURL := func(name string) string { return "http://127.0.0.1:9999/c/" + name }
	routing := planCodexRouting(codexProviderConfig{}, true)
	if !routing.Injected {
		t.Fatalf("expected the official provider to be injected: %+v", routing)
	}
	if routing.Providers[codexOfficialProviderName] != codexChatGPTBaseURL {
		t.Fatalf("providers = %+v", routing.Providers)
	}
	got := codexRoutingArgs(routing, proxyURL)
	want := []string{
		"-c", `model_provider="mindfs-chatgpt"`,
		"-c", `model_providers.mindfs-chatgpt.name="mindfs-chatgpt"`,
		"-c", `model_providers.mindfs-chatgpt.base_url="http://127.0.0.1:9999/c/mindfs-chatgpt"`,
		"-c", `model_providers.mindfs-chatgpt.wire_api="responses"`,
		"-c", `model_providers.mindfs-chatgpt.requires_openai_auth=true`,
	}
	if len(got) != len(want) {
		t.Fatalf("args = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args[%d] = %q want %q", i, got[i], want[i])
		}
	}
}

// An API-key login reaches OpenAI-compatible endpoints that need the /v1
// shape and the bearer key; routing it through the ChatGPT provider would
// break authentication, so it is never touched.
func TestPlanCodexRoutingLeavesAPIKeySessionUntouched(t *testing.T) {
	routing := planCodexRouting(codexProviderConfig{}, false)
	args := codexRoutingArgs(routing, func(name string) string {
		return "http://127.0.0.1:9999/c/" + name + "/v1"
	})
	if routing.Injected || len(args) != 0 || len(routing.Providers) != 0 {
		t.Fatalf("routing = %+v args = %v", routing, args)
	}
}

// A configured provider is the user's explicit choice of endpoint; redirect it
// and do not also override model_provider.
func TestPlanCodexRoutingRedirectsConfiguredProviderOnly(t *testing.T) {
	cfg := codexProviderConfig{
		ModelProvider: "llmux",
		Providers:     map[string]string{"llmux": "https://up.example/llmux/v1"},
	}
	routing := planCodexRouting(cfg, true)
	if routing.Injected {
		t.Fatalf("must not inject while model_provider names a configured provider: %+v", routing)
	}
	got := codexRoutingArgs(routing, func(name string) string {
		return "http://127.0.0.1:9999/c/" + name + "/v1"
	})
	want := []string{"-c", `model_providers.llmux.base_url="http://127.0.0.1:9999/c/llmux/v1"`}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("args = %v", got)
	}
}

// A provider table may exist while model_provider is unset (the built-in
// openai is in use); then both steps apply.
func TestPlanCodexRoutingRedirectsAndInjectsWhenModelProviderUnset(t *testing.T) {
	cfg := codexProviderConfig{Providers: map[string]string{"llmux": "https://up.example/llmux/v1"}}
	routing := planCodexRouting(cfg, true)
	if !routing.Injected {
		t.Fatalf("expected injection: %+v", routing)
	}
	if routing.Providers["llmux"] != "https://up.example/llmux/v1" {
		t.Fatalf("providers = %+v", routing.Providers)
	}
	got := codexRoutingArgs(routing, func(name string) string {
		// Mirrors ProviderProxyURL: the shape follows the registered base URL,
		// and the ChatGPT backend has no /v1.
		if name == codexOfficialProviderName {
			return "http://127.0.0.1:9999/c/" + name
		}
		return "http://127.0.0.1:9999/c/" + name + "/v1"
	})
	want := []string{
		"-c", `model_providers.llmux.base_url="http://127.0.0.1:9999/c/llmux/v1"`,
		"-c", `model_provider="mindfs-chatgpt"`,
		"-c", `model_providers.mindfs-chatgpt.name="mindfs-chatgpt"`,
		"-c", `model_providers.mindfs-chatgpt.base_url="http://127.0.0.1:9999/c/mindfs-chatgpt"`,
		"-c", `model_providers.mindfs-chatgpt.wire_api="responses"`,
		"-c", `model_providers.mindfs-chatgpt.requires_openai_auth=true`,
	}
	if len(got) != len(want) {
		t.Fatalf("args = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args[%d] = %q want %q", i, got[i], want[i])
		}
	}
}

// Injection only pays off while the proxy is accepting connections: a dead
// proxy would take the whole Codex session down with it, so the caller must be
// able to detect that and fall back to the direct path.
func TestProxyListeningDetectsClosedPort(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	url := server.URL
	if !proxyListening(url) {
		t.Fatal("live server not detected")
	}
	server.Close()
	if proxyListening(url) {
		t.Fatal("closed server still reported reachable")
	}
}

// model_provider may spell the built-in provider out explicitly; that is still
// a ChatGPT login, and folding must not depend on the key being omitted.
func TestPlanCodexRoutingInjectsWhenModelProviderIsBuiltinOpenAI(t *testing.T) {
	routing := planCodexRouting(codexProviderConfig{ModelProvider: "openai"}, true)
	if !routing.Injected {
		t.Fatalf("expected injection for an explicit built-in provider: %+v", routing)
	}
}

// The argv must be built after the providers are registered: ProviderProxyURL
// reads the path shape from the registry, and building it first handed Codex a
// `/c/mindfs-chatgpt/v1` address for a backend that has no version segment —
// the first real request answered 404. This pins the order.
func TestCodexRoutingArgsCarryRegisteredPathShape(t *testing.T) {
	s, err := proxy.NewServerWithLiveCompact(proxy.LiveCompactConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	routing := planCodexRouting(codexProviderConfig{}, true)
	for name, base := range routing.Providers {
		s.RegisterProvider(name, base)
	}
	args := codexRoutingArgs(routing, s.ProviderProxyURL)
	want := fmt.Sprintf(`model_providers.%s.base_url="http://127.0.0.1:%d/c/%s"`,
		codexOfficialProviderName, s.Port(), codexOfficialProviderName)
	for _, arg := range args {
		if arg == want {
			return
		}
	}
	t.Fatalf("args = %v\nwant base_url %q", args, want)
}
