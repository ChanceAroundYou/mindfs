package api

// 端到端：真实 TLS + 真实 gorilla WS + 真实 httputil.ReverseProxy，
// 一台上游当「主控」，一条边控端隧道拨过去，再从主控的 /relay/<name>/ 取数据。
//
// 这条测试的意义在于钉住**协议形状**：单元测试各自证明了自己那一块对，
// 但只有真正跑一遍才能发现「签名覆盖的查询串与服务端校验的那一串不一致」
// 「数据通道的 wsConn 包装毁了 101 后续字节」「剥离前缀后上游路径多一层」这类
// 只有在边界处才暴露的问题。

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mindfs/server/internal/controlsync"
	"mindfs/server/internal/preferences"
)

// edgeHTTPFace 是「边控端本机」的 HTTP 面：只要有一个能区分出「确实到了这台机器」
// 的端点就够了。真实部署里它就是边控端的完整 mux。
func edgeHTTPFace(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/whoami", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"edge":  "hasee",
			"path":  r.URL.Path,
			"query": r.URL.RawQuery,
		})
	})
	// 大块响应：验证 wsConn 的字节流拼接能承载跨帧的响应体。
	mux.HandleFunc("/api/blob", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, 256*1024))
	})
	return mux
}

// 主控侧：Store + Hub + relayService 的最小组合，只装配本测试需要的部分。
func newPrimaryRelay(t *testing.T, token string) (*HTTPHandler, *controlsync.RelayHub, *httptest.Server) {
	t.Helper()
	store, err := controlsync.Open(filepath.Join(t.TempDir(), "control_sync.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	hub := controlsync.NewRelayHub()
	svc := &testRelayService{store: store, hub: hub}

	prefs, err := preferences.NewStore()
	if err != nil {
		t.Fatalf("prefs: %v", err)
	}
	h := &HTTPHandler{
		AppContext:       &AppContext{Prefs: prefs},
		ControlSyncToken: token,
		ControlSyncRelay: svc,
	}
	mux := http.NewServeMux()
	mux.Handle("/", h.Routes())
	mux.Handle("/relay/", NewRelayHandler(svc))
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	return h, hub, srv
}

// testRelayService 复刻 relayService 的协议相关部分（不动 nodes/store 的业务部分）。
// 主控侧 Hub().Register 由 handleRelayTunnel 在控制通道拨通时调用，不需要单独方法。
type testRelayService struct {
	store   *controlsync.Store
	hub     *controlsync.RelayHub
	base    string
	lastPub string
}

func (s *testRelayService) Hub() *controlsync.RelayHub { return s.hub }

func (s *testRelayService) ObserveEdge(ctx context.Context, id string) (controlsync.EdgeInfo, error) {
	return s.store.ObserveEdge(ctx, id, time.Now())
}

func (s *testRelayService) AllocateName(ctx context.Context, id, candidate string) (string, error) {
	edge, err := s.store.EdgeByID(ctx, id)
	if err != nil {
		return "", err
	}
	taken, err := s.store.RelayNames(ctx)
	if err != nil {
		return "", err
	}
	name := controlsync.AssignRelayName(edge.RelayName, candidate, id, taken)
	if err := s.store.SetRelayName(ctx, id, name); err != nil {
		return "", err
	}
	return name, nil
}

func (s *testRelayService) OnTunnelUp(ctx context.Context, id, name, base string) {
	s.base, s.lastPub = base, name
}

func (s *testRelayService) OnTunnelDown(ctx context.Context, id string) {}

func (s *testRelayService) EdgeByRelayName(ctx context.Context, name string) (controlsync.EdgeInfo, error) {
	return s.store.EdgeByRelayName(ctx, name)
}

func (s *testRelayService) Acquire(ctx context.Context, id string) (net.Conn, error) {
	return s.hub.Acquire(ctx, id)
}
func (s *testRelayService) Online(id string) bool { return s.hub.Online(id) }

// 端到端主线：边控端拨进来 → 主控分配名字 → 主控 /relay/<name>/ 取回边控端的数据。
func TestRelayEndToEndOverTLS(t *testing.T) {
	const token = "relay-e2e-token"
	_, hub, srv := newPrimaryRelay(t, token)

	// 边控端信任测试证书。
	caFile := writeTestCA(t, srv)

	client, err := controlsync.NewTunnelClient(controlsync.TunnelOptions{
		PrimaryURL:        srv.URL, // httptest.NewTLSServer 给的就是 https://
		Token:             token,
		InstanceID:        "hasee",
		Name:              "hasee",
		IdleChannels:      2,
		MaxChannels:       4,
		CAFile:            caFile,
		HeartbeatInterval: 200 * time.Millisecond,
	}, edgeHTTPFace(t))
	if err != nil {
		t.Fatalf("NewTunnelClient: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Run(ctx)

	// 等隧道在线（主控 hub 认识这台机器）。
	waitOnline(t, hub, "hasee", 5*time.Second)

	// 名字由主控分配后写进 hub 与 store。测试里直接走分配逻辑再注册：
	// 真实流程里这两步发生在 handleRelayTunnel 里。
	// 上面 Run 已经拨通并登记了控制通道；查询它拿到的名字。
	status := client.Status()
	if status.Name == "" {
		t.Fatalf("隧道状态里没有名字：%+v", status)
	}

	// 通过主控的 /relay/<name>/ 取边控端的数据。
	// 用不校验证书的客户端即可：这里测的是路径与握手，不是证书信任。
	body := getViaRelay(t, srv.URL+"/relay/"+status.Name+"/api/whoami?root=demo", 10*time.Second)
	var got map[string]string
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("响应不是 JSON: %v（body=%q）", err, string(body))
	}
	if got["edge"] != "hasee" {
		t.Fatalf("响应不是来自边控端：%+v", got)
	}
	if got["path"] != "/api/whoami" {
		t.Fatalf("上游收到的路径 = %q，want /api/whoami（relay 前缀必须被剥掉）", got["path"])
	}
	if got["query"] != "root=demo" {
		t.Fatalf("上游收到的查询串 = %q，want root=demo", got["query"])
	}
}

// 大块响应穿透隧道：WS 单帧上限与 wsConn 的拼接必须能吃下 256KB 响应体。
func TestRelayEndToEndLargeBody(t *testing.T) {
	const token = "relay-e2e-large"
	_, hub, srv := newPrimaryRelay(t, token)
	client, err := controlsync.NewTunnelClient(controlsync.TunnelOptions{
		PrimaryURL:        srv.URL,
		Token:             token,
		InstanceID:        "hasee",
		Name:              "hasee",
		CAFile:            writeTestCA(t, srv),
		HeartbeatInterval: 200 * time.Millisecond,
		OnError:           func(err error) { t.Logf("[tunnel] %v", err) },
	}, edgeHTTPFace(t))
	if err != nil {
		t.Fatalf("NewTunnelClient: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Run(ctx)
	waitOnline(t, hub, "hasee", 5*time.Second)

	name := client.Status().Name
	body := getViaRelay(t, srv.URL+"/relay/"+name+"/api/blob", 10*time.Second)
	if len(body) != 256*1024 {
		t.Fatalf("大块响应长度 = %d，want 262144", len(body))
	}
}

// 隧道断开后，主控的 /relay/<name>/ 必须立刻变 503，而不是挂住等超时。
func TestRelayEndToEndOfflineAfterDisconnect(t *testing.T) {
	const token = "relay-e2e-off"
	_, hub, srv := newPrimaryRelay(t, token)
	client, err := controlsync.NewTunnelClient(controlsync.TunnelOptions{
		PrimaryURL:        srv.URL,
		Token:             token,
		InstanceID:        "hasee",
		Name:              "hasee",
		CAFile:            writeTestCA(t, srv),
		HeartbeatInterval: 200 * time.Millisecond,
		OnError:           func(err error) { t.Logf("[tunnel] %v", err) },
	}, edgeHTTPFace(t))
	if err != nil {
		t.Fatalf("NewTunnelClient: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go client.Run(ctx)
	waitOnline(t, hub, "hasee", 5*time.Second)
	name := client.Status().Name

	cancel()
	deadline := time.Now().Add(5 * time.Second)
	for hub.Online("hasee") {
		if time.Now().After(deadline) {
			t.Fatal("取消后主控仍认为隧道在线")
		}
		time.Sleep(20 * time.Millisecond)
	}

	resp, err := insecureClient().Get(srv.URL + "/relay/" + name + "/api/whoami")
	if err != nil {
		t.Fatalf("请求 relay: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("离线后状态码 = %d，want 503", resp.StatusCode)
	}
}

func waitOnline(t *testing.T, hub *controlsync.RelayHub, id string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !hub.Online(id) {
		if time.Now().After(deadline) {
			t.Fatalf("%s 在 %v 内没有上线", id, d)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func insecureClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // 测试证书
		},
	}
}

func getViaRelay(t *testing.T, url string, d time.Duration) []byte {
	t.Helper()
	client := insecureClient()
	client.Timeout = d
	// relay 是普通 HTTP GET，WS 升级由下游的 /ws 自行发起。
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读响应: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("relay 返回 %d：%s", resp.StatusCode, string(body))
	}
	return body
}

// writeTestCA 把 httptest 的自签证书写成 PEM，供边控端当 CAFile 信任。
func writeTestCA(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatalf("写 CA 文件: %v", err)
	}
	return path
}
