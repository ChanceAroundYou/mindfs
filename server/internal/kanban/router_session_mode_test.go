package kanban

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// run_workflow 的 session_mode（2026-11-20）：缺省 new，continue 是显式选择，
// 其它值必须当轮拒绝 —— 否则一个拼错的 mode 会静默退化成 new，用户以为接续了
// 上一段对话，实际看到的是一段全新的会话。
func TestRouterRunWorkflowToolSessionMode(t *testing.T) {
	brief := &SituationBrief{Workflows: []RouterBriefWorkflow{{ID: "wf-1"}}}
	env := &routerToolEnv{brief: brief, collector: newRouterTurnCollector(RouterConfig{})}
	tool := env.runWorkflowTool()

	call := func(args map[string]any) error {
		raw, err := json.Marshal(args)
		if err != nil {
			t.Fatalf("marshal args: %v", err)
		}
		_, err = tool.Handler(context.Background(), raw)
		return err
	}
	if err := call(map[string]any{"workflow_id": "wf-1"}); err != nil {
		t.Fatalf("default mode: %v", err)
	}
	if actions := env.collector.snapshot(); len(actions) != 1 || actions[0].SessionMode != RouterSessionModeNew {
		t.Fatalf("default session_mode actions = %#v, want new", actions)
	}

	env = &routerToolEnv{brief: brief, collector: newRouterTurnCollector(RouterConfig{})}
	tool = env.runWorkflowTool()
	if err := call(map[string]any{"workflow_id": "wf-1", "session_mode": "continue"}); err != nil {
		t.Fatalf("continue mode: %v", err)
	}
	if actions := env.collector.snapshot(); len(actions) != 1 || actions[0].SessionMode != RouterSessionModeContinue {
		t.Fatalf("continue session_mode actions = %#v", actions)
	}

	env = &routerToolEnv{brief: brief, collector: newRouterTurnCollector(RouterConfig{})}
	tool = env.runWorkflowTool()
	err := call(map[string]any{"workflow_id": "wf-1", "session_mode": "resume"})
	if err == nil || !strings.Contains(err.Error(), "session_mode") {
		t.Fatalf("invalid session_mode err = %v, want a session_mode rejection", err)
	}
	if actions := env.collector.snapshot(); len(actions) != 0 {
		t.Fatalf("invalid session_mode recorded actions: %#v", actions)
	}

	// 从库里回放的决策同样要过校验：手改/旧版本留下的非法值不能悄悄生效。
	bad := []RouterAction{{Kind: RouterActionRunWorkflow, WorkflowID: "wf-1", SessionMode: "resume"}}
	if err := validateRouterActions(brief, bad, RouterConfig{}.Normalized()); err == nil {
		t.Fatal("validateRouterActions accepted an invalid session_mode")
	}
}
