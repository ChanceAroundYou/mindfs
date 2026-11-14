package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"mindfs/server/internal/agent"
	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/kanban"
	"mindfs/server/internal/session"
)

// testProberWithAgents 造一个「这些 Agent 都可用」的探针。ReportSuccess 是
// 生产链路里真实存在的入口（运行时成功会标可用），不额外开测试专用写口。
func testProberWithAgents(names ...string) *agent.Prober {
	prober := agent.NewProber(nil, nil, time.Minute)
	for _, name := range names {
		prober.ReportSuccess(name)
	}
	return prober
}

// 纯对话模式必须自己决定用哪个 Agent：RunDirectActivity 走的是
// usecase.SendMessage，空 agent 会在 EnsureAgentBinding 里变成 "agent required"，
// 用户看到的是一句实现细节，而不是一次正常回复（2026-11-14 隔离实例实测）。
func TestPickDefaultAgent(t *testing.T) {
	cases := []struct {
		name     string
		statuses []agent.Status
		want     string
	}{
		{name: "no agents", want: ""},
		{
			name:     "single configured but unavailable agent still usable as last resort",
			statuses: []agent.Status{{Name: "codex"}},
			want:     "codex",
		},
		{
			name:     "prefers installed over merely configured",
			statuses: []agent.Status{{Name: "codex"}, {Name: "claude", Installed: true}},
			want:     "claude",
		},
		{
			name:     "prefers available over installed",
			statuses: []agent.Status{{Name: "codex", Installed: true}, {Name: "claude", Installed: true, Available: true}},
			want:     "claude",
		},
		{
			name:     "keeps configured order among equals",
			statuses: []agent.Status{{Name: "a", Available: true}, {Name: "b", Available: true}},
			want:     "a",
		},
		{
			name:     "ignores unnamed entries",
			statuses: []agent.Status{{Name: "  "}, {Name: "claude", Installed: true}},
			want:     "claude",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pickDefaultAgent(tc.statuses); got != tc.want {
				t.Fatalf("pickDefaultAgent(%+v) = %q, want %q", tc.statuses, got, tc.want)
			}
		})
	}
}

// 新建任务主会话时要把选中的 Agent 同时写进会话与这一轮请求；否则会话里没有
// agent 记录，下一次复用又要重新猜。
func TestRunDirectActivityUsesDefaultAgentForNewMainSession(t *testing.T) {
	ctx := context.Background()
	app, rootID := newWorkflowAdapterTestApp(t)
	app.Prober = testProberWithAgents("claude")

	root, err := app.GetRoot(rootID)
	if err != nil {
		t.Fatalf("GetRoot: %v", err)
	}
	store, err := kanban.NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateTask(ctx,
		kanban.Task{ID: "task-agent", RootID: rootID, TaskNumber: 2, Name: "默认 Agent", Status: kanban.StatusPending},
		kanban.TaskEvent{ID: "ev-agent", TaskID: "task-agent", Type: "created"},
	); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	app.Kanban = kanban.NewService(app)

	var captured workflowTurnRequest
	app.workflowTurnOverride = func(_ context.Context, req workflowTurnRequest) (string, error) {
		captured = req
		return "好的", nil
	}

	task, err := store.GetTask(ctx, "task-agent")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if _, err := app.RunDirectActivity(ctx, task, "帮我看看"); err != nil {
		t.Fatalf("RunDirectActivity: %v", err)
	}
	if captured.Agent != "claude" {
		t.Fatalf("turn agent = %q, want claude", captured.Agent)
	}
	created, _, err := (&usecase.Service{Registry: app}).GetSession(ctx, usecase.GetSessionInput{
		RootID: rootID, Key: captured.SessionKey,
	})
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got := session.InferAgentFromSession(created); got != "claude" {
		t.Fatalf("session agent = %q, want claude", got)
	}
}

// 已有主会话时沿用会话自己的 Agent：任务中途换 Agent 会丢掉上下文连续性，
// 默认值只负责「还没有答案」的情况。
func TestRunDirectActivityKeepsAgentOfExistingMainSession(t *testing.T) {
	ctx := context.Background()
	app, rootID := newWorkflowAdapterTestApp(t)
	app.Prober = testProberWithAgents("claude")

	root, err := app.GetRoot(rootID)
	if err != nil {
		t.Fatalf("GetRoot: %v", err)
	}
	store, err := kanban.NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateTask(ctx,
		kanban.Task{ID: "task-reuse", RootID: rootID, TaskNumber: 3, Name: "已有会话", Status: kanban.StatusPending},
		kanban.TaskEvent{ID: "ev-reuse", TaskID: "task-reuse", Type: "created"},
	); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	app.Kanban = kanban.NewService(app)

	created, err := (&usecase.Service{Registry: app}).CreateSession(ctx, usecase.CreateSessionInput{
		RootID: rootID,
		Input:  session.CreateInput{Type: session.TypeChat, Agent: "codex", Name: "旧会话", TaskID: "task-reuse"},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	task, err := store.GetTask(ctx, "task-reuse")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	task.MainSessionKey = created.Key
	if err := store.UpdateTask(ctx, task); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	var captured workflowTurnRequest
	app.workflowTurnOverride = func(_ context.Context, req workflowTurnRequest) (string, error) {
		captured = req
		return "好的", nil
	}
	if _, err := app.RunDirectActivity(ctx, task, "继续"); err != nil {
		t.Fatalf("RunDirectActivity: %v", err)
	}
	if captured.SessionKey != created.Key || captured.Agent != "codex" {
		t.Fatalf("turn = %#v, want existing session %q with agent codex", captured, created.Key)
	}
}

// 一个 Agent 都没有时，错误必须说清楚是「没有可用的 Agent」，而不是让
// SendMessage 深处抛 "agent required"。
func TestRunDirectActivityWithoutAvailableAgentFailsClearly(t *testing.T) {
	ctx := context.Background()
	app, rootID := newWorkflowAdapterTestApp(t)
	app.Prober = testProberWithAgents()

	root, err := app.GetRoot(rootID)
	if err != nil {
		t.Fatalf("GetRoot: %v", err)
	}
	store, err := kanban.NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateTask(ctx,
		kanban.Task{ID: "task-noagent", RootID: rootID, TaskNumber: 4, Name: "没有 Agent", Status: kanban.StatusPending},
		kanban.TaskEvent{ID: "ev-noagent", TaskID: "task-noagent", Type: "created"},
	); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	app.Kanban = kanban.NewService(app)

	task, err := store.GetTask(ctx, "task-noagent")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	_, err = app.RunDirectActivity(ctx, task, "在吗")
	if err == nil || !strings.Contains(err.Error(), "Agent") {
		t.Fatalf("err = %v, want a clear no-Agent error", err)
	}
}
