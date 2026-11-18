package kanban

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// 运行期事件 brief 的证据区块（R1 T8）。这批用例只钉行为：事件 brief 必须描述
// 事件自己的 Run 与真实节点输出，不能拿任务当前 Run 顶替，也不能无界放大。

func eventBriefTrigger(f *coordinateFixture, ev RouterEvent) RouterTrigger {
	return RouterTrigger{
		Kind:           triggerKindForEventKind(ev.Kind),
		TaskID:         ev.TaskID,
		RootID:         f.root.ID,
		UserActivityID: ev.UserActivityID,
		TurnID:         ev.TurnID,
		Mode:           RouterModeCoordinate,
		Event:          &ev,
	}
}

func triggerKindForEventKind(kind RouterEventKind) RouterTriggerKind {
	switch kind {
	case RouterEventRunSettled:
		return RouterTriggerRunSettled
	case RouterEventRunFailed:
		return RouterTriggerRunFailed
	case RouterEventGateWaiting:
		return RouterTriggerGateWaiting
	case RouterEventRunStalled:
		return RouterTriggerRunStalled
	default:
		return RouterTriggerStartup
	}
}

// eventBriefFixture 是「一个已结算 Run + 一条原始留言」的最小现场。
func eventBriefFixture(t *testing.T) (*coordinateFixture, string) {
	t.Helper()
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)
	user := f.addUserActivity(t, "uact-event-brief")
	return f, user
}

func evidenceNodesByID(brief *SituationBrief) map[string]RouterBriefEvidenceNode {
	out := map[string]RouterBriefEvidenceNode{}
	if brief == nil || brief.Evidence == nil {
		return out
	}
	for _, node := range brief.Evidence.Nodes {
		out[node.NodeID] = node
	}
	return out
}

// 行为 1：完成摘要证据。事件三段齐全、原始要求在、两个成功节点的可读输出都在，
// 事件回合不得给出别的 Run / 活跃执行 / workflow 列表。
func TestEventBriefSettledSummary(t *testing.T) {
	ctx := context.Background()
	f, user := eventBriefFixture(t)
	f.addRun(t, "task-1", "run-settled", WorkflowTriggeredByUser, map[string]string{
		"agent-1": NodeRunSuccess, "agent-2": NodeRunSuccess,
	})
	f.addNodeRun(t, NodeRun{ID: "nr-settled-1", RunID: "run-settled", NodeID: "agent-1", Attempt: 1,
		Status: NodeRunSuccess, SessionKey: "sess-1", Output: map[string]any{"summary": "改好了登录页"}})
	f.addNodeRun(t, NodeRun{ID: "nr-settled-2", RunID: "run-settled", NodeID: "agent-2", Attempt: 1,
		Status: NodeRunSuccess, Output: map[string]any{"summary": "补了三条测试"}})
	f.setTaskStatus(t, "task-1", StatusSuccess)
	ev := RouterEvent{
		ID: "revt-settled", RootID: f.root.ID, TaskID: "task-1", RunID: "run-settled",
		Kind: RouterEventRunSettled, TurnID: "revt-turn:settled", UserActivityID: user,
		PayloadJSON: `{"run_id":"run-settled","status":"success"}`,
	}
	brief, err := f.router.turnBrief(ctx, "task-1", eventBriefTrigger(f, ev), RouterConfig{})
	must(t, err, "turnBrief")

	if brief.Event == nil {
		t.Fatalf("event brief has no event section: %#v", brief)
	}
	if brief.Event.ID != ev.ID || brief.Event.Kind != string(RouterEventRunSettled) || brief.Event.RunID != "run-settled" {
		t.Fatalf("event = %#v, want id/kind/run of the settled event", brief.Event)
	}
	if brief.Trigger.Ref != ev.ID {
		t.Fatalf("trigger.ref = %q, want the event id %q", brief.Trigger.Ref, ev.ID)
	}
	if brief.Trigger.UserMessage == nil || brief.Trigger.UserMessage.Body != "原始需求" {
		t.Fatalf("trigger.user_message = %#v, want the original user message", brief.Trigger.UserMessage)
	}
	if brief.Evidence == nil {
		t.Fatalf("event brief has no evidence section: %#v", brief)
	}
	if brief.Evidence.TaskStatus != StatusSuccess || brief.Evidence.RunStatus != "settled" {
		t.Fatalf("evidence status = %q/%q, want %q/settled", brief.Evidence.TaskStatus, brief.Evidence.RunStatus, StatusSuccess)
	}
	nodes := evidenceNodesByID(brief)
	first, ok := nodes["agent-1"]
	if !ok || !strings.Contains(first.OutputExcerpt, "改好了登录页") {
		t.Fatalf("agent-1 evidence = %#v (all=%#v), want its readable output", first, brief.Evidence.Nodes)
	}
	second, ok := nodes["agent-2"]
	if !ok || !strings.Contains(second.OutputExcerpt, "补了三条测试") {
		t.Fatalf("agent-2 evidence = %#v, want its readable output", second)
	}
	if brief.Budget == nil {
		t.Fatalf("event brief has no budget section: %#v", brief)
	}
	if brief.Budget.MaxTurns != RouterCoordinateMaxTurnsPerUserActivity || brief.Budget.TurnsUsed != 1 ||
		brief.Budget.TurnsRemaining != RouterCoordinateMaxTurnsPerUserActivity-1 ||
		brief.Budget.MaxChainDepth != routerEventMaxChainDepth {
		t.Fatalf("budget = %#v, want first turn of %d", brief.Budget, RouterCoordinateMaxTurnsPerUserActivity)
	}
	if brief.LatestRun != nil || brief.ActiveExecution != nil || len(brief.Workflows) != 0 {
		t.Fatalf("event turn leaks other runs/workflows: latest=%#v active=%#v workflows=%#v",
			brief.LatestRun, brief.ActiveExecution, brief.Workflows)
	}
}

