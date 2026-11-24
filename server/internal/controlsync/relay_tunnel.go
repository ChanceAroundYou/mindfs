package controlsync

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// TunnelClient 是边控端那一半：主动拨向主控，把自己的 HTTP 服务挂上去。
//
// 它由两部分组成：
//
//   - 一条**控制通道**（长连接）：注册、心跳、收主控的指令（踢人 / 补通道）；
//   - 若干**数据通道**：每拨一条，主控就用它服务一次请求；用完了再拨。
//
// 全部由边控端主动拨出，所以 NAT / 防火墙后面也能用 —— 这正是这个设计存在的
// 理由：办公室里的机器没有可暴露的域名和端口。
//
// 数据通道接进来的请求由一个**一次性 listener** 喂给本机原来那个 http.Handler。
// 于是 /api/*、/ws、上传下载、WS 会话流全都不用改，边控端就是把自己「原样」
// 挂到了主控的路径下面。
type TunnelClient struct {
	opts    TunnelOptions
	handler http.Handler

	mu      sync.Mutex
	running bool
	stop    context.CancelFunc
	wg      sync.WaitGroup
	status  TunnelStatus
}

// TunnelOptions 是边控端的隧道参数。
type TunnelOptions struct {
	// PrimaryURL 是主控基址（含部署前缀），例如 https://home.example.com/mindfs。
	PrimaryURL string
	// Token 与同步通道共用。隧道搬的是同一台机器的完整 HTTP 面，
	// 另起一套凭据只会多一个要被保护的东西。
	Token string
	// InstanceID 是边控端自己的标识（主控按它管连接）。
	InstanceID string
	// Name 是期望的对外名字（候选名）。主控可能改它，最终值回写在 TunnelStatus.Name。
	Name string
	// CAFile 可选：自签 / 内网 CA。
	CAFile string
	// IdleChannels 是预热的空闲数据通道数。
	IdleChannels int
	// MaxChannels 是允许同时存在的数据通道上限（含正在服务的）。
	MaxChannels int
	// ReconnectMin / ReconnectMax 是指数退避的上下限。
	ReconnectMin time.Duration
	ReconnectMax time.Duration
	// HeartbeatInterval 是控制通道心跳间隔。
	HeartbeatInterval time.Duration
	// OnError 只在状态真的变了时报告，避免心跳刷屏。
	OnError func(error)
}

// TunnelStatus 是隧道的当前观测值，供 /api/control-sync/status 与诊断用。
type TunnelStatus struct {
	Running     bool   `json:"running"`
	Name        string `json:"name"`
	Connected   bool   `json:"connected"`
	IdleChans   int    `json:"idle_channels"`
	LastErrorAt string `json:"last_error_at,omitempty"`
	LastError   string `json:"last_error,omitempty"`
}

const (
	defaultTunnelIdleChannels = 8
	defaultTunnelMaxChannels  = 32
	defaultTunnelReconnectMin = time.Second
	defaultTunnelReconnectMax = 30 * time.Second
	defaultTunnelHeartbeat    = 30 * time.Second
)

// NewTunnelClient 校验配置并构造。handler 就是本机原来的那个 HTTP handler。
func NewTunnelClient(opts TunnelOptions, handler http.Handler) (*TunnelClient, error) {
	raw := strings.TrimSpace(opts.PrimaryURL)
	if raw == "" {
		return nil, errors.New("controlsync: tunnel primary_url required")
	}
	base, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("controlsync: invalid tunnel primary_url: %w", err)
	}
	if base.Scheme != "https" {
		// 与同步通道同一条规矩：隧道跑的是整台机器的 API（含口令 hash 能到达的
		// 那些端点），明文等于把它摊在网络上。
		return nil, fmt.Errorf("controlsync: tunnel primary_url must be https, got %q", base.Scheme)
	}
	if base.Host == "" {
		return nil, errors.New("controlsync: tunnel primary_url has no host")
	}
	if strings.TrimSpace(opts.Token) == "" {
		return nil, errors.New("controlsync: tunnel token required")
	}
	if strings.TrimSpace(opts.InstanceID) == "" {
		return nil, errors.New("controlsync: tunnel instance id required")
	}
	if handler == nil {
		return nil, errors.New("controlsync: tunnel handler required")
	}
	if opts.IdleChannels <= 0 {
		opts.IdleChannels = defaultTunnelIdleChannels
	}
	if opts.MaxChannels < opts.IdleChannels {
		opts.MaxChannels = defaultTunnelMaxChannels
	}
	if opts.ReconnectMin <= 0 {
		opts.ReconnectMin = defaultTunnelReconnectMin
	}
	if opts.ReconnectMax < opts.ReconnectMin {
		opts.ReconnectMax = defaultTunnelReconnectMax
	}
	if opts.HeartbeatInterval <= 0 {
		opts.HeartbeatInterval = defaultTunnelHeartbeat
	}
	opts.PrimaryURL = raw
	return &TunnelClient{opts: opts, handler: handler}, nil
}

