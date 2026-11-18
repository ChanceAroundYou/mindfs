package kanban

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"mindfs/server/internal/fs"
)

// T6：生命周期事件生产。断言只围绕「权威持久化状态 → 幂等事件」这一条链路。

type routerEventFixture struct {
	svc    *Service
	store  *TaskStore
	root   fs.RootInfo
	events *RouterStore
}

// must 让测试里的落库/读库调用保持两行以内；断言失败信息仍带上下文。
func must(t *testing.T, err error, what string, args ...any) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", fmt.Sprintf(what, args...), err)
	}
}

func newRouterEventFixture(t *testing.T) *routerEventFixture {
	t.Helper()
	root := fs.NewRootInfo("root", "root", t.TempDir())
	svc := NewService(testRoots{root: root})
	store, err := svc.taskStore(root.ID)
	must(t, err, "taskStore")
	t.Cleanup(func() { _ = store.Close() })
	f := &routerEventFixture{svc: svc, store: store, root: root, events: NewRouterStore(store.db)}
	f.addTask(t, "task-1")
	return f
}
func (f *routerEventFixture) addTask(t *testing.T, taskID string) {
	t.Helper()
	if _, err := f.store.CreateTask(context.Background(),
		Task{ID: taskID, RootID: f.root.ID, Status: StatusRunning},
		TaskEvent{ID: "event-" + taskID, TaskID: taskID, Type: "created"},
	); err != nil {
		t.Fatalf("CreateTask(%s): %v", taskID, err)
	}
}

// addRun 建一条 Run 并绑定为任务当前执行引擎。节点 kind 固定：agent-1 agent、
// gate-1 human_gate、agent-2 agent；states 决定投影。
func (f *routerEventFixture) addRun(t *testing.T, taskID, runID, triggeredBy string, states map[string]string) *WorkflowRun {
	t.Helper()
	run := &WorkflowRun{
		ID: runID, TaskID: taskID, Version: "wf:v1", Revision: 2,
		Graph:       wfDef(wfNode("agent-1", NodeKindAgent), wfNode("gate-1", NodeKindHumanGate), wfNode("agent-2", NodeKindAgent)),
		TriggeredBy: triggeredBy, ActivityID: "uact-" + runID, NodeStates: states,
	}
	must(t, f.store.CreateWorkflowRunAndSetEngine(context.Background(), run), "CreateWorkflowRunAndSetEngine(%s)", runID)
	return run
}
func (f *routerEventFixture) saveStates(t *testing.T, runID string, states map[string]string) {
	t.Helper()
	run, err := f.store.WorkflowRunByID(context.Background(), runID)
	must(t, err, "WorkflowRunByID(%s)", runID)
	run.NodeStates = states
	must(t, f.store.SaveWorkflowRun(context.Background(), run), "SaveWorkflowRun(%s)", runID)
}

// markOwned 造出 Router-owned 判据：一条绑定了 run 且带 router_decision_id 的回复活动。
func (f *routerEventFixture) markOwned(t *testing.T, taskID, runID, decisionID string) {
	t.Helper()
	acts := NewActivityStore(f.store.db)
	ctx := context.Background()
	if err := acts.Insert(ctx, &TaskActivity{ID: "uact-" + runID, TaskID: taskID, RootID: f.root.ID,
		Type: ActivityTypeUserMessage, Author: "user", Role: "user", Body: "请执行"}); err != nil {
		t.Fatalf("insert user activity: %v", err)
	}
	if err := acts.Insert(ctx, &TaskActivity{ID: "reply-" + runID, TaskID: taskID, RootID: f.root.ID,
		Type: ActivityTypeAgentReply, RunID: runID, RouterDecisionID: decisionID,
		ReplyToID: "uact-" + runID, Status: ActivityStatusRunning}); err != nil {
		t.Fatalf("insert reply activity: %v", err)
	}
}
func (f *routerEventFixture) setTaskStatus(t *testing.T, taskID, status string) {
	t.Helper()
	must(t, f.store.UpdateTaskStatus(context.Background(), taskID, status, isTerminalStatus(status)), "UpdateTaskStatus(%s)", status)
}
func (f *routerEventFixture) addNodeRun(t *testing.T, nr NodeRun) {
	t.Helper()
	must(t, f.store.UpsertWorkflowNodeRun(context.Background(), nr), "UpsertWorkflowNodeRun(%s)", nr.ID)
}
func (f *routerEventFixture) scan(t *testing.T, now time.Time) RouterEventScanResult {
	t.Helper()
	res, err := f.svc.ScanRouterEventSources(context.Background(), f.root.ID, now)
	must(t, err, "ScanRouterEventSources")
	return res
}
func (f *routerEventFixture) list(t *testing.T, taskID string) []RouterEvent {
	t.Helper()
	events, err := f.events.ListEventsByTask(context.Background(), taskID)
	must(t, err, "ListEventsByTask")
	return events
}
func (f *routerEventFixture) task(t *testing.T, taskID string) Task {
	t.Helper()
	task, err := f.store.GetTask(context.Background(), taskID)
	must(t, err, "GetTask")
	return task
}
func countKind(events []RouterEvent, kind RouterEventKind) int {
	n := 0
	for _, ev := range events {
		if ev.Kind == kind {
			n++
		}
	}
	return n
}