// 行为 2：失败原因证据。失败节点必须带错误摘要，Run 状态是 failed 而不是任务状态。
func TestEventBriefFailedError(t *testing.T) {
	ctx := context.Background()
	f, user := eventBriefFixture(t)
	f.addRun(t, "task-1", "run-failed", WorkflowTriggeredByUser, map[string]string{"agent-1": NodeRunFail})
	f.addNodeRun(t, NodeRun{ID: "nr-failed-1", RunID: "run-failed", NodeID: "agent-1", Attempt: 2,
		Status: NodeRunFail, Output: map[string]any{"error": "编译失败：unused import"}})
	f.setTaskStatus(t, "task-1", StatusFail)
	ev := RouterEvent{
		ID: "revt-failed", RootID: f.root.ID, TaskID: "task-1", RunID: "run-failed",
		Kind: RouterEventRunFailed, TurnID: "revt-turn:failed", UserActivityID: user, Attempt: 2,
		PayloadJSON: `{"run_id":"run-failed","status":"failed","error":"编译失败：unused import"}`,
	}
	brief, err := f.router.turnBrief(ctx, "task-1", eventBriefTrigger(f, ev), RouterConfig{})
	must(t, err, "turnBrief")

	if brief.Event == nil || brief.Evidence == nil {
		t.Fatalf("event/evidence section missing: %#v", brief)
	}
	if brief.Evidence.RunStatus != "failed" {
		t.Fatalf("evidence.run_status = %q, want failed", brief.Evidence.RunStatus)
	}
	node, ok := evidenceNodesByID(brief)["agent-1"]
	if !ok {
		t.Fatalf("failed node missing from evidence: %#v", brief.Evidence.Nodes)
	}
	if node.Status != NodeRunFail || !strings.Contains(node.ErrorExcerpt, "unused import") {
		t.Fatalf("failed node evidence = %#v, want error excerpt", node)
	}
	if node.Attempt != 2 {
		t.Fatalf("failed node attempt = %d, want 2", node.Attempt)
	}
}

