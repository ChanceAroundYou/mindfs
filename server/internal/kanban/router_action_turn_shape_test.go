package kanban

import (
	"context"
	"strings"
	"testing"
)

// S1：`router_ask_user` 可以与一条 `router_say` 同轮。
//
// 现场（只读诊断报告 R1）：提示词规则 5 与规则 18 自相矛盾 —— 前者要求提问卡
// 必须先说结论，后者要求提问必须单独。模型照规则 18 写，结论就被动作一起拒掉。
// 现在合法组合是 `[say, ask_user]`：结论在前，提问在后；`router_silent` 与所有
// 执行/监管动作仍然必须单独。
//
// 模型实际调用顺序常常是「先问再补结论」，归一放在 collector 出口，所以这里的
// 反序输入必须也落成 say 在前。
func TestRouterRouteAllowsSayBeforeAskUser(t *testing.T) {
	for _, tc := range []struct {
		name  string
		calls []scriptedRouterCall
	}{
		{"say first", []scriptedRouterCall{
			{tool: "router_say", args: map[string]any{"body": "两个方案都可行，但风险不同。"}},
			{tool: "router_ask_user", args: map[string]any{"question": "走哪个？", "options": []any{"A", "B"}}},
		}},
		{"ask first", []scriptedRouterCall{
			{tool: "router_ask_user", args: map[string]any{"question": "走哪个？", "options": []any{"A", "B"}}},
			{tool: "router_say", args: map[string]any{"body": "两个方案都可行，但风险不同。"}},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRouteFixture(t, RouterModeRoute)
			f.agent.calls = tc.calls
			ctx := context.Background()

			_, placeholder, err := f.activity.CreateUserActivity(ctx, "task-1", "这个怎么选", CommentWorkflowChoiceEmpty, nil)
			if err != nil {
				t.Fatalf("CreateUserActivity: %v", err)
			}
			decision := f.waitDecision(t, RouterDecisionApplied)
			if decision.AppliedSeq != 2 {
				t.Fatalf("applied_seq = %d, want both actions applied", decision.AppliedSeq)
			}
			if len(decision.Actions) != 2 {
				t.Fatalf("actions = %#v", decision.Actions)
			}
			if decision.Actions[0].Kind != RouterActionSay || decision.Actions[1].Kind != RouterActionAskUser {
				t.Fatalf("action order = %s, %s; want say then ask_user", decision.Actions[0].Kind, decision.Actions[1].Kind)
			}

			// 占位归第一条回复（结论），提问新起一条卡。
			say := f.reloadActivity(t, placeholder.ID)
			if say.Status != ActivityStatusCompleted || !strings.Contains(say.Body, "两个方案都可行") {
				t.Fatalf("placeholder = status %q body %q", say.Status, say.Body)
			}
			if say.Ordinal != 0 || say.Purpose != "chat" {
				t.Fatalf("say ordinal %d purpose %q, want the leading position", say.Ordinal, say.Purpose)
			}
			all, err := NewActivityStore(f.store.db).List(ctx, "task-1")
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			var question *TaskActivity
			for _, act := range all {
				if act.ID != placeholder.ID && act.Type == ActivityTypeQuestion {
					question = act
				}
			}
			if question == nil {
				t.Fatalf("no question card; activities = %+v", all)
			}
			if question.Ordinal != 1 || question.Status != ActivityStatusWaitingUser ||
				!strings.Contains(question.Body, "走哪个？") {
				t.Fatalf("question = ordinal %d status %q body %q", question.Ordinal, question.Status, question.Body)
			}
		})
	}
}
