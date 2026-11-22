package usecase

import "testing"

// CLAUDE_CODE_DISABLE_1M_CONTEXT 是逐进程的 env，运行中的 CLI 无法改。
// 所以「1m」开关翻转不能走原地 SetModel —— 必须重建 runtime，让新进程带上
// 对应的窗口策略；否则关掉开关后当前进程仍然按 1M 跑。
func TestShouldReopenSessionForLongContext(t *testing.T) {
	tests := []struct {
		name    string
		current string
		next    string
		want    bool
	}{
		{name: "同名模型不重建", current: "ok", next: "ok"},
		{name: "同一 1m 状态换模型不重建", current: "os[1m]", next: "op[1m]"},
		{name: "裸模型之间不重建", current: "os", next: "ok"},
		{name: "打开 1m 要重建", current: "ok", next: "ok[1m]", want: true},
		{name: "关闭 1m 要重建", current: "ok[1m]", next: "ok", want: true},
		{name: "换模型同时开 1m 也要重建", current: "os", next: "op[1m]", want: true},
		{name: "空模型到 1m 要重建", current: "", next: "ok[1m]", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldReopenSessionForLongContext(tt.current, tt.next); got != tt.want {
				t.Fatalf("shouldReopenSessionForLongContext(%q, %q) = %v, want %v", tt.current, tt.next, got, tt.want)
			}
		})
	}
}

// defaultModelForAgent：默认档就是 200k，agent 默认值里不能再留下 [1m]，
// 否则新建会话会以 1M 起步、开关显示与实际窗口对不上。
func TestDefaultModelForAgentStripsLongContext(t *testing.T) {
	tests := []struct {
		name  string
		agent string
		model string
		want  string
	}{
		{name: "claude 去掉后缀", agent: "claude", model: "ok[1m]", want: "ok"},
		{name: "claude 大写去掉后缀", agent: "Claude", model: "os[1M]", want: "os"},
		{name: "claude 裸模型原样", agent: "claude", model: "ok", want: "ok"},
		{name: "非 claude 不动", agent: "codex", model: "gpt-6[1m]", want: "gpt-6[1m]"},
		{name: "空 agent 不动", agent: "", model: "ok[1m]", want: "ok[1m]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := defaultModelForAgent(tt.agent, tt.model); got != tt.want {
				t.Fatalf("defaultModelForAgent(%q, %q) = %q, want %q", tt.agent, tt.model, got, tt.want)
			}
		})
	}
}

// decideSessionReopen 是「复用池会话还是重建 runtime」的唯一闸门。
// 1M 翻转必须优先于其它设置变化：否则 effort 没变时会走进原地 SetModel，
// 而窗口策略只在子进程 env 里，开关就永远不生效。
func TestDecideSessionReopen(t *testing.T) {
	tests := []struct {
		name     string
		current  string
		next     string
		settings bool
		want     sessionReopen
	}{
		{name: "都没变就复用", current: "ok", next: "ok", want: sessionReuse},
		{name: "只换模型仍复用", current: "os", next: "ok", want: sessionReuse},
		{name: "设置变化就重建", current: "ok", next: "ok", settings: true, want: sessionReopenSettings},
		{name: "开 1m 优先于设置", current: "ok", next: "ok[1m]", settings: true, want: sessionReopenLongContext},
		{name: "关 1m 优先于设置", current: "ok[1m]", next: "ok", settings: true, want: sessionReopenLongContext},
		{name: "只翻转 1m 也重建", current: "os[1m]", next: "os", want: sessionReopenLongContext},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decideSessionReopen(tt.current, tt.next, tt.settings); got != tt.want {
				t.Fatalf("decideSessionReopen(%q, %q, %v) = %v, want %v", tt.current, tt.next, tt.settings, got, tt.want)
			}
		})
	}
}
