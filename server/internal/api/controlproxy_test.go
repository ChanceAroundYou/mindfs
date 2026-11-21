package api

import (
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// writeCACert 把 httptest TLS 服务的自签证书写成 PEM 文件，模拟配置里的 ca_file。
func writeCACert(t *testing.T, cert *x509.Certificate) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ca.pem")
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("写 ca_file: %v", err)
	}
	return path
}

// 在线时边缘的控制面请求必须原样落到主节点：方法、query、body、Content-Type 与
// 身份头逐项保留；本机其它头不外泄；响应状态码、头、体原样回传。
func TestControlProxyForwardsControlPlaneRequestVerbatim(t *testing.T) {
	type captured struct {
		method      string
		path        string
		query       string
		body        string
		contentType string
		cookie      string
		auth        string
		forwardedBy string
		localHeader string
	}
	var got captured
	primary := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = captured{
			method:      r.Method,
			path:        r.URL.Path,
			query:       r.URL.RawQuery,
			body:        string(body),
			contentType: r.Header.Get("Content-Type"),
			cookie:      r.Header.Get("Cookie"),
			auth:        r.Header.Get("Authorization"),
			forwardedBy: r.Header.Get("X-MindFS-Forwarded-By"),
			localHeader: r.Header.Get("X-Local-Secret"),
		}
		w.Header().Set("X-Primary-Echo", "yes")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer primary.Close()

	proxy, err := NewControlProxy(ControlProxyOptions{
		PrimaryURL:  primary.URL,
		CAFile:      writeCACert(t, primary.Certificate()),
		ForwardedBy: "pc",
	})
	if err != nil {
		t.Fatalf("NewControlProxy: %v", err)
	}
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/preferences/cors?v=2", strings.NewReader(`{"value":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", "mindfs_session=abc")
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("X-Local-Secret", "must-not-leak")
	proxy.Handler(next).ServeHTTP(rec, req)

	if called {
		t.Fatalf("控制面请求被本地处理了：在线时不得本地执行")
	}
	want := captured{
		method:      http.MethodPut,
		path:        "/api/preferences/cors",
		query:       "v=2",
		body:        `{"value":true}`,
		contentType: "application/json",
		cookie:      "mindfs_session=abc",
		auth:        "Bearer token",
		forwardedBy: "pc",
	}
	if got != want {
		t.Fatalf("主节点收到 %+v，期望 %+v", got, want)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("响应码 = %d，期望 201", rec.Code)
	}
	if got := rec.Header().Get("X-Primary-Echo"); got != "yes" {
		t.Fatalf("响应头没回传: X-Primary-Echo = %q", got)
	}
	if body := rec.Body.String(); body != `{"ok":true}` {
		t.Fatalf("响应体 = %q", body)
	}
}

// 主节点不可达：返回 503 primary_unreachable，且绝不在本地执行（否则就是双写）。
func TestControlProxyReturns503WithoutLocalFallback(t *testing.T) {
	proxy, err := NewControlProxy(ControlProxyOptions{
		PrimaryURL:  "https://127.0.0.1:1",
		ForwardedBy: "pc",
	})
	if err != nil {
		t.Fatalf("NewControlProxy: %v", err)
	}
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	rec := httptest.NewRecorder()
	proxy.Handler(next).ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/preferences/cors", nil))
	if called {
		t.Fatalf("转发失败后回落本地执行了：会与主节点双写")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("响应码 = %d，期望 503", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "primary_unreachable") {
		t.Fatalf("响应体 = %q，应说明 primary_unreachable", body)
	}
}

// 本机域与数据面不转发：web-push / app update / 节点自述 / 同步自身 / 会话。
func TestControlProxyLeavesLocalAndDataPlanePathsAlone(t *testing.T) {
	var primaryHits int
	primary := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits++
	}))
	defer primary.Close()
	proxy, err := NewControlProxy(ControlProxyOptions{
		PrimaryURL:  primary.URL,
		CAFile:      writeCACert(t, primary.Certificate()),
		ForwardedBy: "pc",
	})
	if err != nil {
		t.Fatalf("NewControlProxy: %v", err)
	}
	for _, path := range []string{
		"/api/web-push/status",
		"/api/app/update",
		"/api/node-info",
		"/api/control-sync/status",
		"/api/sessions",
		"/health",
	} {
		called := false
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
		rec := httptest.NewRecorder()
		proxy.Handler(next).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
		if !called {
			t.Errorf("%s 被转发了，应留给本机处理", path)
		}
	}
	if primaryHits != 0 {
		t.Fatalf("主节点收到 %d 次请求，应为 0", primaryHits)
	}
}

