package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"mindfs/internal/deploy"
	"mindfs/server/internal/controlsync"
)

// ControlSyncRelay 是主节点侧 relay 的全部能力面。
//
// 分成四件事：登记一次隧道访问（复用 edges 的自动登记 + 踢人语义）、给这台机器
// 分配对外名字、登记/注销一条控制通道、以及在隧道建立/断开时发布或下线节点表
// 记录。HTTP 层只做协议，不碰存储与节点表。
type ControlSyncRelay interface {
	// ObserveEdge 记一次访问并返回该边控端状态；被踢的返回 ErrEdgeRevoked。
	ObserveEdge(ctx context.Context, instanceID string) (controlsync.EdgeInfo, error)
	// AllocateName 决定这台机器的对外名字并落库（规则见 controlsync.AssignRelayName）。
	AllocateName(ctx context.Context, instanceID, candidate string) (string, error)
	// Hub 是隧道连接池。
	Hub() *controlsync.RelayHub
	// OnTunnelUp 在控制通道建立后发布节点表记录（其他设备据此同步到访问地址）。
	//
	// publicBase 是主控从这条请求本身推出来的对外基址（scheme + host + 部署前缀）。
	// 主控没有别的途径知道自己的公网地址，而拨进来的边控端手里恰好就是那个地址。
	// 配置里显式给了 base_url 时以配置为准（见 relayService）。
	OnTunnelUp(ctx context.Context, instanceID, name, publicBase string)
	// OnTunnelDown 在隧道断开后把节点标为不可达；**不删记录**。
	OnTunnelDown(ctx context.Context, instanceID string)
}

// handleRelayTunnel 是边控端拨进来的控制通道。
//
// 认证与同步通道完全一致（bearer + 时间戳 + nonce + 签名），因为隧道搬的是同一台
// 机器的完整 HTTP 面 —— 另起一套凭据只会多一个要被保护的东西。签名在 body 之外
// 还要覆盖查询串：`name` 决定这台机器挂在哪个路径下，不能让人改了却不影响签名。
func (h *HTTPHandler) handleRelayTunnel(w http.ResponseWriter, r *http.Request) {
	if h.ControlSyncRelay == nil || h.ControlSyncRelay.Hub() == nil {
		respondError(w, http.StatusNotFound, errInvalidRequest("relay not configured"))
		return
	}
	// 先验签再升级：签名失败时还没交出 101，调用方拿到的是普通 401。
	if err := verifyRelaySignature(r, h.ControlSyncToken, h.syncNonceCache()); err != nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="control-sync"`)
		respondError(w, http.StatusUnauthorized, errControlSyncUnauthorized)
		return
	}
	instanceID := strings.TrimSpace(r.Header.Get(controlsync.SyncInstanceHeader))
	if instanceID == "" {
		instanceID = strings.TrimSpace(r.URL.Query().Get("instance_id"))
	}
	if instanceID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("instance_id required"))
		return
	}
	// 被踢的机器不许建隧道。查在升级之前：否则它会拿到一条 101，然后每个请求都被拒，
	// 日志里看不出真正原因。
	edge, err := h.ControlSyncRelay.ObserveEdge(r.Context(), instanceID)
	if errors.Is(err, controlsync.ErrEdgeRevoked) {
		respondError(w, http.StatusForbidden, errEdgeRevoked)
		return
	}
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("relay registrar unavailable"))
		return
	}

	name, err := h.ControlSyncRelay.AllocateName(r.Context(), instanceID, r.URL.Query().Get("name"))
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("relay name unavailable"))
		return
	}
	edge, err = h.ControlSyncRelay.ObserveEdge(r.Context(), instanceID)
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("relay registrar unavailable"))
		return
	}

	conn, err := relayUpgrader.Upgrade(w, r, http.Header{"X-MindFS-Relay-Name": []string{name}})
	if err != nil {
		// Upgrade 已经把响应写掉了（它自己会回 400），这里只能放弃。
		return
	}
	defer conn.Close()

	pool, err := h.ControlSyncRelay.Hub().Register(instanceID, name, 0)
	if err != nil {
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "register failed"),
			time.Now().Add(2*time.Second))
		return
	}
	defer h.ControlSyncRelay.Hub().Unregister(pool)
	_ = edge

	h.ControlSyncRelay.OnTunnelUp(r.Context(), instanceID, name, publicBaseFromRequest(r))
	defer h.ControlSyncRelay.OnTunnelDown(r.Context(), instanceID)

	// 控制通道只跑两类帧：心跳与踢人通知。数据一律走 channel 端点。
	h.serveControlChannel(r.Context(), conn, instanceID)
}

// serveControlChannel 读心跳、写踢人通知，直到任一端断开。
//
// 读必须真的发生。gorilla 只有在实际读连接时才处理对端的 ping/pong 与关闭帧，
// 也只有读才能发现「对端已经没了」。原先这里只写不读，边控端正常下线（TCP 关闭）
// 时主控要等到下一次写 ping 失败才知道 —— 离线检测被推迟到 30 秒以后，
// 这段时间里 /relay/<name>/ 会借到已经死掉的通道，用户看到的是超时而不是 503。
func (h *HTTPHandler) serveControlChannel(ctx context.Context, conn *websocket.Conn, instanceID string) {
	done := make(chan struct{})
	defer close(done)
	// ctx 结束时把连接关掉，唤醒阻塞中的 ReadMessage。
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()

	go func() {
		ping := time.NewTicker(controlPingInterval)
		defer ping.Stop()
		for {
			select {
			case <-done:
				return
			case <-ping.C:
				// 心跳用 WS 协议级 ping：它不会在应用层留下一条需要解析的消息，
				// 对端由 gorilla 的默认 handler 自动回 pong。
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
					_ = conn.Close()
					return
				}
			}
		}
	}()

	// 任何上行帧都算活着（边控端默认每 30 秒发一次应用层 ping），
	// 连续 controlReadTimeout 完全没有声音就判定隧道已死。
	_ = conn.SetReadDeadline(time.Now().Add(controlReadTimeout))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(controlReadTimeout))
	}
}

// handleRelayChannel 接收一条数据通道。
func (h *HTTPHandler) handleRelayChannel(w http.ResponseWriter, r *http.Request) {
	if h.ControlSyncRelay == nil || h.ControlSyncRelay.Hub() == nil {
		respondError(w, http.StatusNotFound, errInvalidRequest("relay not configured"))
		return
	}
	if err := verifyRelaySignature(r, h.ControlSyncToken, h.syncNonceCache()); err != nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="control-sync"`)
		respondError(w, http.StatusUnauthorized, errControlSyncUnauthorized)
		return
	}
	instanceID := strings.TrimSpace(r.Header.Get(controlsync.SyncInstanceHeader))
	if instanceID == "" {
		instanceID = strings.TrimSpace(r.URL.Query().Get("instance_id"))
	}
	if instanceID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("instance_id required"))
		return
	}
	if !h.ControlSyncRelay.Hub().Online(instanceID) {
		// 没有控制通道的数据通道是无主连接：收下也没人借，只会占着内存。
		respondError(w, http.StatusConflict, errRelayOffline)
		return
	}
	// 池满必须在**升级之前**拒。先交出 101 再 close，边控端看到的是一条刚建好
	// 就被掐断的连接，它分不清「主控没有空位」和「网络抖了一下」，于是立刻重拨；
	// 池子长期满着的时候这就是每秒上千次带签名的跨网握手（实测 460→833 次/秒）。
	// 回 429 是个明确的、可退避的信号：gorilla 的 Dial 会把它变成
	// ErrBadHandshake + resp.StatusCode，边控端据此指数退避。
	if !h.ControlSyncRelay.Hub().HasRoom(instanceID) {
		respondError(w, http.StatusTooManyRequests, errInvalidRequest("relay pool full"))
		return
	}
	conn, err := relayUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	// HasRoom 与 Attach 之间还有极窄的争抢窗口。真撞上了只能关掉，但对端已经
	// 拿到 101，所以这条通道算「没服务过请求」——边控端会退避后再拨，不会再热循环。
	if !h.ControlSyncRelay.Hub().Attach(instanceID, newWSConnFromUpgrade(conn)) {
		_ = conn.Close()
		return
	}
	// 通道交进池子之后所有权就转移了：这里不能 defer Close，
	// 否则请求函数一返回就会把刚进池子的连接关掉。
}

