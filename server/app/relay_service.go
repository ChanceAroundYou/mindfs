package app

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"mindfs/server/internal/api"
	"mindfs/server/internal/controlsync"
	"mindfs/server/internal/nodes"
)

// relayService 是主节点侧 relay 的实现：管隧道、分配名字、并把「这台边控端
// 现在可以从哪访问」发布进节点表。
//
// 为什么发布进节点表：nodes 本来就是同步域（主控权威、边缘只读副本）。写一条
// 记录，其他设备在下一拍（5 秒）自动学到这个地址 —— 不需要新同步域，也不需要
// 前端做任何事（getApiBaseURL 直接从节点表取 base）。
type relayService struct {
	store *controlsync.Store
	hub   *controlsync.RelayHub
	nodes *nodes.Store

	mu sync.Mutex
	// published 记住「这台边控端当前发布的节点 id」，断开时按它下线。
	// 键是 instance id，不是 relay name：名字理论上不变，但 id 一定不变。
	published map[string]string
	// relayBase 是主控自己的对外基址（https://主控/mindfs）。
	// 用它拼出 /relay/<name>。通常留空 —— 边控端拨进来的请求里带着对外 Host，
	// 比配置里的字面量更准（见 OnTunnelUp）。
	relayBase string
}

var (
	_ api.ControlSyncRelay = (*relayService)(nil)
	// 转发端要的只是一份「按名字找机器、借通道、问在线」的能力；让 relayService
	// 直接满足它，装配处就不必拼两个对象。
	_ api.RelayHubService = (*relayService)(nil)
)

func (r *relayService) Hub() *controlsync.RelayHub { return r.hub }

// EdgeByRelayName / Acquire / Online 是转发端要的最小能力面（api.RelayHubService）。
func (r *relayService) EdgeByRelayName(ctx context.Context, name string) (controlsync.EdgeInfo, error) {
	return r.store.EdgeByRelayName(ctx, name)
}

func (r *relayService) Acquire(ctx context.Context, instanceID string) (net.Conn, error) {
	return r.hub.Acquire(ctx, instanceID)
}

func (r *relayService) Online(instanceID string) bool { return r.hub.Online(instanceID) }

func (r *relayService) ObserveEdge(ctx context.Context, instanceID string) (controlsync.EdgeInfo, error) {
	return r.store.ObserveEdge(ctx, instanceID, timeNow())
}

// AllocateName 决定并落库对外名字。
//
// 「已用过」的优先保留：地址一旦发出去，其他设备的节点表里就存着它。改名等于
// 让那些设备手里的地址失效 —— 只有真正的冲突（另一台机器占着）才允许改。
func (r *relayService) AllocateName(ctx context.Context, instanceID, candidate string) (string, error) {
	taken, err := r.store.RelayNames(ctx)
	if err != nil {
		return "", err
	}
	existing := ""
	if cur, ok := taken[strings.TrimSpace(candidate)]; ok && cur == instanceID {
		existing = candidate
	}
	// 取这台机器已登记的名字：重连时必须原样返回，不能让 Address 漂移。
	if edge, err := r.store.EdgeByID(ctx, instanceID); err == nil {
		existing = edge.RelayName
	}
	name := controlsync.AssignRelayName(existing, candidate, instanceID, taken)
	if err := r.store.SetRelayName(ctx, instanceID, name); err != nil {
		return "", err
	}
	// 登记了一张新的占用表视图之后，pool 里其它机器也该能看到这次改名。
	// 这里不需要主动广播：节点表在 OnTunnelUp 里写，下一拍同步就会带过去。
	return name, nil
}

// OnTunnelUp 在隧道建立后发布节点表记录。
//
// 记录内容只有一件事：这台机器现在可以从哪个 base 访问。前端 getApiBaseURL
// 直接取它当 /api/* 与 /ws 的 base，所以不需要任何前端改动。
//
// 基址优先取配置，其次取「边控端拨进来的那条请求」里看到的 scheme+Host+前缀 ——
// 后者天然就是这台机器对外的样子，不需要人去填一遍自己的公网域名。
func (r *relayService) OnTunnelUp(ctx context.Context, instanceID, name, publicBase string) {
	if r == nil || r.nodes == nil || name == "" {
		return
	}
	base := strings.TrimRight(strings.TrimSpace(r.relayBase), "/")
	if base == "" {
		base = strings.TrimRight(strings.TrimSpace(publicBase), "/")
	}
	if base == "" {
		// 两条路都没给出可用地址。发一条明显不对的 URL 不如不发：
		// 至少运维能从这条日志看出「为什么别的设备看不到这台机器」。
		log.Printf("[relay] publish skipped: no public base instance=%s name=%s", instanceID, name)
		return
	}
	nodeID := relayNodeID(instanceID)
	rec := nodes.NodeConnection{
		ID:     nodeID,
		Name:   name,
		URL:    base + "/relay/" + name,
		Remote: true,
	}
	if _, err := r.nodes.Upsert(rec); err != nil {
		log.Printf("[relay] publish failed instance=%s name=%s err=%v", instanceID, name, err)
		return
	}
	r.mu.Lock()
	r.published[instanceID] = nodeID
	r.mu.Unlock()
	log.Printf("[relay] published node=%s url=%s", nodeID, rec.URL)
}

