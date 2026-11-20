package kanban

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Router 主动监管 S0：观测不改任何状态，只把「派出去的 worker 现在怎么样」
// 整理成结构化事实，挂到 run_failed / run_stalled 事件上。

func TestRouterSupervisionConfigDefaultsAndNormalization(t *testing.T) {
	off := RouterConfig{}.Normalized().Supervision
	if off.Mode != RouterSupervisionOff {
		t.Fatalf("default supervision mode = %q, want off", off.Mode)
	}
	if off.MaxRetriesPerNode != defaultSupervisionMaxRetriesPerNode {
		t.Fatalf("default retries = %d, want %d", off.MaxRetriesPerNode, defaultSupervisionMaxRetriesPerNode)
	}
	if off.MaxActionsPerHour != defaultSupervisionMaxActionsPerHour {
		t.Fatalf("default actions/hour = %d, want %d", off.MaxActionsPerHour, defaultSupervisionMaxActionsPerHour)
	}
	on := RouterConfig{Supervision: RouterSupervisionConfig{
		Mode: RouterSupervisionOn, MaxRetriesPerNode: 1, MaxActionsPerHour: 3,
	}}.Normalized().Supervision
	if on.Mode != RouterSupervisionOn || on.MaxRetriesPerNode != 1 || on.MaxActionsPerHour != 3 {
		t.Fatalf("normalized = %+v", on)
	}
	unknown := RouterConfig{Supervision: RouterSupervisionConfig{Mode: "banana"}}.Normalized().Supervision
	if unknown.Mode != RouterSupervisionOff {
		t.Fatalf("unknown mode = %q, want off", unknown.Mode)
	}
}

// 行为：开放节点带水位（空闲秒数、会话是否在回复、已用重试），失败节点带分类；
// 预算余额按「所有失败节点里用得最多的那个」算，不能拿别的节点充数。
func TestSupervisionObservationReportsOpenAndFailedNodes(t *testing.T) {
	f := newRouterEventFixture(t)
	f.svc.SetRouterConfig(RouterConfig{
		Mode:        RouterModeCoordinate,
		IdleClose:   time.Hour,
		Supervision: RouterSupervisionConfig{Mode: RouterSupervisionOn},
	})
	f.svc.SetSessionRunningProbe(func(key string) bool { return key == "sess-busy" })
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	f.addRun(t, "task-1", "run-1", WorkflowTriggeredByUser, map[string]string{
		"agent-1": NodeRunRunning, "agent-2": NodeRunFail,
	})
	f.addNodeRun(t, NodeRun{
		ID: "nr-1", RunID: "run-1", NodeID: "agent-1", Status: NodeRunRunning,
		Attempt: 1, SessionKey: "sess-busy", StartedAt: now.Add(-5 * time.Minute),
	})
	f.addNodeRun(t, NodeRun{
		ID: "nr-2a", RunID: "run-1", NodeID: "agent-2", Status: NodeRunFail,
		Attempt: 1, StartedAt: now.Add(-10 * time.Minute), FinishedAt: now.Add(-9 * time.Minute),
		Output: map[string]any{"error": "response stream ended unexpectedly"},
	})
	f.addNodeRun(t, NodeRun{
		ID: "nr-2b", RunID: "run-1", NodeID: "agent-2", Status: NodeRunFail,
		Attempt: 2, PrevAttemptID: "nr-2a", StartedAt: now.Add(-8 * time.Minute), FinishedAt: now.Add(-7 * time.Minute),
		Output: map[string]any{"error": "response stream ended unexpectedly"},
	})

	task := f.task(t, "task-1")
	run, err := f.store.WorkflowRunByID(context.Background(), "run-1")
	must(t, err, "WorkflowRunByID")
	obs := f.svc.supervisionObservation(context.Background(), f.store, task, run, now)
	if obs == nil {
		t.Fatal("observation is nil with supervision=on")
	}
	if obs.RunID != "run-1" || obs.TaskID != "task-1" || obs.Mode != RouterSupervisionOn {
		t.Fatalf("observation header = %+v", obs)
	}
	if len(obs.OpenNodes) != 1 {
		t.Fatalf("open nodes = %+v", obs.OpenNodes)
	}
	open := obs.OpenNodes[0]
	if open.NodeID != "agent-1" || !open.Replying {
		t.Fatalf("open node = %+v, want agent-1 replying", open)
	}
	if open.IdleSeconds < 240 {
		t.Fatalf("idle seconds = %d, want >= 240", open.IdleSeconds)
	}
	if len(obs.FailedNodes) != 1 {
		t.Fatalf("failed nodes = %+v", obs.FailedNodes)
	}
	failed := obs.FailedNodes[0]
	if failed.NodeID != "agent-2" || failed.FailureKind != SupervisionFailureRetryable {
		t.Fatalf("failed node = %+v, want retryable", failed)
	}
	if failed.RetriesUsed != 1 {
		t.Fatalf("retries used = %d, want 1 (attempt 2 minus first)", failed.RetriesUsed)
	}
	if obs.RetriesLeft != defaultSupervisionMaxRetriesPerNode-1 {
		t.Fatalf("retries left = %d, want %d", obs.RetriesLeft, defaultSupervisionMaxRetriesPerNode-1)
	}
	raw, err := json.Marshal(obs)
	if err != nil {
		t.Fatalf("marshal observation: %v", err)
	}
	lower := strings.ToLower(string(raw))
	for _, forbidden := range []string{"session_key", "transcript"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("observation leaks %q: %s", forbidden, raw)
		}
	}
}

