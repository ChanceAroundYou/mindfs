package kanban

import (
	"context"
	"encoding/json"
	"testing"
)

// router_inspect_run 是 Router 唯一的只读工具。设计（R1 spec §运行期事件）明确
// 运行期事件回合「只允许 say / ask_user / silent / inspect_run」—— 只读工具不在
// actions 里，不受动作白名单约束。
//
// 但事件 brief 刻意把 LatestRun 置 nil（不许把任务最新 Run 当事件的 Run 用），
// 而 inspectRun 原来只认 LatestRun：于是这个「允许」在事件回合里 100% 不可用 ——
// 模型想核对事件那个 Run 的节点终态，只会拿到 "no workflow run for this task"。
// 这几条用例钉住「事件回合能看事件自己的 Run、且只能看它」。

func inspectRunPayload(t *testing.T, env *routerToolEnv, runID string) (map[string]any, error) {
	t.Helper()
	raw, err := env.inspectRun(context.Background(), runID)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("inspectRun 返回的不是 JSON: %v (%s)", err, raw)
	}
	return payload, nil
}

func payloadNodeIDs(payload map[string]any) []string {
	nodes, _ := payload["nodes"].([]any)
	out := make([]string, 0, len(nodes))
	for _, item := range nodes {
		node, _ := item.(map[string]any)
		if id, ok := node["node_id"].(string); ok {
			out = append(out, id)
		}
	}
	return out
}

// eventInspectFixture 造出「任务引擎已切到 run-new，事件还指向 run-old」的现场：
// 这是事件回合唯一会出现的形态，也是唯一能把两个 Run 混起来的机会。
func eventInspectFixture(t *testing.T) (*coordinateFixture, string) {
	t.Helper()
	f, user := eventBriefFixture(t)
	// 旧 Run 必须整图终态（含 gate-1）：只有绑定的 Run 已终态时才允许任务切到
	// 新引擎，否则现场根本造不出来。
	f.addRun(t, "task-1", "run-old", WorkflowTriggeredByUser, map[string]string{
		"agent-1": NodeRunSuccess, "gate-1": NodeRunSuccess, "agent-2": NodeRunFail,
	})
	f.addNodeRun(t, NodeRun{ID: "nr-old-1", RunID: "run-old", NodeID: "agent-1", Attempt: 1,
		Status: NodeRunSuccess, Output: map[string]any{"summary": "旧 Run 的节点一"}})
	f.addNodeRun(t, NodeRun{ID: "nr-old-2", RunID: "run-old", NodeID: "agent-2", Attempt: 1,
		Status: NodeRunFail, Output: map[string]any{"error": "旧 Run 的节点二失败"}})
	f.addRun(t, "task-1", "run-new", WorkflowTriggeredByUser, map[string]string{"agent-1": NodeRunSuccess})
	f.addNodeRun(t, NodeRun{ID: "nr-new", RunID: "run-new", NodeID: "agent-1", Attempt: 1,
		Status: NodeRunSuccess, Output: map[string]any{"summary": "新 Run 的节点"}})
	return f, user
}

// 行为 1：事件回合 inspect_run（不带 run_id）给的是事件自己的 Run。
func TestRouterInspectRunServesEventTurn(t *testing.T) {
	ctx := context.Background()
	f, user := eventInspectFixture(t)
	ev := RouterEvent{
		ID: "revt-inspect", RootID: f.root.ID, TaskID: "task-1", RunID: "run-old",
		Kind: RouterEventRunSettled, TurnID: "revt-turn:inspect", UserActivityID: user,
	}
	brief, err := f.router.turnBrief(ctx, "task-1", eventBriefTrigger(f, ev), RouterConfig{})
	must(t, err, "turnBrief")
	if brief.LatestRun != nil {
		t.Fatalf("前置条件不成立：事件 brief 不该带 latest_run：%#v", brief.LatestRun)
	}
	env := &routerToolEnv{store: f.store, brief: brief}

	payload, err := inspectRunPayload(t, env, "")
	if err != nil {
		t.Fatalf("事件回合 inspect_run 不可用：%v", err)
	}
	run, _ := payload["run"].(map[string]any)
	if got, _ := run["id"].(string); got != "run-old" {
		t.Fatalf("inspect_run 指向 %q，应当是事件自己的 run-old", got)
	}
	ids := payloadNodeIDs(payload)
	if len(ids) != 2 {
		t.Fatalf("节点 = %v，应当只有 run-old 的两个节点", ids)
	}
	for _, id := range ids {
		if id != "agent-1" && id != "agent-2" {
			t.Fatalf("节点 %q 不属于 run-old", id)
		}
	}
}

// 行为 2：显式要别的 Run 必须拒绝 —— 否则模型可以拿任务最新 Run 顶替事件 Run。
func TestRouterInspectRunRejectsForeignRunInEventTurn(t *testing.T) {
	ctx := context.Background()
	f, user := eventInspectFixture(t)
	ev := RouterEvent{
		ID: "revt-inspect-foreign", RootID: f.root.ID, TaskID: "task-1", RunID: "run-old",
		Kind: RouterEventRunSettled, TurnID: "revt-turn:inspect-foreign", UserActivityID: user,
	}
	brief, err := f.router.turnBrief(ctx, "task-1", eventBriefTrigger(f, ev), RouterConfig{})
	must(t, err, "turnBrief")
	env := &routerToolEnv{store: f.store, brief: brief}

	if _, err := env.inspectRun(ctx, "run-new"); err == nil {
		t.Fatal("事件回合索要任务最新 Run 必须被拒绝")
	}
	if _, err := env.inspectRun(ctx, "run-old"); err != nil {
		t.Fatalf("事件自己的 run_id 应当可用：%v", err)
	}
}

// 行为 3：用户留言回合语义不变（LatestRun 就是目标 Run）。
func TestRouterInspectRunKeepsUserTurnSemantics(t *testing.T) {
	ctx := context.Background()
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)
	user := f.addUserActivity(t, "uact-inspect-user")
	f.addRun(t, "task-1", "run-user", WorkflowTriggeredByUser, map[string]string{"agent-1": NodeRunSuccess})
	f.addNodeRun(t, NodeRun{ID: "nr-user", RunID: "run-user", NodeID: "agent-1", Attempt: 1,
		Status: NodeRunSuccess, Output: map[string]any{"summary": "用户回合的节点"}})
	brief, err := f.router.turnBrief(ctx, "task-1", RouterTrigger{
		Kind: RouterTriggerUserMessage, TaskID: "task-1", RootID: f.root.ID,
		UserActivityID: user, TurnID: "turn-user", Mode: RouterModeRoute,
	}, RouterConfig{})
	must(t, err, "turnBrief")
	if brief.LatestRun == nil {
		t.Fatalf("前置条件不成立：用户回合应当带 latest_run：%#v", brief)
	}
	env := &routerToolEnv{store: f.store, brief: brief}

	payload, err := inspectRunPayload(t, env, "")
	if err != nil {
		t.Fatalf("用户回合 inspect_run 报错：%v", err)
	}
	run, _ := payload["run"].(map[string]any)
	if got, _ := run["id"].(string); got != "run-user" {
		t.Fatalf("inspect_run 指向 %q，应当是 run-user", got)
	}
	if ids := payloadNodeIDs(payload); len(ids) != 1 || ids[0] != "agent-1" {
		t.Fatalf("节点 = %v，应当是 run-user 的 agent-1", ids)
	}
	if _, err := env.inspectRun(ctx, "run-other"); err == nil {
		t.Fatal("用户回合索要别的 Run 必须被拒绝")
	}
}