// OnTunnelDown 在隧道断开时把节点标为不可达。
//
// **不删记录**：删掉等于告诉其他设备「这台机器没了」，而它只是暂时离线。
// 可达性由前端的存活探测负责标红（services/net/nodeHealth.ts）。
func (r *relayService) OnTunnelDown(ctx context.Context, instanceID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	_, ok := r.published[instanceID]
	r.mu.Unlock()
	if !ok {
		return
	}
	// 记录留着，地址留着 —— 隧道一恢复，同一个地址立刻又能用了。
	log.Printf("[relay] tunnel down instance=%s (node record kept)", instanceID)
}

// relayNodeID 由 instance id 派生节点 id。
//
// 加前缀是为了与手工添加的节点区分开：运维一眼能看出这一行是 relay 自动发布的，
// 手工删掉它时也知道删的是什么。
func relayNodeID(instanceID string) string {
	slug := controlsync.SlugRelayName(instanceID)
	if slug == "" {
		slug = "edge"
	}
	return "relay:" + slug
}

// timeNow 单独抽出来，测试要能钉住时刻。
var timeNow = func() time.Time { return time.Now() }

// newRelayService 组装主节点侧的 relay。
func newRelayService(store *controlsync.Store, nodeStore *nodes.Store, relayBase string) (*relayService, error) {
	if store == nil {
		return nil, errors.New("relay: control sync store required")
	}
	if nodeStore == nil {
		return nil, errors.New("relay: nodes store required")
	}
	return &relayService{
		store:     store,
		hub:       controlsync.NewRelayHub(),
		nodes:     nodeStore,
		published: make(map[string]string),
		relayBase: relayBase,
	}, nil
}

// startControlSyncRelay 组装主节点侧的 relay。
//
// 只有「主节点 + 有控制面库 + 有节点表」三者同时成立才建：边控端不接受别人的
// 隧道（它自己拨出去，见 startEdgeRelayTunnel），worker 根本没有控制面库。
// 三者缺一返回 (nil, nil)，路由不注册，机器保持改造前的行为。
func startControlSyncRelay(w *controlSyncWiring, nodeStore *nodes.Store, opts *ControlSyncOptions) (*relayService, error) {
	if w == nil || w.runtime == nil || nodeStore == nil {
		return nil, nil
	}
	cfg := w.runtime.Config()
	if cfg.Role != controlsync.RolePrimary {
		return nil, nil
	}
	base := ""
	if opts != nil {
		base = relayBaseFromConfig(opts.RelayBaseURL)
	}
	return newRelayService(w.runtime.Store(), nodeStore, base)
}

// startEdgeRelayTunnel 在边控端拨出隧道。
//
// handler 是这台机器自己的根 handler：隧道上跑的就是它的完整 HTTP 面，
// 主控只是把路径换了个前缀。返回 nil 表示这台机器不该拨（不是 edge / 没配 url）。
func startEdgeRelayTunnel(ctx context.Context, w *controlSyncWiring, handler http.Handler, opts *ControlSyncOptions) (*controlsync.TunnelClient, error) {
	if w == nil || w.runtime == nil || handler == nil || opts == nil {
		return nil, nil
	}
	cfg := w.runtime.Config()
	if cfg.Role != controlsync.RoleEdge {
		return nil, nil
	}
	name := strings.TrimSpace(opts.RelayName)
	if name == "" {
		// 没显式配名字就用主机名：它是人最有辨识度的那一项，且同一台机器稳定。
		if host, err := os.Hostname(); err == nil {
			name = host
		}
	}
	name = controlsync.SlugRelayName(name)
	if name == "" {
		name = "edge"
	}
	client, err := controlsync.NewTunnelClient(controlsync.TunnelOptions{
		PrimaryURL: cfg.PrimaryURL,
		Token:      cfg.Token,
		InstanceID: cfg.InstanceID,
		Name:       name,
		CAFile:     cfg.CAFile,
		OnError:    func(err error) { log.Printf("[relay] tunnel.error instance=%s err=%v", cfg.InstanceID, err) },
	}, handler)
	if err != nil {
		return nil, err
	}
	go client.Run(ctx)
	log.Printf("[relay] tunnel client started instance=%s name=%s primary=%s", cfg.InstanceID, name, cfg.PrimaryURL)
	return client, nil
}

// relayBaseFromConfig 从主控自己的配置推出对外基址。
//
// 留空是常态：主控不需要知道自己叫什么 —— 边控端拨进来的那条请求里已经有
// scheme 与 Host（见 api.PublicBaseFromRequest）。配置只在「反向代理改写了
// Host、靠请求推不准」时才需要显式指定。
func relayBaseFromConfig(primaryURL string) string {
	return strings.TrimRight(strings.TrimSpace(primaryURL), "/")
}
