package kanban

import (
	"encoding/json"
	"strings"
	"testing"
)

// Router 提示词是 fork 定制（上游没有会话连续性这一层）。它没有行为分支可测，
// 唯一能挡住「合上游时被整份替换」的就是这条内容守卫：规则 4 必须把 message_agent
// 放在 run_workflow 前面，并且明确写出跨轮的会话选择规则。
//
// 为什么值得钉：2026-11-19 用户报告「每条留言都被派给工作流、每轮都新开会话」；
// 2026-11-20 用户进一步定下「一轮 workflow 一条会话，跨轮由 Router 选 new/continue」。
// 规则 4 必须同时表达这两件事 —— 少了任何一句，模型就会滑回「每轮新会话」。
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
	if !strings.Contains(prompt, "共用**一条**会话") {
		t.Fatal("提示词必须写明一轮 workflow 内部共用一条会话")
	}
	if !strings.Contains(prompt, "session_mode") {
		t.Fatal("提示词必须告诉 Router 跨轮用 session_mode 选 new/continue")
	}
	// 可复用的固定流程仍然是 workflow 的判据；不能把它删掉，否则又会滑回「一律直连」。
	if !strings.Contains(prompt, "可复用的固定流程") {
		t.Fatal("workflow 的判据（可复用的固定流程）必须保留")
	}
}

// #68：Agent 在会话里把四条待拍板事项写成了回复正文，Router 又把它们原样写进
// progress —— 它们既没进入任务结论，也没有变成用户能点的选择。规则 18 必须要求
// 「有决策/审批项就同轮 ask_user，每题 2–4 个选项」，并点明规则 17 的七类事项
// 不受「不用问了」豁免。
func TestRouterPromptTurnsDecisionItemsIntoQuestionCards(t *testing.T) {
	prompt := RouterDeveloperInstructions()
	for _, want := range []string{"2–4 个选项", "规则 17", "不受「不用问了」豁免"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("规则 18 缺少 %q", want)
		}
	}
}

// 起草图时只写 prompt 是模型的常态：提示词与两个工具的 graph 描述都必须写明
// params.agent 可省略、默认 claude。2026-11-21 隔离验收里 Router 起草与固化出的
// 两张图都没有 agent —— 能校验、能发布，一执行就 "agent required"。
func TestRouterWorkflowGuidanceExplainsDefaultNodeAgent(t *testing.T) {
	if prompt := RouterDeveloperInstructions(); !strings.Contains(prompt, "`params.agent` 可省略") {
		t.Fatal("提示词要写明 params.agent 可省略（默认 claude）")
	}
	env := &routerToolEnv{brief: &SituationBrief{}, collector: newRouterTurnCollector(RouterConfig{})}
	for _, name := range []string{"router_run_workflow", "router_save_workflow"} {
		tool := toolByName(t, env, name)
		schema, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("marshal %s schema: %v", name, err)
		}
		if !strings.Contains(string(schema), "params.agent 可省略") {
			t.Fatalf("%s 的 graph 描述要写明 params.agent 可省略（默认 claude）", name)
		}
	}
}
