package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"mindfs/server/internal/controlsync"
)

// modeGateFixture 造一个「主节点」与一个包着转发器的本机 mux 探针。
type modeGateFixture struct {
	primaryHits atomic.Int32
	nextHits    atomic.Int32
	primary     *httptest.Server
	handler     http.Handler
	mode        controlsync.Mode
	blocked     map[string]bool
}

func newModeGateFixture(t *testing.T) *modeGateFixture {
	t.Helper()
	f := &modeGateFixture{mode: controlsync.ModeOnline, blocked: map[string]bool{}}
	f.primary = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.primaryHits.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("primary"))
	}))
	t.Cleanup(f.primary.Close)

	proxy, err := NewControlProxy(ControlProxyOptions{PrimaryURL: f.primary.URL})
	if err != nil {
		t.Fatalf("NewControlProxy: %v", err)
	}
	// 测试用 httptest 的自签证书；生产只信系统根 + ca_file，这里只是为了让
	// 转发真的打到桩服务上，不改变 NewControlProxy 的 https-only 校验。
	proxy.client = f.primary.Client()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.nextHits.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("local"))
	})
	proxy.Mode = func() controlsync.Mode { return f.mode }
	proxy.BlockedDomain = func(domain string) bool { return f.blocked[domain] }
	f.handler = proxy.Handler(next)
	return f
}

func (f *modeGateFixture) do(method, path, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, r)
	return rec
}

func TestControlProxyForwardsOnlyWhileOnline(t *testing.T) {
	f := newModeGateFixture(t)

	rec := f.do(http.MethodGet, "/api/preferences/cors", "")
	if rec.Code != http.StatusOK || rec.Body.String() != "primary" {
		t.Fatalf("online 读 = %d %q，应转发到主节点", rec.Code, rec.Body.String())
	}

	for _, mode := range []controlsync.Mode{
		controlsync.ModeOfflineLocal,
		controlsync.ModeReconciling,
		controlsync.ModeConflictReview,
	} {
		f.mode = mode
		f.primaryHits.Store(0)
		rec := f.do(http.MethodGet, "/api/preferences/cors", "")
		if rec.Body.String() != "local" {
			t.Fatalf("%s 下读请求落到了 %q，应在本地处理", mode, rec.Body.String())
		}
		if n := f.primaryHits.Load(); n != 0 {
			t.Fatalf("%s 下仍然转发了 %d 次", mode, n)
		}
	}
}

// reconciling 是「正在合并」的短暂状态：任何控制面写都不许落，否则会和合并
// 的结果分叉。读照常走本地副本。
func TestControlProxyFreezesWritesWhileReconciling(t *testing.T) {
	f := newModeGateFixture(t)
	f.mode = controlsync.ModeReconciling

	rec := f.do(http.MethodPut, "/api/preferences/cors", `{"allow":true}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("reconciling 写 = %d，应为 409（body=%s）", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "sync_conflict_pending") {
		t.Fatalf("响应没说明冻结原因：%s", rec.Body.String())
	}
	if n := f.nextHits.Load(); n != 0 {
		t.Fatalf("被冻结的写不该交给本地处理（命中 %d 次）", n)
	}
	if n := f.primaryHits.Load(); n != 0 {
		t.Fatalf("被冻结的写不该转发（命中 %d 次）", n)
	}

	if rec := f.do(http.MethodGet, "/api/preferences/cors", ""); rec.Code != http.StatusOK || rec.Body.String() != "local" {
		t.Fatalf("reconciling 读 = %d %q，应读本地副本", rec.Code, rec.Body.String())
	}

	// 登录是拿到冲突面板的前提：reconciling 冻的是数据写，不是身份。
	if rec := f.do(http.MethodPost, "/api/auth/login", `{}`); rec.Code != http.StatusOK || rec.Body.String() != "local" {
		t.Fatalf("reconciling 下登录被冻结：%d %q", rec.Code, rec.Body.String())
	}
}

// conflict-review 按域冻结：只有真有冲突的域被挡，其他域照常写本地（后续推送）。
func TestControlProxyFreezesOnlyConflictedDomains(t *testing.T) {
	f := newModeGateFixture(t)
	f.mode = controlsync.ModeConflictReview
	f.blocked["pins"] = true

	rec := f.do(http.MethodPut, "/api/pins/session", `{}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "sync_conflict_pending") {
		t.Fatalf("冲突域写 = %d %q，应 409", rec.Code, rec.Body.String())
	}

	rec = f.do(http.MethodPut, "/api/preferences/cors", `{}`)
	if rec.Code != http.StatusOK || rec.Body.String() != "local" {
		t.Fatalf("未冲突域写 = %d %q，应落本地", rec.Code, rec.Body.String())
	}

	// 登录是拿到冲突面板的前提，任何模式下都不能冻结。
	rec = f.do(http.MethodPost, "/api/auth/login", `{}`)
	if rec.Code != http.StatusOK || rec.Body.String() != "local" {
		t.Fatalf("登录被冻结了：%d %q", rec.Code, rec.Body.String())
	}
}

// 没配模式回调（未启用同步）时必须保持旧行为：照常转发。
func TestControlProxyWithoutModeCallbackForwards(t *testing.T) {
	primary := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("primary"))
	}))
	defer primary.Close()
	proxy, err := NewControlProxy(ControlProxyOptions{PrimaryURL: primary.URL})
	if err != nil {
		t.Fatalf("NewControlProxy: %v", err)
	}
	proxy.client = primary.Client()
	h := proxy.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("local"))
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/preferences/cors", strings.NewReader(`{}`)))
	if rec.Body.String() != "primary" {
		t.Fatalf("未配模式时 = %q，应照旧转发", rec.Body.String())
	}
}
