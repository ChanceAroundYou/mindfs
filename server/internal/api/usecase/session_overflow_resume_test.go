package usecase

import (
	"testing"
	"time"

	"mindfs/server/internal/session"
)

// overflowResumeTestRegistry 在卫生状态机之上再加一个重发 runner 可选能力。
type overflowResumeTestRegistry struct {
	hygieneTestRegistry
	runner SessionOverflowResumeRunner
}

func (r *overflowResumeTestRegistry) ResumeSessionOverflowTurn(spec OverflowResumeSpec) bool {
	if r.runner == nil {
		return false
	}
	return r.runner.ResumeSessionOverflowTurn(spec)
}

// overflowResumeTestService 装配一份只有卫生状态机的 Service：溢出重发的全部
// 判据都在 tracker + 失败上下文里，不需要真实的 agent。
func overflowResumeTestService(t *testing.T, runner SessionOverflowResumeRunner) (*Service, *HygieneTracker) {
	t.Helper()
	tracker := NewHygieneTracker(hygieneConfig())
	reg := &overflowResumeTestRegistry{hygieneTestRegistry: hygieneTestRegistry{tracker: tracker}, runner: runner}
	return &Service{Registry: reg}, tracker
}

type resumeRunnerCall struct {
	spec OverflowResumeSpec
}

type recordingResumeRunner struct {
	calls  []resumeRunnerCall
	accept bool
}

func (r *recordingResumeRunner) ResumeSessionOverflowTurn(spec OverflowResumeSpec) bool {
	r.calls = append(r.calls, resumeRunnerCall{spec: spec})
	return r.accept
}

func overflowResumeFailureContext(sess *session.Session) sendFailureContext {
	return sendFailureContext{
		RootID:        "root",
		SessionKey:    "chat",
		AgentName:     "claude",
		Model:         "os",
		Mode:          "default",
		Effort:        "high",
		FastService:   "auto",
		PlanMode:      true,
		RootAbs:       "/tmp/root",
		Content:       "把这一大段代码全部读完再回答",
		UserTimestamp: time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC),
		TurnSeq:       41,
		TurnID:        "turn-41",
		Current:       sess,
	}
}

func overflowTestSession() *session.Session {
	return &session.Session{Key: "chat", Type: session.TypeChat, Name: "长会话", AgentCtxSeq: map[string]int{"claude": 1}}
}

// T2：终端溢出登记的是**那条失败回合**的完整输入与身份，重发才能逐项复现前提。
func TestOverflowFailureArmsResumeWithOriginalTurn(t *testing.T) {
	svc, tracker := overflowResumeTestService(t, nil)
	// 压缩已在途：ensureOverflowCompact 只登记待办，不会再起一个压缩 goroutine。
	tracker.MarkCompactPending("root", "chat")
	in := overflowResumeFailureContext(overflowTestSession())

	if !svc.ensureOverflowCompact(in) {
		t.Fatal("ensureOverflowCompact = false, want true（压缩已在途）")
	}
	spec, ok := tracker.TakeOverflowResume("root", "chat")
	if !ok {
		t.Fatal("溢出失败后没有登记重发待办")
	}
	if spec.Content != in.Content || spec.TurnID != "turn-41" || spec.TurnSeq != 41 {
		t.Fatalf("待办没有复用原回合：%#v", spec)
	}
	if spec.Agent != "claude" || spec.Model != "os" || spec.Effort != "high" || spec.Mode != "default" {
		t.Fatalf("待办丢了模型前提：%#v", spec)
	}
	if spec.PlanMode == nil || !*spec.PlanMode {
		t.Fatalf("待办丢了计划模式：%#v", spec.PlanMode)
	}
	if spec.SessionType != session.TypeChat || spec.SessionName != "长会话" || spec.RuntimeRootPath != "/tmp/root" {
		t.Fatalf("待办丢了会话元数据：%#v", spec)
	}
	if !spec.UserTimestamp.Equal(in.UserTimestamp) {
		t.Fatalf("待办时间戳 = %v, want %v", spec.UserTimestamp, in.UserTimestamp)
	}
}

