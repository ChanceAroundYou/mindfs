package webpush

import (
	"fmt"
	"testing"
	"time"
)

func newTestPresence(now func() time.Time, ttl time.Duration) *PresenceTracker {
	tracker := NewPresenceTracker()
	tracker.now = now
	tracker.ttl = ttl
	return tracker
}

// 页面还开着 = 至少有一个客户端在 TTL 内发过心跳。
func TestPresenceLiveRequiresHeartbeatWithinTTL(t *testing.T) {
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	tracker := newTestPresence(func() time.Time { return now }, 10*time.Minute)

	if tracker.Live("https://push.example/endpoint") {
		t.Fatal("没心跳的订阅不能算活着")
	}
	tracker.Touch("https://push.example/endpoint", "tab-1")
	if !tracker.Live("https://push.example/endpoint") {
		t.Fatal("刚发过心跳的订阅应当算活着")
	}

	now = now.Add(9 * time.Minute)
	if !tracker.Live("https://push.example/endpoint") {
		t.Fatal("TTL 内应当继续算活着")
	}

	now = now.Add(2 * time.Minute)
	if tracker.Live("https://push.example/endpoint") {
		t.Fatal("超过 TTL 的心跳必须过期")
	}
}

// 关一个 tab 不能把同订阅下还开着的另一个 tab 一起注销。
func TestPresenceLeaveOnlyRemovesThatClient(t *testing.T) {
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	tracker := newTestPresence(func() time.Time { return now }, 10*time.Minute)
	tracker.Touch("https://push.example/endpoint", "tab-1")
	tracker.Touch("https://push.example/endpoint", "tab-2")

	tracker.Leave("https://push.example/endpoint", "tab-1")
	if !tracker.Live("https://push.example/endpoint") {
		t.Fatal("tab-2 还开着，整个订阅不能被判死")
	}

	tracker.Leave("https://push.example/endpoint", "tab-2")
	if tracker.Live("https://push.example/endpoint") {
		t.Fatal("最后一个 tab 关闭后必须立刻判死，不能等 TTL")
	}
}

// pagehide beacon 与 SW 的兜底上报拿不到 client_id，此时按整条订阅注销。
func TestPresenceLeaveWithoutClientIDClearsEndpoint(t *testing.T) {
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	tracker := newTestPresence(func() time.Time { return now }, 10*time.Minute)
	tracker.Touch("https://push.example/endpoint", "tab-1")
	tracker.Touch("https://push.example/endpoint", "tab-2")

	tracker.Leave("https://push.example/endpoint", "")
	if tracker.Live("https://push.example/endpoint") {
		t.Fatal("无 client_id 的注销必须清掉整条订阅")
	}
}

// 同一个浏览器反复开页面会换 client_id，账本不能无限涨。
func TestPresenceCapsClientsPerEndpoint(t *testing.T) {
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	tracker := newTestPresence(func() time.Time { return now }, 10*time.Minute)
	for i := 0; i < maxPresenceClientsPerEndpoint+20; i++ {
		now = now.Add(time.Second)
		tracker.Touch("https://push.example/endpoint", fmt.Sprintf("tab-%d", i))
	}
	tracker.mu.Lock()
	clients := len(tracker.endpoints["https://push.example/endpoint"])
	tracker.mu.Unlock()
	if clients > maxPresenceClientsPerEndpoint {
		t.Fatalf("clients = %d, want <= %d", clients, maxPresenceClientsPerEndpoint)
	}
}

// 长期运行不能因为 endpoint 只增不减把内存吃光。
func TestPresenceExpiresEndpointsAfterTTL(t *testing.T) {
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	tracker := newTestPresence(func() time.Time { return now }, 10*time.Minute)
	for i := 0; i < 50; i++ {
		tracker.Touch("https://push.example/e"+time.Duration(i).String(), "tab")
	}
	now = now.Add(11 * time.Minute)
	// 触发一次清理：任何 Live 查询都会顺手扫过期条目。
	_ = tracker.Live("https://push.example/e0")
	tracker.mu.Lock()
	remaining := len(tracker.endpoints)
	tracker.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("expired endpoints = %d, want 0", remaining)
	}
}
