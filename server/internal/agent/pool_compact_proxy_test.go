package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/server/internal/agent/proxy"
)

// writeClaudeSettings 在 dir 下写一个只含 env.ANTHROPIC_BASE_URL 的 settings.json。
func writeClaudeSettings(t *testing.T, dir, baseURL string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "settings.json")
	body := fmt.Sprintf(`{"env":{"ANTHROPIC_BASE_URL":%q}}`, baseURL)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestResolveClaudeUpstreamPrefersUserSettingsEnv(t *testing.T) {
	// 这是实测出来的优先级：进程环境指到不可达端口时 CLI 仍走 settings 的地址
	// 并成功回包，所以代理必须转发到 settings 那个地址，而不是 def.Env。
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("ANTHROPIC_BASE_URL", "https://process-env.example/v1")
	writeClaudeSettings(t, filepath.Join(home, ".claude"), "https://settings.example/llmux/v1")

	got := resolveClaudeUpstreamBaseURL(map[string]string{
		"ANTHROPIC_BASE_URL": "https://agent-def.example/v1",
	}, "")
	if got != "https://settings.example/llmux/v1" {
		t.Fatalf("upstream = %q, want the user settings value", got)
	}
}

func TestResolveClaudeUpstreamIsolatedConfigDirDoesNotReadUserSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("ANTHROPIC_BASE_URL", "")
	writeClaudeSettings(t, filepath.Join(home, ".claude"), "https://settings.example/v1")

	isolated := t.TempDir() // Router 会话的 ConfigDir：没有 settings.json
	got := resolveClaudeUpstreamBaseURL(map[string]string{
		"ANTHROPIC_BASE_URL": "https://agent-def.example/v1",
	}, isolated)
	if got != "https://agent-def.example/v1" {
		t.Fatalf("upstream = %q, want agent def (isolated dir must not fall back to ~/.claude)", got)
	}
}

func TestResolveClaudeUpstreamFallsBackToProcessEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("ANTHROPIC_BASE_URL", "https://process-env.example/v1")

	if got := resolveClaudeUpstreamBaseURL(nil, ""); got != "https://process-env.example/v1" {
		t.Fatalf("upstream = %q, want process env fallback", got)
	}
}

func TestCompactProxyEnvRoutesCliThroughSettingsOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("ANTHROPIC_BASE_URL", "")
	writeClaudeSettings(t, filepath.Join(home, ".claude"), "https://settings.example/v1")

	ps, err := proxy.NewServer()
	if err != nil {
		t.Fatal(err)
	}
	defer ps.Close()
	p := &Pool{compactProxy: ps, pendingProxyTokens: map[string]string{}}

	binding := p.compactProxyEnv("session-1", Definition{}, "")
	if binding == nil {
		t.Fatal("binding = nil, want the proxy to engage when settings.json has a base URL")
	}
	proxyURL := binding.env["ANTHROPIC_BASE_URL"]
	if !strings.HasPrefix(proxyURL, "http://127.0.0.1:") || !strings.Contains(proxyURL, "/p/") {
		t.Fatalf("proxy URL = %q, want the loopback proxy path", proxyURL)
	}
	if binding.env["MINDFS_PROXY_TOKEN"] == "" {
		t.Fatal("MINDFS_PROXY_TOKEN missing: the PostCompact hook needs it")
	}
	if p.pendingProxyTokens["session-1"] != binding.env["MINDFS_PROXY_TOKEN"] {
		t.Fatal("pendingProxyTokens not recorded: the token would leak on close")
	}

	// 关键：进程环境的 ANTHROPIC_BASE_URL 会被用户 settings 覆盖，只有 --settings
	// 能改道。这里钉住 settings JSON 的形状与值。
	var settings struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal([]byte(binding.settingsJSON), &settings); err != nil {
		t.Fatalf("settingsJSON is not valid JSON: %v", err)
	}
	if settings.Env["ANTHROPIC_BASE_URL"] != proxyURL {
		t.Fatalf("--settings base = %q, want %q", settings.Env["ANTHROPIC_BASE_URL"], proxyURL)
	}
	// 只带 base URL：token 等其余 env 由用户 settings 合并下来，不能被我们复制。
	if len(settings.Env) != 1 {
		t.Fatalf("--settings must only carry ANTHROPIC_BASE_URL, got %#v", settings.Env)
	}
}

func TestCompactProxyEnvReturnsNilWhenUpstreamUnknown(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("ANTHROPIC_BASE_URL", "")

	ps, err := proxy.NewServer()
	if err != nil {
		t.Fatal(err)
	}
	defer ps.Close()
	p := &Pool{compactProxy: ps, pendingProxyTokens: map[string]string{}}

	// 解析不到上游时宁可不过代理：把 CLI 指到一个转不出去的代理会让所有请求 502。
	if binding := p.compactProxyEnv("session-2", Definition{}, ""); binding != nil {
		t.Fatalf("binding = %#v, want nil when upstream is unknown", binding)
	}
	if len(p.pendingProxyTokens) != 0 {
		t.Fatal("no token should be registered for an unbounded session")
	}
}

func TestCompactProxyEnvMarksIsolatedSessionsObserveOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("ANTHROPIC_BASE_URL", "https://process-env.example/v1")
	writeClaudeSettings(t, filepath.Join(home, ".claude"), "https://settings.example/v1")

	ps, err := proxy.NewServer()
	if err != nil {
		t.Fatal(err)
	}
	defer ps.Close()
	p := &Pool{compactProxy: ps, pendingProxyTokens: map[string]string{}}

	userBinding := p.compactProxyEnv("session-user", Definition{}, "")
	if userBinding == nil {
		t.Fatal("user session should be proxied")
	}
	if !ps.SessionFoldAllowed(userBinding.env["MINDFS_PROXY_TOKEN"]) {
		t.Fatal("user session must be foldable")
	}

	// Router / workflow sessions run under an isolated ConfigDir and inherit
	// none of the user's tool permissions: archive pointers could be unreadable,
	// so they are measured but never folded.
	isolated := t.TempDir()
	isoBinding := p.compactProxyEnv("session-iso", Definition{}, isolated)
	if isoBinding == nil {
		t.Fatal("isolated session should still be proxied for measurement")
	}
	if ps.SessionFoldAllowed(isoBinding.env["MINDFS_PROXY_TOKEN"]) {
		t.Fatal("isolated ConfigDir session must be observe-only")
	}
}

// cloneEnv 在空输入时返回 nil。agent 定义没有 env 是常态（本机 claude 的
// provider 配置在 ~/.claude/settings.json），所以合并必须自己建 map ——
// 否则 probe 直接 panic：assignment to entry in nil map。
func TestMergeCompactProxyEnvHandlesNilBase(t *testing.T) {
	binding := &compactProxyBinding{env: map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:1/p/tok"}}
	got := mergeCompactProxyEnv(nil, binding)
	if got["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:1/p/tok" {
		t.Fatalf("merged env = %#v", got)
	}
	if got := mergeCompactProxyEnv(nil, nil); got != nil {
		t.Fatalf("nil binding must not allocate an env map, got %#v", got)
	}
}
