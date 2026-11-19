package app

import "testing"

// 任务派发默认值必须自带内建值：config.json 里没写 task 段是常态（单节点
// 机器），此时新任务仍要落到 claude + sonnet，而不是探针顺序里的第一个
// 可用 Agent（2026-11-20 事故：整批任务落到 codex）。
func TestTaskConfigDefaults(t *testing.T) {
	cfg := taskConfigFromOptions(StartOptions{})
	if cfg.Agent != "claude" || cfg.Model != "sonnet" {
		t.Fatalf("默认任务配置 = %+v, want claude/sonnet", cfg)
	}
}

// 显式配置要能覆盖，且空格要清掉（config.json 手写容易带空格）。
func TestTaskConfigFromOptionsHonorsExplicitValues(t *testing.T) {
	cfg := taskConfigFromOptions(StartOptions{TaskAgent: " codex ", TaskModel: " gpt-6.1-terra "})
	if cfg.Agent != "codex" || cfg.Model != "gpt-6.1-terra" {
		t.Fatalf("显式任务配置 = %+v, want codex/gpt-6.1-terra", cfg)
	}
	// 只配 Agent、不配模型：模型留空 = 用该 Agent 自己的默认，不硬塞 sonnet。
	cfg = taskConfigFromOptions(StartOptions{TaskAgent: "codex"})
	if cfg.Agent != "codex" || cfg.Model != "" {
		t.Fatalf("只配 Agent 时 = %+v, want codex + 空模型", cfg)
	}
}
