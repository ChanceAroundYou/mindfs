package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// codexConfigPath is where Codex keeps its configuration: <CODEX_HOME>/config.toml
// when the environment names a home, ~/.codex/config.toml otherwise. Read-only;
// mindfs never writes this file.
func codexConfigPath() string {
	home := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if home == "" {
		userHome, _ := os.UserHomeDir()
		home = filepath.Join(strings.TrimSpace(userHome), ".codex")
	}
	return filepath.Join(home, "config.toml")
}

// codexProviderBaseURLs reads the providers Codex is configured with, keyed by
// provider name. A missing config file is not an error — the ChatGPT-login
// setup has no `[model_providers]` at all — and a provider without a `base_url`
// is skipped, which is how Codex spells "this one logs in instead".
//
// Only plain provider names are returned: the name ends up as a TOML key in a
// `-c` override, and a quoted key there would need escaping rules of its own.
func codexProviderBaseURLs() (map[string]string, error) {
	path := codexConfigPath()
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg struct {
		ModelProviders map[string]struct {
			BaseURL string `toml:"base_url"`
		} `toml:"model_providers"`
	}
	if err := toml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	providers := make(map[string]string, len(cfg.ModelProviders))
	for name, provider := range cfg.ModelProviders {
		base := strings.TrimSpace(provider.BaseURL)
		if base == "" || !plainTOMLKey(name) {
			continue
		}
		providers[name] = base
	}
	return providers, nil
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

// codexProxyArgs builds the `-c` overrides that point every provider at the
// proxy. The right-hand side is a quoted TOML string because `-c` parses the
// value as TOML and a bare URL is not a valid TOML value. Providers are sorted
// so the argv is reproducible.
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
