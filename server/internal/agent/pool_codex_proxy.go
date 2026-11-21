package agent

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// codexConfigPath is where Codex keeps its configuration. Read-only; mindfs
// never writes this file. Empty when no home can be resolved, which reads as
// "no config" rather than as a relative path.
func codexConfigPath() string {
	home := codexHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, "config.toml")
}

// codexHomeDir 是 Codex 的配置根目录：优先 CODEX_HOME，否则 <user home>/.codex。
func codexHomeDir() string {
	if value := strings.TrimSpace(os.Getenv("CODEX_HOME")); value != "" {
		return value
	}
	home := UserHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".codex")
}

// codexProviderConfig is the part of config.toml that decides where Codex sends
// requests. Providers maps a provider name to its base_url; ModelProvider is
// the selected one, and empty means the built-in `openai`.
type codexProviderConfig struct {
	Providers     map[string]string
	ModelProvider string
}

// readCodexProviderConfig reads the provider routing Codex is configured with.
// A missing config file is not an error — the ChatGPT-login setup has no
// `[model_providers]` at all — and a provider without a `base_url` is skipped,
// which is how Codex spells "this one logs in instead".
//
// Only plain provider names are returned: the name ends up as a TOML key in a
// `-c` override, and a quoted key there would need escaping rules of its own.
func readCodexProviderConfig() (codexProviderConfig, error) {
	path := codexConfigPath()
	if path == "" {
		return codexProviderConfig{}, nil
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return codexProviderConfig{}, nil
	}
	if err != nil {
		return codexProviderConfig{}, err
	}
	var cfg struct {
		ModelProvider  string `toml:"model_provider"`
		ModelProviders map[string]struct {
			BaseURL string `toml:"base_url"`
		} `toml:"model_providers"`
	}
	if err := toml.Unmarshal(raw, &cfg); err != nil {
		return codexProviderConfig{}, fmt.Errorf("parse %s: %w", path, err)
	}
	out := codexProviderConfig{
		ModelProvider: strings.TrimSpace(cfg.ModelProvider),
		Providers:     make(map[string]string, len(cfg.ModelProviders)),
	}
	for name, provider := range cfg.ModelProviders {
		base := strings.TrimSpace(provider.BaseURL)
		if base == "" || !plainTOMLKey(name) {
			continue
		}
		out.Providers[name] = base
	}
	return out, nil
}

