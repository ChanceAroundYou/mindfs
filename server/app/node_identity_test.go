package app

import (
	"os"
	"strings"
	"testing"
)

// 身份解析是前端 scope 键与节点表的源头：错了会把本机认成另一台机器，
// local 伪节点的旧问题就是这么回来的。这里钉住缺省分支与显式配置的优先级。
func TestResolveNodeIdentity(t *testing.T) {
	host, _ := os.Hostname()
	host = strings.TrimSpace(host)

	cases := []struct {
		name      string
		opts      StartOptions
		wantID    string
		wantName  string
		needsHost bool
	}{
		{
			name:     "主控缺省 home/主控",
			opts:     StartOptions{},
			wantID:   PrimaryNodeID,
			wantName: PrimaryNodeName,
		},
		{
			name:     "边缘取 control_sync.instance_id",
			opts:     StartOptions{ControlSync: &ControlSyncOptions{Enabled: true, Role: "edge", InstanceID: "pc-win"}},
			wantID:   "pc-win",
			wantName: "pc-win",
		},
		{
			name:      "边缘没配 instance_id 时用主机名",
			opts:      StartOptions{ControlSync: &ControlSyncOptions{Enabled: true, Role: "edge"}},
			wantID:    host,
			wantName:  host,
			needsHost: true,
		},
		{
			name:      "worker 用主机名而不是主控身份",
			opts:      StartOptions{Role: "worker"},
			wantID:    host,
			wantName:  host,
			needsHost: true,
		},
		{
			name:     "显式 node.id/name 优先于边缘缺省",
			opts:     StartOptions{NodeID: "office-pc", NodeName: "书房 PC", ControlSync: &ControlSyncOptions{Role: "edge", InstanceID: "pc-win"}},
			wantID:   "office-pc",
			wantName: "书房 PC",
		},
		{
			name:     "显式 id 只覆盖 id，name 仍走缺省",
			opts:     StartOptions{NodeID: "home-2"},
			wantID:   "home-2",
			wantName: PrimaryNodeName,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.needsHost && host == "" {
				t.Skip("本机取不到主机名，主机名分支无法断言")
			}
			gotID, gotName := resolveNodeIdentity(tc.opts)
			if gotID != tc.wantID || gotName != tc.wantName {
				t.Fatalf("resolveNodeIdentity = (%q, %q), want (%q, %q)", gotID, gotName, tc.wantID, tc.wantName)
			}
		})
	}
}

// 主机名也取不到时 worker 仍有确定性兜底：空白 id 会让前端拼出 "::" 开头的
// scope 键，所有本机状态落进同一个桶。
func TestResolveNodeIdentityWorkerFallback(t *testing.T) {
	if host, _ := os.Hostname(); strings.TrimSpace(host) != "" {
		t.Skip("本机有主机名，兜底分支无法触发")
	}
	id, name := resolveNodeIdentity(StartOptions{Role: "worker"})
	if id != "worker" || name != "worker" {
		t.Fatalf("identity = (%q, %q), want worker/worker", id, name)
	}
}

// 身份不能带首尾空白：它原样参与 scope 键与前缀比较。
func TestResolveNodeIdentityTrims(t *testing.T) {
	id, name := resolveNodeIdentity(StartOptions{NodeID: "  office-pc  ", NodeName: "  书房 PC  "})
	if id != "office-pc" || name != "书房 PC" {
		t.Fatalf("identity = (%q, %q), want trimmed", id, name)
	}
}
