package webpush

import (
	"sync"
	"time"
)

// 页面在场表：Web Push 是唯一与页面生命周期无关的通道，订阅一旦建立就永久有效。
// 桌面浏览器关掉页面后，后台 agent 每跑完一轮仍会扇出到那条订阅 —— 用户看到的就是
// 「页面关了通知还在弹」。这里按「订阅（endpoint）→ 活着的页面客户端」记账，
// 扇出时只投递给还有页面开着的订阅。
//
// 三个信号：
//   - Touch：页面心跳（可见时 30s 一次；后台标签页被浏览器节流后约 1 次/分钟）；
//   - Leave（带 client_id）：pagehide beacon，关一个 tab 只摘掉它自己；
//   - Leave（不带 client_id）：service worker 发现一个窗口都没有时的自愈上报。
//
// TTL 存在的意义是「beacon 没送出去」的兜底（进程崩溃、强杀、断电）：心跳过期后
// 服务端自动回到静默。正常关页不等 TTL，beacon 到达即生效。
const (
	defaultPresenceTTL            = 10 * time.Minute
	maxPresenceClientsPerEndpoint = 16
	maxPresenceEndpoints          = 512
)

type PresenceTracker struct {
	mu sync.Mutex
	// now 可注入，便于测试 TTL。
	now func() time.Time
	ttl time.Duration
	// endpoint → client_id → 最后一次心跳时刻。
	endpoints map[string]map[string]time.Time
}

func NewPresenceTracker() *PresenceTracker {
	return &PresenceTracker{
		now:       time.Now,
		ttl:       defaultPresenceTTL,
		endpoints: make(map[string]map[string]time.Time),
	}
}

// Touch 记录一次页面心跳。空 endpoint 直接忽略（订阅还没建立）。
func (p *PresenceTracker) Touch(endpoint, clientID string) {
	if p == nil || endpoint == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	p.pruneLocked(now)
	clients, ok := p.endpoints[endpoint]
	if !ok {
		if len(p.endpoints) >= maxPresenceEndpoints {
			p.evictOldestEndpointLocked()
		}
		clients = make(map[string]time.Time)
		p.endpoints[endpoint] = clients
	}
	if _, seen := clients[clientID]; !seen && len(clients) >= maxPresenceClientsPerEndpoint {
		// 同一个浏览器反复开页面会换 client_id；淘汰最旧的一个，别让账本无限涨。
		oldestID := ""
		var oldest time.Time
		for id, at := range clients {
			if oldestID == "" || at.Before(oldest) {
				oldestID, oldest = id, at
			}
		}
		delete(clients, oldestID)
	}
	clients[clientID] = now
}

// Leave 注销客户端。clientID 为空表示「这条订阅下所有页面都没了」：
// pagehide beacon 拿不到 id、或 service worker 发现零个窗口时走这条路。
func (p *PresenceTracker) Leave(endpoint, clientID string) {
	if p == nil || endpoint == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pruneLocked(p.now())
	if clientID == "" {
		delete(p.endpoints, endpoint)
		return
	}
	clients, ok := p.endpoints[endpoint]
	if !ok {
		return
	}
	delete(clients, clientID)
	if len(clients) == 0 {
		delete(p.endpoints, endpoint)
	}
}

// Live 表示这条订阅下至少还有一个 TTL 内的页面客户端。
func (p *PresenceTracker) Live(endpoint string) bool {
	if p == nil || endpoint == "" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	p.pruneLocked(now)
	clients, ok := p.endpoints[endpoint]
	if !ok {
		return false
	}
	for _, seen := range clients {
		if now.Sub(seen) <= p.ttl {
			return true
		}
	}
	return false
}

func (p *PresenceTracker) pruneLocked(now time.Time) {
	for endpoint, clients := range p.endpoints {
		for id, seen := range clients {
			if now.Sub(seen) > p.ttl {
				delete(clients, id)
			}
		}
		if len(clients) == 0 {
			delete(p.endpoints, endpoint)
		}
	}
}

func (p *PresenceTracker) evictOldestEndpointLocked() {
	oldestEndpoint := ""
	var oldest time.Time
	for endpoint, clients := range p.endpoints {
		var latest time.Time
		for _, seen := range clients {
			if seen.After(latest) {
				latest = seen
			}
		}
		if oldestEndpoint == "" || latest.Before(oldest) {
			oldestEndpoint, oldest = endpoint, latest
		}
	}
	if oldestEndpoint != "" {
		delete(p.endpoints, oldestEndpoint)
	}
}