// Status 返回当前观测值。
func (c *TunnelClient) Status() TunnelStatus {
	if c == nil {
		return TunnelStatus{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

// Run 跑到 ctx 结束。控制通道断开后按退避重连，永不因单次失败退出。
func (c *TunnelClient) Run(ctx context.Context) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	c.stop = cancel
	c.running = true
	c.status.Running = true
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.running = false
		c.status.Running = false
		c.status.Connected = false
		c.mu.Unlock()
		cancel()
		c.wg.Wait()
	}()

	backoff := c.opts.ReconnectMin
	for {
		if runCtx.Err() != nil {
			return
		}
		err := c.runOnce(runCtx)
		if runCtx.Err() != nil {
			return
		}
		c.report(err)
		// 成功连上过就重置退避：一次正常的会话结束后不该等满 30 秒。
		if err == nil {
			backoff = c.opts.ReconnectMin
		}
		select {
		case <-runCtx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < c.opts.ReconnectMax {
			backoff *= 2
			if backoff > c.opts.ReconnectMax {
				backoff = c.opts.ReconnectMax
			}
		}
	}
}

// runOnce 建一条控制通道并保持到它断开。返回 nil 表示对端正常关闭。
func (c *TunnelClient) runOnce(ctx context.Context) error {
	conn, name, err := c.dialTunnel(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	c.mu.Lock()
	c.status.Name = name
	c.status.Connected = true
	c.mu.Unlock()
	relayLogf("tunnel connected name=%s primary=%s", name, c.opts.PrimaryURL)

	sessionCtx, sessionCancel := context.WithCancel(ctx)
	defer sessionCancel()

	// 先补满预热通道，再进心跳循环。
	c.spawnChannels(sessionCtx, c.opts.IdleChannels)

	errCh := make(chan error, 2)
	go func() { errCh <- c.readControl(sessionCtx, conn) }()
	go func() { errCh <- c.heartbeat(sessionCtx, conn) }()

	err = <-errCh
	sessionCancel()
	c.mu.Lock()
	c.status.Connected = false
	c.mu.Unlock()
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	return err
}

// dialTunnel 用与同步通道相同的签名方式拨控制通道。
//
// 签名覆盖 timestamp + nonce + 查询串（GET 没有 body）。这里复用 VerifySyncRequest
// 的实现：主控那边对隧道端点和 push 端点跑的是同一套校验，不新增第二套凭据。
func (c *TunnelClient) dialTunnel(ctx context.Context) (*websocket.Conn, string, error) {
	base, err := url.Parse(c.opts.PrimaryURL)
	if err != nil {
		return nil, "", err
	}
	target := *base
	target.Path = joinSyncPath(base.Path, "/api/control-sync/relay/tunnel")
	q := target.Query()
	q.Set("name", c.opts.Name)
	q.Set("instance_id", c.opts.InstanceID)
	target.RawQuery = q.Encode()

	header := http.Header{}
	req := &http.Request{Method: http.MethodGet, URL: &target, Header: header}
	if err := SignSyncRequest(req, c.opts.Token, c.opts.InstanceID, []byte(target.RawQuery), time.Now()); err != nil {
		return nil, "", err
	}
	dialer, err := c.websocketDialer(base)
	if err != nil {
		return nil, "", err
	}
	conn, resp, err := dialer.DialContext(ctx, target.String(), header)
	if err != nil {
		if resp != nil {
			return nil, "", fmt.Errorf("controlsync: tunnel dial: %w (status %d)", err, resp.StatusCode)
		}
		return nil, "", fmt.Errorf("controlsync: tunnel dial: %w", err)
	}
	// 名字由主控分配并在握手响应头里回传：其他设备同步到的节点地址就带着它，
	// 所以边控端必须用主控给的这一份，不能自己再算一遍。
	name := strings.TrimSpace(resp.Header.Get("X-MindFS-Relay-Name"))
	if name == "" {
		name = SlugRelayName(c.opts.Name)
	}
	return conn, name, nil
}

// websocketDialer 用与同步通道一致的 TLS 信任链：系统根 + 可选 ca_file，
// 没有 insecure 开关。
func (c *TunnelClient) websocketDialer(base *url.URL) (*websocket.Dialer, error) {
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if strings.TrimSpace(c.opts.CAFile) != "" {
		pool := x509.NewCertPool()
		pem, err := os.ReadFile(c.opts.CAFile)
		if err != nil {
			return nil, err
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("controlsync: ca_file %q has no certificates", c.opts.CAFile)
		}
		tlsCfg.RootCAs = pool
	}
	return &websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 15 * time.Second,
		TLSClientConfig:  tlsCfg,
		// 大数据通道与 WS 会话流都跑在这上面，帧上限不能卡住一次大块写入。
		ReadBufferSize:  32 * 1024,
		WriteBufferSize: 32 * 1024,
	}, nil
}

// readControl 处理控制通道上的上行帧（心跳回执、踢人、补充通道指令）。
func (c *TunnelClient) readControl(ctx context.Context, conn *websocket.Conn) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		_, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		msg := parseControlFrame(data)
		switch msg.Type {
		case controlFrameRevoked:
			// 被踢：立刻停掉所有通道。返回错误让退避循环重连时会拿到 403。
			return ErrEdgeRevoked
		case controlFramePing:
			if err := writeControlFrame(conn, controlFrame{Type: controlFramePong}); err != nil {
				return err
			}
		}
	}
}

