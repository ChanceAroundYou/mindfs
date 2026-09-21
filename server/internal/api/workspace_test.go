package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

type fakeWorkspaceProvider struct {
	primary string
	contexts map[string]*AppContext
}

func (f *fakeWorkspaceProvider) Workspace(userID string) (*AppContext, error) {
	if userID == "" {
		userID = f.primary
	}
	ctx, ok := f.contexts[userID]
	if !ok {
		// 必须包 ErrUnknownUser：ScopedRouter 靠它把「账户不存在」映射成 404。
		// 真实 provider（server/app/workspace.go）只在**本机账户被删**时才包它
		// （有数据目录但不在账户表）；单纯是别的机器的账户会返回空工作区，
		// 不是 404——别把两者混起来。
		return nil, fmt.Errorf("%w: %s", ErrUnknownUser, userID)
	}
	return ctx, nil
}

func (f *fakeWorkspaceProvider) PrimaryUserID() string { return f.primary }

// 每账户必须拿到独立的 handler / AppContext / StreamHub —— 这是 WS 广播隔离的根据。
// 漏掉任何一层，B 账户就能实时收到 A 账户的事件。
func TestScopedRouterIsolatesAccounts(t *testing.T) {
	primaryCtx := &AppContext{}
	otherCtx := &AppContext{}
	provider := &fakeWorkspaceProvider{
		primary:  "u_primary",
		contexts: map[string]*AppContext{"u_primary": primaryCtx, "u_other": otherCtx},
	}

	var mu sync.Mutex
	var seen []*AppContext
	router := NewScopedRouter(provider, func(ctx *AppContext) http.Handler {
		mu.Lock()
		seen = append(seen, ctx)
		mu.Unlock()
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	})

	serve := func(target string) int {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		return rec.Code
	}

	if code := serve("/api/dirs?user=u_primary"); code != http.StatusOK {
		t.Fatalf("primary request = %d, want 200", code)
	}
	if code := serve("/api/dirs?user=u_other"); code != http.StatusOK {
		t.Fatalf("other request = %d, want 200", code)
	}
	// 同一个账户再来一次：必须复用同一个 handler，不能每次重建
	if code := serve("/api/dirs?user=u_other"); code != http.StatusOK {
		t.Fatalf("other repeat = %d, want 200", code)
	}

	mu.Lock()
	built := append([]*AppContext(nil), seen...)
	mu.Unlock()
	if len(built) != 2 {
		t.Fatalf("built %d workspaces, want 2 (one per account, cached)", len(built))
	}
	if built[0] == built[1] {
		t.Fatal("both accounts got the same AppContext")
	}
	if built[0].GetSessionStreamHub() == built[1].GetSessionStreamHub() {
		t.Fatal("accounts share a StreamHub: 广播会跨账户泄漏")
	}
}

// 没带 user= 的请求（CLI / 定时任务 / 老客户端）必须落到主账户。
func TestScopedRouterFallsBackToPrimary(t *testing.T) {
	primaryCtx := &AppContext{}
	provider := &fakeWorkspaceProvider{
		primary:  "u_primary",
		contexts: map[string]*AppContext{"u_primary": primaryCtx},
	}
	var got *AppContext
	router := NewScopedRouter(provider, func(ctx *AppContext) http.Handler {
		got = ctx
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/dirs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got != primaryCtx {
		t.Fatal("request without user= did not fall back to the primary account")
	}
}

// 声明了不存在的账户 → 404，让前端知道该重新登录，而不是静默落到别人的数据上。
func TestScopedRouterRejectsUnknownAccount(t *testing.T) {
	provider := &fakeWorkspaceProvider{
		primary:  "u_primary",
		contexts: map[string]*AppContext{"u_primary": {}},
	}
	router := NewScopedRouter(provider, func(*AppContext) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/dirs?user=u_missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestRequestUserIDPrefersQuery(t *testing.T) {
	provider := &fakeWorkspaceProvider{primary: "u_primary"}
	r := httptest.NewRequest(http.MethodGet, "/api/dirs?user=u_x", nil)
	if got := RequestUserID(r, provider); got != "u_x" {
		t.Fatalf("RequestUserID = %q, want u_x", got)
	}
	r = httptest.NewRequest(http.MethodGet, "/api/dirs", nil)
	if got := RequestUserID(r, provider); got != "u_primary" {
		t.Fatalf("RequestUserID = %q, want u_primary", got)
	}
}