// 行为：分类器优先看结构化证据（worktree_error），其次按错误文本兜底；
// 没有证据的 unknown 一律算「不确定」，绝不自动重试。
func TestSupervisionFailureClassification(t *testing.T) {
	cases := []struct {
		name   string
		node   NodeRun
		kind   SupervisionFailureKind
		banned string
	}{
		{name: "worktree uncertain", node: NodeRun{Status: NodeRunUnknown,
			Output: map[string]any{"worktree_error": "merge conflict while closing"}}, kind: SupervisionFailureUncertain},
		{name: "unknown without evidence", node: NodeRun{Status: NodeRunUnknown}, kind: SupervisionFailureUncertain},
		{name: "upstream 502", node: NodeRun{Status: NodeRunFail,
			Output: map[string]any{"error": "502 Bad Gateway"}}, kind: SupervisionFailureRetryable},
		{name: "stream interrupted", node: NodeRun{Status: NodeRunFail,
			Output: map[string]any{"error": "response stream ended unexpectedly"}}, kind: SupervisionFailureRetryable},
		{name: "validation error", node: NodeRun{Status: NodeRunFail,
			Output: map[string]any{"error": "invalid workflow graph: missing agent"}}, kind: SupervisionFailureNonRetryable},
		{name: "auth error", node: NodeRun{Status: NodeRunFail,
			Output: map[string]any{"error": "401 unauthorized"}}, kind: SupervisionFailureNonRetryable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, summary := classifySupervisionFailure(tc.node)
			if kind != tc.kind {
				t.Fatalf("kind = %q, want %q (summary %q)", kind, tc.kind, summary)
			}
			if strings.TrimSpace(summary) == "" {
				t.Fatalf("empty summary for %s", tc.name)
			}
		})
	}
}