// Q3：同步通道一律 https。明文、缺 scheme、坏 ca_file 都在构造时拒绝。
func TestNewControlProxyRejectsInsecureConfig(t *testing.T) {
	cases := []struct {
		name string
		opts ControlProxyOptions
	}{
		{name: "明文 http", opts: ControlProxyOptions{PrimaryURL: "http://home.example:7331"}},
		{name: "缺 scheme", opts: ControlProxyOptions{PrimaryURL: "home.example:7331"}},
		{name: "空地址", opts: ControlProxyOptions{PrimaryURL: "  "}},
		{name: "ca_file 不存在", opts: ControlProxyOptions{PrimaryURL: "https://home.example:7331", CAFile: filepath.Join(t.TempDir(), "nope.pem")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if proxy, err := NewControlProxy(tc.opts); err == nil {
				t.Fatalf("居然接受了 %+v（proxy=%v）", tc.opts, proxy)
			}
		})
	}
}

// 流式响应要逐块透传，而不是攒满再回 —— 网关后面的长响应靠这个不至于整体超时。
func TestControlProxyStreamsResponseChunks(t *testing.T) {
	release := make(chan struct{})
	primary := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		_, _ = w.Write([]byte("data: one\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
		<-release
		_, _ = w.Write([]byte("data: two\n\n"))
	}))
	defer primary.Close()

	proxy, err := NewControlProxy(ControlProxyOptions{
		PrimaryURL:  primary.URL,
		CAFile:      writeCACert(t, primary.Certificate()),
		ForwardedBy: "pc",
	})
	if err != nil {
		t.Fatalf("NewControlProxy: %v", err)
	}
	defer close(release)

	var mu sync.Mutex
	var chunks []string
	sink := &streamSink{header: http.Header{}, record: func(chunk string) {
		mu.Lock()
		defer mu.Unlock()
		chunks = append(chunks, chunk)
	}}

	done := make(chan struct{})
	go func() {
		defer close(done)
		proxy.Handler(http.NotFoundHandler()).ServeHTTP(sink, httptest.NewRequest(http.MethodPost, "/api/preferences/cors", nil))
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(chunks)
		sink.flushed.Lock()
		flushed := sink.flushedAt
		sink.flushed.Unlock()
		mu.Unlock()
		if n > 0 && flushed > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("第一块没有在收到后立刻刷出（chunks=%d）", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
	release <- struct{}{}
	<-done
	if got := strings.Join(chunks, ""); got != "data: one\n\ndata: two\n\n" {
		t.Fatalf("流式体 = %q", got)
	}
}

// streamSink 是只支持 Write + Flush 的 ResponseWriter，用来观察「是否立刻刷出」。
type streamSink struct {
	header    http.Header
	buf       strings.Builder
	written   int
	record    func(string)
	flushed   sync.Mutex
	flushedAt int
}

func (s *streamSink) Header() http.Header { return s.header }

func (s *streamSink) Write(p []byte) (int, error) {
	s.record(string(p))
	return len(p), nil
}

func (s *streamSink) WriteHeader(int) {}

func (s *streamSink) Flush() {
	s.flushed.Lock()
	defer s.flushed.Unlock()
	s.flushedAt++
}
