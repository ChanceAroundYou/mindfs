package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// W2 T2：节点级管理的写侧动作与读侧细节。
//
//   - router_set_node_model：改节点模型档。运行中的节点一律拒绝（用户裁定「运行中
//     不要切换」），失败 / 未跑过的节点允许，下一 attempt 生效。
//   - router_skip_node：把还没跑过的节点按跳过语义收尾，让下游继续。
//   - inspect_run(node_id=...)：只取该节点详情，不拉整轮节点列表，更不读转录。

func nodeAdminToolEnv(t *testing.T, store *TaskStore, taskID string) *routerToolEnv {
	t.Helper()
	return &routerToolEnv{store: store, brief: briefFor(t, store, taskID), collector: newRouterTurnCollector(RouterConfig{}.Normalized())}
}

func toolByName(t *testing.T, env *routerToolEnv, name string) RouterToolSpec {
	t.Helper()
	for _, tool := range env.tools() {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q not registered", name)
	return RouterToolSpec{}
}

func calledActions(t *testing.T, env *routerToolEnv) []RouterAction {
	t.Helper()
	actions := env.collector.snapshot()
	for _, action := range actions {
		if action.Kind == RouterActionSay && strings.TrimSpace(action.Body) == "" {
			t.Fatalf("empty say recorded: %+v", action)
		}
	}
	return actions
}

func twoAgentGraph() WorkflowDefinition {
	return WorkflowDefinition{
		Nodes: []WorkflowNode{
			{ID: "execute", Kind: NodeKindAgent, Params: map[string]any{"prompt": "改代码"}},
			{ID: "verify", Kind: NodeKindAgent, Params: map[string]any{"prompt": "验证"}},
		},
		Edges: []WorkflowEdge{{From: "execute", To: "verify"}},
	}
}

func TestRouterSetNodeModelToolRecordsAndValidates(t *testing.T) {
	store, _, run, _ := newWorkflowCommentFixture(t, "task-model", "run-model", twoAgentGraph())
	env := nodeAdminToolEnv(t, store, run.TaskID)
	tool := toolByName(t, env, "router_set_node_model")
	if _, err := tool.Handler(context.Background(), json.RawMessage(`{"node_id":"verify","model":"sonnet"}`)); err != nil {
		t.Fatalf("handler: %v", err)
	}
	actions := calledActions(t, env)
	if len(actions) != 1 || actions[0].Kind != RouterActionSetNodeModel {
		t.Fatalf("actions = %+v", actions)
	}
	if actions[0].NodeID != "verify" || actions[0].Model != "sonnet" {
		t.Fatalf("action = %+v, want node verify / model sonnet", actions[0])
	}
	if err := validateRouterActions(env.brief, actions, RouterConfig{}.Normalized()); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// 运行中不切换：节点行还在跑时，改档在工具层就要被拒，并且说清楚为什么。
func TestRouterSetNodeModelRejectsRunningNode(t *testing.T) {
	store, _, run, _ := newWorkflowCommentFixture(t, "task-model-run", "run-model-run", twoAgentGraph())
	startWorkflowAgentNode(t, store, run.ID, "execute", "sess-execute")
	env := nodeAdminToolEnv(t, store, run.TaskID)
	tool := toolByName(t, env, "router_set_node_model")
	_, err := tool.Handler(context.Background(), json.RawMessage(`{"node_id":"execute","model":"sonnet"}`))
	if err == nil || !strings.Contains(err.Error(), "运行中") {
		t.Fatalf("err = %v, want a running-node refusal", err)
	}
	if got := len(env.collector.snapshot()); got != 0 {
		t.Fatalf("recorded %d actions on a refused call", got)
	}
}

// 失败节点允许改档：下一 attempt 用新模型，不用先等它变回 pending。
func TestSetWorkflowNodeModelForRouterUpdatesGraph(t *testing.T) {
	ctx := context.Background()
	store, svc, run, root := newWorkflowCommentFixture(t, "task-model-svc", "run-model-svc", twoAgentGraph())
	nr := startWorkflowAgentNode(t, store, run.ID, "execute", "sess-execute")
	if _, err := store.MarkWorkflowNodeRunUnknown(ctx, nr, "上游 502"); err != nil {
		t.Fatalf("MarkWorkflowNodeRunUnknown: %v", err)
	}
	before, err := store.WorkflowRunByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("WorkflowRunByID: %v", err)
	}
	if err := svc.SetWorkflowNodeModelForRouter(ctx, root.ID, run.TaskID, "execute", "sonnet", "中继不稳"); err != nil {
		t.Fatalf("SetWorkflowNodeModelForRouter: %v", err)
	}
	after, err := store.WorkflowRunByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("WorkflowRunByID: %v", err)
	}
	node, ok := after.Graph.node("execute")
	if !ok {
		t.Fatal("execute node missing")
	}
	if model, _ := node.Params["model"].(string); model != "sonnet" {
		t.Fatalf("model = %q, want sonnet", model)
	}
	if after.Revision <= before.Revision {
		t.Fatalf("revision %d -> %d, want advanced", before.Revision, after.Revision)
	}
	rows, err := store.ListWorkflowNodeRuns(ctx, run.ID)
	if err != nil {
		t.Fatalf("ListWorkflowNodeRuns: %v", err)
	}
	if latest, ok := latestTopLevelRow(rows, "execute"); !ok || latest.Status != NodeRunUnknown {
		t.Fatalf("latest row = %+v, want the unknown attempt preserved", latest)
	}
}

