package api

import "testing"

// 子代理「早已完成却一直显示运行、停止无效」的收敛回归。
//
// 现场（wsl / CMAI，子会话 1795584428）：内容 13:41 就停了，服务端内存 pending
// 却一直留着；三次停止请求都是 target_turn=0，CancelSessionTurn 在 active==nil
// 分支静默返回，界面因此永远「运行中」。残留 pending 没有活跃轮可取消，停止按钮
// 必须能按 sessionKey 幂等清掉它。
func TestReconcileOrphanedSubSessionPendingClearsStaleReply(t *testing.T) {
	app, rootID, _ := newTaskQuestionTestApp(t, "task-orphan-pending")
	const key = "child-orphan-1"
	if turnID := app.SetSessionPendingReply(rootID, key, "Research differentiable wind farm models", "turn-orphan-1"); turnID == "" {
		t.Fatal("SetSessionPendingReply returned an empty turn id")
	}
	hub := app.GetSessionStreamHub()
	if _, found := hub.PendingSessionSnapshotFound(key); !found {
		t.Fatal("前置条件不成立：pending 没置上")
	}

	h := &WSHandler{AppContext: app}
	if !h.reconcileOrphanedSubSessionPending(rootID, key, "req-1") {
		t.Fatal("没有活跃轮的残留子会话 pending 没有被清掉：停止按钮仍是结构性 no-op")
	}
	if _, found := hub.PendingSessionSnapshotFound(key); found {
		t.Fatal("清完之后 pending 仍在：灯不会灭")
	}
	// 幂等：第二次停止请求不能报「清掉了」。
	if h.reconcileOrphanedSubSessionPending(rootID, key, "req-2") {
		t.Fatal("第二次调用仍声称清掉了一条，实际已经没有条目")
	}
}

// 根不匹配时绝不能清：跨节点的同名 sessionKey 不能互相误伤。
func TestReconcileOrphanedSubSessionPendingIgnoresOtherRoot(t *testing.T) {
	app, rootID, _ := newTaskQuestionTestApp(t, "task-orphan-other-root")
	const key = "child-orphan-2"
	app.SetSessionPendingReply(rootID, key, "子会话", "turn-orphan-2")
	h := &WSHandler{AppContext: app}

	if h.reconcileOrphanedSubSessionPending("another-root", key, "req-1") {
		t.Fatal("根不匹配却清掉了 pending")
	}
	if _, found := app.GetSessionStreamHub().PendingSessionSnapshotFound(key); !found {
		t.Fatal("根不匹配的请求把 pending 清掉了")
	}
}
