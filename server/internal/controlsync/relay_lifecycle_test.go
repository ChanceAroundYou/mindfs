package controlsync

// 数据通道生命周期的不变量。它们全是「只在运行期才暴露」的那一类：
// 单次请求永远成功，池子在若干次请求之后悄悄失效、或者反过来变成每秒上千次
// 重拨，而日志里什么都没有。
//
// 端到端（api/relay_e2e_test.go）能抓到症状，但抓到时不告诉你是哪一条破了。
// 这里把每件事各自钉住。

import (
	"bufio"
	"context"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// 一次性 listener 的第二次 Accept 必须立刻返回错误。
//
// 如果它阻塞等 Close，而 Close 只由 Serve 返回后才触发，两边互等：
// srv.Serve 永不返回 → 通道永不归还 → 池子被逐条吃空 → 第 N+1 个请求起
// 永久 502。这个死锁在单次请求的测试里完全不可见。
func TestOneShotListenerSecondAcceptReturnsImmediately(t *testing.T) {
	l := &oneShotListener{conn: &fakeConn{}}
	if _, err := l.Accept(); err != nil {
		t.Fatalf("第一次 Accept: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := l.Accept()
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("第二次 Accept 返回了 nil 连接")
		}
	case <-time.After(time.Second):
		t.Fatal("第二次 Accept 阻塞了 —— srv.Serve 将永不返回，数据通道不会被补充")
	}
}

// closeSignalConn 把「底层连接被关」翻译成一个可等待的信号。
//
// serveOneChannel 靠它等请求真正结束：srv.Serve 只负责把连接交出去，
// 它返回时响应可能还没写完，此时关掉隧道会截断响应。
func TestCloseSignalConnSignalsOnClose(t *testing.T) {
	inner := &fakeConn{}
	c := &closeSignalConn{Conn: inner, done: make(chan struct{})}
	select {
	case <-c.done:
		t.Fatal("还没关就发了信号")
	default:
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-c.done:
	case <-time.After(time.Second):
		t.Fatal("关掉连接后 done 没有关闭 —— serveOneChannel 会一直等下去")
	}
	if err := c.Close(); err != nil { // 二次关闭不能 panic
		t.Fatalf("二次 Close: %v", err)
	}
}

// 池子里的 worker 数取 min(IdleChannels, MaxChannels)：一条通道同一时刻只服务
// 一个请求，拨多于 MaxChannels 就绕过了上限。
func TestChannelPoolWorkerCountRespectsMax(t *testing.T) {
	for _, tc := range []struct {
		idle, max, want int
	}{
		{idle: 8, max: 32, want: 8},
		{idle: 8, max: 4, want: 4},
		{idle: 1, max: 1, want: 1},
	} {
		got := channelWorkers(tc.idle, tc.max)
		if got != tc.want {
			t.Fatalf("channelWorkers(%d,%d) = %d，want %d", tc.idle, tc.max, got, tc.want)
		}
	}
}

// 拨通道失败时必须把错误交回调用方，由 worker 循环退避：主控临时不可达时，
// 热循环会在一秒内打出上千次带签名的握手。
func TestServeOneChannelReturnsErrorOnDialFailure(t *testing.T) {
	// 指向一个必然拒绝的地址（TCP 端口 1）。
	c, err := NewTunnelClient(TunnelOptions{
		PrimaryURL:   "https://127.0.0.1:1",
		Token:        "tunnel-test-token-0123456789",
		InstanceID:   "hasee",
		ReconnectMin: 200 * time.Millisecond,
	}, http.NotFoundHandler())
	if err != nil {
		t.Fatalf("NewTunnelClient: %v", err)
	}
	start := time.Now()
	if err := c.serveOneChannel(context.Background()); err == nil {
		t.Fatal("拨号失败却返回了 nil —— worker 会当成正常结束，立刻重拨")
	}
	// 退避归 worker 循环管；这里如果自己睡，循环里的指数退避就再也长不起来。
	if elapsed := time.Since(start); elapsed > 150*time.Millisecond {
		t.Fatalf("serveOneChannel 自己睡了 %v —— 退避必须交给 worker 循环", elapsed)
	}
}

// 池子不能热循环：拨号一直失败时，500ms 里的重拨次数必须是个位数。
//
// 2026-11-25 的现场是 460→833 次/秒，因为 serveOneChannel 失败后立刻返回、
// worker 循环无条件重拨，中间没有任何等待。
func TestChannelPoolDoesNotHotLoopOnDialFailure(t *testing.T) {
	var attempts atomic.Int32
	c, err := NewTunnelClient(TunnelOptions{
		PrimaryURL:   "https://127.0.0.1:1",
		Token:        "tunnel-test-token-0123456789",
		InstanceID:   "hasee",
		IdleChannels: 1,
		MaxChannels:  1,
		ReconnectMin: 50 * time.Millisecond,
		OnError:      func(error) { attempts.Add(1) },
	}, http.NotFoundHandler())
	if err != nil {
		t.Fatalf("NewTunnelClient: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	c.runChannelPool(ctx)
	c.wg.Wait()

	if got := attempts.Load(); got > 8 {
		t.Fatalf("500ms 内拨了 %d 次 —— 没有退避，这就是那个握手风暴", got)
	}
	if got := attempts.Load(); got == 0 {
		t.Fatal("一次都没拨 —— 测试没走到拨号路径")
	}
}

// 退避序列必须翻倍并封顶：撞上「主控池子满了」（429）这种正常状态时，
// 固定间隔会让每条 worker 每秒都去撞一次门。
func TestNextChannelBackoffDoublesAndCaps(t *testing.T) {
	const min, max = time.Second, 5 * time.Second
	cur := min
	want := []time.Duration{2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second}
	for i, w := range want {
		cur = nextChannelBackoff(cur, min, max)
		if cur != w {
			t.Fatalf("第 %d 次退避 = %v，want %v", i+1, cur, w)
		}
	}
	if got := nextChannelBackoff(0, min, max); got != 2*min {
		t.Fatalf("零值起步 = %v，want %v", got, 2*min)
	}
}

// 数据通道服务器的读超时必须保持关闭。
//
// 这条连接的正常状态就是「空闲地摆在主控池子里等请求」，可能等几分钟。
// 曾经照搬通用模板设了 30s：主控池里留下死连接，边控端每 30 秒自己掐死一条
// 健康通道再重拨，两边叠加成握手风暴。单测/短测都看不出来，因为它只在
// 「空闲超过 30 秒」时发生。
func TestChannelServerHasNoHeaderReadTimeout(t *testing.T) {
	srv := newChannelServer(http.NotFoundHandler())
	if srv.ReadHeaderTimeout != 0 {
		t.Fatalf("ReadHeaderTimeout=%v —— 空闲通道会在超时后被边控端自己杀掉", srv.ReadHeaderTimeout)
	}
	if srv.ReadTimeout != 0 || srv.WriteTimeout != 0 {
		t.Fatalf("ReadTimeout=%v WriteTimeout=%v —— 一次请求可能跑很久（大文件、WS 会话流）",
			srv.ReadTimeout, srv.WriteTimeout)
	}
}

// 主控建好通道又立刻收回时，这条通道也算「没跑起来」：worker 必须退避，
// 否则「主控池子满 → HasRoom 与 Attach 撞窗口 → 立刻关」会变成热循环。
func TestServeOneChannelReportsUnusedChannel(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		_ = conn.Close()
	}))
	defer srv.Close()

	c, err := NewTunnelClient(TunnelOptions{
		PrimaryURL: srv.URL,
		Token:      "tunnel-test-token-0123456789",
		InstanceID: "hasee",
		CAFile:     writeTunnelTestCA(t, srv),
	}, http.NotFoundHandler())
	if err != nil {
		t.Fatalf("NewTunnelClient: %v", err)
	}
	if err := c.serveOneChannel(context.Background()); !errors.Is(err, errChannelUnused) {
		t.Fatalf("err = %v，want errChannelUnused", err)
	}
}

