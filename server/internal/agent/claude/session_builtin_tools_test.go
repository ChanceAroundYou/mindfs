package claude

import (
	"testing"

	claudeagent "github.com/roasbeef/claude-agent-sdk-go"
)

// Router 回合只需要进程内 MCP 工具；内置工具（Bash/Read/Agent/Workflow…）
// 的定义有 ~190 KB，全部随每次请求发送，是 Router 上下文的固定大头。
func TestAppendClaudeExtraArgsDisablesBuiltinTools(t *testing.T) {
	none := ""
	options := claudeagent.DefaultOptions()
	for _, apply := range appendClaudeExtraArgs(nil, "", "", &none) {
		apply(&options)
	}
	got, ok := options.ExtraArgs["tools"]
	if !ok || got == nil {
		t.Fatalf("--tools 未传给 CLI: %#v", options.ExtraArgs)
	}
	if *got != "" {
		t.Fatalf("--tools = %q, want empty string", *got)
	}
}

// 只读复核回合要用 Read/Grep/Glob，但同样不该带上其余内置工具。
func TestAppendClaudeExtraArgsKeepsBuiltinSubset(t *testing.T) {
	subset := "Read,Grep,Glob"
	options := claudeagent.DefaultOptions()
	for _, apply := range appendClaudeExtraArgs(nil, "", "", &subset) {
		apply(&options)
	}
	got := options.ExtraArgs["tools"]
	if got == nil || *got != subset {
		t.Fatalf("--tools = %#v, want %q", got, subset)
	}
}

// 普通会话不传这个值，CLI 保持默认工具集。
func TestAppendClaudeExtraArgsLeavesBuiltinToolsWhenUnset(t *testing.T) {
	options := claudeagent.DefaultOptions()
	for _, apply := range appendClaudeExtraArgs(nil, "render markdown", "", nil) {
		apply(&options)
	}
	if _, ok := options.ExtraArgs["tools"]; ok {
		t.Fatalf("未设置时不应传 --tools: %#v", options.ExtraArgs["tools"])
	}
}
