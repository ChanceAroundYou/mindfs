package api

import (
	"testing"
	"time"
)

// 挂上会话时必须收到一份**权威队列状态**，空队列也要发。
//
// 症状（2026-10-25 用户实测）：一句排队的指令在**非前台**状态下轮到它了、甚至跑完了，
// 界面上「排队中」的样式一直不消失。
//
// 根因：队列帧只有 BroadcastSessionQueueUpdated 这一条即时的、发完即忘的通道。
// 后台标签页错过「出队/清空」那一帧之后再挂上来（切回会话 / WS 重连 / 重锚定都会
// 重发 session.ready），服务端这里按 `len(queue) == 0 → return` 什么都不发，客户端
// 手里那份旧队列就再也没有任何东西去纠正它。
func TestReplayQueueSendsEmptyStateToLateClient(t *testing.T) {
	hub := NewStreamHub()
	conn := dialHub(t, hub)

	// 一轮在跑、队列里有一条 —— 客户端在后台错过了「出队」那一帧。
	if _, queued := hub.reserveOrQueueSessionMessage("root", "sess-1", "title", QueuedUserMessage{
		ID:                 "q1",
		PendingUserMessage: PendingUserMessage{Content: "排队的话", Timestamp: time.Now().UTC()},
	}); queued {
		t.Fatal("首条消息应当直接开跑，不该排队")
	}
	hub.EnqueueSessionMessage("root", "sess-1", "title", QueuedUserMessage{
		ID:                 "q2",
		PendingUserMessage: PendingUserMessage{Content: "第二条", Timestamp: time.Now().UTC()},
	})
	// 一轮结束（Active 置回 false），随后排队消息被取出 —— 这一步正是后台客户端
	// 错过的那一帧。
	hub.ClearSessionPending("sess-1", "")
	if _, queue, ok := hub.PopQueuedSessionMessage("sess-1", "q2"); !ok || len(queue) != 0 {
		t.Fatalf("出队失败，队列剩 %d 条", len(queue))
	}
	// 现在队列是空的：挂上来的客户端必须收到那条「空」帧，才能把本地旧队列收敛掉。
	hub.ReplayPending("root", "test-client", "sess-1")

	resp := readBroadcast(t, conn, "session.queue.updated")
	if got, _ := resp.Payload["root_id"].(string); got != "root" {
		t.Fatalf("root_id = %q, want root", got)
	}
	if got, _ := resp.Payload["session_key"].(string); got != "sess-1" {
		t.Fatalf("session_key = %q, want sess-1", got)
	}
	queue, ok := resp.Payload["queue"].([]any)
	if !ok {
		// nil 切片会被序列化成 null，前端 Array.isArray 判据会把它当成「没有队列字段」，
		// 于是这次收敛又白发了。
		t.Fatalf("queue 必须是数组（空也要是 []），得到 %#v", resp.Payload["queue"])
	}
	if len(queue) != 0 {
		t.Fatalf("queue = %#v, want 空数组（客户端据此清掉排队样式）", queue)
	}
}