// 服务过一条请求的通道是正常结束，不算失败：否则每服务一个请求都要退避一次。
//
// 方向是「假的边控端拨进来」：测试服务端冒充主控，升级之后往隧道里写一条 HTTP
// 请求，再从同一条连接上读回边控端 handler 的应答。
func TestServeOneChannelServedRequestIsNotAnError(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	served := make(chan struct{})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		// 隧道上跑的是 HTTP/1.1 字节流，但底层是 WS 消息：两边都必须用 wsConn
		// 包一层，直接写裸字节对面读不到（它等的是 WS 数据帧）。
		wconn := NewWSConn(ws)
		if _, err := wconn.Write([]byte("GET /probe HTTP/1.1\r\nHost: edge\r\n\r\n")); err != nil {
			return
		}
		resp, err := http.ReadResponse(bufio.NewReader(wconn), nil)
		if err != nil {
			return
		}
		body, err := io.ReadAll(resp.Body)
		if err == nil && resp.StatusCode == http.StatusOK && string(body) == "edge" {
			close(served)
		}
	}))
	defer srv.Close()

	c, err := NewTunnelClient(TunnelOptions{
		PrimaryURL: srv.URL,
		Token:      "tunnel-test-token-0123456789",
		InstanceID: "hasee",
		CAFile:     writeTunnelTestCA(t, srv),
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("edge"))
	}))
	if err != nil {
		t.Fatalf("NewTunnelClient: %v", err)
	}
	if err := c.serveOneChannel(context.Background()); err != nil {
		t.Fatalf("服务过请求的通道报了错：%v", err)
	}
	select {
	case <-served:
	case <-time.After(2 * time.Second):
		t.Fatal("通道上根本没服务过请求 —— 这个用例没测到「服务过」")
	}
}

func writeTunnelTestCA(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatalf("写 CA 文件: %v", err)
	}
	return path
}

// 拨数据通道被拒时，错误里必须带 HTTP 状态码。
//
// 429（主控池子满了）是正常状态，退避重试即可；403/409（被踢 / 没有控制通道）
// 是配置或状态问题。只报 gorilla 的 "bad handshake" 时两者在日志里完全一样，
// 排查时没有任何线索指向是哪一边。
func TestChannelDialErrorCarriesStatus(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c, err := NewTunnelClient(TunnelOptions{
		PrimaryURL: srv.URL,
		Token:      "tunnel-test-token-0123456789",
		InstanceID: "hasee",
		CAFile:     writeTunnelTestCA(t, srv),
	}, http.NotFoundHandler())
	if err != nil {
		t.Fatalf("NewTunnelClient: %v", err)
	}
	err = c.serveOneChannel(t.Context())
	if err == nil {
		t.Fatal("429 却拨成功了")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Fatalf("错误里没有状态码：%v", err)
	}
}
