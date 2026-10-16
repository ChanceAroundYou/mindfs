package agent

import (
	"context"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
)

func loadPoolTestConfig(t *testing.T) Config {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller failed")
	}
	cfgPath := filepath.Join(filepath.Dir(thisFile), "testdata", "agents.json")
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig(%s) failed: %v", cfgPath, err)
	}
	return cfg
}

func TestPoolGetOrCreateRequiresSessionKey(t *testing.T) {
	pool := NewPool(loadPoolTestConfig(t))
	_, err := pool.GetOrCreate(context.Background(), agenttypes.OpenSessionInput{
		SessionKey: "",
		AgentName:  "gemini",
		RootPath:   t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "session key required") {
		t.Fatalf("expected session key required error, got: %v", err)
	}
}

func TestPoolSupportsDeveloperInstructionsByConfiguredProtocol(t *testing.T) {
	pool := NewPool(Config{Agents: []Definition{
		{Name: "codex-custom", Protocol: ProtocolCodexSDK},
		{Name: "claude-custom", Protocol: ProtocolClaudeSDK},
		{Name: "acp-custom", Protocol: ProtocolACP},
	}})
	defer pool.CloseAll()

	if !pool.SupportsDeveloperInstructions("codex-custom") {
		t.Fatal("codex-sdk should support developer instructions")
	}
	if !pool.SupportsDeveloperInstructions("claude-custom") {
		t.Fatal("claude-sdk should support developer instructions")
	}
	if pool.SupportsDeveloperInstructions("acp-custom") {
		t.Fatal("ACP should use the user-message compatibility path")
	}
}

func TestPoolGetOrCreateUnknownAgent(t *testing.T) {
	pool := NewPool(loadPoolTestConfig(t))
	_, err := pool.GetOrCreate(context.Background(), agenttypes.OpenSessionInput{
		SessionKey: "s-1",
		AgentName:  "unknown-agent",
		RootPath:   t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "agent not configured") {
		t.Fatalf("expected agent not configured error, got: %v", err)
	}
}

func TestPoolGetOrCreateUsesAgentsJSONConfig(t *testing.T) {
	cfg := loadPoolTestConfig(t)
	def, ok := cfg.GetAgent("gemini")
	if !ok {
		t.Fatalf("expected gemini in test agents.json")
	}
	def.Command = "this-command-should-not-exist-for-tests"
	for i := range cfg.Agents {
		if cfg.Agents[i].Name == "gemini" {
			cfg.Agents[i] = def
		}
	}

	pool := NewPool(cfg)
	_, err := pool.GetOrCreate(context.Background(), agenttypes.OpenSessionInput{
		SessionKey: "s-2",
		AgentName:  "gemini",
		RootPath:   t.TempDir(),
	})
	if err == nil {
		t.Fatalf("expected start error from non-existent command")
	}
	if !strings.Contains(err.Error(), "this-command-should-not-exist-for-tests") {
		t.Fatalf("expected overridden command in error, got: %v", err)
	}
}

func TestPoolCloseAndCloseAll(t *testing.T) {
	pool := NewPool(loadPoolTestConfig(t))
	pool.sessions["s-3"] = &sessionEntry{
		agentName:  "test-agent",
		sessionKey: "s-3",
		session:    nil,
	}

	pool.Close("s-3")
	if _, ok := pool.sessions["s-3"]; ok {
		t.Fatalf("expected session removed after Close")
	}

	pool.CloseAll()
	if len(pool.sessions) != 0 {
		t.Fatalf("expected sessions cleared by CloseAll")
	}
}

func TestPoolCloseAllCancelsProcessContextBeforeRuntimeLocks(t *testing.T) {
	pool := NewPool(loadPoolTestConfig(t))
	processCtx := pool.processCtx
	group := runtimeGroup{agentName: "gemini", protocol: ProtocolACP}
	unlock := pool.lockRuntimeGroups([]runtimeGroup{group})
	done := make(chan struct{})
	go func() {
		pool.CloseAll()
		close(done)
	}()

	select {
	case <-processCtx.Done():
	case <-time.After(time.Second):
		unlock()
		<-done
		t.Fatal("process context was not canceled before waiting for runtime lock")
	}
	unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("CloseAll did not finish after runtime lock was released")
	}
}

func TestPoolCloseAllClosesClaudeSessionProcesses(t *testing.T) {
	pool := NewPool(loadPoolTestConfig(t))
	sess := &idleReleaseTestSession{}
	pool.sessions["claude-session"] = &sessionEntry{
		agentName:  "claude",
		sessionKey: "claude-session",
		protocol:   ProtocolClaudeSDK,
		session:    sess,
	}

	pool.CloseAll()
	if sess.closeCount != 1 {
		t.Fatalf("Claude session Close calls = %d, want 1", sess.closeCount)
	}
}

type idleReleaseTestSession struct {
	agenttypes.Session
	closeCount int
	closeErr   error
}

func (s *idleReleaseTestSession) Close() error {
	s.closeCount++
	return s.closeErr
}

func TestPoolGetOrCreateAfterCloseAll(t *testing.T) {
	pool := NewPool(loadPoolTestConfig(t))
	pool.CloseAll()

	_, err := pool.GetOrCreate(context.Background(), agenttypes.OpenSessionInput{
		SessionKey: "s-closed",
		AgentName:  "gemini",
		RootPath:   t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "agent pool closed") {
		t.Fatalf("expected agent pool closed error, got: %v", err)
	}
}

func TestPoolConfigReturnsLoadedConfig(t *testing.T) {
	cfg := loadPoolTestConfig(t)
	pool := NewPool(cfg)

	got := pool.Config()
	if _, ok := got.GetAgent("gemini"); !ok {
		t.Fatalf("expected gemini in pool config")
	}
}

func TestPoolUpdateConfigAppliesRuntimeEnvWithoutFreezingHostedFields(t *testing.T) {
	local := Config{
		Agents: []Definition{
			{Name: "codex", Command: "local-codex", Brief: "local brief", Env: map[string]string{"LOCAL": "1"}},
		},
	}
	hostedV1 := Config{
		Agents: []Definition{
			{Name: "codex", Command: "hosted-codex", Brief: "hosted brief v1"},
			{Name: "hosted-only", Command: "hosted-only", Brief: "hosted only v1"},
		},
	}
	pool := NewPool(MergeHostedConfig(hostedV1, local))
	if err := pool.SetAgentEnv("codex", map[string]string{"RUNTIME": "1"}); err != nil {
		t.Fatalf("SetAgentEnv: %v", err)
	}

	hostedV2 := Config{
		Agents: []Definition{
			{Name: "codex", Command: "hosted-codex-v2", Brief: "hosted brief v2"},
			{Name: "hosted-only", Command: "hosted-only-v2", Brief: "hosted only v2"},
		},
	}
	effective := pool.UpdateConfig(MergeHostedConfig(hostedV2, local))

	codex, ok := effective.GetAgent("codex")
	if !ok {
		t.Fatalf("expected codex")
	}
	if codex.Command != "local-codex" || codex.Brief != "local brief" {
		t.Fatalf("local override not preserved: %+v", codex)
	}
	if !reflect.DeepEqual(codex.Env, map[string]string{"RUNTIME": "1"}) {
		t.Fatalf("runtime env not preserved: %#v", codex.Env)
	}
	hostedOnly, ok := effective.GetAgent("hosted-only")
	if !ok {
		t.Fatalf("expected hosted-only")
	}
	if hostedOnly.Command != "hosted-only-v2" || hostedOnly.Brief != "hosted only v2" {
		t.Fatalf("hosted update was frozen: %+v", hostedOnly)
	}
}

func TestProbeInstalledAgentWithPoolSkipsMissingCommand(t *testing.T) {
	def := Definition{
		Name:    "missing-agent",
		Command: "mindfs-test-agent-command-that-does-not-exist",
	}
	status := probeInstallStatus(def.Name, def, time.Now().UTC())
	if status.Installed {
		t.Fatalf("test command unexpectedly exists in PATH")
	}

	got := probeInstalledAgentWithPool(context.Background(), def.Name, def, nil, nil, status, probePhaseBackground)
	if got.Installed {
		t.Fatalf("missing command should not be marked installed: %+v", got)
	}
	if got.Available {
		t.Fatalf("missing command should not be available: %+v", got)
	}
	if !strings.Contains(got.ProbeError, def.Command) {
		t.Fatalf("probe error = %q, want command name", got.ProbeError)
	}
}
