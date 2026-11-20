package api

import (
	"testing"

	"mindfs/server/internal/kanban"
)

// Router 会话只用进程内 MCP 工具：内置工具集必须整组关掉，
// 否则每次请求都要付 ~190 KB 的工具定义（约 49k tokens）。
func TestRouterToolPolicyDisablesBuiltinTools(t *testing.T) {
	p := routerAgentToolPolicy(kanban.DefaultRouterToolPolicy())
	if p == nil || p.BuiltinTools == nil {
		t.Fatalf("router policy 未限制内置工具: %#v", p)
	}
	if *p.BuiltinTools != "" {
		t.Fatalf("router --tools = %q, want empty", *p.BuiltinTools)
	}
}

// 只读复核回合要保留 Read/Grep/Glob 三个内置工具，其余不带。
func TestVerifierToolPolicyKeepsOnlyReadOnlyBuiltins(t *testing.T) {
	p := routerVerifierToolPolicy()
	if p == nil || p.BuiltinTools == nil {
		t.Fatalf("verifier policy 未限制内置工具: %#v", p)
	}
	if *p.BuiltinTools != "Read,Grep,Glob" {
		t.Fatalf("verifier --tools = %q, want Read,Grep,Glob", *p.BuiltinTools)
	}
}
