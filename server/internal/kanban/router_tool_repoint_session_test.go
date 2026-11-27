package kanban

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestRouterRepointSessionToolRecordsAction(t *testing.T) {
	collector := newRouterTurnCollector(RouterConfig{})
	env := &routerToolEnv{
		brief:            &SituationBrief{Task: RouterBriefTask{ID: "task-1", RootID: "root-1"}},
		collector:        collector,
		sessionRepointer: &stubSessionRepointer{},
	}
	tools := env.tools()
	var tool *RouterToolSpec
	for i := range tools {
		if tools[i].Name == "router_repoint_session" {
			tool = &tools[i]
		}
	}
	if tool == nil {
		t.Fatal("router_repoint_session is not registered")
	}
	if tool.Class != RouterToolWrite {
		t.Fatalf("tool class = %q, want write", tool.Class)
	}

	out, err := tool.Handler(context.Background(), json.RawMessage(`{"session_key":"  sess-1  ","target_path":"  /tmp/target  "}`))
	if err != nil || !strings.Contains(out, `"status":"recorded"`) {
		t.Fatalf("handler out=%q err=%v", out, err)
	}
	actions := collector.snapshot()
	if len(actions) != 1 {
		t.Fatalf("actions = %#v, want one", actions)
	}
	got := actions[0]
	if got.Kind != RouterActionRepointSession || got.SessionKey != "sess-1" || got.TargetPath != "/tmp/target" || got.Seq != 0 {
		t.Fatalf("recorded action = %#v", got)
	}

	// 空 target_path 透传为空，不默认填主 checkout。
	emptyEnv := &routerToolEnv{
		brief:            &SituationBrief{Task: RouterBriefTask{ID: "task-2", RootID: "root-1"}},
		collector:        newRouterTurnCollector(RouterConfig{}),
		sessionRepointer: &stubSessionRepointer{},
	}
	if _, err := emptyEnv.repointSessionTool().Handler(context.Background(), json.RawMessage(`{"session_key":"sess-2"}`)); err != nil {
		t.Fatalf("empty target_path: %v", err)
	}
	if got := emptyEnv.collector.snapshot()[0]; got.TargetPath != "" {
		t.Fatalf("empty target_path became %q", got.TargetPath)
	}
}

func TestRouterRepointSessionToolRejectsBadCalls(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		env    *routerToolEnv
		wantIn string
	}{
		{"missing_key", `{}`, &routerToolEnv{brief: &SituationBrief{Task: RouterBriefTask{RootID: "root-1"}}, collector: newRouterTurnCollector(RouterConfig{}), sessionRepointer: &stubSessionRepointer{}}, "session_key"},
		{"blank_key", `{"session_key":"   "}`, &routerToolEnv{brief: &SituationBrief{Task: RouterBriefTask{RootID: "root-1"}}, collector: newRouterTurnCollector(RouterConfig{}), sessionRepointer: &stubSessionRepointer{}}, "session_key"},
		{"malformed", `{`, &routerToolEnv{brief: &SituationBrief{Task: RouterBriefTask{RootID: "root-1"}}, collector: newRouterTurnCollector(RouterConfig{}), sessionRepointer: &stubSessionRepointer{}}, "invalid arguments"},
		{"nil_port", `{"session_key":"sess"}`, &routerToolEnv{brief: &SituationBrief{Task: RouterBriefTask{RootID: "root-1"}}, collector: newRouterTurnCollector(RouterConfig{})}, "端口未配置"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.env.repointSessionTool().Handler(context.Background(), json.RawMessage(tc.raw))
			if err == nil || !strings.Contains(err.Error(), tc.wantIn) {
				t.Fatalf("err = %v, want %q", err, tc.wantIn)
			}
			if len(tc.env.collector.snapshot()) != 0 {
				t.Fatalf("rejected call recorded actions: %#v", tc.env.collector.snapshot())
			}
		})
	}
}

func TestValidateRouterActionsRepoint(t *testing.T) {
	brief := &SituationBrief{Task: RouterBriefTask{ID: "task-1", RootID: "root-1", Status: StatusPending}}
	repoint := func(seq int) RouterAction {
		return RouterAction{Seq: seq, Kind: RouterActionRepointSession, SessionKey: "sess-1"}
	}
	cases := []struct {
		name    string
		actions []RouterAction
		wantErr string
	}{
		{"valid", []RouterAction{repoint(0)}, ""},
		{"missing_key", []RouterAction{{Seq: 0, Kind: RouterActionRepointSession}}, "session_key"},
		{"two_repoints", []RouterAction{repoint(0), repoint(1)}, "at most one repoint_session"},
		{"with_run_workflow", []RouterAction{repoint(0), {Seq: 1, Kind: RouterActionMessageAgent, Body: "go"}}, "cannot be combined with an execution action"},
		{"with_answer_waiting", []RouterAction{repoint(0), {Seq: 1, Kind: RouterActionAnswerWaiting, Body: "answer"}}, "cannot be combined with an execution action"},
		{"with_complete_task", []RouterAction{repoint(0), {Seq: 1, Kind: RouterActionComplete, Summary: "done"}}, "cannot be combined with complete_task"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRouterActions(brief, tc.actions, RouterConfig{}.Normalized())
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("validate: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}

	// 运行期事件回合默认拒绝：巡检/结算没有用户留言锚点，不能自动搬会话。
	err := validateRuntimeEventActions(brief, []RouterAction{repoint(0)}, RouterConfig{})
	if err == nil || !strings.Contains(err.Error(), "not allowed for runtime event turns") {
		t.Fatalf("runtime event validation err = %v", err)
	}
}

type stubSessionRepointer struct{}

func (s *stubSessionRepointer) RepointSessionForRouter(context.Context, SessionRepointInput) error {
	return nil
}