// 行为 3：gate 提醒证据。gate_waiting 事件必须带 waiting_user 节点，且策略提示
// 明确 Router 不能代批 HumanGate。
func TestEventBriefGateWaiting(t *testing.T) {
	ctx := context.Background()
	f, user := eventBriefFixture(t)
	f.addRun(t, "task-1", "run-gate", WorkflowTriggeredByUser, map[string]string{
		"agent-1": NodeRunSuccess, "gate-1": NodeRunWaitingUser,
	})
	f.addNodeRun(t, NodeRun{ID: "nr-gate-1", RunID: "run-gate", NodeID: "gate-1", Attempt: 1, Status: NodeRunWaitingUser})
	f.setTaskStatus(t, "task-1", StatusWaitingUser)
	ev := RouterEvent{
		ID: "revt-gate", RootID: f.root.ID, TaskID: "task-1", RunID: "run-gate",
		Kind: RouterEventGateWaiting, TurnID: "revt-turn:gate", UserActivityID: user, Attempt: 1,
		PayloadJSON: `{"run_id":"run-gate","gate_node_id":"gate-1","node_run_id":"nr-gate-1","status":"waiting_user"}`,
	}
	brief, err := f.router.turnBrief(ctx, "task-1", eventBriefTrigger(f, ev), RouterConfig{})
	must(t, err, "turnBrief")

	if brief.Evidence == nil {
		t.Fatalf("event brief has no evidence section: %#v", brief)
	}
	if brief.Evidence.RunStatus != "gate_waiting" {
		t.Fatalf("evidence.run_status = %q, want gate_waiting", brief.Evidence.RunStatus)
	}
	node, ok := evidenceNodesByID(brief)["gate-1"]
	if !ok || node.Status != NodeRunWaitingUser {
		t.Fatalf("gate evidence = %#v (all=%#v), want a waiting_user node", node, brief.Evidence.Nodes)
	}
	if node.Kind != NodeKindHumanGate {
		t.Fatalf("gate node kind = %q, want %q", node.Kind, NodeKindHumanGate)
	}
	if hints := strings.Join(brief.PolicyHints, " "); !strings.Contains(hints, "不能批准 HumanGate") {
		t.Fatalf("policy hints = %#v, want an explicit HumanGate approval ban", brief.PolicyHints)
	}
}

// 行为 4：有界裁剪。25 个节点、超长输出、超大 payload 都必须被压进上限，
// 并且明确标出截断，而不是静默丢证据。
func TestEventBriefBoundedTruncation(t *testing.T) {
	ctx := context.Background()
	f, user := eventBriefFixture(t)
	graph := wfDef()
	states := map[string]string{}
	for i := 0; i < 25; i++ {
		id := fmt.Sprintf("node-%02d", i)
		graph.Nodes = append(graph.Nodes, wfNode(id, NodeKindAgent))
		states[id] = NodeRunSuccess
	}
	must(t, f.store.CreateWorkflowRunAndSetEngine(ctx, &WorkflowRun{
		ID: "run-big", TaskID: "task-1", Revision: 1, Graph: graph, NodeStates: states,
	}), "create big run")
	for i := 0; i < 25; i++ {
		id := fmt.Sprintf("node-%02d", i)
		f.addNodeRun(t, NodeRun{ID: "nr-" + id, RunID: "run-big", NodeID: id, Attempt: 1, Status: NodeRunSuccess,
			Output: map[string]any{"summary": strings.Repeat("很长的节点输出", 500)}})
	}
	f.setTaskStatus(t, "task-1", StatusSuccess)
	ev := RouterEvent{
		ID: "revt-big", RootID: f.root.ID, TaskID: "task-1", RunID: "run-big",
		Kind: RouterEventRunSettled, TurnID: "revt-turn:big", UserActivityID: user,
		PayloadJSON: `{"run_id":"run-big","secret":"` + strings.Repeat("x", 3000) + `","error":"` + strings.Repeat("y", 3000) + `"}`,
	}
	brief, err := f.router.turnBrief(ctx, "task-1", eventBriefTrigger(f, ev), RouterConfig{})
	must(t, err, "turnBrief")

	if brief.Event == nil || len(brief.Event.Payload) == 0 {
		t.Fatalf("event payload missing: %#v", brief.Event)
	}
	if !brief.Event.Truncated {
		t.Fatalf("event payload with unknown keys/oversize must be marked truncated: %#v", brief.Event)
	}
	allowed := map[string]bool{"run_id": true, "task_id": true, "node_id": true, "node_run_id": true,
		"gate_node_id": true, "status": true, "reason": true, "error": true, "attempt": true, "since": true}
	for key := range brief.Event.Payload {
		if !allowed[key] {
			t.Fatalf("payload key %q is not whitelisted: %#v", key, brief.Event.Payload)
		}
	}
	if str, _ := brief.Event.Payload["error"].(string); len(str) > routerEventBriefPayloadValueLimit+len("…") {
		t.Fatalf("payload error value length = %d, want truncated to %d bytes", len(str), routerEventBriefPayloadValueLimit)
	}
	if brief.Evidence == nil {
		t.Fatalf("event brief has no evidence section: %#v", brief)
	}
	if !brief.Evidence.Truncated {
		t.Fatalf("evidence with 25 nodes must be marked truncated: %#v", brief.Evidence)
	}
	if len(brief.Evidence.Nodes) > routerEventBriefMaxNodes {
		t.Fatalf("evidence nodes = %d, want <= %d", len(brief.Evidence.Nodes), routerEventBriefMaxNodes)
	}
	if brief.Evidence.OmittedNodes <= 0 {
		t.Fatalf("evidence omitted_nodes = %d, want the dropped count", brief.Evidence.OmittedNodes)
	}
	for _, node := range brief.Evidence.Nodes {
		if len(node.OutputExcerpt) > routerEventBriefOutputLimit+len("…") {
			t.Fatalf("node %s excerpt length = %d bytes, want <= %d", node.NodeID, len(node.OutputExcerpt), routerEventBriefOutputLimit)
		}
	}
	if size := briefSize(brief); size > routerBriefMaxBytes {
		t.Fatalf("serialized brief = %d bytes, want <= %d", size, routerBriefMaxBytes)
	}
}

