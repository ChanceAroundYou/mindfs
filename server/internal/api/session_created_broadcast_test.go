package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"mindfs/server/internal/fs"
	"mindfs/server/internal/kanban"
	"mindfs/server/internal/session"
)

// dialHub 起一条真 WebSocket 连到 hub 上，返回客户端连接。
// 走真连接而不是直接摸 hub.clients：BroadcastAll → SendToClient → WriteJSON
// 这条链上任何一处没把 payload 带出去，这个测试都会红。
func dialHub(t *testing.T, hub *StreamHub) *websocket.Conn {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		hub.RegisterClient("test-client", conn)
		// 别让 handler 立刻返回把连接关掉
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial hub: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func readBroadcast(t *testing.T, conn *websocket.Conn, wantType string) WSResponse {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		var resp WSResponse
		if err := conn.ReadJSON(&resp); err != nil {
			t.Fatalf("read broadcast (want %s): %v", wantType, err)
		}
		if resp.Type == wantType {
			return resp
		}
	}
}

// 看板新建的会话必须广播 session.created。
//
// 为什么这条重要：session.created 的前端 handler 会重拉会话列表并补进分组，
// 而 session.meta.updated 那个 handler 只写 sessionCacheRef 里**已存在**的条目。
// 少这一条的症状就是「用户不在这个项目页时，看板建的会话在库里、在 API 返回里、
// 就是不在对话列表里」（实测：llmux 任务 #5）。
func TestEnsureAgentSessionBroadcastsSessionCreated(t *testing.T) {
	registry := fs.NewRegistry(filepath.Join(t.TempDir(), "registry.json"))
	root, err := registry.Upsert(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app := &AppContext{Dirs: registry}
	conn := dialHub(t, app.GetSessionStreamHub())

	key, err := app.EnsureAgentSession(context.Background(), kanban.AgentStageExecution{
		RootID: root.ID,
		Task: kanban.Task{
			ID:         "task_1",
			Name:       "看板任务",
			TaskNumber: 5,
		},
		Stage: kanban.StageTemplate{Agent: "claude", Model: "test-model"},
		Prompt: "hello",
	})
	if err != nil {
		t.Fatalf("EnsureAgentSession: %v", err)
	}
	if strings.TrimSpace(key) == "" {
		t.Fatal("EnsureAgentSession returned an empty session key")
	}

	// 两条都要到：meta.updated 是既有契约（已缓存条目更新），created 是新增的那条。
	_ = readBroadcast(t, conn, "session.meta.updated")
	created := readBroadcast(t, conn, "session.created")
	payload := created.Payload
	if payload == nil {
		t.Fatalf("session.created carried no payload: %#v", created.Payload)
	}
	if got, _ := payload["root_id"].(string); got != root.ID {
		t.Fatalf("root_id = %q, want %q", got, root.ID)
	}
	raw, _ := json.Marshal(payload["session"])
	var item map[string]any
	if err := json.Unmarshal(raw, &item); err != nil {
		t.Fatalf("decode session payload: %v", err)
	}
	if got, _ := item["key"].(string); got != key {
		t.Fatalf("session.key = %q, want %q", got, key)
	}
	// 列表行形状必须齐全 —— 前端 toSessionItem 靠这些字段建条目
	for _, field := range []string{"type", "name", "task_id", "agent", "created_at", "updated_at"} {
		if _, ok := item[field]; !ok {
			t.Fatalf("session.created payload is missing %q: %#v", field, item)
		}
	}
	if got, _ := item["task_id"].(string); got != "task_1" {
		t.Fatalf("task_id = %q, want task_1 (kanban-created sessions must carry their task)", got)
	}
}

// BroadcastSessionCreated 直接调也要成立：scheduled 那条路径经接口转调它。
func TestBroadcastSessionCreatedCarriesListRowShape(t *testing.T) {
	registry := fs.NewRegistry(filepath.Join(t.TempDir(), "registry.json"))
	root, err := registry.Upsert(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app := &AppContext{Dirs: registry}
	manager, err := app.GetSessionManager(root.ID)
	if err != nil {
		t.Fatal(err)
	}
	command, err := manager.Create(context.Background(), session.CreateInput{Type: session.TypeCommand})
	if err != nil {
		t.Fatal(err)
	}
	conn := dialHub(t, app.GetSessionStreamHub())
	app.BroadcastSessionCreated(root.ID, command)
	created := readBroadcast(t, conn, "session.created")
	payload := created.Payload
	raw, _ := json.Marshal(payload["session"])
	var item map[string]any
	if err := json.Unmarshal(raw, &item); err != nil {
		t.Fatalf("decode session payload: %v", err)
	}
	if got, _ := item["key"].(string); got != command.Key {
		t.Fatalf("session.key = %q, want %q", got, command.Key)
	}
	// AppContext 没有 HTTPHandler，shell 的解析得能自己走通（command 会话要有 shell 字段）
	if _, ok := item["shell"]; !ok {
		t.Fatalf("command session payload must carry a shell field: %#v", item)
	}
}

// nil 会话不能广播出一条空壳事件：那会让前端白重拉一次列表。
func TestBroadcastSessionCreatedSkipsNilSession(t *testing.T) {
	registry := fs.NewRegistry(filepath.Join(t.TempDir(), "registry.json"))
	app := &AppContext{Dirs: registry}
	conn := dialHub(t, app.GetSessionStreamHub())
	app.BroadcastSessionCreated("any-root", nil)
	_ = conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	var resp WSResponse
	if err := conn.ReadJSON(&resp); err == nil {
		t.Fatalf("expected no broadcast for a nil session, got %#v", resp)
	}
}
