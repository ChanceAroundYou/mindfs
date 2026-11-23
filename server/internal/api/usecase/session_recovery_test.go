package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	rootfs "mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

func TestSendMessageUserTimestampUsesInputWhenPresent(t *testing.T) {
	fallback := time.Date(2026, 7, 29, 10, 0, 30, 0, time.UTC)
	actual := time.Date(2026, 7, 29, 10, 0, 0, int(123*time.Millisecond), time.FixedZone("CST", 8*60*60))

	got := sendMessageUserTimestamp(SendMessageInput{UserTimestamp: actual}, fallback)

	if !got.Equal(actual.UTC()) {
		t.Fatalf("sendMessageUserTimestamp = %s, want %s", got.Format(time.RFC3339Nano), actual.UTC().Format(time.RFC3339Nano))
	}
}

func TestSendMessageUserTimestampFallsBackToStartTime(t *testing.T) {
	fallback := time.Date(2026, 7, 29, 10, 0, 30, 0, time.UTC)

	got := sendMessageUserTimestamp(SendMessageInput{}, fallback)

	if !got.Equal(fallback) {
		t.Fatalf("sendMessageUserTimestamp = %s, want fallback %s", got.Format(time.RFC3339Nano), fallback.Format(time.RFC3339Nano))
	}
}