// 行为 4b：未验证项的裁剪必须留下「还有 N 条」标记。证据不全要能看出来，
// 不能裁成恰好 5 条就装完整。
func TestEventBriefUnverifiedTruncation(t *testing.T) {
	ctx := context.Background()
	f, user := eventBriefFixture(t)
	graph := wfDef()
	states := map[string]string{}
	for i := 0; i < 25; i++ {
		id := fmt.Sprintf("node-%02d", i)
		graph.Nodes = append(graph.Nodes, wfNode(id, NodeKindAgent))
		states[id] = NodeRunSuccess
	}
	must(t, f.store.CreateWorkflowRunAndSetEngine(ctx, &WorkflowRun{
		ID: "run-novout", TaskID: "task-1", Revision: 1, Graph: graph, NodeStates: states,
	}), "create silent run")
	for i := 0; i < 25; i++ {
		id := fmt.Sprintf("node-%02d", i)
		// 成功但没有记录输出：每条都是一个未验证项。
		f.addNodeRun(t, NodeRun{ID: "nr-" + id, RunID: "run-novout", NodeID: id, Attempt: 1, Status: NodeRunSuccess})
	}
	f.setTaskStatus(t, "task-1", StatusSuccess)
	ev := RouterEvent{
		ID: "revt-novout", RootID: f.root.ID, TaskID: "task-1", RunID: "run-novout",
		Kind: RouterEventRunSettled, TurnID: "revt-turn:novout", UserActivityID: user,
		PayloadJSON: `{"run_id":"run-novout"}`,
	}
	brief, err := f.router.turnBrief(ctx, "task-1", eventBriefTrigger(f, ev), RouterConfig{})
	must(t, err, "turnBrief")

	if brief.Evidence == nil || len(brief.Evidence.Unverified) == 0 {
		t.Fatalf("evidence lost its unverified items: %#v", brief.Evidence)
	}
	// 常规上限是 10 条 + 「还有 N 条」标记；5 条是 24KB 紧急裁剪路径。
	if len(brief.Evidence.Unverified) > routerEventBriefUnverifiedLimit+1 {
		t.Fatalf("unverified items = %d, want <= %d plus marker",
			len(brief.Evidence.Unverified), routerEventBriefUnverifiedLimit)
	}
	last := brief.Evidence.Unverified[len(brief.Evidence.Unverified)-1]
	if !strings.Contains(last, "more unverified items") {
		t.Fatalf("unverified omission marker missing, last = %q (all=%#v)", last, brief.Evidence.Unverified)
	}
}

// 行为 4c：24KB 紧急裁剪把未验证项压到 5 条时，标记必须保留。
func TestTrimRouterEventUnverifiedKeepsMarker(t *testing.T) {
	brief := &SituationBrief{
		Event: &RouterBriefEvent{ID: "revt-marker"},
		Evidence: &RouterBriefEvidence{Unverified: []string{
			"a", "b", "c", "d", "e", "f", "g", "h",
		}},
	}
	if !trimRouterEventUnverified(brief) {
		t.Fatal("trim reported no change for 8 items")
	}
	if len(brief.Evidence.Unverified) != routerEventBriefKeptUnverifd {
		t.Fatalf("unverified items = %d, want %d", len(brief.Evidence.Unverified), routerEventBriefKeptUnverifd)
	}
	last := brief.Evidence.Unverified[len(brief.Evidence.Unverified)-1]
	if last != "… and 4 more unverified items" {
		t.Fatalf("last unverified item = %q, want the omission marker", last)
	}
	if !brief.Evidence.Truncated {
		t.Fatal("trim must set evidence.truncated")
	}
}

