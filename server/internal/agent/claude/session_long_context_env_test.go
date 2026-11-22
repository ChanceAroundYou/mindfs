package claude

import "testing"

// 线上事实（pc-win，claude CLI 2.1.330）：裸别名 `ok` 也被 CLI 判成 1M 窗口
// （日志 `context_window model="ok" window=1000000`，且 712k token 的请求成功），
// 于是「1M」开关只改了模型名、窗口没变。CLI 自己用 CLAUDE_CODE_DISABLE_1M_CONTEXT
// 作为整组 1M 路径（[1m] 后缀 / 1M beta / 原生 1M）的总闸，MindFS 按模型是否带
// [1m] 逐进程设置它，默认模型才会稳定落在 200k。
func TestWithLongContextPolicy(t *testing.T) {
	tests := []struct {
		name        string
		model       string
		env         map[string]string
		wantDisable bool
	}{
		{name: "裸别名默认 200k", model: "ok", wantDisable: true},
		{name: "裸家族名默认 200k", model: "sonnet", wantDisable: true},
		{name: "空模型默认 200k", model: "", wantDisable: true},
		{name: "开 1m 后不设闸", model: "ok[1m]", wantDisable: false},
		{name: "大写后缀同样不设闸", model: "os[1M]", wantDisable: false},
		{name: "非别名模型带后缀也不设闸", model: "glm-5[1m]", wantDisable: false},
		{
			name:        "开 1m 时清掉残留的闸",
			model:       "os[1m]",
			env:         map[string]string{"CLAUDE_CODE_DISABLE_1M_CONTEXT": "1"},
			wantDisable: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WithLongContextPolicy(tt.env, tt.model)
			value, has := got["CLAUDE_CODE_DISABLE_1M_CONTEXT"]
			if has != tt.wantDisable {
				t.Fatalf("model=%q disabled=%v (value=%q), want %v", tt.model, has, value, tt.wantDisable)
			}
			if tt.wantDisable && value != "1" {
				t.Fatalf("model=%q gate value = %q, want \"1\"", tt.model, value)
			}
		})
	}
}

func TestWithLongContextPolicyKeepsOtherEnv(t *testing.T) {
	env := map[string]string{"ANTHROPIC_BASE_URL": "https://example.invalid", "FOO": "bar"}
	got := WithLongContextPolicy(env, "ok")
	if got["ANTHROPIC_BASE_URL"] != "https://example.invalid" || got["FOO"] != "bar" {
		t.Fatalf("unrelated env lost: %#v", got)
	}
	if got["CLAUDE_CODE_DISABLE_1M_CONTEXT"] != "1" {
		t.Fatalf("1M gate missing: %#v", got)
	}
	// nil 也要能安全返回可写的 map（没配 env 的 agent 是常态）。
	if nilEnv := WithLongContextPolicy(nil, "ok"); nilEnv["CLAUDE_CODE_DISABLE_1M_CONTEXT"] != "1" {
		t.Fatalf("nil env not gated: %#v", nilEnv)
	}
}