// T2：重发回合本身再撞墙不再排压缩、不再登记 —— 一次为限。
func TestOverflowResumeTurnDoesNotArmAgain(t *testing.T) {
	svc, tracker := overflowResumeTestService(t, nil)
	tracker.MarkCompactPending("root", "chat")
	in := overflowResumeFailureContext(overflowTestSession())
	in.OverflowResume = true

	if svc.ensureOverflowCompact(in) {
		t.Fatal("重发回合又排了一次压缩")
	}
	if tracker.HasOverflowResume("root", "chat") {
		t.Fatal("重发回合又登记了一次待办")
	}
}

// T3：只有用户自己发起的回合作废待办；压缩回合与重发回合必须保留。
func TestUserTurnDropsPendingResume(t *testing.T) {
	svc, tracker := overflowResumeTestService(t, nil)
	arm := func() {
		tracker.MarkOverflowResume("root", "chat", OverflowResumeSpec{RootID: "root", Key: "chat", TurnSeq: 1, TurnID: "turn-1", Content: "hi"})
	}

	arm()
	svc.maybeDropOverflowResumeForUserTurn(SendMessageInput{RootID: "root", Key: "chat"})
	if tracker.HasOverflowResume("root", "chat") {
		t.Fatal("用户新回合没有作废待办")
	}

	arm()
	svc.maybeDropOverflowResumeForUserTurn(SendMessageInput{RootID: "root", Key: "chat", hygieneCompact: true})
	if !tracker.HasOverflowResume("root", "chat") {
		t.Fatal("压缩回合错误地作废了待办")
	}

	svc.maybeDropOverflowResumeForUserTurn(SendMessageInput{RootID: "root", Key: "chat", OverflowResume: true})
	if !tracker.HasOverflowResume("root", "chat") {
		t.Fatal("重发回合错误地作废了待办")
	}
}

// T4：压缩成功且会话空闲 → 正好交给 runner 一次。
func TestCompactSuccessConsumesResume(t *testing.T) {
	runner := &recordingResumeRunner{accept: true}
	svc, tracker := overflowResumeTestService(t, runner)
	tracker.MarkOverflowResume("root", "chat", OverflowResumeSpec{RootID: "root", Key: "chat", TurnSeq: 7, TurnID: "turn-7", Content: "重发我"})

	svc.consumeOverflowResume("root", "chat")

	if len(runner.calls) != 1 {
		t.Fatalf("runner 调用 %d 次, want 1", len(runner.calls))
	}
	if got := runner.calls[0].spec; got.Content != "重发我" || got.TurnID != "turn-7" {
		t.Fatalf("runner 收到 %#v", got)
	}
	if tracker.HasOverflowResume("root", "chat") {
		t.Fatal("消费后待办没有清掉")
	}
	// 再消费一次不能重复重发。
	svc.consumeOverflowResume("root", "chat")
	if len(runner.calls) != 1 {
		t.Fatalf("重复消费又重发了一次：%d", len(runner.calls))
	}
}

// T4：会话忙（用户已经接手）→ 丢弃，不重发。
func TestCompactSuccessDropsResumeWhenSessionBusy(t *testing.T) {
	runner := &recordingResumeRunner{accept: true}
	svc, tracker := overflowResumeTestService(t, runner)
	tracker.MarkOverflowResume("root", "busy-chat", OverflowResumeSpec{RootID: "root", Key: "busy-chat", TurnSeq: 3, TurnID: "turn-3", Content: "旧内容"})
	registerActiveTurn("root", "busy-chat", func() {})
	t.Cleanup(func() { unregisterActiveTurn("root", "busy-chat") })

	svc.consumeOverflowResume("root", "busy-chat")

	if len(runner.calls) != 0 {
		t.Fatal("会话忙时仍然重发了")
	}
	if tracker.HasOverflowResume("root", "busy-chat") {
		t.Fatal("会话忙时待办应当被丢弃")
	}
}