// 行为 5：旧事件不串话。事件指向 run-old 时证据只能来自 run-old，即使任务当前
// 引擎已经换成 run-new。
func TestEventBriefOldRunNotCrossTalk(t *testing.T) {
	ctx := context.Background()
	f, user := eventBriefFixture(t)
	// 旧 Run 先落成终态历史，再让任务引擎切到 run-new：这正是「事件还指向旧
	// Run、任务已经跑了新一轮」的现场。
	f.addRun(t, "task-1", "run-old", WorkflowTriggeredByUser, map[string]string{
		"agent-1": NodeRunSuccess, "gate-1": NodeRunSuccess, "agent-2": NodeRunSuccess,
	})
	f.addNodeRun(t, NodeRun{ID: "nr-old", RunID: "run-old", NodeID: "agent-1", Attempt: 1,
		Status: NodeRunSuccess, Output: map[string]any{"summary": "旧 Run 的输出"}})
	f.addRun(t, "task-1", "run-new", WorkflowTriggeredByUser, map[string]string{"agent-2": NodeRunSuccess})
	f.addNodeRun(t, NodeRun{ID: "nr-new", RunID: "run-new", NodeID: "agent-2", Attempt: 1,
		Status: NodeRunSuccess, Output: map[string]any{"summary": "新 Run 的输出"}})
	current, err := f.store.WorkflowRunForTask(ctx, "task-1")
	must(t, err, "WorkflowRunForTask")
	if current.ID != "run-new" {
		t.Fatalf("fixture precondition: current engine = %q, want run-new", current.ID)
	}
	f.setTaskStatus(t, "task-1", StatusSuccess)
	ev := RouterEvent{
		ID: "revt-old", RootID: f.root.ID, TaskID: "task-1", RunID: "run-old",
		Kind: RouterEventRunSettled, TurnID: "revt-turn:old", UserActivityID: user,
		PayloadJSON: `{"run_id":"run-old"}`,
	}
	brief, err := f.router.turnBrief(ctx, "task-1", eventBriefTrigger(f, ev), RouterConfig{})
	must(t, err, "turnBrief")

	if brief.Event == nil || brief.Event.RunID != "run-old" {
		t.Fatalf("event.run_id = %#v, want run-old", brief.Event)
	}
	if brief.LatestRun != nil {
		t.Fatalf("event turn still exposes the newest run: %#v", brief.LatestRun)
	}
	if brief.Evidence == nil {
		t.Fatalf("event brief has no evidence section: %#v", brief)
	}
	nodes := evidenceNodesByID(brief)
	if len(nodes) != 1 || nodes["agent-1"].OutputExcerpt == "" {
		t.Fatalf("evidence nodes = %#v, want only run-old's agent-1", brief.Evidence.Nodes)
	}
	if _, leaked := nodes["agent-2"]; leaked {
		t.Fatalf("evidence leaked the current run's node: %#v", brief.Evidence.Nodes)
	}
}

// 行为 6：生产载荷不算截断。routerEventPayload 自己写的摘要键（nodes /
// workflow_id / revision / nodes_truncated）是受控摘要，不属于「未知键」，不得
// 仅因它们存在就让 brief 报 truncated —— 否则每条真实事件都会在用户可见汇报
// 里多出一句「事件载荷被截断」，而实际信息完整。
func TestEventBriefProductionPayloadIsNotTruncated(t *testing.T) {
	ctx := context.Background()
	f, user := eventBriefFixture(t)
	f.addRun(t, "task-1", "run-prod", WorkflowTriggeredByUser, map[string]string{"agent-1": NodeRunSuccess})
	f.addNodeRun(t, NodeRun{ID: "nr-prod", RunID: "run-prod", NodeID: "agent-1", Attempt: 1,
		Status: NodeRunSuccess, Output: map[string]any{"summary": "改好了"}})
	f.setTaskStatus(t, "task-1", StatusSuccess)
	payload := `{"nodes":{"agent-1":"success"},"revision":3,"run_id":"run-prod","task_id":"task-1","workflow_id":"wf-1"}`
	ev := RouterEvent{
		ID: "revt-prod", RootID: f.root.ID, TaskID: "task-1", RunID: "run-prod",
		Kind: RouterEventRunSettled, TurnID: "revt-turn:prod", UserActivityID: user,
		PayloadJSON: payload,
	}
	brief, err := f.router.turnBrief(ctx, "task-1", eventBriefTrigger(f, ev), RouterConfig{})
	must(t, err, "turnBrief")

	if brief.Event == nil {
		t.Fatalf("event brief has no event section")
	}
	if brief.Event.Truncated {
		t.Fatalf("controlled summary keys must not mark the payload truncated: %#v", brief.Event)
	}
	for _, item := range brief.Evidence.Unverified {
		if strings.Contains(item, "payload was truncated") {
			t.Fatalf("no truncation caveat expected, got unverified=%#v", brief.Evidence.Unverified)
		}
	}
	// 受控摘要键不得漏进给模型的载荷（brief 只放白名单字段）。
	for _, key := range []string{"nodes", "workflow_id", "revision"} {
		if _, ok := brief.Event.Payload[key]; ok {
			t.Fatalf("summary key %q leaked into brief payload: %#v", key, brief.Event.Payload)
		}
	}
}

