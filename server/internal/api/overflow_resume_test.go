package api

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/session"
)

// 队列里已经有用户消息时，溢出自动重发必须让路：用户已经改主意了，
// 把旧内容插到他前面只会打乱顺序。
func TestResumeSessionOverflowTurnDeclinesWhenQueueHasMessages(t *testing.T) {
	app := &AppContext{}
	hub := app.GetSessionStreamHub()
	hub.EnqueueSessionMessage("root", "chat", "长会话", QueuedUserMessage{ID: "q1", PendingUserMessage: PendingUserMessage{Content: "我改主意了"}})

	spec := usecase.OverflowResumeSpec{
		RootID:      "root",
		Key:         "chat",
		SessionType: session.TypeChat,
		SessionName: "长会话",
		Agent:       "claude",
		Content:     "重发我",
		TurnSeq:     12,
		TurnID:      "turn-12",
	}
	if app.ResumeSessionOverflowTurn(spec) {
		t.Fatal("队列已有用户消息时仍然重发了")
	}
}

// 没有可复用的回合身份就不重发：否则会多写一条 user 行、界面上多出一个气泡。
func TestResumeSessionOverflowTurnDeclinesWithoutTurnIdentity(t *testing.T) {
	app := &AppContext{}
	spec := usecase.OverflowResumeSpec{RootID: "root", Key: "chat", Content: "重发我"}
	if app.ResumeSessionOverflowTurn(spec) {
		t.Fatal("没有回合身份仍然重发了")
	}
}

func TestResumeSessionOverflowTurnDeclinesWhenDisabled(t *testing.T) {
	var app *AppContext
	if app.ResumeSessionOverflowTurn(usecase.OverflowResumeSpec{Key: "chat", TurnSeq: 1, TurnID: "t"}) {
		t.Fatal("空 AppContext 仍然重发了")
	}
}

// spec → 队列任务是唯一的转换点：逐项搬运，且重发回合必须被标记，
// 否则它再一次撞到溢出时会继续排压缩、继续登记，形成无限重发。
func TestOverflowResumeJobCarriesTurnIdentity(t *testing.T) {
	ts := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	planMode := true
	job := overflowResumeJob(usecase.OverflowResumeSpec{
		RootID:          "root",
		RuntimeRootPath: "/tmp/root",
		Key:             " chat ",
		SessionType:     session.TypeChat,
		SessionName:     "长会话",
		Agent:           "claude",
		Model:           "os",
		Mode:            "default",
		Effort:          "high",
		FastService:     "auto",
		PlanMode:        &planMode,
		Content:         "重发我",
		UserTimestamp:   ts,
		TurnSeq:         12,
		TurnID:          " turn-12 ",
	})
	if !job.OverflowResume {
		t.Fatal("重发任务没有标记 OverflowResume，会再次排压缩")
	}
	if job.Key != "chat" {
		t.Fatalf("key = %q, want chat", job.Key)
	}
	if job.ReuseUserTurn == nil || job.ReuseUserTurn.Seq != 12 || job.ReuseUserTurn.TurnID != "turn-12" {
		t.Fatalf("重发任务没有复用原回合：%#v", job.ReuseUserTurn)
	}
	if job.User.Content != "重发我" || job.User.Agent != "claude" || job.User.Model != "os" ||
		job.User.Mode != "default" || job.User.Effort != "high" || job.User.FastService != "auto" {
		t.Fatalf("重发任务丢了模型前提：%#v", job.User)
	}
	if !job.User.PlanMode {
		t.Fatal("重发任务丢了计划模式")
	}
	if !job.User.Timestamp.Equal(ts) {
		t.Fatalf("timestamp = %v, want %v", job.User.Timestamp, ts)
	}
}

// 没有 TurnSeq 或 TurnID 时不设置复用身份：调用方据此拒绝重发（宁可不发，
// 也不能多写一条 user 行）。
func TestOverflowResumeJobOmitsIdentityWhenIncomplete(t *testing.T) {
	job := overflowResumeJob(usecase.OverflowResumeSpec{Key: "chat", TurnID: "turn-1"})
	if job.ReuseUserTurn != nil {
		t.Fatalf("没有 TurnSeq 却设置了复用身份：%#v", job.ReuseUserTurn)
	}
}

func TestOverflowResumeAcceptableReflectsQueue(t *testing.T) {
	hub := NewStreamHub()
	if !overflowResumeAcceptable(hub, "chat") {
		t.Fatal("空队列却拒绝重发")
	}
	hub.EnqueueSessionMessage("root", "chat", "长会话", QueuedUserMessage{ID: "q1", PendingUserMessage: PendingUserMessage{Content: "hi"}})
	if overflowResumeAcceptable(hub, "chat") {
		t.Fatal("队列里有用户消息仍然接受重发")
	}
	hub.RemoveQueuedSessionMessage("chat", "q1")
	if !overflowResumeAcceptable(hub, "chat") {
		t.Fatal("清空队列后仍然拒绝重发")
	}
	if overflowResumeAcceptable(nil, "chat") {
		t.Fatal("nil hub 接受了重发")
	}
}

func TestHasQueuedSessionMessage(t *testing.T) {
	hub := NewStreamHub()
	if hub.HasQueuedSessionMessage("chat") {
		t.Fatal("空队列报告有消息")
	}
	hub.EnqueueSessionMessage("root", "chat", "长会话", QueuedUserMessage{ID: "q1", PendingUserMessage: PendingUserMessage{Content: "hi"}})
	if !hub.HasQueuedSessionMessage("chat") {
		t.Fatal("队列里有消息却没报告")
	}
	hub.RemoveQueuedSessionMessage("chat", "q1")
	if hub.HasQueuedSessionMessage("chat") {
		t.Fatal("清空队列后仍然报告有消息")
	}
	var nilHub *StreamHub
	if nilHub.HasQueuedSessionMessage("chat") {
		t.Fatal("nil hub 报告有消息")
	}
}

// 重发回合复用原回合的 mindfs_id 作为回合身份，因此**不能**再广播一条用户
// 气泡（否则界面上会出现两条一模一样的消息）；它只置 pending，让正文接在
// 原来那条消息下面。这条是源码契约：真正的 OnStart 只有跑真实 agent 才会执行。
func TestOverflowResumeStartHookSkipsUserBubble(t *testing.T) {
	raw, err := os.ReadFile("ws_session_message.go")
	if err != nil {
		t.Fatalf("read ws_session_message.go: %v", err)
	}
	src := stripAPILineComments(string(raw))
	startIdx := strings.Index(src, "OnStart: func(start usecase.MessageStart) {")
	if startIdx < 0 {
		t.Fatal("找不到 OnStart 钩子")
	}
	updateIdx := strings.Index(src[startIdx:], "OnUpdate:")
	if updateIdx < 0 {
		t.Fatal("找不到 OnStart 的结束边界")
	}
	body := src[startIdx : startIdx+updateIdx]

	branch := regexp.MustCompile(`(?s)if job\.OverflowResume \{\s*turnID = h\.AppContext\.SetSessionPendingReply\([^)]*start\.MindFSID\)\s*return\s*\}`)
	if !branch.MatchString(body) {
		t.Fatal("重发回合没有走「只置 pending、不广播用户气泡」的分支")
	}
	pendingIdx := strings.Index(body, "SetSessionPendingReply(")
	broadcastIdx := strings.Index(body, "BroadcastSessionUserMessageAt(")
	if broadcastIdx < 0 {
		t.Fatal("普通回合的用户气泡广播不见了")
	}
	if pendingIdx > broadcastIdx {
		t.Fatal("重发回合的分支落在了普通广播之后")
	}
}