// 行为 1：Router-owned 成功 → 恰一条 run_settled（InstanceKey=RunID），重复扫描零新增。
func TestRouterEventSourcesSettledRunProducesOnce(t *testing.T) {
	f := newRouterEventFixture(t)
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	f.addRun(t, "task-1", "run-1", WorkflowTriggeredByUser, map[string]string{"agent-1": NodeRunSuccess})
	f.markOwned(t, "task-1", "run-1", "decision-1")
	f.setTaskStatus(t, "task-1", StatusSuccess)

	res := f.scan(t, now)
	if len(res.Produced) != 1 {
		t.Fatalf("produced = %d, want 1 (%+v)", len(res.Produced), res)
	}
	ev := res.Produced[0]
	if ev.Kind != RouterEventRunSettled || ev.InstanceKey != "run-1" || ev.RunID != "run-1" ||
		ev.TaskID != "task-1" || ev.RootID != f.root.ID {
		t.Fatalf("settled event = %+v", ev)
	}
	if ev.UserActivityID != "uact-run-1" || ev.SourceDecisionID != "decision-1" {
		t.Fatalf("originating user/decision = %q/%q", ev.UserActivityID, ev.SourceDecisionID)
	}
	if !strings.HasPrefix(ev.TurnID, "revt-turn:") || ev.Status != RouterEventPending || ev.Attempt != 0 {
		t.Fatalf("turn/status/attempt = %q/%q/%d", ev.TurnID, ev.Status, ev.Attempt)
	}
	if !ev.CreatedAt.Equal(now) {
		t.Fatalf("created_at = %v, want injected now %v", ev.CreatedAt, now)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(ev.PayloadJSON), &payload); err != nil {
		t.Fatalf("payload json: %v", err)
	}
	for _, key := range []string{"run_id", "task_id", "nodes", "workflow_id", "revision"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("payload missing %q: %s", key, ev.PayloadJSON)
		}
	}
	nodes, _ := payload["nodes"].(map[string]any)
	if nodes["agent-1"] != NodeRunSuccess {
		t.Fatalf("payload nodes = %v", payload["nodes"])
	}
	for _, forbidden := range []string{"output", "session", "git", "transcript"} {
		if strings.Contains(strings.ToLower(ev.PayloadJSON), forbidden) {
			t.Fatalf("payload leaks %q: %s", forbidden, ev.PayloadJSON)
		}
	}
	again := f.scan(t, now)
	if len(again.Produced) != 0 {
		t.Fatalf("second scan produced %d events, want 0", len(again.Produced))
	}
	if got := f.list(t, "task-1"); len(got) != 1 {
		t.Fatalf("rows after repeat scan = %d, want 1", len(got))
	}
}

