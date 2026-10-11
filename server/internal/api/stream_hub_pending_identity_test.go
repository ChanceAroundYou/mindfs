package api

import "testing"

// pendingActive 直接问「这个会话现在是否被判为在回复」——与 /api/replying-sessions 同源。
func pendingActive(hub *StreamHub, sessionKey string) bool {
	for _, s := range hub.ListReplyingSessions() {
		if s.SessionKey == sessionKey {
			return true
		}
	}
	return false
}

// 症状「灯不亮」：第 A 轮的 done 迟到，落在第 B 轮已开始之后。
// 旧实现 `ClearSessionPending(key)` 无条件删条目，把正在跑的 B 轮灯抹掉。
func TestStaleDoneMustNotClearNewTurnPending(t *testing.T) {
	hub := NewStreamHub()
	genA := hub.SetPendingReply("root", "s1", "t") // 第 A 轮开始
	genB := hub.SetPendingReply("root", "s1", "t") // 第 B 轮开始（覆盖同一条目）
	if genA == genB {
		t.Fatalf("回合代次没有单调递增：genA=%d genB=%d", genA, genB)
	}
	hub.ClearSessionPending("s1", genA) // 迟到的 A 轮 done
	if !pendingActive(hub, "s1") {
		t.Fatalf("迟到的 done 清掉了新轮的 pending（症状「灯不亮」）")
	}
	hub.ClearSessionPending("s1", genB) // 本轮真正的 done
	if pendingActive(hub, "s1") {
		t.Fatalf("本轮的 done 没能清掉 pending（症状「灯不灭」）")
	}
}

// 幂等：重复的 done（同一 gen 清两遍）不得 panic，也不得影响后续状态。
func TestClearSessionPendingIsIdempotent(t *testing.T) {
	hub := NewStreamHub()
	gen := hub.SetPendingReply("root", "s1", "t")
	hub.ClearSessionPending("s1", gen)
	hub.ClearSessionPending("s1", gen) // 重复
	if pendingActive(hub, "s1") {
		t.Fatalf("重复清理后仍处于 pending")
	}
}

// 没有身份的调用点（gen=0）走保守清：两侧都有身份才比对，缺身份时清掉，
// 保持改造前行为，绝不因缺身份而漏清（漏清 = 灯不灭）。
func TestClearSessionPendingZeroGenStillClears(t *testing.T) {
	hub := NewStreamHub()
	_ = hub.SetPendingReply("root", "s1", "t") // TurnGen != 0
	hub.ClearSessionPending("s1", 0)
	if pendingActive(hub, "s1") {
		t.Fatalf("gen=0 的清理被错误跳过（会把「灯不灭」带回来）")
	}
}

// 用户发送路径（SetPendingUserAt）同样返回可用代次，且能被它自己的终结器清掉。
func TestSetPendingUserAtReturnsTurnGenAndClears(t *testing.T) {
	hub := NewStreamHub()
	_, gen := hub.SetPendingUser("root", "s2", "t", "claude", "m", "md", "chat", "high", "", false, "hi")
	if gen == 0 {
		t.Fatalf("SetPendingUser 未返回回合代次")
	}
	if !pendingActive(hub, "s2") {
		t.Fatalf("用户发送后应立即处于 pending")
	}
	hub.ClearSessionPending("s2", gen)
	if pendingActive(hub, "s2") {
		t.Fatalf("带正确代次的清理失败")
	}
}

// 子会话在父轮收尾时被终结：用父轮记下的代次清子会话 pending。
func TestSubSessionPendingEndedByParentTurn(t *testing.T) {
	hub := NewStreamHub()
	subGen := hub.SetPendingReply("root", "sub1", "child")
	if !pendingActive(hub, "sub1") {
		t.Fatalf("子会话创建后应处于 pending")
	}
	// 父轮收尾：带子会话自己的代次清理（子会话没等到 MessageDone）。
	hub.ClearSessionPending("sub1", subGen)
	if pendingActive(hub, "sub1") {
		t.Fatalf("父轮收尾未能终结子会话 pending")
	}
}

// 终结器的前置判据：迟到的 done（代次不匹配）必须被判为「不属于当前条目」，
// 从而使 EndSessionTurn 整条丢弃（不广播 done 帧）。否则帧仍会清掉新轮的灯。
func TestPendingTurnGenMatches(t *testing.T) {
	hub := NewStreamHub()
	genA := hub.SetPendingReply("root", "s1", "t")
	genB := hub.SetPendingReply("root", "s1", "t")

	if hub.PendingTurnGenMatches("s1", genA) {
		t.Fatalf("迟到 done 的代次不应匹配当前条目")
	}
	if !hub.PendingTurnGenMatches("s1", genB) {
		t.Fatalf("本轮 done 的代次应匹配")
	}
	// 没有身份的调用点保守放行。
	if !hub.PendingTurnGenMatches("s1", 0) {
		t.Fatalf("gen=0 应保守放行")
	}
	// 条目不存在时放行（前端仍需 done 帧清本地乐观灯）。
	hub.ClearSessionPending("s1", genB)
	if !hub.PendingTurnGenMatches("s1", genA) {
		t.Fatalf("条目不存在时应放行")
	}
}
