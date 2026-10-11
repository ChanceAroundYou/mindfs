package api

import (
	"strings"
	"testing"

	"mindfs/server/internal/session"
)

// pendingActive 直接问「这个会话现在是否被判为在回复」——与 /api/replying-sessions 同源。
// 从 stream_hub_pending_identity_test.go 移来（S2e 换身份表示时那个文件整体作废）。
func pendingActive(hub *StreamHub, sessionKey string) bool {
	for _, s := range hub.ListReplyingSessions() {
		if s.SessionKey == sessionKey {
			return true
		}
	}
	return false
}

// S2e：pending 的回合身份从单调代次（TurnGen uint64）换成 mindfs_id（UUID string）。
//
// 先钉**规格**，再改实现（RED → GREEN）。规格是四条：
//  1. 身份是一个 UUIDv4 形状的字符串，不是计数器 —— 于是同一个逻辑 id 在主日志
//     （ExchangeIdentity.MindFSID）与 pending 灯上只有一种表示（方案 D3/D6）。
//  2. put（SetPending*）返回它，terminator（EndSessionTurn/EndSessionTurnWithIdentity）
//     用同一个值收尾；迟到的拿旧 id 的 done 必须 no-op。
//  3. 空串是「无身份」的哨兵（旧 `0` 的对位）：保守清 —— 绝不因缺身份而漏清。
//  4. 单调性不再需要：同会话两轮的身份必须**不同**，靠随机 UUID 保证，不靠 ++。
//
// 为什么不能只断言「编译不过」：类型替换本身没有行为变化，编译过了测试就该立刻绿。
// 这里钉的是「换了类型之后语义没变」，包括空串哨兵这条新边界。

func TestPendingTurnIDIsUUID(t *testing.T) {
	hub := NewStreamHub()
	id := hub.SetPendingReply("root", "s1", "t")
	if id == "" {
		t.Fatal("SetPendingReply 没有返回回合身份")
	}
	if !session.IsMindFSIDShape(id) {
		t.Fatalf("回合身份不是 UUIDv4 形状: %q", id)
	}
}

func TestPendingTurnIDsAreDistinctAcrossTurns(t *testing.T) {
	hub := NewStreamHub()
	first := hub.SetPendingReply("root", "s1", "t")
	second := hub.SetPendingReply("root", "s1", "t")
	if first == second {
		t.Fatalf("同一会话两轮的回合身份相同: %q —— 迟到的 done 会误判为本轮", first)
	}
}

// 症状「灯不亮」：第 A 轮的 done 迟到，落在第 B 轮已开始之后。
func TestStaleDoneMustNotClearNewTurnPendingTurnID(t *testing.T) {
	hub := NewStreamHub()
	idA := hub.SetPendingReply("root", "s1", "t") // 第 A 轮开始
	idB := hub.SetPendingReply("root", "s1", "t") // 第 B 轮开始（覆盖同一条目）
	hub.ClearSessionPending("s1", idA)            // 迟到的 A 轮 done
	if !pendingActive(hub, "s1") {
		t.Fatal("迟到的 done 清掉了新轮的 pending（症状「灯不亮」）")
	}
	hub.ClearSessionPending("s1", idB) // 本轮真正的 done
	if pendingActive(hub, "s1") {
		t.Fatal("本轮的 done 没能清掉 pending（症状「灯不灭」）")
	}
}

func TestClearSessionPendingIsIdempotentWithTurnID(t *testing.T) {
	hub := NewStreamHub()
	id := hub.SetPendingReply("root", "s1", "t")
	hub.ClearSessionPending("s1", id)
	hub.ClearSessionPending("s1", id) // 重复
	if pendingActive(hub, "s1") {
		t.Fatal("重复清理后仍处于 pending")
	}
}

// 空串哨兵对位旧的 gen=0：没有身份的调用点仍然清得掉（漏清 = 灯不灭）。
func TestClearSessionPendingEmptyTurnIDStillClears(t *testing.T) {
	hub := NewStreamHub()
	_ = hub.SetPendingReply("root", "s1", "t")
	hub.ClearSessionPending("s1", "")
	if pendingActive(hub, "s1") {
		t.Fatal("空身份的清理被错误跳过（会把「灯不灭」带回来）")
	}
}