// 行为 2：失败 → run_failed；与同 Run 的 run_settled 不同 UniqueKey，各留一行。
func TestRouterEventSourcesFailedRunKeepsOwnRow(t *testing.T) {
	f := newRouterEventFixture(t)
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	f.addRun(t, "task-1", "run-1", WorkflowTriggeredByUser, map[string]string{"agent-1": NodeRunFail})
	f.markOwned(t, "task-1", "run-1", "decision-1")
	f.setTaskStatus(t, "task-1", StatusFail)

	res := f.scan(t, now)
	if len(res.Produced) != 1 || res.Produced[0].Kind != RouterEventRunFailed {
		t.Fatalf("produced = %+v, want one run_failed", res.Produced)
	}
	failedKey := res.Produced[0].UniqueKey

	// 同一 Run 重试后成功：投影改回 success，扫描补出 settled 行。
	f.saveStates(t, "run-1", map[string]string{"agent-1": NodeRunSuccess})
	f.setTaskStatus(t, "task-1", StatusSuccess)
	res = f.scan(t, now.Add(time.Minute))
	if len(res.Produced) != 1 || res.Produced[0].Kind != RouterEventRunSettled {
		t.Fatalf("second produced = %+v, want one run_settled", res.Produced)
	}
	settledKey := res.Produced[0].UniqueKey
	if failedKey == settledKey {
		t.Fatalf("settled and failed share unique key %q", failedKey)
	}
	rows := f.list(t, "task-1")
	if len(rows) != 2 || countKind(rows, RouterEventRunSettled) != 1 || countKind(rows, RouterEventRunFailed) != 1 {
		t.Fatalf("rows = %+v, want settled+failed", rows)
	}
}

// 行为 3：gate waiting 每条 attempt 恰一条，重复物化不重复。
func TestRouterEventSourcesGateWaitingPerAttempt(t *testing.T) {
	f := newRouterEventFixture(t)
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	f.addRun(t, "task-1", "run-1", WorkflowTriggeredByUser, map[string]string{"gate-1": NodeRunWaitingUser})
	f.markOwned(t, "task-1", "run-1", "decision-1")
	f.setTaskStatus(t, "task-1", StatusWaitingUser)
	f.addNodeRun(t, NodeRun{ID: "run-1:gate-1::0:1", RunID: "run-1", NodeID: "gate-1",
		Attempt: 1, Status: NodeRunWaitingUser})

	res := f.scan(t, now)
	if len(res.Produced) != 1 || res.Produced[0].Kind != RouterEventGateWaiting {
		t.Fatalf("produced = %+v, want one gate_waiting", res.Produced)
	}
	if got := res.Produced[0].InstanceKey; got != "run-1:gate-1::0:1:1" {
		t.Fatalf("instance key = %q, want <nodeRunID>:1", got)
	}
	if res.Produced[0].Attempt != 1 {
		t.Fatalf("attempt = %d, want 1", res.Produced[0].Attempt)
	}
	if again := f.scan(t, now); len(again.Produced) != 0 {
		t.Fatalf("repeat materialization produced %d, want 0", len(again.Produced))
	}
	f.addNodeRun(t, NodeRun{ID: "run-1:gate-1::0:2", RunID: "run-1", NodeID: "gate-1",
		Attempt: 2, Status: NodeRunWaitingUser})
	res = f.scan(t, now)
	if len(res.Produced) != 1 || res.Produced[0].InstanceKey != "run-1:gate-1::0:2:2" {
		t.Fatalf("second attempt produced = %+v", res.Produced)
	}
	if rows := f.list(t, "task-1"); len(rows) != 2 {
		t.Fatalf("gate rows = %d, want 2 (one per attempt)", len(rows))
	}
}

// 行为 4：显式（无 router 决策）与 legacy_import Run 零事件，Notes 记录原因。
func TestRouterEventSourcesSkipsExplicitAndLegacyRuns(t *testing.T) {
	f := newRouterEventFixture(t)
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	f.addRun(t, "task-1", "run-explicit", WorkflowTriggeredByUser, map[string]string{"agent-1": NodeRunSuccess})
	f.setTaskStatus(t, "task-1", StatusSuccess)
	f.addTask(t, "task-2")
	f.addRun(t, "task-2", "run-legacy", WorkflowTriggeredByLegacyImport, map[string]string{"agent-1": NodeRunSuccess})
	f.markOwned(t, "task-2", "run-legacy", "decision-legacy")

	res := f.scan(t, now)
	if len(res.Produced) != 0 {
		t.Fatalf("explicit/legacy produced = %+v, want none", res.Produced)
	}
	notes := strings.Join(res.Notes, "\n")
	if !strings.Contains(notes, "run-explicit") || !strings.Contains(notes, "no router decision") {
		t.Fatalf("notes missing explicit reason: %q", notes)
	}
	if !strings.Contains(notes, "run-legacy") || !strings.Contains(notes, "legacy_import") {
		t.Fatalf("notes missing legacy reason: %q", notes)
	}
	if rows := f.list(t, "task-1"); len(rows) != 0 {
		t.Fatalf("explicit run wrote %d events", len(rows))
	}
	if rows := f.list(t, "task-2"); len(rows) != 0 {
		t.Fatalf("legacy run wrote %d events", len(rows))
	}
}

