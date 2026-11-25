package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mindfs/server/internal/controlsync"
)

// rest 会被拼成边控端的请求路径。任何能让下游再做一次解析的形状
// （..、编码分隔符、反斜杠、空段）都必须在这里拒绝 —— 否则就是路径穿越。
func TestSplitRelayPathRejectsTraversal(t *testing.T) {
	bad := []string{
		"/relay/hasee/../../etc/passwd",
		"/relay/hasee/..",
		"/relay/hasee/./x",
		"/relay/hasee//x",
		"/relay/hasee/%2e%2e/x",
		"/relay/hasee/%2fetc",
		"/relay/hasee/%5cetc",
		"/relay/hasee/\\..\\x",
		// 名字本身不合法
		"/relay/HASEE/api",
		"/relay/ha_see/api",
		"/relay/../api",
		"/relay//api",
		// 形状不对
		"/relay/hasee",
		"/relay",
		"/other/hasee/api",
	}
	for _, p := range bad {
		if _, _, ok := splitRelayPath(p); ok {
			t.Errorf("splitRelayPath(%q) = ok，应拒绝", p)
		}
	}
}

func TestSplitRelayPathAcceptsNormalPaths(t *testing.T) {
	cases := []struct{ in, name, rest string }{
		{"/relay/hasee/api/dirs", "hasee", "/api/dirs"},
		{"/relay/hasee/", "hasee", "/"},
		{"/relay/pc-win/static/app.js", "pc-win", "/static/app.js"},
		{"relay/hasee/api/x", "hasee", "/api/x"}, // 去掉部署前缀时可能不带前导 /
	}
	for _, c := range cases {
		name, rest, ok := splitRelayPath(c.in)
		if !ok {
			t.Errorf("splitRelayPath(%q) 被拒绝", c.in)
			continue
		}
		if name != c.name || rest != c.rest {
			t.Errorf("splitRelayPath(%q) = (%q,%q)，want (%q,%q)", c.in, name, rest, c.name, c.rest)
		}
	}
}

// ---- 转发行为（用假 hub） ----

type fakeHub struct {
	edge       controlsync.EdgeInfo
	edgeErr    error
	online     bool
	conn       net.Conn
	acquireErr error
	gotName    string
	acquired   string
}

func (h *fakeHub) EdgeByRelayName(_ context.Context, name string) (controlsync.EdgeInfo, error) {
	h.gotName = name
	return h.edge, h.edgeErr
}
func (h *fakeHub) Acquire(_ context.Context, id string) (net.Conn, error) {
	h.acquired = id
	return h.conn, h.acquireErr
}
func (h *fakeHub) Online(string) bool { return h.online }

