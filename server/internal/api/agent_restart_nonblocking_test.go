package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mindfs/server/internal/agent"
)

func TestScheduleAgentRestartReturnsBeforeKill(t *testing.T) {
	killStarted := make(chan struct{})
	releaseKill := make(chan struct{})
	probeCalled := make(chan string, 1)

	returned := make(chan struct{})
	go func() {
		scheduleAgentRestart("claude", agentRestartHooks{
			kill: func(name string) {
				close(killStarted)
				<-releaseKill
			},
			probe: func(name string) { probeCalled <- name },
		})
		close(returned)
	}()

	select {
	case <-killStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("kill was never scheduled")
	}
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("restart scheduling waited for kill")
	}
	select {
	case name := <-probeCalled:
		t.Fatalf("probe ran before kill finished: %s", name)
	default:
	}

	close(releaseKill)
	select {
	case name := <-probeCalled:
		if name != "claude" {
			t.Fatalf("probe received %q, want claude", name)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("probe was not called after kill")
	}
}

func TestHandleAgentRestartRespondsBeforeKill(t *testing.T) {
	handler := &HTTPHandler{AppContext: &AppContext{
		Agents: agent.NewPool(agent.Config{Agents: []agent.Definition{
			{Name: "claude", Protocol: agent.ProtocolClaudeSDK},
		}}),
	}}
	request := httptest.NewRequest(http.MethodPost, "/api/agents/restart", strings.NewReader(`{"agent":"claude"}`))
	recorder := httptest.NewRecorder()

	handler.handleAgentRestart(recorder, request)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d (body %s)", recorder.Code, http.StatusAccepted, recorder.Body.String())
	}
	var body struct {
		Restarting bool   `json:"restarting"`
		Agent      string `json:"agent"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Restarting || body.Agent != "claude" {
		t.Fatalf("unexpected response: %+v", body)
	}
}
