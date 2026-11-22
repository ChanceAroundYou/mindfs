package kanban

// 直连回合与回复收尾：注入的执行器、运行中进度不改写回复、项目状态回写结算回复。
// 从 activity_service_test.go 拆出（硬线 500，2026-11-22）。

import (
	"context"
	"strings"
	"testing"
	"time"
)

type fakeDirectExecutor struct {
	reply string
	err   error
	got   string
	done  chan struct{}
}

func (f *fakeDirectExecutor) RunDirectActivity(_ context.Context, _ Task, body string) (string, error) {
	f.got = body
	defer close(f.done)
	return f.reply, f.err
}

func TestActivityService_CreateUserActivity_Disabled_WithExecutor(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)
	direct := &fakeDirectExecutor{reply: "好的，已处理", done: make(chan struct{})}
	f.service.SetDirectExecutor(direct)

	_, agentAct, err := f.service.CreateUserActivity(ctx, "task-1", "手动执行", CommentWorkflowChoiceDisabled, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	select {
	case <-direct.done:
	case <-time.After(2 * time.Second):
		t.Fatal("direct executor was never invoked")
	}
	if direct.got != "手动执行" {
		t.Fatalf("executor body = %q, want the user message", direct.got)
	}
	var stored *TaskActivity
	for i := 0; i < 50; i++ {
		stored, err = f.service.activities.Get(ctx, agentAct.ID)
		if err == nil && stored.Status == ActivityStatusCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if stored == nil || stored.Status != ActivityStatusCompleted || stored.Body != "好的，已处理" {
		t.Fatalf("stored activity = %#v, want completed with the reply as body", stored)
	}
	// 直连回合的回复也要落到任务行上，否则看板卡片只能看到用户原始需求。
	// 写的是结构化摘要（W1-3），不是回复原文：卡片上那句「本轮未生成总结」+
	// 用户诉求/最后进展才读得懂；后续压缩总结会覆盖它。
	task, err := f.tasks.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if !strings.Contains(task.ResultSummary, "本轮未生成总结") ||
		!strings.Contains(task.ResultSummary, "好的，已处理") {
		t.Fatalf("task result summary = %q, want the structured digest", task.ResultSummary)
	}
}

// Router 拥有对话流：节点进度只改这一轮回复的正文，绝不新增活动行。
func TestService_WorkflowReplyProgressStaysOnRouterReply(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)

	userAct, agentAct, err := f.service.CreateUserActivity(ctx, "task-1", "跑一下", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	run, err := f.tasks.WorkflowRunByID(ctx, agentAct.RunID)
	if err != nil {
		t.Fatalf("WorkflowRunByID: %v", err)
	}
	run.Graph = WorkflowDefinition{Nodes: []WorkflowNode{
		{ID: "step-1", Name: "分析", Kind: NodeKindNotify},
		{ID: "step-2", Name: "收尾", Kind: NodeKindNotify},
	}}
	if err := f.service.activities.UpdateStatus(ctx, agentAct.ID, ActivityStatusRunning, nil); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	svc := NewService(testRoots{root: f.root})
	svc.SetActivityBroadcaster(f.broadcast)
	svc.updateWorkflowReplyProgress(ctx, f.service.tasks, run, "step-1")

	stored, err := f.service.activities.Get(ctx, agentAct.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Body != "步骤 1/2: 分析" {
		t.Fatalf("body = %q, want 步骤 1/2: 分析", stored.Body)
	}
	list, err := f.service.activities.List(ctx, "task-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("activities = %d, want 2 (user + one router reply); node progress must not append rows", len(list))
	}
	if list[0].ID != userAct.ID || list[1].ID != agentAct.ID {
		t.Fatalf("unexpected activity order: %#v", list)
	}
}

// 任务进入真实终态时，Router 把结果结算到这一轮的回复上（completed/failed/
// waiting_user）。运行中不结算。
func TestService_ProjectWorkflowTaskStatusSettlesRouterReply(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name           string
		nodeState      string
		nodeStatus     string
		output         map[string]any
		wantStatus     string
		wantInSum      string
		wantTaskStatus string
	}{
		{"success", NodeRunSuccess, NodeRunSuccess, map[string]any{"summary": "改完了"}, ActivityStatusCompleted, "改完了", StatusWaitingUser},
		// 一轮失败也交回待审核：回复行照实标 failed 并带原因（证据），但任务不封板，
		// 用户可以接着发消息重试（2026-11-19 用户现场：一次 502 把任务扔进了取消）。
		{"fail", NodeRunFail, NodeRunFail, map[string]any{"error": "测试没过"}, ActivityStatusFailed, "测试没过", StatusWaitingUser},
		{"waiting_user", NodeRunWaitingUser, NodeRunWaitingUser, map[string]any{"reason": "缺权限"}, ActivityStatusWaitingUser, "缺权限", StatusWaitingUser},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := setupActivityService(t)
			_, agentAct, err := f.service.CreateUserActivity(ctx, "task-1", "跑一下", CommentWorkflowChoiceEmpty, nil)
			if err != nil {
				t.Fatalf("CreateUserActivity: %v", err)
			}
			run, err := f.tasks.WorkflowRunByID(ctx, agentAct.RunID)
			if err != nil {
				t.Fatalf("WorkflowRunByID: %v", err)
			}
			if err := f.service.activities.UpdateStatus(ctx, agentAct.ID, ActivityStatusRunning, nil); err != nil {
				t.Fatalf("UpdateStatus: %v", err)
			}
			run.NodeStates["agent-1"] = tc.nodeState
			run.NodeOutputs["agent-1"] = tc.output
			if err := f.tasks.UpsertWorkflowNodeRun(ctx, NodeRun{
				ID: run.ID + ":agent-1::1:1", RunID: run.ID, NodeID: "agent-1",
				Attempt: 1, Status: tc.nodeStatus, Output: tc.output, FinishedAt: time.Now().UTC(),
			}); err != nil {
				t.Fatalf("UpsertWorkflowNodeRun: %v", err)
			}

			svc := NewService(testRoots{root: f.root})
			svc.SetActivityBroadcaster(f.broadcast)
			if err := svc.projectWorkflowTaskStatus(ctx, f.service.tasks, run); err != nil {
				t.Fatalf("projectWorkflowTaskStatus: %v", err)
			}
			stored, err := f.service.activities.Get(ctx, agentAct.ID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if stored.Status != tc.wantStatus {
				t.Fatalf("status = %q, want %q", stored.Status, tc.wantStatus)
			}
			if !strings.Contains(stored.Summary, tc.wantInSum) {
				t.Fatalf("summary = %q, want it to contain %q", stored.Summary, tc.wantInSum)
			}
			// 看板卡片读的是任务行上的结果摘要：不写它，任务列表就只能看到
			// 用户原始需求，和对话里的结果对不上。
			task, err := f.service.tasks.GetTask(ctx, "task-1")
			if err != nil {
				t.Fatalf("GetTask: %v", err)
			}
			if task.Status != tc.wantTaskStatus {
				t.Fatalf("task status = %q, want %q", task.Status, tc.wantTaskStatus)
			}
			if !strings.Contains(task.ResultSummary, tc.wantInSum) {
				t.Fatalf("task result summary = %q, want it to contain %q", task.ResultSummary, tc.wantInSum)
			}
		})
	}
}