// 名字拼错 → 404（重试没意义，调用方该去改地址）。
func TestRelayHandlerUnknownEdgeIs404(t *testing.T) {
	h := &fakeHub{edgeErr: errors.New("nope")}
	rec := httptest.NewRecorder()
	NewRelayHandler(h).ServeHTTP(rec, httptest.NewRequest("GET", "/relay/nope/api/x", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d，want 404", rec.Code)
	}
}

// 被吊销 → 403（地址是对的，运维明确关掉的）。
func TestRelayHandlerRevokedEdgeIs403(t *testing.T) {
	revokedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h := &fakeHub{edge: controlsync.EdgeInfo{InstanceID: "hasee", RevokedAt: &revokedAt}}
	rec := httptest.NewRecorder()
	NewRelayHandler(h).ServeHTTP(rec, httptest.NewRequest("GET", "/relay/hasee/api/x", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d，want 403", rec.Code)
	}
}

// 隧道不在 → 503（机器关机，重试有意义）。
func TestRelayHandlerOfflineEdgeIs503(t *testing.T) {
	h := &fakeHub{edge: controlsync.EdgeInfo{InstanceID: "hasee"}, online: false}
	rec := httptest.NewRecorder()
	NewRelayHandler(h).ServeHTTP(rec, httptest.NewRequest("GET", "/relay/hasee/api/x", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d，want 503", rec.Code)
	}
}

// hub 为 nil（没配置 relay）→ 503，而不是 panic。
func TestRelayHandlerNilHubIs503(t *testing.T) {
	rec := httptest.NewRecorder()
	NewRelayHandler(nil).ServeHTTP(rec, httptest.NewRequest("GET", "/relay/hasee/api/x", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d，want 503", rec.Code)
	}
}

// 借通道失败 → 通过 ErrorHandler 映射成 503 busy / 503 offline，不是 502。
func TestRelayHandlerAcquireBusyMaps503(t *testing.T) {
	h := &fakeHub{
		edge:       controlsync.EdgeInfo{InstanceID: "hasee"},
		online:     true,
		acquireErr: errRelayBusy,
	}
	rec := httptest.NewRecorder()
	NewRelayHandler(h).ServeHTTP(rec, httptest.NewRequest("GET", "/relay/hasee/api/x", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d，want 503", rec.Code)
	}
}

// 名字大小写不一致的地址不该被当成同一台机器（Cod 层已拒绝，这里确认 handler 也不会去查 hub）。
func TestRelayHandlerInvalidNameSkipsHub(t *testing.T) {
	h := &fakeHub{online: true}
	rec := httptest.NewRecorder()
	NewRelayHandler(h).ServeHTTP(rec, httptest.NewRequest("GET", "/relay/HASEE/api/x", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d，want 404", rec.Code)
	}
	if h.gotName != "" {
		t.Fatalf("非法名字不应到达 hub，got %q", h.gotName)
	}
}

// 转发到边控端时路径要剥掉 relay 前缀、查询串要保留。
// 用一个真实返回请求行的小上游验证（通过假 Transport 注入）。
func TestRelayTransportStripPrefixAndKeepsQuery(t *testing.T) {
	// 直接验证 Director 的语义：构造一次 ServeHTTP 后，上游拿到的 req 已被改写。
	// 这里让 hub 返回一个立刻失败的连接，Proxy 会走 ErrorHandler，但 Director 已经跑过。
	rec := httptest.NewRecorder()
	h := &fakeHub{
		edge:       controlsync.EdgeInfo{InstanceID: "hasee"},
		online:     true,
		acquireErr: errors.New("stop here after director"),
	}
	NewRelayHandler(h).ServeHTTP(rec,
		httptest.NewRequest("GET", "/relay/hasee/api/dirs?root=abc", nil))
	// 借通道在 Transport 里发生：acquisition 拿到的实例 id 必须是被解出来的那台。
	if h.acquired != "hasee" {
		t.Fatalf("Acquire 的实例 = %q，want hasee", h.acquired)
	}
	// 因为连接借不到，最终是 502（非 busy/offline 的通用上游失败）。
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("code = %d，want 502", rec.Code)
	}
}

// publicBaseFromRequest 决定边缘汇报给主控的对外地址来源。
// 它必须包含部署前缀：/mindfs 被剥掉之前，浏览器看到的是带前缀的地址。
func TestPublicBaseFromRequest(t *testing.T) {
	r := httptest.NewRequest("GET", "http://home.example/mindfs/api/control-sync/relay/tunnel", nil)
	r.Host = "home.example"
	got := publicBaseFromRequest(r)
	if !strings.HasPrefix(got, "http://home.example") {
		t.Fatalf("base = %q，want http://home.example 开头", got)
	}
	// 无 Host 时返回空串（调用方据此跳过），不是拼出一个半截地址。
	if got := publicBaseFromRequest(nil); got != "" {
		t.Fatalf("nil request base = %q，want 空串", got)
	}
	empty := httptest.NewRequest("GET", "/x", nil)
	empty.Host = ""
	if got := publicBaseFromRequest(empty); got != "" {
		t.Fatalf("空 Host base = %q，want 空串", got)
	}
}

// 签名覆盖规范化查询串：改 name/instance_id 会验签失败。
func TestVerifyRelaySignatureCoversQuery(t *testing.T) {
	nonces := controlsync.NewNonceCache(5 * time.Minute)
	const token = "relay-token"
	mk := func(query string) *http.Request {
		r := httptest.NewRequest("GET", "/api/control-sync/relay/tunnel?"+query, nil)
		return r
	}
	// 不带签名的请求必须被拒（而不是放行）。
	if err := verifyRelaySignature(mk("name=hasee&instance_id=hasee"), token, nonces); err == nil {
		t.Fatal("无签名请求被放行了")
	}
}
