package usecase

import (
	"context"
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/session"
)

// 同一条子会话先后以不同 id 形状出现时的回归。
//
// 根因：wrapper 只在 byRef（tool:/task: 前缀键）里登记。终态摘要的 id 形状
// （task-id / tool-use-id / 裸 id）和 Task 调用不一致时 find 命中不了，ensure 又
// 只看 claimedBinding 不看"本轮已经给这条子会话 attach 过"，于是同一 sessionKey
// 被 attach 第二次：新 wrapper 拿到新回合身份，旧 wrapper 的 close 被身份校验
// 拒掉，pending 残留到父回合结束。上面挂的灯就会在 5s 轮询里反复亮/灭。
//
// 这两条钉住 bySession 兜底：ref 键认不出时按 sessionKey 找回同一个 wrapper，
// 不重复 attach、不重复发回合身份；终态摘要也走同一条找回路径。

func TestClaudeSubagentRefShapeDriftReusesAttachedWrapper(t *testing.T) {
	router, _, _ := newClaudeSubagentTestRouter(t, nil)
	var turnIDs []string
	router.in.OnSubAgentAttached = func(child *session.Session, turnID string) {
		turnIDs = append(turnIDs, turnID)
	}
	child := openClaudeSubagentChild(t, router)
	defer router.FinishAll()
	defer unregisterActiveTurn(router.in.RootID, child.Key)

	if len(turnIDs) != 1 {
		t.Fatalf("OnSubAgentAttached 调用 %d 次, want 1", len(turnIDs))
	}

	// 模拟 id 形状漂移：byRef 里查不到这条子会话，只剩 session 绑定。
	router.byRef = map[string]*claudeSyntheticSubagent{}
	if consumed := router.Handle(context.Background(), agenttypes.Event{
		Type: agenttypes.EventTypeMessageChunk,
		Data: agenttypes.MessageChunk{
			Content:         "second chunk",
			ParentToolUseID: "tool-1",
			TaskID:          "task-1",
		},
	}); !consumed {
		t.Fatal("subagent chunk was not consumed")
	}

	if len(router.children) != 1 {
		t.Fatalf("wrapper 数量 = %d, want 1：同一子会话被重复 attach", len(router.children))
	}
	if len(turnIDs) != 1 {
		t.Fatalf("OnSubAgentAttached 调用 %d 次, want 1：重复 attach 会给同一子会话发第二枚回合身份", len(turnIDs))
	}
	if len(router.bySession) != 1 {
		t.Fatalf("bySession 条目 = %d, want 1", len(router.bySession))
	}
	if got := router.find(claudeSubagentRef{ParentToolUseID: "tool-1", TaskID: "task-1"}); got == nil {
		t.Fatal("找回后没有重新绑定 ref 键，后续同形状事件仍会走兜底路径")
	}
}

func TestClaudeSubagentTerminalSummaryFindsWrapperBySession(t *testing.T) {
	router, _, _ := newClaudeSubagentTestRouter(t, nil)
	child := openClaudeSubagentChild(t, router)
	defer router.FinishAll()
	defer unregisterActiveTurn(router.in.RootID, child.Key)

	// 终态摘要的 ref 键在 byRef 里查不到，只能经绑定 → bySession 找回。
	router.byRef = map[string]*claudeSyntheticSubagent{}
	router.Handle(context.Background(), agenttypes.Event{
		Type: agenttypes.EventTypeToolUpdate,
		Data: agenttypes.ToolCall{
			RawType: "subagent_result",
			Meta:    map[string]any{"parentToolUseId": "tool-1"},
		},
	})

	if len(router.children) != 1 {
		t.Fatalf("wrapper 数量 = %d, want 1", len(router.children))
	}
	if !router.children[0].closed {
		t.Fatal("终态摘要没有关闭已 attach 的子代理，灯会一直亮到父回合结束")
	}
	if ActiveTurnID(router.in.RootID, child.Key) != 0 {
		t.Fatal("关闭后活跃轮次没有注销")
	}
}