func TestSetWorkflowNodeModelForRouterRejectsRunningNode(t *testing.T) {
	ctx := context.Background()
	store, svc, run, root := newWorkflowCommentFixture(t, "task-model-svc-run", "run-model-svc-run", twoAgentGraph())
	startWorkflowAgentNode(t, store, run.ID, "execute", "sess-execute")
	err := svc.SetWorkflowNodeModelForRouter(ctx, root.ID, run.TaskID, "execute", "sonnet", "")
	if err == nil || !strings.Contains(err.Error(), "运行中") {
		t.Fatalf("err = %v, want a running-node refusal", err)
	}
}

// 跳过还没跑过的节点：写一行 skipped、投影同步收尾，下游随即可 ready。
func TestSkipWorkflowNodeForRouterUnblocksDownstream(t *testing.T) {
	ctx := context.Background()
	store, svc, run, root := newWorkflowCommentFixture(t, "task-skip", "run-skip", twoAgentGraph())
	runner := &scriptedWorkflowNodeRunner{outcomes: []WorkflowNodeResult{{Outcome: WorkflowNodeDone, Output: map[string]any{"summary": "ok"}}}}
	svc.SetWorkflowNodeRunner(runner)
	if err := svc.SkipWorkflowNodeForRouter(ctx, root.ID, run.TaskID, "execute", "这一步没必要跑"); err != nil {
		t.Fatalf("SkipWorkflowNodeForRouter: %v", err)
	}
	if err := svc.WaitWorkflowRunIdle(ctx, root.ID, run.TaskID); err != nil {
		t.Fatalf("WaitWorkflowRunIdle: %v", err)
	}
	if runner.runCalls != 1 {
		t.Fatalf("下游节点被派发 %d 次，want 1（跳过不能让整轮停摆）", runner.runCalls)
	}
	fresh, err := store.WorkflowRunByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("WorkflowRunByID: %v", err)
	}
	if st := fresh.NodeStates["execute"]; st != NodeRunSkipped {
		t.Fatalf("node state = %q, want skipped", st)
	}
	rows, err := store.ListWorkflowNodeRuns(ctx, run.ID)
	if err != nil {
		t.Fatalf("ListWorkflowNodeRuns: %v", err)
	}
	var skipped *NodeRun
	for i := range rows {
		if rows[i].NodeID == "execute" {
			skipped = &rows[i]
		}
	}
	if skipped == nil || skipped.Status != NodeRunSkipped {
		t.Fatalf("skipped row = %+v", skipped)
	}
	if st := fresh.NodeStates["verify"]; st != NodeRunSuccess {
		t.Fatalf("verify projection = %q, want success (states=%v)", st, fresh.NodeStates)
	}
}

func TestSkipWorkflowNodeForRouterRejectsRunningNode(t *testing.T) {
	ctx := context.Background()
	store, svc, run, root := newWorkflowCommentFixture(t, "task-skip-run", "run-skip-run", twoAgentGraph())
	startWorkflowAgentNode(t, store, run.ID, "execute", "sess-execute")
	err := svc.SkipWorkflowNodeForRouter(ctx, root.ID, run.TaskID, "execute", "")
	if err == nil || !strings.Contains(err.Error(), "运行中") {
		t.Fatalf("err = %v, want a running-node refusal", err)
	}
}