// publicBaseFromRequest 从拨进来的那条请求推出主控的对外基址。
//
// 边控端是自己拿 primary_url 拨的，所以 scheme 与 Host 就是它眼里主控的样子 ——
// 比让用户再填一遍「我的公网域名」可靠。反向代理改写 Host 时才需要配置兜底。
//
// 部署前缀取自本进程的编译期常量而不是请求路径：请求路径已经被 StripDeployPrefix
// 剥过了，直接从 URL 里读会拿不到。
func publicBaseFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	host := strings.TrimSpace(r.Host)
	if host == "" {
		return ""
	}
	return schemeOf(r) + "://" + host + deploy.NormalizedPrefix()
}

// verifyRelaySignature 与 requireSyncToken 同一套校验，区别只在签名覆盖的内容：// GET 没有 body，改用规范化后的查询串。
//
// 查询串必须进签名：`name` 决定对外路径，`instance_id` 决定这是哪台机器。
// 如果它们可以随便改，一条合法连接就能冒充另一台。
func verifyRelaySignature(r *http.Request, token string, nonces *controlsync.NonceCache) error {
	q := r.URL.Query()
	// 排除客户端可随意附加的诊断参数，其余按 Encode() 的稳定顺序参与签名。
	q.Del("_")
	payload := []byte(q.Encode())
	return controlsync.VerifySyncRequest(r, payload, controlsync.SyncVerifyOptions{
		Token:  token,
		Nonces: nonces,
	})
}

// relayUpgrader 允许跨源：边控端是从另一台机器拨进来的，Origin 与 Host 必然不同。
// 认证靠签名头，不靠 Origin —— 后者是浏览器概念，对非浏览器客户端没有意义。
var relayUpgrader = websocket.Upgrader{
	CheckOrigin:     func(*http.Request) bool { return true },
	ReadBufferSize:  32 * 1024,
	WriteBufferSize: 32 * 1024,
}

const (
	// controlPingInterval 是主控向边控端发协议级 ping 的间隔。
	controlPingInterval = 30 * time.Second
	// controlReadTimeout 是控制通道上「多久没听到任何上行就判定隧道已死」。
	// 边控端默认每 30 秒发一次应用层心跳，取三倍留出网络抖动余量。
	controlReadTimeout = 90 * time.Second
)

// newWSConnFromUpgrade 把升级后的连接包成 net.Conn。
//
// 包一层的原因：数据通道上跑的是 HTTP 字节流（可能再套一层 WS 升级），
// 上层只认 net.Conn，不关心底层是 TCP 还是 websocket。
func newWSConnFromUpgrade(conn *websocket.Conn) net.Conn {
	return controlsync.NewWSConn(conn)
}