// 行为：run_stalled 也带同一份观测，且开放节点明确标注「会话没在回复」——
// 这是 S2 能区分「卡死待重试」和「长回合正在跑」的唯一依据。
func TestRouterEventStalledCarriesSupervisionBlock(t *testing.T) {
	base := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	f := newStallFixture(t, base.Add(-2*time.Minute), "sess-1")
	f.svc.SetRouterConfig(RouterConfig{Mode: RouterModeCoordinate, IdleClose: time.Hour,
		Supervision: RouterSupervisionConfig{Mode: RouterSupervisionOn}})
	f.svc.SetSessionRunningProbe(func(string) bool { return false })
	f.scan(t, base)
	res := f.scan(t, base.Add(31*time.Second))
	if len(res.Produced) != 1 || res.Produced[0].Kind != RouterEventRunStalled {
		t.Fatalf("produced = %+v, want one run_stalled", res.Produced)
	}
	var payload map[string]any
	must(t, json.Unmarshal([]byte(res.Produced[0].PayloadJSON), &payload), "payload json")
	block, ok := payload["supervision"].(map[string]any)
	if !ok {
		t.Fatalf("stalled payload has no supervision block: %s", res.Produced[0].PayloadJSON)
	}
	open, _ := block["open_nodes"].([]any)
	if len(open) != 1 {
		t.Fatalf("open nodes = %v, want 1", block["open_nodes"])
	}
	node, _ := open[0].(map[string]any)
	if node["node_id"] != "agent-1" || node["status"] != NodeRunRunning {
		t.Fatalf("open node = %v", node)
	}
	if replying, _ := node["replying"].(bool); replying {
		t.Fatalf("replying = true for an inactive session: %v", node)
	}
	if idle, _ := node["idle_seconds"].(float64); idle < 120 {
		t.Fatalf("idle seconds = %v, want >= 120", node["idle_seconds"])
	}
}

// 行为：run_failed 事件带 supervision 块（预算是可执行事实，不是形容词）；
// supervision=off 时 payload 与今天完全一致。
func TestRouterEventFailedRunCarriesSupervisionBlock(t *testing.T) {
	f := newRouterEventFixture(t)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	f.svc.SetRouterConfig(RouterConfig{Mode: RouterModeCoordinate, IdleClose: time.Hour,
		Supervision: RouterSupervisionConfig{Mode: RouterSupervisionShadow}})
	f.addRun(t, "task-1", "run-1", WorkflowTriggeredByUser, map[string]string{"agent-1": NodeRunFail})
	f.markOwned(t, "task-1", "run-1", "decision-1")
	f.setTaskStatus(t, "task-1", StatusFail)
	f.addNodeRun(t, NodeRun{ID: "nr-1", RunID: "run-1", NodeID: "agent-1", Status: NodeRunFail,
		Attempt: 1, StartedAt: now.Add(-3 * time.Minute), FinishedAt: now.Add(-2 * time.Minute),
		Output: map[string]any{"error": "503 Service Unavailable"}})

	res := f.scan(t, now)
	if len(res.Produced) != 1 || res.Produced[0].Kind != RouterEventRunFailed {
		t.Fatalf("produced = %+v", res.Produced)
	}
	var payload map[string]any
	must(t, json.Unmarshal([]byte(res.Produced[0].PayloadJSON), &payload), "payload json")
	block, ok := payload["supervision"].(map[string]any)
	if !ok {
		t.Fatalf("payload has no supervision block: %s", res.Produced[0].PayloadJSON)
	}
	if block["run_id"] != "run-1" || block["mode"] != string(RouterSupervisionShadow) {
		t.Fatalf("supervision block = %v", block)
	}
	if block["retries_left"] != float64(defaultSupervisionMaxRetriesPerNode) {
		t.Fatalf("retries_left = %v, want %d", block["retries_left"], defaultSupervisionMaxRetriesPerNode)
	}

	off := newRouterEventFixture(t)
	offNow := now
	off.addRun(t, "task-1", "run-1", WorkflowTriggeredByUser, map[string]string{"agent-1": NodeRunFail})
	off.markOwned(t, "task-1", "run-1", "decision-1")
	off.setTaskStatus(t, "task-1", StatusFail)
	offRes := off.scan(t, offNow)
	var offPayload map[string]any
	must(t, json.Unmarshal([]byte(offRes.Produced[0].PayloadJSON), &offPayload), "off payload json")
	if _, present := offPayload["supervision"]; present {
		t.Fatalf("supervision=off still attached a block: %s", offRes.Produced[0].PayloadJSON)
	}
}
