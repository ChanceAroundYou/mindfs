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

func TestExplainTerminalTurnErrorScheduledTellsUserToWait(t *testing.T) {
	terminal := &agenttypes.TurnTerminalError{Reason: "blocking_limit", Message: "Prompt is too long"}
	err := explainTerminalTurnError(&overflowCompactScheduledError{err: terminal})
	msg := err.Error()
	if !strings.Contains(msg, "完成后会自动重发") || !strings.Contains(msg, "无需手动发送") {
		t.Fatalf("已排压缩的文案没有说明自动重发：%q", msg)
	}
	if strings.Contains(msg, "请重新发送") {
		t.Fatalf("已排压缩的文案仍在诱导用户重试：%q", msg)
	}
	if got := agenttypes.TurnTerminalReason(err); got != "blocking_limit" {
		t.Fatalf("包装后丢失原始原因：%q", got)
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

func newOverflowTestSession(t *testing.T, agent string) (*session.Manager, *session.Session) {
	t.Helper()
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := session.NewManager(root)
	created, err := manager.Create(context.Background(), session.CreateInput{
		Type:  session.TypeChat,
		Agent: agent,
		Name:  "chat",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return manager, created
}

func overflowFailureContext(manager *session.Manager, current *session.Session) sendFailureContext {
	return sendFailureContext{
		Err:               &agenttypes.TurnTerminalError{Reason: "blocking_limit", Message: "Prompt is too long"},
		RootID:            "root",
		SessionKey:        current.Key,
		AgentName:         "claude",
		Manager:           manager,
		Current:           current,
		CurrentSession:    &fakeUsecaseAgentSession{id: "claude-session"},
		SawAssistantChunk: true,
	}
}

// applyExplainedSendFailure 复刻生产顺序：先决定失败动作，再把终端错误翻成
// 用户文案（session_send.go 在 persist 之前做这两步）。
func applyExplainedSendFailure(service *Service, in sendFailureContext) (agenttypes.Session, error) {
	sess, err := service.applySendFailure(in)
	return sess, explainTerminalTurnError(err)
}

// 上下文用满之后补一次有界压缩：复用 hygiene 的 /compact 通道，同一段溢出
// 连续命中时只允许一个在途压缩，且原始错误链（blocking_limit）不能丢。
func TestApplySendFailureTerminalSchedulesOneShotCompaction(t *testing.T) {
	tracker := NewHygieneTracker(hygieneConfig())
	service := newHygieneTestService(tracker)
	manager, current := newOverflowTestSession(t, "claude")

	release := make(chan struct{})
	defer close(release)
	sent := make(chan SendMessageInput, 4)
	service.hygieneSend = func(_ context.Context, in SendMessageInput) error {
		sent <- in
		<-release
		if in.onCompactSeen != nil {
			in.onCompactSeen()
		}
		return nil
	}

	in := overflowFailureContext(manager, current)
	for attempt := 0; attempt < 2; attempt++ {
		_, gotErr := applyExplainedSendFailure(service, in)
		if gotErr == nil {
			t.Fatal("terminal failure returned nil error")
		}
		if !strings.Contains(gotErr.Error(), "已开始压缩") {
			t.Fatalf("attempt %d: user message does not report the scheduled compaction: %v", attempt, gotErr)
		}
		if reason := agenttypes.TurnTerminalReason(gotErr); reason != "blocking_limit" {
			t.Fatalf("attempt %d: wrapped reason = %q, want blocking_limit (err=%v)", attempt, reason, gotErr)
		}
	}

	select {
	case got := <-sent:
		if !got.hygieneCompact || got.Content != hygieneCompactCommand {
			t.Fatalf("scheduled turn is not a compact turn: %#v", got)
		}
		if got.Key != current.Key || got.Agent != "claude" {
			t.Fatalf("compact went to the wrong session: %#v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("overflow did not schedule a compaction")
	}
	select {
	case extra := <-sent:
		t.Fatalf("second overflow scheduled a duplicate compaction: %#v", extra)
	case <-time.After(200 * time.Millisecond):
	}
}

// 没有本地压缩通道的 harness 只能维持旧文案，不能假装已开始压缩。
func TestApplySendFailureTerminalKeepsManualHintWithoutCompaction(t *testing.T) {
	tracker := NewHygieneTracker(hygieneConfig())
	service := newHygieneTestService(tracker)
	manager, current := newOverflowTestSession(t, "gemini")

	called := make(chan struct{}, 1)
	service.hygieneSend = func(context.Context, SendMessageInput) error {
		called <- struct{}{}
		return nil
	}

	_, gotErr := applyExplainedSendFailure(service, overflowFailureContext(manager, current))
	if gotErr == nil {
		t.Fatal("terminal failure returned nil error")
	}
	if strings.Contains(gotErr.Error(), "已开始压缩") {
		t.Fatalf("unsupported harness reported a compaction that cannot run: %v", gotErr)
	}
	if !strings.Contains(gotErr.Error(), "请先压缩") {
		t.Fatalf("user message lost the manual next step: %v", gotErr)
	}
	select {
	case <-called:
		t.Fatal("unsupported harness was asked to compact")
	case <-time.After(200 * time.Millisecond):
	}
}

// 压缩通道刚失败过就不再自动重试，否则每一轮都用同一个失败换一条噪声。
func TestApplySendFailureTerminalSkipsCompactionAfterCompactFailure(t *testing.T) {
	tracker := NewHygieneTracker(hygieneConfig())
	service := newHygieneTestService(tracker)
	manager, current := newOverflowTestSession(t, "claude")
	tracker.MarkAutoFailed("root", current.Key)

	called := make(chan struct{}, 1)
	service.hygieneSend = func(context.Context, SendMessageInput) error {
		called <- struct{}{}
		return nil
	}

	_, gotErr := applyExplainedSendFailure(service, overflowFailureContext(manager, current))
	if gotErr == nil || !strings.Contains(gotErr.Error(), "请先压缩") {
		t.Fatalf("expected manual hint after a failed compaction, got %v", gotErr)
	}
	select {
	case <-called:
		t.Fatal("compaction was retried after it already failed")
	case <-time.After(200 * time.Millisecond):
	}
}

// 压缩回合自己撞上溢出时不能再排一次压缩（否则递归）。
func TestApplySendFailureOnCompactTurnDoesNotScheduleAnother(t *testing.T) {
	tracker := NewHygieneTracker(hygieneConfig())
	service := newHygieneTestService(tracker)
	manager, current := newOverflowTestSession(t, "claude")

	called := make(chan struct{}, 1)
	service.hygieneSend = func(context.Context, SendMessageInput) error {
		called <- struct{}{}
		return nil
	}

	in := overflowFailureContext(manager, current)
	in.HygieneCompact = true
	_, gotErr := applyExplainedSendFailure(service, in)
	if gotErr == nil || !strings.Contains(gotErr.Error(), "请先压缩") {
		t.Fatalf("compact turn should not re-schedule compaction, got %v", gotErr)
	}
	select {
	case <-called:
		t.Fatal("compact turn re-scheduled itself")
	case <-time.After(200 * time.Millisecond):
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
