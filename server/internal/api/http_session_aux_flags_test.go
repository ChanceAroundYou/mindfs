package api

import (
	"context"
	"path/filepath"
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

// TestSessionListResponseCarriesAuxFlags 钉住列表行带 aux_flags（G-AD 定制防覆盖
// 测试：合上游时 payload 加列被冲掉，这里先红）。
func TestSessionListResponseCarriesAuxFlags(t *testing.T) {
	sess := &session.Session{
		Key:  "k1",
		Type: session.TypeChat,
		Name: "Chat",
		Aux: session.SessionAuxFlags{
			AskUserWaiting: true,
			HasPlan:        true,
			SessionError:   "boom",
		},
	}
	item := sessionListResponse(sess, "")
	raw, ok := item["aux_flags"].(map[string]any)
	if !ok {
		t.Fatalf("aux_flags missing or wrong shape: %#v", item["aux_flags"])
	}
	if raw["ask_user_waiting"] != true || raw["has_plan"] != true {
		t.Fatalf("aux_flags = %#v, want ask_user_waiting/has_plan true", raw)
	}
	if raw["session_error"] != "boom" {
		t.Fatalf("session_error = %#v, want boom", raw["session_error"])
	}
	if _, ok := raw["has_todos"]; !ok {
		t.Fatalf("aux_flags missing has_todos: %#v", raw)
	}
}

// TestAuxFlagsFromEventMirrorToSessionRow 钉住「流事件 → 会话行 aux_flags」的写入。
//
// 2026-10-19 前 ask_user 分支写完任务行就 return，镜像只挂在 plan/todo/task 那一支上，
// 于是会话行的 ask_user_waiting 只有清位（ClearTaskAuxFlagsForSession）没有置位 ——
// 名字上方的问号徽标（SessionCardTopBadges 读 session.aux_flags.ask_user_waiting）
// 是死代码。任务行那条还有 sess.TaskID 守卫，普通聊天会话连任务行都写不进。
// 这条测试之前是空的：web/tests/session-status-badges.test.mjs 只扫前端源码模式 +
// 后端列名，字段在、列在、徽标在，就是没人写。
func TestAuxFlagsFromEventMirrorToSessionRow(t *testing.T) {
	cases := []struct {
		name  string
		prime bool // 先发一条 ask_user 提问，把位置起来再发 tc.event
		event *StreamEvent
		read  func(session.SessionAuxFlags) bool
	}{
		{
			"ask_user 提问发出 → 会话行置位",
			false,
			&StreamEvent{Type: "tool_call", Data: agenttypes.ToolCall{CallID: "c1", Kind: agenttypes.ToolKindAskUser}},
			func(f session.SessionAuxFlags) bool { return f.AskUserWaiting },
		},
		{
			"ask_user 答完 → 会话行清位",
			true,
			&StreamEvent{Type: "tool_call_update", Data: agenttypes.ToolCall{CallID: "c1", Kind: agenttypes.ToolKindAskUser, Status: "complete"}},
			func(f session.SessionAuxFlags) bool { return !f.AskUserWaiting },
		},
		{
			// 对照组：镜像挂在 plan 那一支上一直是通的。它红了说明整条镜像路径断了，
			// 不是 ask_user 这一支的问题。
			"plan 更新 → 会话行置位（对照组）",
			false,
			&StreamEvent{Type: string(agenttypes.EventTypePlanUpdate)},
			func(f session.SessionAuxFlags) bool { return f.HasPlan },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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
			// 普通聊天会话：没有 task_id —— 任务行写不进去，只有会话行这一条通道。
			chat, err := manager.Create(context.Background(), session.CreateInput{Type: session.TypeChat})
			if err != nil {
				t.Fatal(err)
			}
			read := func() session.SessionAuxFlags {
				got, err := manager.Get(context.Background(), chat.Key, 0)
				if err != nil || got == nil {
					t.Fatalf("get: %v", err)
				}
				return got.Aux
			}
			if tc.prime {
				app.updateTaskAuxFlagsFromEvent(root.ID, chat.Key, &StreamEvent{
					Type: "tool_call",
					Data: agenttypes.ToolCall{CallID: "c1", Kind: agenttypes.ToolKindAskUser},
				})
				// 不先断言置位，清位那格就是空断言（本来就是 false）。
				if !read().AskUserWaiting {
					t.Fatalf("前置置位失败，清位用例无从谈起")
				}
			}
			app.updateTaskAuxFlagsFromEvent(root.ID, chat.Key, tc.event)
			if flags := read(); !tc.read(flags) {
				t.Fatalf("会话行 aux_flags = %+v", flags)
			}
		})
	}
}
