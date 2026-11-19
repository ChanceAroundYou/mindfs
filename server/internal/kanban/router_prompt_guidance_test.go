package kanban

import (
	"strings"
	"testing"
)

// Router 提示词是 fork 定制（上游没有会话连续性这一层）。它没有行为分支可测，
// 唯一能挡住「合上游时被整份替换」的就是这条内容守卫：规则 4 必须把 message_agent
// 放在 run_workflow 前面，并且明确写出「连续处理同一件事默认 message_agent」。
//
// 为什么值得钉：2026-11-19 用户报告「每条留言都被派给工作流、每轮都新开会话」，
// 根因就在规则 4 —— 当时把「多步骤」直接映射到 run_workflow，于是改代码、修 bug
// 这类持续对话也被搬进新会话。
func TestRouterPromptPrefersMessageAgentForContinuity(t *testing.T) {
	prompt := RouterDeveloperInstructions()

	msgAt := strings.Index(prompt, "→ `router_message_agent`")
	wfAt := strings.Index(prompt, "才 `router_run_workflow`")
	if msgAt < 0 || wfAt < 0 {
		t.Fatalf("规则 4 的两条分支缺失（message_agent=%d run_workflow=%d）", msgAt, wfAt)
	}
	if msgAt > wfAt {
		t.Fatal("规则 4 必须先说 message_agent（延续主会话），再说 run_workflow")
	}
	if !strings.Contains(prompt, "会话连续性") {
		t.Fatal("提示词必须明确写出会话连续性原则")
	}
	if !strings.Contains(prompt, "拿不准时选 `message_agent`，不要选 workflow") {
		t.Fatal("拿不准时的默认选择必须是 message_agent")
	}
	// 可复用的固定流程仍然是 workflow 的判据；不能把它删掉，否则又会滑回「一律直连」。
	if !strings.Contains(prompt, "可复用的固定流程") {
		t.Fatal("workflow 的判据（可复用的固定流程）必须保留")
	}
}
