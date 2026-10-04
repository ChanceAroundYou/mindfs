package nodeinfo

import "testing"

func TestNormalize(t *testing.T) {
	cases := []struct {
		in   string
		want Role
	}{
		{"", RoleControl},
		{"control", RoleControl},
		{"  CONTROL  ", RoleControl},
		{"worker", RoleWorker},
		{" Worker ", RoleWorker},
		// 未知值必须回落 control：猜成 worker 会让节点丧失全部 UI，
		// 猜成 control 只是多提供几个没人用的端点。
		{"nonsense", RoleControl},
		{"headless", RoleControl},
	}
	for _, tc := range cases {
		if got := Normalize(tc.in); got != tc.want {
			t.Fatalf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestZeroValueIsControl(t *testing.T) {
	var r Role
	if r.IsWorker() {
		t.Fatal("zero Role must behave as control, not worker")
	}
	if Normalize(string(r)) != RoleControl {
		t.Fatal("zero Role must normalize to control")
	}
}

func TestIsControlPlane(t *testing.T) {
	control := []string{
		"/api/users",
		"/api/auth/login",
		"/api/preferences/cors",
		"/api/prompts",
		"/api/task-templates",
		"/api/task-stage-templates",
		"/api/web-push/status",
		"/api/nodes",
		"/api/node-info",
		"/api/relay/status",
		"/api/e2ee/open",
		"/api/token-station/userinfo",
		"/api/app/update",
	}
	for _, p := range control {
		if !IsControlPlane(p) {
			t.Errorf("IsControlPlane(%q) = false, want true", p)
		}
	}
}

// 数据面与运行时端点**必须不在**控制面表内 —— 否则 worker 无法执行任何任务。
// 这是本包最重要的一条不变量：表写错一个前缀，运行节点就少一块能力。
func TestDataPlaneIsNotControlPlane(t *testing.T) {
	dataPlane := []string{
		"/",
		"/health",
		"/ws",
		"/api/dirs",
		"/api/tree",
		"/api/file",
		"/api/file/operation",
		"/api/upload",
		"/api/candidates",
		"/api/sessions",
		"/api/sessions/search",
		"/api/sessions/children",
		"/api/replying-sessions",
		"/api/tasks",
		"/api/tasks/overview",
		"/api/tasks/t1/run-now",
		"/api/git/status",
		"/api/git/worktrees",
		"/api/scheduled-agent-tasks",
		"/api/local_dirs",
		// agent 运行时配置改的是本机，不是全局控制面。
		"/api/agents",
		"/api/agents/restart",
		"/api/agents/memory",
		"/api/agent-config/defaults",
		"/api/agent-api-providers",
		"/api/imports/github",
	}
	for _, p := range dataPlane {
		if IsControlPlane(p) {
			t.Errorf("IsControlPlane(%q) = true, must stay available on worker nodes", p)
		}
	}
}

func TestIsControlPlaneSegmentBoundary(t *testing.T) {
	// 段边界：只有整段相等或后跟 "/" 才算控制面。
	if IsControlPlane("/api/preferences-extra") {
		t.Error("/api/preferences-extra must not match the /api/preferences prefix")
	}
	if !IsControlPlane("/api/preferences/cors") {
		t.Error("/api/preferences/cors must match")
	}
	if !IsControlPlane("/api/preferences") {
		t.Error("exact match must count")
	}
	if IsControlPlane("") {
		t.Error("empty path must not be control plane")
	}
	if IsControlPlane("   ") {
		t.Error("blank path must not be control plane")
	}
}

// 前缀表是对外的契约：改了它就改了「worker 还剩什么能力」。
// 这里把整张表钉住，改动必须是有意的。
func TestControlPlanePrefixesSnapshot(t *testing.T) {
	got := ControlPlanePrefixes()
	want := []string{
		"/api/auth",
		"/api/users",
		"/api/preferences",
		"/api/pins",
		"/api/prompts",
		"/api/task-templates",
		"/api/task-stage-templates",
		"/api/web-push",
		"/api/nodes",
		"/api/node-info",
		"/api/relay",
		"/api/e2ee",
		"/api/token-station",
		"/api/app/update",
	}
	if len(got) != len(want) {
		t.Fatalf("prefix count = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("prefix[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

// 返回的必须是副本：调用方改返回值不该污染包内状态。
func TestControlPlanePrefixesIsCopy(t *testing.T) {
	a := ControlPlanePrefixes()
	a[0] = "/mutated"
	if ControlPlanePrefixes()[0] == "/mutated" {
		t.Fatal("ControlPlanePrefixes must return a copy")
	}
}