// inspect_run 带 node_id 时只回该节点的详情（最近一次 attempt 的产出），
// 不再把整轮节点清单重复一遍。
func TestInspectRunScopesToNode(t *testing.T) {
	ctx := context.Background()
	store, _, run, _ := newWorkflowCommentFixture(t, "task-inspect-node", "run-inspect-node", twoAgentGraph())
	nr := startWorkflowAgentNode(t, store, run.ID, "execute", "sess-execute")
	if _, err := store.MarkWorkflowNodeRunUnknown(ctx, nr, "上游 502，回合未完成"); err != nil {
		t.Fatalf("MarkWorkflowNodeRunUnknown: %v", err)
	}
	env := nodeAdminToolEnv(t, store, run.TaskID)
	tool := toolByName(t, env, "router_inspect_run")
	raw, err := tool.Handler(ctx, json.RawMessage(`{"node_id":"execute"}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("payload: %v (%s)", err, raw)
	}
	node, ok := payload["node"].(map[string]any)
	if !ok {
		t.Fatalf("payload = %s, want node detail", raw)
	}
	if node["node_id"] != "execute" || node["status"] != NodeRunUnknown {
		t.Fatalf("node = %+v", node)
	}
	if summary, _ := node["summary"].(string); !strings.Contains(summary, "502") {
		t.Fatalf("summary = %q, want the last output", summary)
	}
	if _, hasList := payload["nodes"]; hasList {
		t.Fatalf("payload still carries the full node list: %s", raw)
	}
	// 未知节点名要报错，而不是悄悄返回空详情。
	if _, err := tool.Handler(ctx, json.RawMessage(`{"node_id":"nope"}`)); err == nil {
		t.Fatal("unknown node accepted")
	}
}

// 跳过只用于「还没开始执行」的节点：失败过的节点已经有 attempt，跳过不会让下游
// 动起来（它需要的是重试或改档），所以直接拒绝并说明。
func TestSkipWorkflowNodeForRouterRejectsStartedNode(t *testing.T) {
	ctx := context.Background()
	store, svc, run, root := newWorkflowCommentFixture(t, "task-skip-ran", "run-skip-ran", twoAgentGraph())
	nr := startWorkflowAgentNode(t, store, run.ID, "execute", "sess-execute")
	if _, err := store.MarkWorkflowNodeRunUnknown(ctx, nr, "上游 502"); err != nil {
		t.Fatalf("MarkWorkflowNodeRunUnknown: %v", err)
	}
	err := svc.SkipWorkflowNodeForRouter(ctx, root.ID, run.TaskID, "execute", "")
	if err == nil || !strings.Contains(err.Error(), "已经跑过") {
		t.Fatalf("err = %v, want an already-ran refusal", err)
	}
}

// apply 前复核：卡片是工具那一刻的快照，节点可能已经跑起来了。校验层必须按
// 同一份卡片口径再拒一次，而不是把越界动作交给 apply。
func TestValidateRouterActionsRejectsRunningNodeCard(t *testing.T) {
	store, _, run, _ := newWorkflowCommentFixture(t, "task-validate-run", "run-validate-run", twoAgentGraph())
	startWorkflowAgentNode(t, store, run.ID, "execute", "sess-execute")
	brief := briefFor(t, store, run.TaskID)
	err := validateRouterActions(brief, []RouterAction{{
		Seq: 0, Kind: RouterActionSetNodeModel, NodeID: "execute", Model: "sonnet",
	}}, RouterConfig{}.Normalized())
	if err == nil || !strings.Contains(err.Error(), "运行中") {
		t.Fatalf("err = %v, want a running-node refusal", err)
	}
	// 未跑过的节点则通过。
	err = validateRouterActions(brief, []RouterAction{{
		Seq: 0, Kind: RouterActionSetNodeModel, NodeID: "verify", Model: "sonnet",
	}}, RouterConfig{}.Normalized())
	if err != nil {
		t.Fatalf("pending node rejected: %v", err)
	}
}

type stubNodeAdmin struct {
	modelCalls []string
	skipCalls  []string
}

func (s *stubNodeAdmin) SetWorkflowNodeModelForRouter(ctx context.Context, rootID, taskID, nodeID, model, reason string) error {
	s.modelCalls = append(s.modelCalls, taskID+"/"+nodeID+"/"+model)
	return nil
}

func (s *stubNodeAdmin) SkipWorkflowNodeForRouter(ctx context.Context, rootID, taskID, nodeID, reason string) error {
	s.skipCalls = append(s.skipCalls, taskID+"/"+nodeID)
	return nil
}

// 两个动作都要真的转到端口上，并各留一条用户看得见的系统行；端口没装配时
// 明确失败，而不是静默什么都不做。
func TestApplyNodeAdminActionsCallPortAndLeaveNote(t *testing.T) {
	ctx := context.Background()
	store, _, run, root := newWorkflowCommentFixture(t, "task-apply-admin", "run-apply-admin", twoAgentGraph())
	port := &stubNodeAdmin{}
	svc := &ActivityService{activities: NewActivityStore(store.db)}
	svc.SetNodeAdmin(port)
	decision := &RouterDecision{ID: "dec-admin", TaskID: run.TaskID, RootID: root.ID}
	task := Task{ID: run.TaskID, RootID: root.ID}

	if err := svc.applyRouteAction(ctx, decision, task, nil, nil, RouterAction{
		Seq: 0, Kind: RouterActionSetNodeModel, NodeID: "execute", Model: "sonnet",
	}); err != nil {
		t.Fatalf("apply set_node_model: %v", err)
	}
	if err := svc.applyRouteAction(ctx, decision, task, nil, nil, RouterAction{
		Seq: 1, Kind: RouterActionSkipNode, NodeID: "verify",
	}); err != nil {
		t.Fatalf("apply skip_node: %v", err)
	}
	if len(port.modelCalls) != 1 || port.modelCalls[0] != run.TaskID+"/execute/sonnet" {
		t.Fatalf("model calls = %v", port.modelCalls)
	}
	if len(port.skipCalls) != 1 || port.skipCalls[0] != run.TaskID+"/verify" {
		t.Fatalf("skip calls = %v", port.skipCalls)
	}
	rows, err := store.db.QueryContext(ctx, `SELECT body FROM task_activities WHERE task_id=? ORDER BY id`, run.TaskID)
	if err != nil {
		t.Fatalf("query activities: %v", err)
	}
	defer rows.Close()
	var bodies []string
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			t.Fatalf("scan: %v", err)
		}
		bodies = append(bodies, body)
	}
	if len(bodies) != 2 || !strings.Contains(bodies[0], "sonnet") || !strings.Contains(bodies[1], "跳过") {
		t.Fatalf("activity notes = %v", bodies)
	}

	// 端口没装配：两个动作都要明确报错。
	bare := &ActivityService{activities: NewActivityStore(store.db)}
	if err := bare.applyRouteAction(ctx, decision, task, nil, nil, RouterAction{Seq: 0, Kind: RouterActionSkipNode, NodeID: "verify"}); err == nil {
		t.Fatal("skip_node without port accepted")
	}
}

// 「改 workflow」（改这一轮 Run 的图）与改档同一条规矩：运行中不切换。节点已经有
// attempt 行、投影还停在 pending 时也必须挡住 —— 证据在行上，不在投影上。
func TestEditWorkflowRunGraphRejectsRunningNodeModel(t *testing.T) {
	ctx := context.Background()
	store, _, run, _ := newWorkflowCommentFixture(t, "task-graph-edit", "run-graph-edit", twoAgentGraph())
	startWorkflowAgentNode(t, store, run.ID, "execute", "sess-execute")
	fresh, err := store.WorkflowRunByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("WorkflowRunByID: %v", err)
	}
	edited := fresh.Graph
	edited.Nodes = append([]WorkflowNode(nil), edited.Nodes...)
	for i := range edited.Nodes {
		if edited.Nodes[i].ID == "execute" {
			params := make(map[string]any, len(edited.Nodes[i].Params)+1)
			for k, v := range edited.Nodes[i].Params {
				params[k] = v
			}
			params["model"] = "sonnet"
			edited.Nodes[i].Params = params
		}
	}
	err = store.EditWorkflowRunGraph(ctx, fresh.ID, fresh.Revision, edited)
	if !errors.Is(err, ErrFrozenNodeEdit) {
		t.Fatalf("err = %v, want ErrFrozenNodeEdit（运行中不切换）", err)
	}
	// 还没跑过的节点不受影响：运行中只冻结已经开始的那部分。
	pending := fresh.Graph
	pending.Nodes = append([]WorkflowNode(nil), pending.Nodes...)
	for i := range pending.Nodes {
		if pending.Nodes[i].ID == "verify" {
			params := make(map[string]any, len(pending.Nodes[i].Params)+1)
			for k, v := range pending.Nodes[i].Params {
				params[k] = v
			}
			params["model"] = "haiku"
			pending.Nodes[i].Params = params
		}
	}
	if err := store.EditWorkflowRunGraph(ctx, fresh.ID, fresh.Revision, pending); err != nil {
		t.Fatalf("待跑节点被误挡：%v", err)
	}
}