// heartbeat 定时发 ping，写失败即断。
func (c *TunnelClient) heartbeat(ctx context.Context, conn *websocket.Conn) error {
	ticker := time.NewTicker(c.opts.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := writeControlFrame(conn, controlFrame{Type: controlFramePing}); err != nil {
				return err
			}
		}
	}
}

// spawnChannels 拨 n 条数据通道。每条都是「拨出去 → 交给主控 → 一直服务到对端关闭」。
//
// 这里不维护本地池：主控那边是唯一知道「什么时候需要一条新通道」的一方
// （它按请求来借），所以边控端只负责在通道被用完之后补一条。
func (c *TunnelClient) spawnChannels(ctx context.Context, n int) {
	for i := 0; i < n; i++ {
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			c.serveOneChannel(ctx)
		}()
	}
}

// serveOneChannel 拨一条数据通道，把它交给主控，然后等它被用掉或对端关闭。
func (c *TunnelClient) serveOneChannel(ctx context.Context) {
	conn, _, err := c.dialChannel(ctx)
	if err != nil {
		if ctx.Err() == nil {
			c.report(err)
		}
		return
	}
	defer conn.Close()

	// 边控端这边把隧道回的字节当成一次 HTTP 连接的输入：一次性 listener 交出
	// 这一条连接，http.Server 用本机原来的 handler 服务它，服务完就退出。
	ln := &oneShotListener{conn: NewWSConn(conn), done: make(chan struct{})}
	srv := &http.Server{
		Handler:           c.handler,
		ReadHeaderTimeout: 30 * time.Second,
	}
	go func() {
		select {
		case <-ctx.Done():
			_ = srv.Close()
		case <-ln.done:
		}
	}()
	// Serve 在这条连接结束后返回 ErrServerClosed 或 nil；错误只影响这一条通道。
	_ = srv.Serve(ln)
}

func (c *TunnelClient) dialChannel(ctx context.Context) (*websocket.Conn, *http.Response, error) {
	base, err := url.Parse(c.opts.PrimaryURL)
	if err != nil {
		return nil, nil, err
	}
	target := *base
	target.Path = joinSyncPath(base.Path, "/api/control-sync/relay/channel")
	q := target.Query()
	q.Set("instance_id", c.opts.InstanceID)
	target.RawQuery = q.Encode()

	header := http.Header{}
	req := &http.Request{Method: http.MethodGet, URL: &target, Header: header}
	if err := SignSyncRequest(req, c.opts.Token, c.opts.InstanceID, []byte(target.RawQuery), time.Now()); err != nil {
		return nil, nil, err
	}
	dialer, err := c.websocketDialer(base)
	if err != nil {
		return nil, nil, err
	}
	return dialer.DialContext(ctx, target.String(), header)
}

func (c *TunnelClient) report(err error) {
	if err == nil || c.opts.OnError == nil {
		return
	}
	c.mu.Lock()
	c.status.LastError = err.Error()
	c.status.LastErrorAt = time.Now().UTC().Format(time.RFC3339)
	c.mu.Unlock()
	c.opts.OnError(err)
}

// oneShotListener 只交出一条连接，之后 Serve 立刻返回。
//
// 这是把「一条现成连接」接进 net/http 的最小代价：不需要伪造一整个 TCP 监听。
type oneShotListener struct {
	conn net.Conn
	once sync.Once
	done chan struct{}
}

func (l *oneShotListener) Accept() (net.Conn, error) {
	var out net.Conn
	taken := false
	l.once.Do(func() {
		out = l.conn
		taken = true
	})
	if taken {
		return out, nil
	}
	// 第二次及以后：没有更多连接，等 done 被关闭。
	<-l.done
	return nil, net.ErrClosed
}

func (l *oneShotListener) Close() error {
	select {
	case <-l.done:
	default:
		close(l.done)
	}
	return nil
}

// Addr 只用于日志；隧道没有真实监听地址。
func (l *oneShotListener) Addr() net.Addr { return tunnelAddr{} }

type tunnelAddr struct{}

func (tunnelAddr) Network() string { return "relay" }
func (tunnelAddr) String() string  { return "relay" }

// 控制帧：只有 type，没有载荷。真正的分配结果走握手响应头，避免两套真相。
const (
	controlFramePing    = "ping"
	controlFramePong    = "pong"
	controlFrameRevoked = "revoked"
)

type controlFrame struct {
	Type string `json:"type"`
}

func parseControlFrame(data []byte) controlFrame {
	var f controlFrame
	_ = json.Unmarshal(data, &f)
	return f
}

func writeControlFrame(conn *websocket.Conn, f controlFrame) error {
	return conn.WriteJSON(f)
}