// 行为 5：结算窗口中断可补齐；钩子先写、扫描后写只留一行。
func TestRouterEventSourcesScanRecoversMissedSettlementWindow(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	hook := newRouterEventFixture(t)
	hook.addRun(t, "task-1", "run-1", WorkflowTriggeredByUser, map[string]string{"agent-1": NodeRunSuccess})
	hook.markOwned(t, "task-1", "run-1", "decision-1")
	hook.setTaskStatus(t, "task-1", StatusSuccess)
	run, err := hook.store.WorkflowRunByID(context.Background(), "run-1")
	must(t, err, "WorkflowRunByID")
	hook.svc.settleWorkflowReply(context.Background(), hook.store, run)
	hooked := hook.list(t, "task-1")
	if len(hooked) != 1 || hooked[0].Kind != RouterEventRunSettled {
		t.Fatalf("hook path rows = %+v, want one run_settled", hooked)
	}
	if res := hook.scan(t, now); len(res.Produced) != 0 {
		t.Fatalf("scan after hook produced %d, want 0", len(res.Produced))
	}
	// 模拟钩子没跑：同一权威状态直接扫描必须补出同一条事件。
	scanOnly := newRouterEventFixture(t)
	scanOnly.addRun(t, "task-1", "run-1", WorkflowTriggeredByUser, map[string]string{"agent-1": NodeRunSuccess})
	scanOnly.markOwned(t, "task-1", "run-1", "decision-1")
	scanOnly.setTaskStatus(t, "task-1", StatusSuccess)
	res := scanOnly.scan(t, now)
	if len(res.Produced) != 1 {
		t.Fatalf("scan-only produced %d, want 1", len(res.Produced))
	}
	if res.Produced[0].UniqueKey != hooked[0].UniqueKey || res.Produced[0].TurnID != hooked[0].TurnID {
		t.Fatalf("scan event %q/%q != hook %q/%q", res.Produced[0].UniqueKey, res.Produced[0].TurnID,
			hooked[0].UniqueKey, hooked[0].TurnID)
	}
}

// stallFixture 造一条 running 的 Router-owned Run，节点启动时间可控。
func newStallFixture(t *testing.T, startedAt time.Time, sessionKey string) *routerEventFixture {
	t.Helper()
	f := newRouterEventFixture(t)
	f.addRun(t, "task-1", "run-1", WorkflowTriggeredByUser, map[string]string{"agent-1": NodeRunRunning})
	f.markOwned(t, "task-1", "run-1", "decision-1")
	f.setTaskStatus(t, "task-1", StatusRunning)
	f.addNodeRun(t, NodeRun{ID: "run-1:agent-1::0:1", RunID: "run-1", NodeID: "agent-1",
		Attempt: 1, Status: NodeRunRunning, SessionKey: sessionKey, StartedAt: startedAt})
	return f
}

