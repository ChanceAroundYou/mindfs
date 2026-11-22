package agent

import (
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
)

// 1M 窗口是逐进程的：模型名不带 [1m] 时必须给子进程设闸，带了必须清掉。
// 这里钉的是真正交给 claude.OpenSession 的那份 env（含紧凑代理与 provider 继承之后的形态）。
func TestClaudeSessionEnvAppliesLongContextPolicyLast(t *testing.T) {
	pool := &Pool{}
	def := Definition{Env: map[string]string{
		"CLAUDE_CODE_DISABLE_1M_CONTEXT": "1",
		"ANTHROPIC_BASE_URL":             "https://example.invalid",
	}}

	env, _ := pool.claudeSessionEnv(def, agenttypes.OpenSessionInput{SessionKey: "s", Model: "ok"})
	if env["CLAUDE_CODE_DISABLE_1M_CONTEXT"] != "1" {
		t.Fatalf("bare model not gated: %#v", env)
	}
	if env["ANTHROPIC_BASE_URL"] != "https://example.invalid" {
		t.Fatalf("provider env lost: %#v", env)
	}

	// 开 1m 时必须把 agent env 里的残留闸清掉，否则开关按下去仍然是 200k。
	env, _ = pool.claudeSessionEnv(def, agenttypes.OpenSessionInput{SessionKey: "s", Model: "ok[1m]"})
	if _, has := env["CLAUDE_CODE_DISABLE_1M_CONTEXT"]; has {
		t.Fatalf("[1m] model still gated: %#v", env)
	}
	if env["ANTHROPIC_BASE_URL"] != "https://example.invalid" {
		t.Fatalf("provider env lost for [1m]: %#v", env)
	}
}