// T4：runner 不接受（队列里已有用户消息）→ 不重发，且待办已消费（不会无限留着重试）。
func TestCompactSuccessDropsResumeWhenRunnerDeclines(t *testing.T) {
	runner := &recordingResumeRunner{accept: false}
	svc, tracker := overflowResumeTestService(t, runner)
	tracker.MarkOverflowResume("root", "chat", OverflowResumeSpec{RootID: "root", Key: "chat", TurnSeq: 2, TurnID: "turn-2", Content: "旧内容"})

	svc.consumeOverflowResume("root", "chat")

	if len(runner.calls) != 1 {
		t.Fatalf("runner 应被询问一次，实际 %d", len(runner.calls))
	}
	if tracker.HasOverflowResume("root", "chat") {
		t.Fatal("被拒后待办应当丢弃")
	}
}

// T4：压缩失败 → 丢弃待办，不重发。
func TestCompactFailureDropsResume(t *testing.T) {
	runner := &recordingResumeRunner{accept: true}
	svc, tracker := overflowResumeTestService(t, runner)
	tracker.MarkOverflowResume("root", "chat", OverflowResumeSpec{RootID: "root", Key: "chat", TurnSeq: 9, TurnID: "turn-9", Content: "旧内容"})

	svc.finishHygieneCompact(SendMessageInput{RootID: "root", Key: "chat"}, "compact failed: boom")

	if len(runner.calls) != 0 {
		t.Fatal("压缩失败仍然重发了")
	}
	if tracker.HasOverflowResume("root", "chat") {
		t.Fatal("压缩失败后待办应当丢弃")
	}
	if !tracker.Snapshot("root", "chat", "claude", 1000, 200000).AutoFailed {
		t.Fatal("压缩失败没有记录 auto_failed")
	}
}

// T4：压缩成功 → finishHygieneCompact 走消费路径（成功收口只有这一个入口）。
func TestCompactSuccessPathConsumesResume(t *testing.T) {
	runner := &recordingResumeRunner{accept: true}
	svc, tracker := overflowResumeTestService(t, runner)
	tracker.MarkOverflowResume("root", "chat", OverflowResumeSpec{RootID: "root", Key: "chat", TurnSeq: 5, TurnID: "turn-5", Content: "旧内容"})

	svc.finishHygieneCompact(SendMessageInput{RootID: "root", Key: "chat"}, "")

	if len(runner.calls) != 1 {
		t.Fatalf("压缩成功后 runner 调用 %d 次, want 1", len(runner.calls))
	}
}

// T5 的 usecase 半边：重发回合复用原回合身份，不再写第二条 user 行。
// 判据抽成纯函数，落盘点留在 SendMessage 里由源码契约用例钉住。
func TestReuseUserTurnIdentity(t *testing.T) {
	seq, turnID, ok := reuseUserTurnIdentity(SendMessageInput{
		Content:       "重发我",
		ReuseUserTurn: &UserTurnIdentity{Seq: 88, TurnID: "turn-88"},
	})
	if !ok {
		t.Fatal("有完整身份时应复用")
	}
	if seq != 88 || turnID != "turn-88" {
		t.Fatalf("复用身份 = (%d,%q), want (88,turn-88)", seq, turnID)
	}

	for name, in := range map[string]SendMessageInput{
		"无身份":      {Content: "普通回合"},
		"seq 为零":   {ReuseUserTurn: &UserTurnIdentity{Seq: 0, TurnID: "turn-0"}},
		"缺 turnID": {ReuseUserTurn: &UserTurnIdentity{Seq: 7, TurnID: "  "}},
	} {
		if _, _, ok := reuseUserTurnIdentity(in); ok {
			t.Fatalf("%s：不该复用", name)
		}
	}
}
