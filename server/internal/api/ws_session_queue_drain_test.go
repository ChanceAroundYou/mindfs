package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

// 排队消息必须在**上一轮真正结束之后**被自动取出执行。
//
// 症状（丢了会怎样）：模型正在回答时发的消息进了排队序列，上一轮结束后它一直躺在
// 队列里不动，用户得手动点「立即发送」才跑 —— 排队等于失效。
//
// 根因：回合的收尾（EndSessionTurn → ClearSessionPending，把 pending 的 Active 置回
// false）住在 runSessionMessage 的 defer 里，而取队列的动作写在函数尾。defer 永远在
// return 时才跑，所以取队列那一刻 Active 仍是 true，PopQueuedSessionMessage 的
// 「有回合在跑就不许再起一轮」闸门直接把它挡回去（返回 ok=false），队列原封不动。
// 改造前的顺序恰好相反（先 ClearSessionPending 再取队列），S2e 把收尾挪进 defer 时
// 这一步被落在了 defer 外面。
//
// 这个测试跑的是真函数：reserve 占住第一轮 → 塞第二条 → 同步跑完第一轮 → 队列必须空。
// 没有 agent pool，SendMessage 立刻失败，但「失败也是一轮结束」，收尾路径一模一样。
func TestQueuedMessageRunsAfterTurnEnds(t *testing.T) {
	registry := fs.NewRegistry(filepath.Join(t.TempDir(), "registry.json"))
	// 第二轮是后台 goroutine：队列一空本测试就结束，而它可能还在往
	// <root>/.mindfs/sessions 落盘。t.TempDir 的清理是「一次 RemoveAll 失败即测试失败」，
	// 于是收尾时机就成了偶发失败点（2026-10-22 实测：整包跑必红、单跑必绿）。
	// 根目录改用普通临时目录 + 清理重试，语义不变，只是不再拿清理竞态当失败。
	rootDir, err := os.MkdirTemp("", "mindfs-queued-drain-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for i := 0; i < 100; i++ {
			if err := os.RemoveAll(rootDir); err == nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
	root, err := registry.Upsert(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	app := &AppContext{Dirs: registry}
	manager, err := app.GetSessionManager(root.ID)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := manager.Create(context.Background(), session.CreateInput{Type: session.TypeChat, Agent: "codex", Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}

	hub := app.GetSessionStreamHub()
	first := QueuedUserMessage{
		ID:                 "first",
		PendingUserMessage: PendingUserMessage{Content: "first", Agent: "codex", Model: "test-model", Timestamp: time.Now().UTC()},
	}
	if _, queued := hub.reserveOrQueueSessionMessage(root.ID, chat.Key, chat.Name, first); queued {
		t.Fatal("第一条消息应当直接开跑，不该排队")
	}
	hub.EnqueueSessionMessage(root.ID, chat.Key, chat.Name, QueuedUserMessage{
		ID:                 "second",
		PendingUserMessage: PendingUserMessage{Content: "second", Agent: "codex", Model: "test-model", Timestamp: time.Now().UTC()},
	})

	h := &WSHandler{AppContext: app}
	h.runSessionMessage(sessionMessageJob{
		RootID:      root.ID,
		Key:         chat.Key,
		RequestID:   first.ID,
		SessionType: session.TypeChat,
		SessionName: chat.Name,
		User:        first.PendingUserMessage,
	})

	// 第二轮由 startNextQueuedSessionMessage 起 goroutine，队列在返回前就该被取走。
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, queue, _ := hub.queueSnapshot(chat.Key)
		if len(queue) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("上一轮结束后排队消息仍留在队列里（剩 %d 条），自动取出没有发生", len(queue))
		}
		time.Sleep(10 * time.Millisecond)
	}
}
