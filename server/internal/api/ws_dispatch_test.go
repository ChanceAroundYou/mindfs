package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// wsDispatchPaths 枚举可能出现 handleWSRequest 的 ws_*.go 源码文件。
var wsDispatchPaths = []string{"ws.go", "ws_upgrade.go"}

func TestParseClientContext(t *testing.T) {
	payload := map[string]any{
		"context": map[string]any{
			"current_root": "ignored-by-payload",
			"selection": map[string]any{
				"file_path":  "docs/readme.md",
				"start_line": 1,
				"end_line":   3,
				"text":       "abc",
			},
		},
	}

	got := parseClientContext(payload, "mindfs")
	if got.CurrentRoot != "ignored-by-payload" {
		t.Fatalf("unexpected current root: %q", got.CurrentRoot)
	}
	if got.Selection == nil || got.Selection.Text != "abc" {
		t.Fatalf("unexpected selection: %#v", got.Selection)
	}

	got = parseClientContext(map[string]any{}, "fallback-root")
	if got.CurrentRoot != "fallback-root" {
		t.Fatalf("expected fallback root, got %q", got.CurrentRoot)
	}
}

func TestRegisterClientSupersedesPreviousConnection(t *testing.T) {
	hub := NewStreamHub()
	first := &websocket.Conn{}
	second := &websocket.Conn{}

	if previous := hub.RegisterClient("client-1", first); previous != nil {
		t.Fatalf("first registration returned previous connection %p", previous)
	}
	hub.BindSessionClient("session-1", "client-1")
	if previous := hub.RegisterClient("client-1", second); previous != first {
		t.Fatalf("replacement returned %p, want %p", previous, first)
	}

	hub.UnregisterClient("client-1", first)
	if got := hub.clients["client-1"]; got != second {
		t.Fatalf("old connection unregistered replacement: got %p, want %p", got, second)
	}
	if _, ok := hub.connLocks[first]; ok {
		t.Fatal("old connection lock was not released")
	}
	if got := hub.GetSessionClientIDs("session-1", false); len(got) != 1 || got[0] != "client-1" {
		t.Fatalf("old connection removed replacement session binding: %#v", got)
	}

	hub.UnregisterClient("client-1", second)
	if got := hub.clients["client-1"]; got != nil {
		t.Fatalf("replacement connection still registered: %p", got)
	}
}

func TestWSRequestHandlersAreDispatchedAsynchronously(t *testing.T) {
	fset := token.NewFileSet()
	// handleWSRequest 住在哪个 ws_*.go 里是拆分细节，守卫必须跟着它走而不是钉住一个文件名。
	var dispatch *ast.FuncDecl
	for _, path := range wsDispatchPaths {
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "handleWSRequest" {
				dispatch = fn
				break
			}
		}
		if dispatch != nil {
			break
		}
	}
	if dispatch == nil {
		t.Fatal("handleWSRequest not found")
	}
	async := make(map[*ast.CallExpr]bool)
	ast.Inspect(dispatch.Body, func(node ast.Node) bool {
		goStmt, ok := node.(*ast.GoStmt)
		if !ok {
			return true
		}
		async[goStmt.Call] = true
		return true
	})
	var handlers []string
	ast.Inspect(dispatch.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel == nil || !strings.HasPrefix(selector.Sel.Name, "handle") {
			return true
		}
		receiver, ok := selector.X.(*ast.Ident)
		if !ok || receiver.Name != "h" {
			return true
		}
		handlers = append(handlers, selector.Sel.Name)
		if !async[call] {
			t.Errorf("%s must be dispatched in its own goroutine", selector.Sel.Name)
		}
		return true
	})
	if len(handlers) == 0 {
		t.Fatal("no websocket request handlers found")
	}
}