// 行为 6b：停摆与启动对账的载荷同样是生产端自己写的（stall 带 started_at，
// reconcile 带 open_node_runs/resumed/unknown）。它们不是白名单字段、不进 brief，
// 但也绝不能算「未知键」—— 否则这两类事件的汇报里会凭空出现「载荷被截断」。
func TestEventBriefStallAndReconcilePayloadsAreNotTruncated(t *testing.T) {
	ctx := context.Background()
	f, user := eventBriefFixture(t)
	f.addRun(t, "task-1", "run-stall", WorkflowTriggeredByUser, map[string]string{"agent-1": NodeRunRunning})
	f.addNodeRun(t, NodeRun{ID: "nr-stall", RunID: "run-stall", NodeID: "agent-1", Attempt: 1, Status: NodeRunRunning})
	cases := map[string]struct {
		kind    RouterEventKind
		payload string
	}{
		"stall": {
			kind:    RouterEventRunStalled,
			payload: `{"run_id":"run-stall","task_id":"task-1","nodes":{"agent-1":"running"},"node_id":"agent-1","node_run_id":"nr-stall","attempt":1,"started_at":"2026-11-19T10:00:00Z"}`,
		},
		"reconcile": {
			kind:    RouterEventStartupReconcile,
			payload: `{"run_id":"run-stall","task_id":"task-1","nodes":{},"open_node_runs":1,"resumed":1,"unknown":0}`,
		},
	}
	for name, tc := range cases {
		ev := RouterEvent{
			ID: "revt-" + name, RootID: f.root.ID, TaskID: "task-1", RunID: "run-stall",
			Kind: tc.kind, TurnID: "revt-turn:" + name, UserActivityID: user,
			PayloadJSON: tc.payload,
		}
		brief, err := f.router.turnBrief(ctx, "task-1", eventBriefTrigger(f, ev), RouterConfig{})
		must(t, err, "turnBrief %s", name)
		if brief.Event == nil {
			t.Fatalf("%s: event brief has no event section", name)
		}
		if brief.Event.Truncated {
			t.Fatalf("%s: producer payload must not be flagged truncated: %#v", name, brief.Event.Payload)
		}
		for _, item := range brief.Evidence.Unverified {
			if strings.Contains(item, "payload was truncated") {
				t.Fatalf("%s: no truncation caveat expected, got unverified=%#v", name, brief.Evidence.Unverified)
			}
		}
	}
}

// 行为 7：真截断仍然报。未知键 / 超长值 / 坏 JSON 都必须照旧标 truncated，
// 不能因为放行受控摘要键就把「证据真的不全」也一起吞掉。
func TestEventBriefUnknownKeysStillTruncated(t *testing.T) {
	ctx := context.Background()
	f, user := eventBriefFixture(t)
	f.addRun(t, "task-1", "run-unk", WorkflowTriggeredByUser, map[string]string{"agent-1": NodeRunSuccess})
	f.setTaskStatus(t, "task-1", StatusSuccess)
	cases := map[string]string{
		"unknown-key": `{"run_id":"run-unk","internal_secret":"x"}`,
		"oversize":    `{"run_id":"run-unk","error":"` + strings.Repeat("y", 3000) + `"}`,
		"broken-json": `{"run_id":`,
	}
	for name, payload := range cases {
		ev := RouterEvent{
			ID: "revt-unk-" + name, RootID: f.root.ID, TaskID: "task-1", RunID: "run-unk",
			Kind: RouterEventRunSettled, TurnID: "revt-turn:" + name, UserActivityID: user,
			PayloadJSON: payload,
		}
		brief, err := f.router.turnBrief(ctx, "task-1", eventBriefTrigger(f, ev), RouterConfig{})
		must(t, err, "turnBrief %s", name)
		if brief.Event == nil || !brief.Event.Truncated {
			t.Fatalf("%s: payload must stay truncated: %#v", name, brief.Event)
		}
	}
}