func plainTOMLKey(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// The synthetic provider used when Codex runs on a ChatGPT login. That setup
// has no config.toml provider to redirect, so folding needs one introduced
// entirely by `-c` overrides. `requires_openai_auth` is what makes Codex attach
// the ChatGPT account credentials; the base URL is the endpoint those
// credentials are valid against.
const (
	codexOfficialProviderName = "mindfs-chatgpt"
	codexChatGPTBaseURL       = "https://chatgpt.com/backend-api/codex"
)

// codexChatGPTLogin reports whether Codex authenticates with a ChatGPT account
// rather than an API key. Only the shape of auth.json is inspected: no
// credential value leaves this function and nothing here is logged.
func codexChatGPTLogin() bool {
	home := codexHomeDir()
	if home == "" {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(home, "auth.json"))
	if err != nil {
		return false
	}
	var auth struct {
		APIKey string `json:"OPENAI_API_KEY"`
		Tokens struct {
			AccessToken string `json:"access_token"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(raw, &auth); err != nil {
		return false
	}
	return strings.TrimSpace(auth.APIKey) == "" && strings.TrimSpace(auth.Tokens.AccessToken) != ""
}

// codexRouting is what the pool must do to route one Codex process: which
// upstreams to register with the proxy, which configured providers to redirect,
// and whether a synthetic provider has to be added.
//
// It stops short of the `-c` overrides on purpose. A provider's proxy address
// depends on how that provider is registered (see proxy.ProviderProxyURL), so
// the caller must register first and build the argv second; building both here
// produced a `/v1` address for the ChatGPT backend before the registration that
// said otherwise, and the first real request answered 404.
type codexRouting struct {
	Providers map[string]string
	// Redirect lists the configured provider names, sorted, that must be
	// pointed at the proxy.
	Redirect []string
	// Injected marks the synthetic ChatGPT-login provider, which also overrides
	// model_provider. It is the one step that can break a session outright, so
	// callers gate it on the proxy being reachable.
	Injected bool
}

// planCodexRouting decides the routing for one Codex process.
//
// Two independent steps. A provider the user configured is redirected to the
// proxy and keeps whatever name and credentials it already had. Separately, a
// ChatGPT-login install runs on the built-in provider, which has no base URL
// anyone can redirect — so a synthetic provider is injected and selected. An
// API-key install is never touched: its endpoint needs the /v1 shape and the
// bearer key, which the ChatGPT provider would not supply.
func planCodexRouting(cfg codexProviderConfig, chatGPTLogin bool) codexRouting {
	routing := codexRouting{Providers: make(map[string]string, len(cfg.Providers)+1)}
	for name, base := range cfg.Providers {
		routing.Providers[name] = base
		routing.Redirect = append(routing.Redirect, name)
	}
	sort.Strings(routing.Redirect)
	effective := cfg.ModelProvider
	if effective != "" && effective != "openai" {
		return routing
	}
	if _, taken := cfg.Providers[codexOfficialProviderName]; taken || !chatGPTLogin {
		return routing
	}
	routing.Providers[codexOfficialProviderName] = codexChatGPTBaseURL
	routing.Injected = true
	return routing
}

// codexRoutingArgs builds the `-c` overrides for a planned routing. Call it
// after the providers in the routing are registered: the proxy address it
// reads carries the same path shape as the upstream it stands for.
func codexRoutingArgs(routing codexRouting, proxyURL func(string) string) []string {
	args := make([]string, 0, len(routing.Redirect)*2+10)
	if len(routing.Redirect) > 0 {
		redirected := make(map[string]string, len(routing.Redirect))
		for _, name := range routing.Redirect {
			redirected[name] = routing.Providers[name]
		}
		args = append(args, codexProxyArgs(redirected, proxyURL)...)
	}
	if !routing.Injected {
		return args
	}
	name := codexOfficialProviderName
	return append(args,
		"-c", `model_provider="`+name+`"`,
		"-c", "model_providers."+name+`.name="`+name+`"`,
		"-c", "model_providers."+name+`.base_url="`+proxyURL(name)+`"`,
		"-c", "model_providers."+name+`.wire_api="responses"`,
		"-c", "model_providers."+name+`.requires_openai_auth=true`,
	)
}

// codexProxyArgs builds the `-c` overrides that point every provider at the
// proxy. The right-hand side is a quoted TOML string because `-c` parses the
// value as TOML and a bare URL is not a valid TOML value. Providers are sorted
// so the argv is reproducible.
//
// The session is not identified here on purpose: these overrides belong to the
// app-server process, which MindFS starts once per agent and shares across
// sessions, so a per-process header would attribute every thread to whichever
// session happened to open it first. Threads are registered per thread id from
// the adapter instead (see proxy.RegisterThread).
func codexProxyArgs(providers map[string]string, proxyURL func(name string) string) []string {
	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	sort.Strings(names)
	args := make([]string, 0, len(names)*2)
	for _, name := range names {
		args = append(args, "-c", "model_providers."+name+".base_url=\""+proxyURL(name)+"\"")
	}
	return args
}

// proxyListening reports whether anything accepts TCP connections at the
// proxy's address. The route overrides are only safe while it does: a dead
// proxy would turn one broken optimization into a broken session, so callers
// skip injection and let Codex talk to its upstream directly.
func proxyListening(proxyURL string) bool {
	parsed, err := url.Parse(proxyURL)
	if err != nil || parsed.Host == "" {
		return false
	}
	conn, err := net.DialTimeout("tcp", parsed.Host, 250*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
