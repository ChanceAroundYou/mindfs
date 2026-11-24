package api

// 数据通道容量信号：池满必须在升 WS 之前拒。
//
// 2026-11-25 的现场：主控先 upgrade 再 Attach，满了就静默 close。边控端看到的
// 是一条「刚建好就被掐断」的连接，分辨不出「没空位」和「网络抖了」，于是立刻
// 重拨 —— 实测 460→833 次/秒、每次都是一次带签名的跨网 TLS 握手，而主控池子
// 从头到尾是满的。
//
// 这里钉住的是**协议信号**：池满 = 429（可退避），不是 101 + close。

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"mindfs/server/internal/controlsync"
)

const relayCapacityToken = "relay-channel-capacity-token"

// signedChannelRequest 造一条签名正确的数据通道请求（查询串参与签名）。
func signedChannelRequest(t *testing.T, instanceID, rawQuery string) *http.Request {
	t.Helper()
	target := "/api/control-sync/relay/channel"
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	r := httptest.NewRequest(http.MethodGet, target, nil)
	r.Header.Set(controlsync.SyncInstanceHeader, instanceID)
	q := r.URL.Query()
	q.Del("_")
	if err := controlsync.SignSyncRequest(r, relayCapacityToken, "", []byte(q.Encode()), time.Now()); err != nil {
		t.Fatalf("签名: %v", err)
	}
	return r
}

func channelHandler(hub *controlsync.RelayHub) *HTTPHandler {
	return &HTTPHandler{
		ControlSyncToken: relayCapacityToken,
		ControlSyncRelay: &testRelayService{hub: hub},
	}
}

// 池满时必须在升级前回 429。
//
// 回 101 再关闭的话，边控端会把「主控没空位」当成网络故障，无退避地重拨。
func TestRelayChannelFullPoolIs429NotUpgrade(t *testing.T) {
	hub := controlsync.NewRelayHub()
	if _, err := hub.Register("hasee", "hasee", 1); err != nil {
		t.Fatalf("register: %v", err)
	}
	if !hub.Attach("hasee", &relayTestConn{}) {
		t.Fatal("第一条通道应被收下（否则这个用例测不到满池）")
	}

	rec := httptest.NewRecorder()
	channelHandler(hub).handleRelayChannel(rec, signedChannelRequest(t, "hasee", "instance_id=hasee"))

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("满池回了 %d，want 429（101 会让边控端无退避地热循环）", rec.Code)
	}
	if upgrade := rec.Header().Get("Upgrade"); upgrade != "" {
		t.Fatalf("满池却发了升级响应头 Upgrade=%q", upgrade)
	}
}

// 池里有空位时不能回 429。
//
// httptest.ResponseRecorder 不支持 Hijack，所以升级本身必然失败（gorilla 回 500）；
// 这里要证明的是「容量检查放行了」，不是「升级成功」—— 后者由 e2e 覆盖。
func TestRelayChannelWithRoomPassesCapacityCheck(t *testing.T) {
	hub := controlsync.NewRelayHub()
	if _, err := hub.Register("hasee", "hasee", 2); err != nil {
		t.Fatalf("register: %v", err)
	}

	rec := httptest.NewRecorder()
	channelHandler(hub).handleRelayChannel(rec, signedChannelRequest(t, "hasee", "instance_id=hasee"))

	if rec.Code == http.StatusTooManyRequests {
		t.Fatal("池子里有两个空位却回了 429 —— 边控端会以为满了，池子永远补不满")
	}
}

// 没有控制通道的数据通道要回 409，而不是 429：两者的处理完全不同
// （前者是「这台机器根本没上线」，后者是「通道够了，别拨了」）。
func TestRelayChannelNoTunnelIs409(t *testing.T) {
	hub := controlsync.NewRelayHub()
	rec := httptest.NewRecorder()
	channelHandler(hub).handleRelayChannel(rec, signedChannelRequest(t, "hasee", "instance_id=hasee"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("code = %d，want 409", rec.Code)
	}
}

// relayTestConn 是占位 net.Conn：hub 只关心「有没有一条连接」。
type relayTestConn struct{}

func (c *relayTestConn) Read([]byte) (int, error)         { return 0, nil }
func (c *relayTestConn) Write(p []byte) (int, error)      { return len(p), nil }
func (c *relayTestConn) Close() error                     { return nil }
func (c *relayTestConn) LocalAddr() net.Addr              { return relayTestAddr{} }
func (c *relayTestConn) RemoteAddr() net.Addr             { return relayTestAddr{} }
func (c *relayTestConn) SetDeadline(time.Time) error      { return nil }
func (c *relayTestConn) SetReadDeadline(time.Time) error  { return nil }
func (c *relayTestConn) SetWriteDeadline(time.Time) error { return nil }

type relayTestAddr struct{}

func (relayTestAddr) Network() string { return "relay-test" }
func (relayTestAddr) String() string  { return "relay-test" }
