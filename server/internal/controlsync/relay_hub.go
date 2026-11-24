package controlsync

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
)

// RelayHub 是主控侧的隧道注册表：记录每台边控端当前有没有活的隧道，
// 并把一条数据通道借给一次 HTTP 请求。
//
// 为什么需要它：边控端没有可暴露的域名/端口（办公室里的机器，外面进不来），
// 所以主控没法直连它。唯一的方向是边控端**主动拨出**。拨出之后主控手上就有
// 一条能双向跑字节的连接，把它交给 net/http 即可原样服务那台机器的 API 与 WS。
//
// 三条约束决定了它的形状：
//
//  1. **一条通道只服务一次请求**。数据通道上没有多路复用帧协议：主控把 HTTP
//     请求写进去，边控端用本机原来的 handler 应答，然后连接关闭。这样 WS 升级
//     （101 + 之后的全双工字节流）不需要任何额外处理，Go 标准库的 ReverseProxy
//     原生支持。
//  2. **池子预热**。每次请求都拨一条新连接要吃一次跨网 TLS 握手；页面加载几十个
//     请求就是几十次。所以边控端保持若干条空闲通道待命。
//  3. **离线不是错误**。边控端关机时它的节点记录要留着、标不可达，而不是让每个
//     请求都撞 502（见 relay 入口 handler）。
type RelayHub struct {
	mu    sync.Mutex
	edges map[string]*relayEdge // instance id → 活着的隧道

	// idleTimeout 是空闲数据通道的最长等待。超过就主动关掉，让边控端按需再拨；
	// 否则连接池会在 NAT 上留下大量已经死掉但又没被发现的连接。
	idleTimeout time.Duration
	// acquireTimeout 是请求等一条空闲通道的最长时间。等不到回 503 relay_busy，
	// 语义与既有的 primary_unreachable 同一档（前端已有处理路径）。
	acquireTimeout time.Duration
}

type relayEdge struct {
	instanceID string
	name       string

	mu       sync.Mutex
	idle     chan net.Conn
	closed   bool
	tunnelAt time.Time
}

// NewRelayHub 建一个空的 hub。两个超时都为零时取默认值。
func NewRelayHub() *RelayHub {
	return &RelayHub{
		edges:          make(map[string]*relayEdge),
		idleTimeout:    2 * time.Minute,
		acquireTimeout: 5 * time.Second,
	}
}

// ErrRelayOffline：这台边控端当前没有活的隧道（关机 / 断网 / 被踢）。
var ErrRelayOffline = errors.New("controlsync: relay edge offline")

// ErrRelayBusy：隧道在，但空闲通道用完了（边控端跟不上请求速率）。
var ErrRelayBusy = errors.New("controlsync: relay edge busy")

// Register 登记一条控制通道，返回该边控端的通道池。
//
// 名字冲突由调用方（HTTP 层）先分配好再进来：hub 不猜名字，它只按 instance id
// 管连接。重复登记（边控端重连）会替换旧池并关掉它的空闲通道 —— 老连接此时
// 已经没用了，留着只会让请求被派到一条对端不再读的连路上。
func (h *RelayHub) Register(instanceID, name string, idleCap int) (*relayEdge, error) {
	if h == nil {
		return nil, errors.New("controlsync: relay hub required")
	}
	instanceID = strings.TrimSpace(instanceID)
	name = strings.TrimSpace(name)
	if instanceID == "" || name == "" {
		return nil, errors.New("controlsync: instance id and relay name required")
	}
	if idleCap <= 0 {
		idleCap = defaultRelayIdleCap
	}
	edge := &relayEdge{
		instanceID: instanceID,
		name:       name,
		idle:       make(chan net.Conn, idleCap),
		tunnelAt:   time.Now(),
	}
	h.mu.Lock()
	prev := h.edges[instanceID]
	h.edges[instanceID] = edge
	h.mu.Unlock()
	if prev != nil {
		prev.close()
	}
	return edge, nil
}

// Unregister 在控制通道断开时清掉登记，只清自己那条（重连后旧连接的延迟清理
// 不能把新隧道一起带走）。
func (h *RelayHub) Unregister(edge *relayEdge) {
	if h == nil || edge == nil {
		return
	}
	h.mu.Lock()
	if cur, ok := h.edges[edge.instanceID]; ok && cur == edge {
		delete(h.edges, edge.instanceID)
	}
	h.mu.Unlock()
	edge.close()
}

// Online 报告这台边控端当前有没有活隧道。
func (h *RelayHub) Online(instanceID string) bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.edges[instanceID]
	return ok
}

// OnlineNames 返回当前在线的所有对外名字（用于运维视图与诊断）。
func (h *RelayHub) OnlineNames() []string {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.edges))
	for _, e := range h.edges {
		out = append(out, e.name)
	}
	sort.Strings(out)
	return out
}

// Attach 把一条已升级的数据通道交进池子。池满时返回 false，调用方应当关掉它。
func (h *RelayHub) Attach(instanceID string, conn net.Conn) bool {
	if h == nil || conn == nil {
		return false
	}
	h.mu.Lock()
	edge, ok := h.edges[strings.TrimSpace(instanceID)]
	h.mu.Unlock()
	if !ok {
		return false
	}
	return edge.attach(conn)
}