// 用户发送路径同样返回可用身份，且能被它自己的终结器清掉。
func TestSetPendingUserReturnsTurnIDAndClears(t *testing.T) {
	hub := NewStreamHub()
	_, id := hub.SetPendingUser("root", "s2", "t", "claude", "m", "md", "chat", "high", "", false, "hi")
	if id == "" {
		t.Fatal("SetPendingUser 未返回回合身份")
	}
	if !session.IsMindFSIDShape(id) {
		t.Fatalf("用户路径的回合身份不是 UUIDv4: %q", id)
	}
	if !pendingActive(hub, "s2") {
		t.Fatal("用户发送后应立即处于 pending")
	}
	hub.ClearSessionPending("s2", id)
	if pendingActive(hub, "s2") {
		t.Fatal("带正确身份的清理失败")
	}
}

// pending 身份与主日志身份**同一种表示**：实时路径把 user 行写进主日志时用的
// mindfs_id 就是 pending 灯该用的那个。这是 S2e 的真正目的 —— 不留第三套。
func TestPendingTurnIDMatchesMainLogTurnIDShape(t *testing.T) {
	hub := NewStreamHub()
	id := hub.SetPendingReply("root", "s1", "t")
	if strings.Count(id, "-") != 4 {
		t.Fatalf("回合身份的段数不是 UUID 的 5 段（4 个连字符）: %q", id)
	}
	if len(id) != 36 {
		t.Fatalf("回合身份长度 = %d, want 36（与主日志 mindfs_id 同一表示）", len(id))
	}
}

func TestSubSessionPendingEndedByParentTurnWithTurnID(t *testing.T) {
	hub := NewStreamHub()
	subID := hub.SetPendingReply("root", "sub1", "child")
	if !pendingActive(hub, "sub1") {
		t.Fatal("子会话创建后应处于 pending")
	}
	hub.ClearSessionPending("sub1", subID)
	if pendingActive(hub, "sub1") {
		t.Fatal("父轮收尾未能终结子会话 pending")
	}
}

// 终结器的前置判据：迟到的 done（身份不匹配）必须被判为「不属于当前条目」，
// 从而使 EndSessionTurn 整条丢弃（不广播 done 帧）。
func TestPendingTurnIDMatches(t *testing.T) {
	hub := NewStreamHub()
	idA := hub.SetPendingReply("root", "s1", "t")
	idB := hub.SetPendingReply("root", "s1", "t")

	if hub.PendingTurnIDMatches("s1", idA) {
		t.Fatal("迟到 done 的身份不应匹配当前条目")
	}
	if !hub.PendingTurnIDMatches("s1", idB) {
		t.Fatal("本轮 done 的身份应匹配")
	}
	// 没有身份的调用点保守放行。
	if !hub.PendingTurnIDMatches("s1", "") {
		t.Fatal("空身份应保守放行")
	}
	// 条目不存在时放行（前端仍需 done 帧清本地乐观灯）。
	hub.ClearSessionPending("s1", idB)
	if !hub.PendingTurnIDMatches("s1", idA) {
		t.Fatal("条目不存在时应放行")
	}
}

func TestStreamReplyEventsCarryPendingMindFSID(t *testing.T) {
	hub := NewStreamHub()
	_, mindfsID := hub.SetPendingUser("root", "s1", "t", "claude", "m", "md", "chat", "high", "", false, "hi")

	event := hub.AppendReplyEvent("s1", StreamEvent{Type: "message_chunk", Data: map[string]string{"content": "reply"}})
	if event.MindFSID != mindfsID {
		t.Fatalf("live event mindfs_id = %q, want %q", event.MindFSID, mindfsID)
	}
	replayed := cloneEvent(event)
	if replayed.MindFSID != mindfsID {
		t.Fatalf("replayed event mindfs_id = %q, want %q", replayed.MindFSID, mindfsID)
	}
}
