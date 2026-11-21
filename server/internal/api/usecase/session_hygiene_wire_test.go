package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	rootfs "mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

// hygieneTestRegistry 给 Service 提供可选的 SessionHygiene 能力；其余接口由
// commandTestRegistry 的零值方法兜住（这些用例不碰它们）。
type hygieneTestRegistry struct {
	commandTestRegistry
	tracker *HygieneTracker
}

func (r *hygieneTestRegistry) SessionHygiene() *HygieneTracker { return r.tracker }

func newHygieneTestService(tracker *HygieneTracker) *Service {
	return &Service{Registry: &hygieneTestRegistry{tracker: tracker}}
}

// 只有自带本地压缩命令的 harness 才允许收 /compact；写错一个名字就会把
// "/compact" 当成普通提问发给 agent。
func TestHygieneAutoCompactSupportedHarnesses(t *testing.T) {
	cases := map[string]bool{
		"claude":    true,
		"codex":     true,
		" Codex ":   true,
		"codex-acp": false,
		"gemini":    false,
		"shell":     false,
		"":          false,
	}
	for harness, want := range cases {
		if got := hygieneAutoCompactSupported(harness); got != want {
			t.Fatalf("supported(%q) = %v, want %v", harness, got, want)
		}
	}
}

// Router / command / view 会话没有可压的对话上下文，也不该被提示。
func TestHygieneAppliesOnlyToChatSessions(t *testing.T) {
	cases := []struct {
		name    string
		current *session.Session
		want    bool
	}{
		{"nil", nil, false},
		{"chat", &session.Session{Type: session.TypeChat}, true},
		{"legacy empty type", &session.Session{}, true},
		{"command", &session.Session{Type: session.TypeCommand}, false},
		{"router", &session.Session{Type: session.TypeRouter}, false},
		{"view", &session.Session{Type: session.TypeView}, false},
	}
	for _, tc := range cases {
		if got := hygieneAppliesToSession(tc.current); got != tc.want {
			t.Fatalf("%s: applies = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// 斜杠命令不是一次对话回合：它既不推进轮数，也不该自己触发自动压缩。
func TestFinishHygieneTurnIgnoresSlashCommands(t *testing.T) {
	tracker := NewHygieneTracker(hygieneConfig())
	service := newHygieneTestService(tracker)
	current := &session.Session{Type: session.TypeChat, AgentCtxSeq: map[string]int{"claude": 0}}
	seedActive(tracker, "root", "s1", 3)
	// 先把提示臂好（只读快照也走同一套状态机），这样后续每一轮都会计数。
	if view := tracker.Snapshot("root", "s1", "claude", 200000); view.State != HygieneStateHint || view.TurnsSinceHint != 0 {
		t.Fatalf("precondition: %+v", view)
	}

	if due := service.finishHygieneTurn(SendMessageInput{RootID: "root", Key: "s1", Content: "/compact"}, current, 200000); due {
		t.Fatal("slash command must not trigger auto compact")
	}
	if view := tracker.Snapshot("root", "s1", "claude", 200000); view.TurnsSinceHint != 0 {
		t.Fatalf("slash command advanced turn count: %+v", view)
	}
	// 对照：同一条会话上的普通留言才开始计数。
	service.finishHygieneTurn(SendMessageInput{RootID: "root", Key: "s1", Content: "继续"}, current, 200000)
	if view := tracker.Snapshot("root", "s1", "claude", 200000); view.TurnsSinceHint != 1 {
		t.Fatalf("plain turn did not advance count: %+v", view)
	}
}

// 自动压缩回合自己不能被当成「用户又聊了一轮」，否则压缩完立刻又到期。
func TestHygieneAutoCompactTurnIsNotObserved(t *testing.T) {
	tracker := NewHygieneTracker(hygieneConfig())
	service := newHygieneTestService(tracker)
	current := &session.Session{Type: session.TypeChat, AgentCtxSeq: map[string]int{"claude": 0}}
	seedActive(tracker, "root", "s1", 3)
	tracker.Observe("root", "s1", "claude", 200000, true) // 提示在这一轮出现

	service.observeHygieneTurnStart(SendMessageInput{RootID: "root", Key: "s1", hygieneCompact: true}, current)
	if due := service.finishHygieneTurn(SendMessageInput{RootID: "root", Key: "s1", hygieneCompact: true, Content: "/compact"}, current, 200000); due {
		t.Fatal("auto compact turn scheduled another auto compact")
	}
	if view := tracker.Snapshot("root", "s1", "claude", 200000); view.TurnsSinceHint != 0 {
		t.Fatalf("auto compact turn counted as a user turn: %+v", view)
	}
}

// 压缩事件到了就清计数 —— 包括我们自己触发的这一轮。否则用户看到的提示永远
// 停在「还差 0 轮」，下一次到期也不会再派发。
func TestMarkHygieneCompactedClearsAutoCompactTurn(t *testing.T) {
	tracker := NewHygieneTracker(hygieneConfig())
	service := newHygieneTestService(tracker)
	seedActive(tracker, "root", "s1", 3)
	tracker.Observe("root", "s1", "claude", 200000, true) // 提示出现
	var due bool
	for i := 0; i < hygieneConfig().HintTurns; i++ {
		due = tracker.Observe("root", "s1", "claude", 200000, true).AutoCompactDue
	}
	if !due {
		t.Fatal("precondition: auto compact should be due")
	}

	service.markHygieneCompacted(SendMessageInput{RootID: "root", Key: "s1", hygieneCompact: true})
	view := tracker.Snapshot("root", "s1", "claude", 200000)
	if view.TurnsSinceHint != 0 || view.AutoCompactDue {
		t.Fatalf("auto compact turn did not clear counters: %+v", view)
	}
}

// 命令发出去了但 harness 没压缩（把 /compact 当普通文本）：必须记失败并停手，
// 否则每一轮都会重发一次。
func TestHygieneAutoCompactMarksFailureWithoutCompactEvent(t *testing.T) {
	tracker := NewHygieneTracker(hygieneConfig())
	service := newHygieneTestService(tracker)
	seedActive(tracker, "root", "s1", 3)
	tracker.Observe("root", "s1", "claude", 200000, true)
	sent := 0
	service.hygieneSend = func(_ context.Context, in SendMessageInput) error {
		sent++
		if !in.hygieneCompact || in.Content != hygieneCompactCommand || in.Agent != "claude" {
			t.Fatalf("unexpected auto compact input: %+v", in)
		}
		return nil
	}

	service.runHygieneAutoCompact(SendMessageInput{RootID: "root", Key: "s1", Agent: "claude"})
	if sent != 1 {
		t.Fatalf("send count = %d, want 1", sent)
	}
	if !tracker.Snapshot("root", "s1", "claude", 200000).AutoFailed {
		t.Fatal("missing compact event must mark auto_failed")
	}
}

func TestHygieneAutoCompactMarksFailureOnSendError(t *testing.T) {
	tracker := NewHygieneTracker(hygieneConfig())
	service := newHygieneTestService(tracker)
	seedActive(tracker, "root", "s1", 3)
	tracker.Observe("root", "s1", "claude", 200000, true)
	service.hygieneSend = func(context.Context, SendMessageInput) error {
		return errors.New("agent unavailable")
	}

	service.runHygieneAutoCompact(SendMessageInput{RootID: "root", Key: "s1", Agent: "claude"})
	if !tracker.Snapshot("root", "s1", "claude", 200000).AutoFailed {
		t.Fatal("send error must mark auto_failed")
	}
}

func TestHygieneAutoCompactSucceedsWhenCompactEventArrives(t *testing.T) {
	tracker := NewHygieneTracker(hygieneConfig())
	service := newHygieneTestService(tracker)
	seedActive(tracker, "root", "s1", 3)
	tracker.Observe("root", "s1", "claude", 200000, true)
	service.hygieneSend = func(_ context.Context, in SendMessageInput) error {
		if in.onCompactSeen == nil {
			t.Fatal("auto compact turn must install onCompactSeen")
		}
		in.onCompactSeen()
		return nil
	}

	service.runHygieneAutoCompact(SendMessageInput{RootID: "root", Key: "s1", Agent: "claude"})
	if tracker.Snapshot("root", "s1", "claude", 200000).AutoFailed {
		t.Fatal("successful compact reported as failure")
	}
}

// 手动入口：不支持的会话必须明确报错，绝不把 /compact 发给没有该命令的 agent。
func TestCompactSessionRejectsUnsupportedSessions(t *testing.T) {
	rootDir := t.TempDir()
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	manager := session.NewManager(root)
	tracker := NewHygieneTracker(hygieneConfig())
	service := &Service{Registry: &hygieneTestRegistry{
		commandTestRegistry: commandTestRegistry{root: root, manager: manager},
		tracker:             tracker,
	}}

	command, err := manager.Create(context.Background(), session.CreateInput{Type: session.TypeCommand, Name: "cmd"})
	if err != nil {
		t.Fatalf("create command session: %v", err)
	}
	if err := service.CompactSession(context.Background(), GetSessionContextSavingInput{RootID: root.ID, Key: command.Key}); err == nil {
		t.Fatal("command session was allowed to compact")
	}

	other, err := manager.Create(context.Background(), session.CreateInput{Type: session.TypeChat, Agent: "gemini", Name: "chat"})
	if err != nil {
		t.Fatalf("create chat session: %v", err)
	}
	if err := service.CompactSession(context.Background(), GetSessionContextSavingInput{RootID: root.ID, Key: other.Key}); err == nil {
		t.Fatal("harness without a local compaction command was allowed")
	}
}

// 手动入口对支持的会话：请求立刻返回，压缩回合在空闲后由后台发出。
func TestCompactSessionSchedulesAutoCompactTurn(t *testing.T) {
	rootDir := t.TempDir()
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	manager := session.NewManager(root)
	tracker := NewHygieneTracker(hygieneConfig())
	service := &Service{Registry: &hygieneTestRegistry{
		commandTestRegistry: commandTestRegistry{root: root, manager: manager},
		tracker:             tracker,
	}}
	got := make(chan SendMessageInput, 1)
	service.hygieneSend = func(_ context.Context, in SendMessageInput) error {
		got <- in
		return nil
	}

	created, err := manager.Create(context.Background(), session.CreateInput{Type: session.TypeChat, Agent: "claude", Name: "chat"})
	if err != nil {
		t.Fatalf("create chat session: %v", err)
	}
	if err := service.CompactSession(context.Background(), GetSessionContextSavingInput{RootID: root.ID, Key: created.Key}); err != nil {
		t.Fatalf("CompactSession: %v", err)
	}
	select {
	case in := <-got:
		if in.Content != hygieneCompactCommand || !in.hygieneCompact || in.Key != created.Key {
			t.Fatalf("unexpected compact turn: %+v", in)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("auto compact turn was never sent")
	}
}
