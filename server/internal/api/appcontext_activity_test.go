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
			if got := pickAgent(tc.statuses, ""); got != tc.want {
				t.Fatalf("pickAgent(%+v) = %q, want %q", tc.statuses, got, tc.want)
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

// 生产事故（2026-11-20）：llmux 恢复后 codex 变成可用，而 agents.json 里 codex
// 排在 claude 前面，新建任务于是整批落到 codex。默认 Agent 必须先看节点配置，
// 探针顺序只做兜底。
func TestPickAgentPrefersConfiguredTaskAgent(t *testing.T) {
	statuses := []agent.Status{
		{Name: "gemini", Available: true},
		{Name: "codex", Available: true},
		{Name: "claude", Installed: true},
	}
	if got := pickAgent(statuses, "claude"); got != "claude" {
		t.Fatalf("pickAgent(preferred=claude) = %q, want claude", got)
	}
	// 探针还没跑完（Available=false）时偏好也必须生效：否则启动窗口里又会退回
	// 列表里第一个可用的 Agent。
	if got := pickAgent([]agent.Status{{Name: "codex", Available: true}, {Name: "claude", Installed: true}}, "claude"); got != "claude" {
		t.Fatalf("已安装的 preferred 应胜出，got %q", got)
	}
	// 配置的 Agent 根本没装：不能硬选它，回退原启发式。
	if got := pickAgent([]agent.Status{{Name: "codex", Available: true}, {Name: "claude"}}, "claude"); got != "codex" {
		t.Fatalf("未安装的 preferred 不应胜出，got %q", got)
	}
	// 配置的 Agent 不在列表里：回退原启发式（第一个可用）。
	if got := pickAgent(statuses, "grok"); got != "gemini" {
		t.Fatalf("缺失的 preferred 应回退启发式，got %q", got)
	}
	// 不配偏好时行为完全不变。
	if got := pickAgent(statuses, ""); got != "gemini" {
		t.Fatalf("pickAgent(preferred=\"\") = %q, want gemini", got)
	}
}

// defaultAgent 必须把节点配置的 TaskAgent 透传下去：它是 RunDirectActivity
// 选 Agent 的唯一入口。
func TestDefaultAgentUsesTaskAgent(t *testing.T) {
	app, _ := newWorkflowAdapterTestApp(t)
	app.Prober = testProberWithAgents("aaa", "claude")

	if got := app.defaultAgent(); got != "aaa" {
		t.Fatalf("defaultAgent without preference = %q, want aaa", got)
	}
	app.TaskAgent = "claude"
	if got := app.defaultAgent(); got != "claude" {
		t.Fatalf("defaultAgent with TaskAgent=claude = %q, want claude", got)
	}
}

// 新建任务主会话要带上任务默认模型（claude + sonnet），否则新会话落回
// preferences 里该 Agent 的默认模型，用户看到的还是一堆不同模型的任务。
func TestRunDirectActivityUsesTaskDefaultsForNewMainSession(t *testing.T) {
	ctx := context.Background()
	app, rootID := newWorkflowAdapterTestApp(t)
	app.Prober = testProberWithAgents("aaa", "claude")
	app.TaskAgent = "claude"
	app.TaskModel = "sonnet"

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
		kanban.Task{ID: "task-defaults", RootID: rootID, TaskNumber: 5, Name: "默认模型", Status: kanban.StatusPending},
		kanban.TaskEvent{ID: "ev-defaults", TaskID: "task-defaults", Type: "created"},
	); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	app.Kanban = kanban.NewService(app)

	var captured workflowTurnRequest
	app.workflowTurnOverride = func(_ context.Context, req workflowTurnRequest) (string, error) {
		captured = req
		return "好的", nil
	}

	task, err := store.GetTask(ctx, "task-defaults")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if _, err := app.RunDirectActivity(ctx, task, "帮我看看"); err != nil {
		t.Fatalf("RunDirectActivity: %v", err)
	}
	if captured.Agent != "claude" || captured.Model != "sonnet" {
		t.Fatalf("turn agent/model = %q/%q, want claude/sonnet", captured.Agent, captured.Model)
	}
	created, _, err := (&usecase.Service{Registry: app}).GetSession(ctx, usecase.GetSessionInput{
		RootID: rootID, Key: captured.SessionKey,
	})
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if created.Model != "sonnet" {
		t.Fatalf("session model = %q, want sonnet", created.Model)
	}
}

// 任务默认模型只属于任务默认 Agent：别的 Agent 的会话（例如修复前建的 codex
// 主会话）绝不能被塞进 claude 的模型名，那会在发送边界直接报错。
func TestRunDirectActivityDoesNotApplyTaskModelToOtherAgents(t *testing.T) {
	ctx := context.Background()
	app, rootID := newWorkflowAdapterTestApp(t)
	app.Prober = testProberWithAgents("aaa", "claude")
	app.TaskAgent = "claude"
	app.TaskModel = "sonnet"

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
		kanban.Task{ID: "task-codex", RootID: rootID, TaskNumber: 6, Name: "旧 codex 会话", Status: kanban.StatusPending},
		kanban.TaskEvent{ID: "ev-codex", TaskID: "task-codex", Type: "created"},
	); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	app.Kanban = kanban.NewService(app)

	created, err := (&usecase.Service{Registry: app}).CreateSession(ctx, usecase.CreateSessionInput{
		RootID: rootID,
		Input:  session.CreateInput{Type: session.TypeChat, Agent: "codex", Name: "旧会话", TaskID: "task-codex"},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	task, err := store.GetTask(ctx, "task-codex")
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
	if captured.Agent != "codex" || captured.Model != "" {
		t.Fatalf("turn agent/model = %q/%q, want codex with no model override", captured.Agent, captured.Model)
	}
}