// 行为 6：stall 只认明确证据——无 probe / active / 单次 inactive / 时长不足都不产。
func TestRouterEventSourcesStallRequiresExplicitEvidence(t *testing.T) {
	base := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	inactive := func(string) bool { return false }
	active := func(string) bool { return true }
	t.Run("no probe", func(t *testing.T) {
		f := newStallFixture(t, base.Add(-2*time.Minute), "sess-1")
		f.scan(t, base)
		if res := f.scan(t, base.Add(time.Minute)); len(res.Produced) != 0 {
			t.Fatalf("no probe produced %+v", res.Produced)
		}
	})
	t.Run("single inactive", func(t *testing.T) {
		f := newStallFixture(t, base.Add(-2*time.Minute), "sess-1")
		f.svc.SetSessionRunningProbe(inactive)
		if res := f.scan(t, base); len(res.Produced) != 0 {
			t.Fatalf("first inactive produced %+v", res.Produced)
		}
	})
	t.Run("gap under 30s", func(t *testing.T) {
		f := newStallFixture(t, base.Add(-2*time.Minute), "sess-1")
		f.svc.SetSessionRunningProbe(inactive)
		f.scan(t, base)
		if res := f.scan(t, base.Add(20*time.Second)); len(res.Produced) != 0 {
			t.Fatalf("sub-30s confirmation produced %+v", res.Produced)
		}
	})
	t.Run("active breaks the pair", func(t *testing.T) {
		f := newStallFixture(t, base.Add(-2*time.Minute), "sess-1")
		f.svc.SetSessionRunningProbe(inactive)
		f.scan(t, base)
		f.svc.SetSessionRunningProbe(active)
		f.scan(t, base.Add(31*time.Second))
		f.svc.SetSessionRunningProbe(inactive)
		if res := f.scan(t, base.Add(62*time.Second)); len(res.Produced) != 0 {
			t.Fatalf("inactive after active produced %+v", res.Produced)
		}
	})
	t.Run("runtime under 60s", func(t *testing.T) {
		f := newStallFixture(t, base.Add(-5*time.Second), "sess-1")
		f.svc.SetSessionRunningProbe(inactive)
		f.scan(t, base)
		if res := f.scan(t, base.Add(31*time.Second)); len(res.Produced) != 0 {
			t.Fatalf("young running node produced %+v", res.Produced)
		}
	})
	t.Run("no session key", func(t *testing.T) {
		f := newStallFixture(t, base.Add(-2*time.Minute), "")
		f.svc.SetSessionRunningProbe(inactive)
		f.scan(t, base)
		if res := f.scan(t, base.Add(31*time.Second)); len(res.Produced) != 0 {
			t.Fatalf("unbound session produced %+v", res.Produced)
		}
	})
	t.Run("confirmed inactive", func(t *testing.T) {
		f := newStallFixture(t, base.Add(-2*time.Minute), "sess-1")
		f.svc.SetSessionRunningProbe(inactive)
		f.scan(t, base)
		res := f.scan(t, base.Add(31*time.Second))
		if len(res.Produced) != 1 || res.Produced[0].Kind != RouterEventRunStalled {
			t.Fatalf("confirmed inactive produced %+v, want one run_stalled", res.Produced)
		}
	})
}

// 行为 7：同一 Run 同一 5 分钟桶只一条；跨桶才允许第二条。
func TestRouterEventSourcesStallDedupesPerBucket(t *testing.T) {
	base := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	f := newStallFixture(t, base.Add(-2*time.Minute), "sess-1")
	f.svc.SetSessionRunningProbe(func(string) bool { return false })

	f.scan(t, base)
	if res := f.scan(t, base.Add(31*time.Second)); len(res.Produced) != 1 {
		t.Fatalf("first bucket produced %d, want 1", len(res.Produced))
	}
	for _, offset := range []time.Duration{61 * time.Second, 91 * time.Second, 2 * time.Minute} {
		if res := f.scan(t, base.Add(offset)); len(res.Produced) != 0 {
			t.Fatalf("same bucket offset %v produced %d, want 0", offset, len(res.Produced))
		}
	}
	res := f.scan(t, base.Add(6*time.Minute))
	if len(res.Produced) != 1 || res.Produced[0].Kind != RouterEventRunStalled {
		t.Fatalf("next bucket produced %+v, want one run_stalled", res.Produced)
	}
	if rows := f.list(t, "task-1"); countKind(rows, RouterEventRunStalled) != 2 {
		t.Fatalf("stall rows = %d, want 2 distinct buckets", countKind(rows, RouterEventRunStalled))
	}
}

// dumpRouterEventTables 抓取权威表的逐行内容（不含 router_events 本身）。
func dumpRouterEventTables(t *testing.T, db *sql.DB) string {
	t.Helper()
	var b strings.Builder
	for _, table := range []string{"tasks", "task_execution_engines", "workflow_runs", "workflow_node_runs", "task_activities", "workflow_run_events"} {
		rows, err := db.Query(`SELECT * FROM ` + table + ` ORDER BY rowid`)
		if err != nil {
			t.Fatalf("dump %s: %v", table, err)
		}
		cols, _ := rows.Columns()
		fmt.Fprintf(&b, "%s:%s\n", table, strings.Join(cols, ","))
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatalf("scan %s: %v", table, err)
			}
			fmt.Fprintf(&b, "%v\n", vals)
		}
		rows.Close()
	}
	return b.String()
}

