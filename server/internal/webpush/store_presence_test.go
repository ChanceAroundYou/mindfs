package webpush

import (
	"os"
	"testing"
	"time"
)

// 旧订阅在收到任意 presence 上报后立刻补上能力声明；之后重复上报不许反复写盘
// （心跳每 30 秒一次，写盘会变成无谓的持续 IO）。
func TestStoreMarkPresenceCapableFlipsLegacyWithoutChurn(t *testing.T) {
	store := NewStoreAt(t.TempDir())
	sub, err := store.Upsert(newTestSubscription(t, "https://push.example/mark-legacy", false))
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if sub.PresenceCapable {
		t.Fatal("precondition: 新保存的旧订阅不应 capable")
	}
	beforeUpdated := sub.UpdatedAt

	time.Sleep(10 * time.Millisecond)
	if err := store.MarkPresenceCapable(sub.Endpoint); err != nil {
		t.Fatalf("mark: %v", err)
	}
	got, ok, err := store.GetByEndpoint(sub.Endpoint)
	if err != nil || !ok {
		t.Fatalf("get after mark: ok=%v err=%v", ok, err)
	}
	if !got.PresenceCapable {
		t.Fatal("presence 上报后订阅必须标记为 capable")
	}
	if !got.Enabled {
		t.Fatal("标记 capable 不能改变启用状态")
	}
	if !got.UpdatedAt.Equal(beforeUpdated) {
		t.Fatalf("UpdatedAt = %v, want %v（心跳不能刷新订阅时间）", got.UpdatedAt, beforeUpdated)
	}

	infoBefore, err := os.Stat(store.path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := store.MarkPresenceCapable(sub.Endpoint); err != nil {
		t.Fatalf("mark again: %v", err)
	}
	infoAfter, err := os.Stat(store.path)
	if err != nil {
		t.Fatalf("stat again: %v", err)
	}
	if !infoBefore.ModTime().Equal(infoAfter.ModTime()) {
		t.Fatal("已 capable 时重复上报不许写盘")
	}
}

// 未知 endpoint 的 presence 上报（例如订阅表被清空后页面还在心跳）不能凭空造一条
// 没有密钥的记录。
func TestStoreMarkPresenceCapableIgnoresUnknownEndpoint(t *testing.T) {
	store := NewStoreAt(t.TempDir())
	if err := store.MarkPresenceCapable("https://push.example/never-saved"); err != nil {
		t.Fatalf("mark unknown: %v", err)
	}
	items, err := store.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("subscriptions = %d, want 0（presence 上报不能创建订阅）", len(items))
	}
}
