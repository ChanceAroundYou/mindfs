package usecase

import (
	"context"
	"errors"
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
