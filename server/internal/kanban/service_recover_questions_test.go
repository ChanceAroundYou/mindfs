package kanban

import (
	"context"
	"strings"
	"testing"
	"time"
)

// 升级前遗留的孤儿等待卡：同一会话里 Agent 换了 tool_use_id 重问，旧卡既不会被
// 去重命中（键是 tool_use_id）也不会被任何收尾路径碰到；而前端锁输入框读的是
// **最早**那张等待卡 —— 用户答完新卡界面照样卡死（现场 #104 / q-84825203e4fe3902，
// 03:21:57 的卡在两分钟后被新 tool_use_id 重问，之后一直停在 waiting_user）。
//
// 运行期的收口只覆盖「新卡进来」那一刻，升级前已经存在的存量卡要由启动清扫收掉。
func TestRecoverOnStartupClosesSupersededQuestionCards(t *testing.T) {
	ctx := context.Background()
	store, svc, root := newDirectReplyRecoveryFixture(t, "task-q", "sess-live", StatusWaitingUser)
	acts := NewActivityStore(store.db)

	seed := func(id, session string, status string, at time.Time) *TaskActivity {
		t.Helper()
		card := &TaskActivity{
			ID: id, TaskID: "task-q", RootID: root.ID,
			Type: ActivityTypeQuestion, Author: "agent", Role: RoleAgent,
			Body: "需要确认", Status: status,
			SessionKey: session, ToolUseID: "tool-" + id,
			PayloadJSON: `{"questions":[{"question":"继续吗？"}]}`,
			CreatedAt:   at,
		}
		if _, err := acts.InsertOrIgnore(ctx, card); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
		return card
	}

	base := time.Date(2026, 11, 23, 3, 0, 0, 0, time.UTC)
	// 现场形状：旧卡仍是 waiting_user，两分钟后 Agent 用新的 tool_use_id 重问
	// （新卡后来被回答，旧卡没人碰）。
	orphan := seed("q-orphan", "sess-live", ActivityStatusWaitingUser, base)
	seed("q-reasked", "sess-live", ActivityStatusCompleted, base.Add(2*time.Minute))
	// 同会话里只有一张提问卡：这是正常的「等人回答」，不能动。
	solo := seed("q-solo", "sess-solo", ActivityStatusWaitingUser, base)
	// 同一会话两张都还等着：留最新的那张，旧的收掉。
	older := seed("q-older", "sess-two", ActivityStatusWaitingUser, base)
	newer := seed("q-newer", "sess-two", ActivityStatusWaitingUser, base.Add(time.Minute))
	// 同会话里更晚的活动如果是 HumanGate（不是重问），更早的提问卡仍然有效：
	// 判据只认 type=question，别的等待态活动不算「更新的提问」。
	gatePeer := seed("q-gate-peer", "sess-gate", ActivityStatusWaitingUser, base)
	gate := &TaskActivity{
		ID: "gate-later", TaskID: "task-q", RootID: root.ID,
		Type: ActivityTypeHumanGate, Author: "agent", Role: RoleAgent,
		Body: "批准吗？", Status: ActivityStatusWaitingUser,
		SessionKey: "sess-gate",
		CreatedAt:  base.Add(time.Minute),
	}
	if _, err := acts.InsertOrIgnore(ctx, gate); err != nil {
		t.Fatalf("seed gate: %v", err)
	}

	if _, err := svc.RecoverOnStartupForRoot(ctx, root.ID, nil); err != nil {
		t.Fatalf("RecoverOnStartupForRoot: %v", err)
	}

	got := func(id string) *TaskActivity {
		t.Helper()
		card, err := acts.Get(ctx, id)
		if err != nil {
			t.Fatalf("Get %s: %v", id, err)
		}
		return card
	}
	closed := got(orphan.ID)
	if closed.Status != ActivityStatusCancelled {
		t.Fatalf("孤儿等待卡 %s 状态 = %q, want cancelled", orphan.ID, closed.Status)
	}
	if !strings.Contains(closed.Summary, supersededByNewerQuestion) {
		t.Fatalf("收口说明 = %q, want 含 %q", closed.Summary, supersededByNewerQuestion)
	}
	if card := got("q-reasked"); card.Status != ActivityStatusCompleted {
		t.Fatalf("已答的卡被改动了: %q", card.Status)
	}
	if card := got(solo.ID); card.Status != ActivityStatusWaitingUser {
		t.Fatalf("同会话唯一等待卡被收掉了: %q", card.Status)
	}
	if card := got(older.ID); card.Status != ActivityStatusCancelled {
		t.Fatalf("同会话更早的等待卡仍开着: %q", card.Status)
	}
	if card := got(newer.ID); card.Status != ActivityStatusWaitingUser {
		t.Fatalf("同会话最新的等待卡被收掉了: %q", card.Status)
	}
	if card := got(gatePeer.ID); card.Status != ActivityStatusWaitingUser {
		t.Fatalf("更晚的是 HumanGate 而不是重问，提问卡不该被收: %q", card.Status)
	}
	if card := got(gate.ID); card.Status != ActivityStatusWaitingUser {
		t.Fatalf("HumanGate 不在清扫范围: %q", card.Status)
	}

	// 再跑一次是空操作：已收口的卡不再命中，计数也不该涨。
	if _, err := svc.RecoverOnStartupForRoot(ctx, root.ID, nil); err != nil {
		t.Fatalf("second RecoverOnStartupForRoot: %v", err)
	}
	if card := got(orphan.ID); card.Status != ActivityStatusCancelled {
		t.Fatalf("二次清扫改动了已收口的卡: %q", card.Status)
	}
}
