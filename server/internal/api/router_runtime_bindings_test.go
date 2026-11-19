package api

import (
	"context"
	"encoding/json"
	"testing"

	"mindfs/server/internal/kanban"
)

// Router 会话在 agent pool 里是**长期存活**的：`router-<taskID>` 第一次
// OpenSession 后，后续每轮都复用同一个 CLI 会话。SDK 只在打开时注册一次
// 进程内 MCP 工具，所以交给 CLI 的 handler 一旦闭包住第一轮的
// routerToolEnv，第二轮起所有工具调用都会写进第一轮的 collector。
//
// 线上症状（2026-11-19 04:16–04:48，task_2ea72d70e560f8e3）：三轮的 seq
// 依次是 0；1,2；3,4,5，本轮 collector 始终为空，于是决策只剩一条由最终
// 文本合成的 say「（已转交，无其他动作。）」，deliver 从未被校验也从未应用；
// 同一原因让 router_inspect_run 读到第一轮那个还没有 Run 的 brief，恒报
// "no workflow run for this task"。
//
// 这条用例钉住抽象层的不变量，不依赖真实 CLI：同一份工具句柄在下一轮被
// 调用时，必须落到**当前轮**的 handler 表。
func TestRouterToolBindingsFollowCurrentTurn(t *testing.T) {
	ctx := context.Background()
	bindings := newRouterToolBindings()

	turn1 := []kanban.RouterToolSpec{{
		Name:        "router_say",
		Description: "第一轮",
		Class:       kanban.RouterToolWrite,
		Handler: func(context.Context, json.RawMessage) (string, error) {
			return "turn-1", nil
		},
	}}
	bindings.Set(turn1)
	// 第一轮注册给 CLI 的就是这批句柄；之后 pool 复用会话不会再注册。
	registered := bindings.Specs()
	if len(registered) != 1 || registered[0].Name != "router_say" {
		t.Fatalf("registered specs = %#v, want the first turn's tool", registered)
	}
	got, err := registered[0].Handler(ctx, json.RawMessage(`{}`))
	if err != nil || got != "turn-1" {
		t.Fatalf("turn1 handler = %q err=%v, want turn-1", got, err)
	}

	// 第二轮的 env 换了：复用同一份句柄必须落到第二轮 handler。
	turn2 := []kanban.RouterToolSpec{{
		Name:        "router_say",
		Description: "第二轮",
		Class:       kanban.RouterToolWrite,
		Handler: func(context.Context, json.RawMessage) (string, error) {
			return "turn-2", nil
		},
	}}
	bindings.Set(turn2)
	if got, err := registered[0].Handler(ctx, json.RawMessage(`{}`)); err != nil || got != "turn-2" {
		t.Fatalf("stale tool handle wrote to turn 1: got %q err=%v, want turn-2", got, err)
	}
	// 复用注册的表不能让第二轮的新工具消失/换序。
	if specs := bindings.Specs(); len(specs) != 1 || specs[0].Name != "router_say" {
		t.Fatalf("specs after turn 2 = %#v", specs)
	}
}

// 每轮工具集相同（Router 工具是固定集合），所以注册表只需第一轮的元数据；
// 但 handler 必须按名字路由到当前轮。缺名字时明确报错，绝不静默落到旧轮。
func TestRouterToolBindingsRejectUnknownTool(t *testing.T) {
	bindings := newRouterToolBindings()
	bindings.Set([]kanban.RouterToolSpec{{
		Name:  "router_say",
		Class: kanban.RouterToolWrite,
		Handler: func(context.Context, json.RawMessage) (string, error) {
			return "ok", nil
		},
	}})
	registered := bindings.Specs()
	bindings.Set([]kanban.RouterToolSpec{{
		Name:  "router_deliver_to_active_run",
		Class: kanban.RouterToolWrite,
		Handler: func(context.Context, json.RawMessage) (string, error) {
			return "delivered", nil
		},
	}})
	if _, err := registered[0].Handler(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("registered router_say silently ran a different turn's tool set")
	}
	if specs := bindings.Specs(); len(specs) != 1 || specs[0].Name != "router_deliver_to_active_run" {
		t.Fatalf("specs after replacing the tool set = %#v", specs)
	}
}
