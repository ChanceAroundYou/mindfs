package kanban

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestRouterFinishWorktreeToolRecordsWriteModes(t *testing.T) {
	collector := newRouterTurnCollector(RouterConfig{})
	env := &routerToolEnv{
		brief: &SituationBrief{Task: RouterBriefTask{
			ID:              "task-1",
			Status:          StatusWaitingUser,
			CreateWorktree:  true,
			WorktreePresent: true,
		}},
		collector:        collector,
		worktreeFinisher: stubWorktreeFinisher{},
	}
	tool := env.finishWorktreeTool()
	if tool.Name != "router_finish_worktree" {
		t.Fatalf("unexpected tool name %q", tool.Name)
	}

	raw := json.RawMessage(`{"mode":"status"}`)
	out, err := tool.Handler(context.Background(), raw)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "可用") || len(collector.snapshot()) != 0 {
		t.Fatalf("status must be read-only, got %q and actions %#v", out, collector.snapshot())
	}

	raw = json.RawMessage(`{"mode":"begin"}`)
	if _, err := tool.Handler(context.Background(), raw); err != nil {
		t.Fatalf("begin: %v", err)
	}
	actions := collector.snapshot()
	if len(actions) != 1 || actions[0].Kind != RouterActionFinishWorktree || actions[0].FinishMode != "begin" {
		t.Fatalf("unexpected actions: %#v", actions)
	}
}

func TestRouterFinishWorktreeToolRejectsRunningTask(t *testing.T) {
	env := &routerToolEnv{
		brief: &SituationBrief{Task: RouterBriefTask{
			ID:              "task-1",
			Status:          StatusRunning,
			CreateWorktree:  true,
			WorktreePresent: true,
		}},
		collector:        newRouterTurnCollector(RouterConfig{}),
		worktreeFinisher: stubWorktreeFinisher{},
	}
	_, err := env.finishWorktreeTool().Handler(context.Background(), json.RawMessage(`{"mode":"begin"}`))
	if err == nil || !strings.Contains(err.Error(), "仍在运行") {
		t.Fatalf("running task must be rejected, got %v", err)
	}
	if len(env.collector.snapshot()) != 0 {
		t.Fatalf("rejected tool call must not record an action")
	}
}

type stubWorktreeFinisher struct {
	beginCalls    int
	teardownCalls int
}

func (s stubWorktreeFinisher) BeginFinishWorktree(context.Context, BeginFinishInput) (TaskDetail, error) {
	s.beginCalls++
	return TaskDetail{}, nil
}

func (s stubWorktreeFinisher) FinishTaskWorktree(context.Context, FinishWorktreeInput) (FinishWorktreeResult, error) {
	s.teardownCalls++
	return FinishWorktreeResult{}, nil
}
