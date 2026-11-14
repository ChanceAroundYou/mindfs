package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"mindfs/server/internal/kanban"
)

type staticRouterAgent struct{}

func (staticRouterAgent) RunRouterTurn(ctx context.Context, req kanban.RouterAgentRequest) (kanban.RouterAgentResult, error) {
	for _, tool := range req.Tools {
		if tool.Name != "router_say" {
			continue
		}
		if _, err := tool.Handler(ctx, json.RawMessage(`{"body":"我先看一下","purpose":"chat"}`)); err != nil {
			return kanban.RouterAgentResult{}, err
		}
	}
	return kanban.RouterAgentResult{
		SessionKey: "router-test",
		FinalText:  "我先看一下",
		Meta:       kanban.RouterTurnMeta{PromptTokens: 11, CompletionTokens: 3},
	}, nil
}

func (staticRouterAgent) CloseRouterSession(context.Context, string, string) error { return nil }

func TestRouterAPI_ShadowDecisionAndStatus(t *testing.T) {
	h, svc, rootID, taskID := activitiesHandler(t)
	svc.SetRouterConfig(kanban.RouterConfig{Mode: kanban.RouterModeShadow, IdleClose: time.Hour})
	svc.SetRouterAgent(staticRouterAgent{})
	if router := svc.RouterServiceForRoot(rootID); router != nil {
		t.Cleanup(router.Close)
	}

	body := `{"root_id":"` + rootID + `","body":"请修复登录","workflow_choice":"empty"}`
	rec := doReq(t, h, http.MethodPost, "/api/tasks/"+taskID+"/activities", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}

	base := "/api/tasks/" + taskID + "/router-decisions?root_id=" + rootID
	var decisions []any
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		rec = doReq(t, h, http.MethodGet, base, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("list status=%d body=%s", rec.Code, rec.Body.String())
		}
		out := decodeBody(t, rec)
		items, _ := out["decisions"].([]any)
		if len(items) == 1 {
			summary := items[0].(map[string]any)
			// InsertDecision 先落行、UpdateDecisionResult 后补 actions：等到回合真正
			// 收尾再断言，否则测到的是「还在跑」的中间态。
			if kinds, ok := summary["action_kinds"].([]any); ok && len(kinds) == 1 {
				decisions = items
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(decisions) != 1 {
		t.Fatalf("decisions = %#v, want one", decisions)
	}
	summary := decisions[0].(map[string]any)
	if summary["status"] != string(kanban.RouterDecisionShadow) {
		t.Fatalf("status = %v (error=%v)", summary["status"], summary["error"])
	}
	if summary["legacy_kind"] != "workflow" {
		t.Fatalf("legacy_kind = %v", summary["legacy_kind"])
	}
	if kinds, ok := summary["action_kinds"].([]any); !ok || len(kinds) != 1 || kinds[0] != "say" {
		t.Fatalf("action_kinds = %#v", summary["action_kinds"])
	}
	if summary["agreement"] != kanban.RouterAgreementMismatch {
		t.Fatalf("agreement = %v", summary["agreement"])
	}

	decisionID, _ := summary["id"].(string)
	rec = doReq(t, h, http.MethodGet, "/api/tasks/"+taskID+"/router-decisions/"+decisionID+"?root_id="+rootID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", rec.Code, rec.Body.String())
	}
	detail := decodeBody(t, rec)
	if detail["turn_id"] == "" || detail["brief"] == nil || detail["router_session_key"] != "router-test" {
		t.Fatalf("detail = %#v", detail)
	}

	rec = doReq(t, h, http.MethodGet, "/api/tasks/"+taskID+"/router-session?root_id="+rootID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("session status=%d body=%s", rec.Code, rec.Body.String())
	}
	sessionOut, _ := decodeBody(t, rec)["session"].(map[string]any)
	if sessionOut == nil || sessionOut["key"] != "router-test" || sessionOut["status"] != string(kanban.RouterSessionActive) {
		t.Fatalf("session = %#v", sessionOut)
	}
	if _, leaked := sessionOut["config_dir"]; leaked {
		t.Fatalf("session endpoint leaked config_dir: %#v", sessionOut)
	}

	rec = doReq(t, h, http.MethodGet, "/api/router/status?root_id="+rootID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status status=%d body=%s", rec.Code, rec.Body.String())
	}
	status, _ := decodeBody(t, rec)["router"].(map[string]any)
	if status == nil || status["mode"] != string(kanban.RouterModeShadow) || status["enabled"] != true {
		t.Fatalf("router status = %#v", status)
	}
}

func TestRouterAPI_RequiresRootAndTaskScope(t *testing.T) {
	h, _, rootID, taskID := activitiesHandler(t)
	for _, path := range []string{
		"/api/tasks/" + taskID + "/router-decisions",
		"/api/tasks/" + taskID + "/router-decisions?root_id=" + rootID + "&before=not-a-time",
		"/api/tasks/" + taskID + "/router-decisions/dec_missing?root_id=" + rootID,
	} {
		rec := doReq(t, h, http.MethodGet, path, "")
		if rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d body=%s", path, rec.Code, rec.Body.String())
		}
	}
}

func TestTruncateRouterRunes(t *testing.T) {
	if got := truncateRouterRunes("你好世界", 2); got != "你好…" {
		t.Fatalf("truncate = %q", got)
	}
	if got := truncateRouterRunes("short", 10); got != "short" {
		t.Fatalf("short truncate = %q", got)
	}
}