package kanban

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// readWSHandlerSource 只截出目标 handler，避免整份 ws.go 的无关改动误伤契约。
func readWSHandlerSource(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile("../api/ws.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(body)
	start := strings.Index(src, "func (h *WSHandler) "+name+"(")
	if start < 0 {
		t.Fatalf("handler %s not found", name)
	}
	rest := src[start:]
	if end := strings.Index(rest, "\nfunc "); end > 0 {
		rest = rest[:end]
	}
	return rest
}

// 一个节点停在审批/提问，另一个节点的会话还在跑：投影必须报运行中。
// 报等待会让任务头从「执行中」掉到「待处理」，正是用户看到的症状。
func TestProjectStatusFromRunRunningBeatsWaiting(t *testing.T) {
	run := &WorkflowRun{
		Graph: WorkflowDefinition{Nodes: []WorkflowNode{
			{ID: "gate", Kind: NodeKindHumanGate},
			{ID: "build", Kind: NodeKindAgent},
		}},
		NodeStates: map[string]string{"gate": NodeRunWaitingUser, "build": NodeRunRunning},
	}
	if got := taskStatusFromRun(run); got != StatusRunning {
		t.Fatalf("taskStatusFromRun = %q, want %q (a live node outranks a waiting one)", got, StatusRunning)
	}
}

// 用户在任务的会话里直接说话 → 任务从 pending/waiting_user 抬到 running；
// 会话结算后，另一个仍活着的会话必须让任务保持 running。
func TestOutOfBandTurnLivenessRestoresOnlyWhenAlone(t *testing.T) {
	ctx := context.Background()
	f := newTurnSettledFixture(t, "task-runtime-oob", "sess-main")
	if err := f.store.UpdateTaskStatus(ctx, f.taskID, StatusPending, false); err != nil {
		t.Fatalf("set pending: %v", err)
	}
	if err := NewActivityStore(f.store.db).Insert(ctx, &TaskActivity{
		ID: "act-runtime-other", TaskID: f.taskID, RootID: f.rootID,
		Type: ActivityTypeAgentReply, Role: RoleAgent, Body: "另一条会话在跑",
		SessionKey: "sess-other", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("insert other session activity: %v", err)
	}
	live := map[string]bool{}
	f.svc.SetSessionRunningProbe(func(key string) bool { return live[key] })
	if !f.svc.BeginOutOfBandSessionTurn(ctx, f.rootID, "sess-main") {
		t.Fatal("BeginOutOfBandSessionTurn from pending did not claim the turn")
	}
	task, err := f.store.GetTask(ctx, f.taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != StatusRunning {
		t.Fatalf("status after direct message = %q, want %q", task.Status, StatusRunning)
	}
	// 主会话结算，但另一条会话仍在回话：不能把任务放回待处理。
	live["sess-other"] = true
	if _, err := f.svc.SettleSessionTurn(ctx, f.rootID, TurnSettledInput{
		SessionKey: "sess-main", TurnID: "turn-main-done", Excerpt: "主会话回完了",
	}); err != nil {
		t.Fatalf("SettleSessionTurn (main): %v", err)
	}
	task, _ = f.store.GetTask(ctx, f.taskID)
	if task.Status != StatusRunning {
		t.Fatalf("status with another live session = %q, want %q", task.Status, StatusRunning)
	}
	// 最后一条会话也结算：此时才回待处理。
	live["sess-other"] = false
	if _, err := f.svc.SettleSessionTurn(ctx, f.rootID, TurnSettledInput{
		SessionKey: "sess-other", TurnID: "turn-other-done", Excerpt: "最后一条回完了",
	}); err != nil {
		t.Fatalf("SettleSessionTurn (other): %v", err)
	}
	task, _ = f.store.GetTask(ctx, f.taskID)
	if task.Status != StatusWaitingUser {
		t.Fatalf("status after all sessions settled = %q, want %q", task.Status, StatusWaitingUser)
	}
}

// 节点回合开始时任务可能仍停在待处理（上一轮的投影/审核留下的）；执行器必须
// 在 agent 真的开跑之前抬到 running，而不是等下一个终态投影。
func TestWorkflowNodeTurnMarksWaitingTaskRunning(t *testing.T) {
	ctx := context.Background()
	store, svc, run, node := newWorkflowAttemptFixture(t, "task-node-live", "run-node-live")
	const taskID = "task-node-live"
	if err := store.UpdateTaskStatus(ctx, taskID, StatusWaitingUser, false); err != nil {
		t.Fatalf("set waiting_user: %v", err)
	}
	var seen string
	runner := &scriptedWorkflowNodeRunner{onRun: func(_ WorkflowNodeExecution) error {
		task, err := store.GetTask(ctx, taskID)
		if err != nil {
			return err
		}
		seen = task.Status
		return nil
	}}
	svc.SetWorkflowNodeRunner(runner)
	if _, err := svc.RunWorkflowNodeAttempt(ctx, store, run, node, attemptNodeRun(run.ID)); err != nil {
		t.Fatalf("RunWorkflowNodeAttempt: %v", err)
	}
	if seen != StatusRunning {
		t.Fatalf("task status seen inside agent turn = %q, want %q", seen, StatusRunning)
	}
}

// 已经落到待处理、但会话/节点还在跑的任务，巡检要把任务头纠回运行中。
func TestHealthSweepCorrectsWaitingBusyTask(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatal(err)
	}
	const taskID = "task-health-busy"
	if _, err := store.CreateTask(ctx, Task{
		ID: taskID, RootID: root.ID, Status: StatusWaitingUser, MainSessionKey: "sess-health-live",
	}, TaskEvent{ID: taskID + "-event", TaskID: taskID, Type: "created"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	svc.SetSessionRunningProbe(func(key string) bool { return key == "sess-health-live" })
	if _, err := svc.taskHealthSweep(ctx, root.ID, time.Now()); err != nil {
		t.Fatalf("taskHealthSweep: %v", err)
	}
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != StatusRunning {
		t.Fatalf("status after waiting_busy sweep = %q, want %q", task.Status, StatusRunning)
	}
}

// 提问卡回答入口必须接上 same-as-发消息 的 OOB 抬升；纯源码契约防止接线被回退。
func TestAnswerQuestionHandlerClaimsOutOfBandTurn(t *testing.T) {
	body := readWSHandlerSource(t, "handleSessionAnswerQuestion")
	if !strings.Contains(body, "BeginOutOfBandSessionTurn(") {
		t.Fatal("handleSessionAnswerQuestion no longer claims an out-of-band turn")
	}
	idx := strings.Index(body, "AnswerQuestion(")
	hook := strings.Index(body, "BeginOutOfBandSessionTurn(")
	if idx < 0 || hook < idx {
		t.Fatal("out-of-band hook must run after AnswerQuestion succeeds")
	}
}