// 行为 8：扫描不改 Task/Run/NodeRun/Activity；旧 Run 迟到事件照实记录自身 run_id。
func TestRouterEventSourcesScanDoesNotRewriteState(t *testing.T) {
	f := newRouterEventFixture(t)
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	f.addRun(t, "task-1", "run-old", WorkflowTriggeredByUser, map[string]string{"agent-1": NodeRunFail})
	f.markOwned(t, "task-1", "run-old", "decision-old")
	f.setTaskStatus(t, "task-1", StatusFail)
	f.addRun(t, "task-1", "run-new", WorkflowTriggeredByUser, map[string]string{"agent-1": NodeRunRunning})
	f.markOwned(t, "task-1", "run-new", "decision-new")
	f.setTaskStatus(t, "task-1", StatusRunning)
	f.addNodeRun(t, NodeRun{ID: "run-new:agent-1::0:1", RunID: "run-new", NodeID: "agent-1",
		Attempt: 1, Status: NodeRunRunning, SessionKey: "sess-new", StartedAt: now.Add(-time.Minute)})

	before := dumpRouterEventTables(t, f.store.db)
	res := f.scan(t, now)
	after := dumpRouterEventTables(t, f.store.db)
	if before != after {
		t.Fatalf("scan rewrote authoritative state:\nBEFORE:\n%s\nAFTER:\n%s", before, after)
	}
	if len(res.Produced) != 1 || res.Produced[0].Kind != RouterEventRunFailed || res.Produced[0].RunID != "run-old" {
		t.Fatalf("produced = %+v, want run_failed for run-old", res.Produced)
	}
	if task := f.task(t, "task-1"); task.Status != StatusRunning {
		t.Fatalf("task status = %q, scan must not overwrite the new turn", task.Status)
	}
}

// 行为 9：取消不产 run_settled；暂停任务不因扫描复活或改状态。
func TestRouterEventSourcesCancelAndPauseProduceNoSettlement(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	cancelled := newRouterEventFixture(t)
	cancelled.addRun(t, "task-1", "run-1", WorkflowTriggeredByUser, map[string]string{"agent-1": NodeRunCancelled})
	cancelled.markOwned(t, "task-1", "run-1", "decision-1")
	cancelled.setTaskStatus(t, "task-1", StatusCancelled)
	if res := cancelled.scan(t, now); len(res.Produced) != 0 {
		t.Fatalf("cancelled run produced %+v, want none", res.Produced)
	}

	paused := newRouterEventFixture(t)
	paused.addRun(t, "task-1", "run-1", WorkflowTriggeredByUser, map[string]string{"agent-1": NodeRunRunning})
	paused.markOwned(t, "task-1", "run-1", "decision-1")
	paused.setTaskStatus(t, "task-1", StatusPaused)
	if res := paused.scan(t, now); len(res.Produced) != 0 {
		t.Fatalf("paused run produced %+v, want none", res.Produced)
	}
	if task := paused.task(t, "task-1"); task.Status != StatusPaused {
		t.Fatalf("paused task status = %q, scan must not revive it", task.Status)
	}
}

// 行为 10（回归）：迁移重复执行不改事件行；再次扫描零新增。
func TestRouterEventSourcesMigrationRerunKeepsEvents(t *testing.T) {
	f := newRouterEventFixture(t)
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	f.addRun(t, "task-1", "run-1", WorkflowTriggeredByUser, map[string]string{"agent-1": NodeRunSuccess})
	f.markOwned(t, "task-1", "run-1", "decision-1")
	f.setTaskStatus(t, "task-1", StatusSuccess)
	if res := f.scan(t, now); len(res.Produced) != 1 {
		t.Fatalf("produced = %d, want 1", len(res.Produced))
	}
	if err := migrateRouterEventTable(f.store.db); err != nil {
		t.Fatalf("migration rerun: %v", err)
	}
	if res := f.scan(t, now); len(res.Produced) != 0 {
		t.Fatalf("scan after migration rerun produced %d, want 0", len(res.Produced))
	}
	if rows := f.list(t, "task-1"); len(rows) != 1 || rows[0].Kind != RouterEventRunSettled {
		t.Fatalf("rows after migration rerun = %+v", rows)
	}
}