func TestIsNonRecoverableAgentError(t *testing.T) {
	testCases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("codex turn failed: exceeded retry limit, last status: 429 Too Many Requests"), true},
		{errors.New("remote compaction failed while compact_remote retried"), true},
		{errors.New("usageLimitExceeded"), true},
		{errors.New("responseTooManyFailedAttempts"), true},
		{errors.New("temporary websocket EOF"), false},
		{context.Canceled, false},
	}

	for _, tc := range testCases {
		if got := isNonRecoverableAgentError(tc.err); got != tc.want {
			t.Fatalf("isNonRecoverableAgentError(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

func TestGetSessionContextWindowFallsBackToPersistedValue(t *testing.T) {
	rootDir := t.TempDir()
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	manager := session.NewManager(root)
	current, err := manager.Create(context.Background(), session.CreateInput{
		Type:  session.TypeChat,
		Agent: "codex",
		Name:  "chat",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	want := agenttypes.ContextWindow{
		TotalTokens:        321,
		ModelContextWindow: 200000,
	}
	if err := manager.UpdateLastContextWindow(context.Background(), current, want); err != nil {
		t.Fatalf("update context window: %v", err)
	}

	service := Service{Registry: &commandTestRegistry{root: root, manager: manager}}
	got, err := service.GetSessionContextWindow(context.Background(), GetSessionContextWindowInput{
		RootID: root.ID,
		Key:    current.Key,
	})
	if err != nil {
		t.Fatalf("get context window: %v", err)
	}
	if got != want {
		t.Fatalf("context window = %#v, want %#v", got, want)
	}
}

func TestRecoverAgentTurnStopsOnNonRecoverableError(t *testing.T) {
	rootDir := t.TempDir()
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	manager := session.NewManager(root)
	current, err := manager.Create(context.Background(), session.CreateInput{
		Type:  session.TypeChat,
		Agent: "codex",
		Name:  "chat",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	service := Service{}
	runtime := &fakeUsecaseAgentSession{id: "codex-thread"}
	var sent []string
	gotSess, err := service.recoverAgentTurn(context.Background(), SendRecoveryInput{
		RootID:            root.ID,
		SessionKey:        current.Key,
		Manager:           manager,
		Current:           current,
		AgentName:         "codex",
		CurrentSession:    runtime,
		Prompt:            "original prompt",
		SawAssistantChunk: true,
		SendWithAttachment: func(_ agenttypes.Session, content string) error {
			sent = append(sent, content)
			return errors.New("codex turn failed: exceeded retry limit, last status: 429 Too Many Requests")
		},
	})
	if err == nil {
		t.Fatal("recoverAgentTurn returned nil error")
	}
	if gotSess != nil {
		t.Fatalf("recoverAgentTurn returned session %#v, want nil", gotSess)
	}
	if len(sent) != 1 || sent[0] != "continue" {
		t.Fatalf("sent = %#v, want one continue recovery attempt", sent)
	}
}

func TestCancelRuntimeAfterNonRecoverableErrorClosesSession(t *testing.T) {
	runtime := &fakeUsecaseAgentSession{id: "codex-thread"}

	cancelRuntimeAfterNonRecoverableError(runtime, nil, "codex", errors.New("429 Too Many Requests"))

	if runtime.cancelCalls != 1 {
		t.Fatalf("cancel calls = %d, want 1", runtime.cancelCalls)
	}
	if runtime.closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", runtime.closeCalls)
	}
}

func TestIsTerminalTurnError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"typed blocking_limit", &agenttypes.TurnTerminalError{Reason: "blocking_limit", Message: "Prompt is too long"}, true},
		{"typed prompt_too_long case and space", &agenttypes.TurnTerminalError{Reason: " Prompt_Too_Long ", Message: "too big"}, true},
		{"typed max_turns is retryable", &agenttypes.TurnTerminalError{Reason: "max_turns", Message: "max turns"}, false},
		{"plain overflow text falls back", errors.New("API error: Prompt is too long"), true},
		{"429 is not terminal", errors.New("429 Too Many Requests"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTerminalTurnError(tc.err); got != tc.want {
				t.Fatalf("isTerminalTurnError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestPlanSendFailure(t *testing.T) {
	terminal := &agenttypes.TurnTerminalError{Reason: "blocking_limit", Message: "Prompt is too long"}
	cases := []struct {
		name        string
		err         error
		sawChunk    bool
		recycle     bool
		recoverTurn bool
		reason      string
	}{
		{"nil", nil, true, false, false, "none"},
		{"non-recoverable wins and recycles", errors.New("429 Too Many Requests"), true, true, false, "non_recoverable"},
		{"terminal with output does not retry", terminal, true, false, false, "terminal_turn"},
		{"terminal without output does not retry", terminal, false, false, false, "terminal_turn"},
		{"plain overflow with output does not retry", errors.New("Prompt is too long"), true, false, false, "terminal_turn"},
		{"generic after output recovers", errors.New("stream ended unexpectedly"), true, false, true, "recover"},
		{"generic without output does not retry", errors.New("stream ended unexpectedly"), false, false, false, "no_assistant_output"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := planSendFailure(tc.err, tc.sawChunk)
			if got.RecycleRuntime != tc.recycle || got.RecoverTurn != tc.recoverTurn || got.Reason != tc.reason {
				t.Fatalf("planSendFailure(%v, %v) = %#v, want recycle=%v recover=%v reason=%s",
					tc.err, tc.sawChunk, got, tc.recycle, tc.recoverTurn, tc.reason)
			}
		})
	}
}

func TestExplainTerminalTurnErrorKeepsCauseAndNextStep(t *testing.T) {
	cause := &agenttypes.TurnTerminalError{Reason: "blocking_limit", Message: "Prompt is too long"}
	got := explainTerminalTurnError(cause)
	if got == nil {
		t.Fatal("explainTerminalTurnError returned nil")
	}
	if !strings.Contains(got.Error(), "压缩") {
		t.Fatalf("message does not tell the user to compact: %v", got)
	}
	if !strings.Contains(got.Error(), "Prompt is too long") {
		t.Fatalf("message lost the harness cause: %v", got)
	}
	if reason := agenttypes.TurnTerminalReason(got); reason != "blocking_limit" {
		t.Fatalf("wrapped terminal reason = %q, want blocking_limit", reason)
	}

	generic := errors.New("boom")
	if got := explainTerminalTurnError(generic); got != generic {
		t.Fatalf("generic error was replaced: %v", got)
	}
}

func TestApplySendFailureTerminalDoesNotCancelRuntime(t *testing.T) {
	runtime := &fakeUsecaseAgentSession{id: "claude-session"}
	service := Service{}
	gotSess, gotErr := service.applySendFailure(sendFailureContext{
		Err:               &agenttypes.TurnTerminalError{Reason: "blocking_limit", Message: "Prompt is too long"},
		AgentName:         "claude",
		CurrentSession:    runtime,
		SawAssistantChunk: true,
	})
	if gotErr == nil {
		t.Fatal("terminal failure returned nil error")
	}
	if gotSess != runtime {
		t.Fatalf("session = %#v, want the original runtime", gotSess)
	}
	if runtime.cancelCalls != 0 || runtime.closeCalls != 0 {
		t.Fatalf("terminal failure touched the runtime: cancel=%d close=%d, want 0/0", runtime.cancelCalls, runtime.closeCalls)
	}
}

func TestApplySendFailureNonRecoverableStillRecycles(t *testing.T) {
	runtime := &fakeUsecaseAgentSession{id: "claude-session"}
	service := Service{}
	_, gotErr := service.applySendFailure(sendFailureContext{
		Err:               errors.New("429 Too Many Requests"),
		AgentName:         "claude",
		CurrentSession:    runtime,
		SawAssistantChunk: true,
	})
	if gotErr == nil {
		t.Fatal("non-recoverable failure returned nil error")
	}
	if runtime.cancelCalls != 1 || runtime.closeCalls != 1 {
		t.Fatalf("non-recoverable failure: cancel=%d close=%d, want 1/1", runtime.cancelCalls, runtime.closeCalls)
	}
}

func TestRecoverAgentTurnStopsOnTerminalError(t *testing.T) {
	rootDir := t.TempDir()
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	manager := session.NewManager(root)
	current, err := manager.Create(context.Background(), session.CreateInput{
		Type:  session.TypeChat,
		Agent: "claude",
		Name:  "chat",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	service := Service{}
	runtime := &fakeUsecaseAgentSession{id: "claude-session"}
	var sent []string
	gotSess, err := service.recoverAgentTurn(context.Background(), SendRecoveryInput{
		RootID:            root.ID,
		SessionKey:        current.Key,
		Manager:           manager,
		Current:           current,
		AgentName:         "claude",
		CurrentSession:    runtime,
		Prompt:            "original prompt",
		SawAssistantChunk: true,
		SendWithAttachment: func(_ agenttypes.Session, content string) error {
			sent = append(sent, content)
			return &agenttypes.TurnTerminalError{Reason: "blocking_limit", Message: "Prompt is too long"}
		},
	})
	if err == nil {
		t.Fatal("recoverAgentTurn returned nil error")
	}
	if gotSess != nil {
		t.Fatalf("recoverAgentTurn returned session %#v, want nil", gotSess)
	}
	if len(sent) != 1 {
		t.Fatalf("sent %d recovery attempts, want exactly 1 (no retry after terminal error): %#v", len(sent), sent)
	}
	if reason := agenttypes.TurnTerminalReason(err); reason != "blocking_limit" {
		t.Fatalf("recoverAgentTurn error reason = %q, want blocking_limit", reason)
	}
}
