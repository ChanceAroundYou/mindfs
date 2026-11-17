package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// writeUserClaudeSettings 把一份用户级 Claude settings 指给 CLAUDE_CONFIG_DIR。
// 隔离 ConfigDir 的会话（Router / 一次性推荐器 / 结果转换器）必须从这份用户
// 配置里继承 provider 凭据，否则 CLI 落到官方登录并报 "Not logged in"。
func writeUserClaudeSettings(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write user settings: %v", err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
}

func TestWithClaudeProviderEnvSkipsNormalSessions(t *testing.T) {
	writeUserClaudeSettings(t, `{"env":{"ANTHROPIC_AUTH_TOKEN":"tok"}}`)

	if got := withClaudeProviderEnv(nil, ""); got != nil {
		t.Fatalf("normal session env = %#v, want unchanged nil (CLI reads user settings itself)", got)
	}
	defEnv := map[string]string{"ANTHROPIC_BASE_URL": "https://def.example"}
	got := withClaudeProviderEnv(defEnv, "  ")
	if got["ANTHROPIC_AUTH_TOKEN"] != "" {
		t.Fatalf("blank config dir must not inherit provider env: %#v", got)
	}
	if got["ANTHROPIC_BASE_URL"] != "https://def.example" {
		t.Fatalf("def env changed: %#v", got)
	}
}

func TestWithClaudeProviderEnvInheritsProviderOnly(t *testing.T) {
	writeUserClaudeSettings(t, `{"env":{
		"ANTHROPIC_AUTH_TOKEN":"tok",
		"ANTHROPIC_BASE_URL":"https://provider.example",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":"ok",
		"BASH_ENV":"/tmp/hook.sh",
		"OMC_SKIP_HOOKS":"1",
		"DISABLE_AUTOUPDATER":"1"
	}}`)

	got := withClaudeProviderEnv(nil, "/tmp/isolated-config")
	if got["ANTHROPIC_AUTH_TOKEN"] != "tok" {
		t.Fatalf("auth token not inherited: %#v", got)
	}
	if got["ANTHROPIC_BASE_URL"] != "https://provider.example" {
		t.Fatalf("base url not inherited: %#v", got)
	}
	if got["ANTHROPIC_DEFAULT_HAIKU_MODEL"] != "ok" {
		t.Fatalf("model alias not inherited: %#v", got)
	}
	// 隔离目录的意义是不让用户 hooks / 进程级开关漏进来；只继承 provider 键。
	for _, key := range []string{"BASH_ENV", "OMC_SKIP_HOOKS", "DISABLE_AUTOUPDATER"} {
		if _, ok := got[key]; ok {
			t.Fatalf("%s must not leak into isolated session: %#v", key, got)
		}
	}
}

func TestWithClaudeProviderEnvKeepsExplicitAgentEnv(t *testing.T) {
	writeUserClaudeSettings(t, `{"env":{
		"ANTHROPIC_AUTH_TOKEN":"user-token",
		"ANTHROPIC_BASE_URL":"https://user.example"
	}}`)

	defEnv := map[string]string{"ANTHROPIC_BASE_URL": "https://agent.example"}
	got := withClaudeProviderEnv(defEnv, "/tmp/isolated-config")
	if got["ANTHROPIC_BASE_URL"] != "https://agent.example" {
		t.Fatalf("explicit agent env must win: %#v", got)
	}
	if got["ANTHROPIC_AUTH_TOKEN"] != "user-token" {
		t.Fatalf("missing provider key should be filled: %#v", got)
	}
	if defEnv["ANTHROPIC_AUTH_TOKEN"] != "" {
		t.Fatal("helper must not mutate the caller's def env in place with inherited keys")
	}
}

func TestWithClaudeProviderEnvWithoutUserSettings(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir()) // 目录存在但没有 settings.json

	defEnv := map[string]string{"FOO": "bar"}
	got := withClaudeProviderEnv(defEnv, "/tmp/isolated-config")
	if len(got) != 1 || got["FOO"] != "bar" {
		t.Fatalf("env changed without user settings: %#v", got)
	}
}