// Acquire 借一条数据通道，只服务于一次请求。
//
// 返回的 conn 由调用方负责关闭（ReverseProxy 的 DisableKeepAlives 会关它）。
// 等不到就区分两种情况：没有任何隧道 = offline，有隧道但通道被占满 = busy。
func (h *RelayHub) Acquire(ctx context.Context, instanceID string) (net.Conn, error) {
	if h == nil {
		return nil, ErrRelayOffline
	}
	h.mu.Lock()
	edge, ok := h.edges[strings.TrimSpace(instanceID)]
	h.mu.Unlock()
	if !ok {
		return nil, ErrRelayOffline
	}
	return edge.acquire(ctx, h.acquireTimeout)
}

func (e *relayEdge) attach(conn net.Conn) bool {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return false
	}
	e.mu.Unlock()
	select {
	case e.idle <- conn:
		return true
	default:
		return false
	}
}

func (e *relayEdge) acquire(ctx context.Context, timeout time.Duration) (net.Conn, error) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case conn := <-e.idle:
		if conn == nil {
			return nil, ErrRelayOffline
		}
		return conn, nil
	case <-timer.C:
		return nil, ErrRelayBusy
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// close 关掉池子里所有的空闲通道。已经被借走的那几条不在这里处理：
// 它们归当次请求所有，由 ReverseProxy 或 handler 关闭。
func (e *relayEdge) close() {
	if e == nil {
		return
	}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.closed = true
	e.mu.Unlock()
	for {
		select {
		case conn := <-e.idle:
			if conn != nil {
				_ = conn.Close()
			}
		default:
			return
		}
	}
}

// defaultRelayIdleCap 是一条隧道允许缓存的空闲数据通道数。
//
// 8 条足够覆盖一次页面加载的并发（浏览器对同一 origin 的并发上限通常是 6），
// 再多只是让边控端白养连接；不够时边控端会按 channel_wanted/channel_grant
// 追加（见 TunnelClient）。
const defaultRelayIdleCap = 8

// ValidRelayName 说明合法的对外名字长什么样：小写字母、数字、连字符。
//
// 它会出现在 URL 路径的一段里，所以不允许大写（避免大小写敏感/不敏感两种解析
// 打起来）、不允许下划线或点（点会和 ".." 混淆），也不允许空。
//
// 导出是因为 api 层要在拼路径之前校验它 —— 名字是外部输入。
func ValidRelayName(name string) bool {
	if name == "" || len(name) > 32 {
		return false
	}
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			continue
		}
		return false
	}
	return name != "-" && !strings.HasPrefix(name, "-") && !strings.HasSuffix(name, "-")
}

// SlugRelayName 从任意字符串（通常是主机名）派生一个合法的对外名字。
//
// 主机名可能带点、大写、下划线、中文。全部折叠成 [a-z0-9-]，并把连续的分隔符
// 压成一个 —— 结果要么是空串（调用方必须自己再给一个），要么满足 validRelayName。
func SlugRelayName(raw string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
		if b.Len() >= 32 {
			break
		}
	}
	out := strings.Trim(b.String(), "-")
	if !ValidRelayName(out) {
		return ""
	}
	return out
}

// AssignRelayName 决定一台边控端最终用哪个名字。
//
// 规则（顺序不能换）：
//  1. 它已经有过名字且没被别的机器占着 → 原样保留。地址稳定性优先于「想要的名字」：
//     主控重启、边控端重连都不该让其他设备手里的地址失效。
//  2. 候选名空闲 → 用它。
//  3. 被另一台机器占着 → 追加 -<instance 前 4 位>。撞两次才会走到下面。
//  4. 仍然撞 → 继续加长后缀，最后兜底成 relay-<instance 前 8 位>。
//
// taken 是「名字 → instance id」的现有占用表。
func AssignRelayName(existing, candidate, instanceID string, taken map[string]string) string {
	instanceID = strings.TrimSpace(instanceID)
	if cur := strings.TrimSpace(existing); ValidRelayName(cur) {
		if owner, ok := taken[cur]; !ok || owner == instanceID {
			return cur
		}
	}
	base := strings.TrimSpace(candidate)
	if !ValidRelayName(base) {
		base = SlugRelayName(base)
	}
	if base == "" {
		base = "edge"
	}
	if owner, ok := taken[base]; !ok || owner == instanceID {
		return base
	}
	short := shortID(instanceID, 4)
	for _, suffix := range []string{short, shortID(instanceID, 8)} {
		if suffix == "" {
			continue
		}
		cand := truncateName(base, 32-len(suffix)-1) + "-" + suffix
		if owner, ok := taken[cand]; !ok || owner == instanceID {
			return cand
		}
	}
	// 兜底：一定会成功，因为 instance id 唯一。
	return "relay-" + shortID(instanceID, 8)
}

// shortID 取实例 id 的前 n 个合法字符。id 里可能有冒号、斜杠，先 slug 一遍。
func shortID(instanceID string, n int) string {
	slug := SlugRelayName(instanceID)
	slug = strings.ReplaceAll(slug, "-", "")
	if len(slug) <= n {
		return slug
	}
	return slug[:n]
}

func truncateName(name string, max int) string {
	if max < 1 {
		max = 1
	}
	if len(name) <= max {
		return name
	}
	return strings.TrimRight(name[:max], "-")
}

// relayLogf 只在真的有状态变化时打日志；隧道每 5 秒一次心跳不该刷屏。
func relayLogf(format string, args ...any) {
	log.Printf("[relay] "+format, args...)
}

// Describe 给诊断用：这条隧道当前缓存了几条空闲通道。
func (e *relayEdge) Describe() string {
	if e == nil {
		return "edge=<nil>"
	}
	return fmt.Sprintf("edge=%s idle=%d/%d", e.name, len(e.idle), cap(e.idle))
}
