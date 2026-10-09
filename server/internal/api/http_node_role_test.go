package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mindfs/server/internal/nodeinfo"
)

// 运行节点必须硬拒绝控制面，而数据面照常服务 —— 后者是它存在的全部理由。
// 前缀表本身在 nodeinfo 包里钉着，这里钉的是「守卫真的挂在路由上、真的生效」。
//
// 守卫直接测而不走 Routes()：corsMiddleware 会先解引用 AppContext，
// 裸 handler 没有它。
func TestWorkerRejectsControlPlaneAndKeepsDataPlane(t *testing.T) {
	h := &HTTPHandler{NodeRole: nodeinfo.RoleWorker}
	guard := h.rejectControlPlaneOnWorker(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	cases := []struct {
		path    string
		blocked bool
		why     string
	}{
		// 控制面：worker 一律 403。
		{"/api/users", true, "account table"},
		{"/api/auth/status", true, "login/accounts"},
		{"/api/preferences/cors", true, "preferences"},
		{"/api/prompts", true, "prompt library"},
		{"/api/nodes", true, "node table"},
		{"/api/task-templates", true, "task templates"},
		{"/api/node-info", true, "role self-description is control plane too"},
		{"/api/web-push/status", true, "web push subscriptions"},
		{"/api/pins", true, "置顶权威在主节点，worker 存一份就等于又分了一次片"},

		// 数据面：worker 必须放行，否则它什么也干不了。
		{"/health", false, "health check"},
		{"/ws", false, "websocket"},
		{"/api/dirs", false, "project list"},
		{"/api/tree", false, "file tree"},
		{"/api/file", false, "file read"},
		{"/api/tasks", false, "kanban tasks"},
		{"/api/task-templates-not-a-prefix", false, "segment boundary"},
		{"/api/agents", false, "agent runtime config stays on workers"},
		{"/api/agent-config", false, "agent config is this machine's own runtime"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			guard.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			blocked := rec.Code == http.StatusForbidden
			if blocked != tc.blocked {
				t.Fatalf("%s blocked=%v (%d), want blocked=%v — %s", tc.path, blocked, rec.Code, tc.blocked, tc.why)
			}
		})
	}
}

// 默认角色必须等于改造前的行为：零值 "" = control，不配置就不能变。
func TestControlRoleIsTheDefault(t *testing.T) {
	for _, role := range []nodeinfo.Role{"", nodeinfo.RoleControl, "unknown-value"} {
		h := &HTTPHandler{NodeRole: role}
		rec := httptest.NewRecorder()
		h.rejectControlPlaneOnWorker(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/nodes", nil))
		if rec.Code == http.StatusForbidden {
			t.Fatalf("role %q must behave like control, but /api/nodes was blocked", role)
		}
	}
}

// 本机 CLI 直连必须豁免：isLocalCLIPath 的白名单里有 /api/task-templates
// （控制面）。CLI 拿 token 从 loopback 调它时若被守卫拦掉，
// 「命令行读模板」在 worker 上就废了。
func TestLocalCLIExemptFromWorkerGuard(t *testing.T) {
	const token = "cli-token-for-test"
	h := &HTTPHandler{NodeRole: nodeinfo.RoleWorker, LocalCLIToken: token}
	guard := h.rejectControlPlaneOnWorker(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// 带对 token 但不是 loopback → 仍应被拒。
	req := httptest.NewRequest(http.MethodGet, "/api/task-templates", nil)
	req.Header.Set(localCLIHeaderName, token)
	req.RemoteAddr = "203.0.113.7:5555"
	rec := httptest.NewRecorder()
	guard.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-loopback CLI request = %d, want 403", rec.Code)
	}

	// loopback + 对的 token → 豁免。
	req = httptest.NewRequest(http.MethodGet, "/api/task-templates", nil)
	req.Header.Set(localCLIHeaderName, token)
	req.RemoteAddr = "127.0.0.1:5555"
	rec = httptest.NewRecorder()
	guard.ServeHTTP(rec, req)
	if rec.Code == http.StatusForbidden {
		t.Fatalf("loopback CLI request must be exempt from the worker guard, got 403: %s", rec.Body.String())
	}
}

// worker 不提供前端：给明确的 403，而不是 StaticDir 为空后落到
// renderFallbackFrontend 输出一张「前端资源缺失」的提示页 —— 那张页
// 会让用户以为是装坏了，而实际上是这台机器按配置就不提供 UI。
func TestWorkerFrontendIsForbidden(t *testing.T) {
	h := &HTTPHandler{NodeRole: nodeinfo.RoleWorker}
	rec := httptest.NewRecorder()
	h.handleFrontend(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("worker GET / = %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "node_is_worker") {
		t.Fatalf("worker GET / body = %s, want it to say node_is_worker", rec.Body.String())
	}
}

// /health 从纯文本 "ok" 改成 JSON（顺带报角色）。前端要靠它区分
// 「对面挂了」和「对面是运行节点，本来就不提供 UI」。
func TestHealthReportsRole(t *testing.T) {
	for _, tc := range []struct {
		role nodeinfo.Role
		want string
	}{
		{nodeinfo.RoleControl, "control"},
		{nodeinfo.RoleWorker, "worker"},
		{"", "control"}, // 零值归一成 control
	} {
		h := &HTTPHandler{NodeRole: tc.role}
		rec := httptest.NewRecorder()
		h.handleHealth(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("role %q: health = %d, want 200", tc.role, rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, `"ok":true`) || !strings.Contains(body, `"role":"`+tc.want+`"`) {
			t.Fatalf("role %q: health body = %s, want ok:true + role %q", tc.role, body, tc.want)
		}
	}
}

// /api/node-info 报 static=false 时前端就不给「打开网页」入口 ——
// 比让用户点了撞 403 好。
func TestNodeInfoReportsStaticAvailability(t *testing.T) {
	h := &HTTPHandler{NodeRole: nodeinfo.RoleWorker}
	rec := httptest.NewRecorder()
	h.handleNodeInfo(rec, httptest.NewRequest(http.MethodGet, "/api/node-info", nil))
	if !strings.Contains(rec.Body.String(), `"static":false`) {
		t.Fatalf("node-info body = %s, want static:false when StaticDir is empty", rec.Body.String())
	}

	h = &HTTPHandler{NodeRole: nodeinfo.RoleControl, StaticDir: "/srv/web"}
	rec = httptest.NewRecorder()
	h.handleNodeInfo(rec, httptest.NewRequest(http.MethodGet, "/api/node-info", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `"static":true`) || !strings.Contains(body, `"role":"control"`) {
		t.Fatalf("node-info body = %s, want static:true + role control", body)
	}
}
