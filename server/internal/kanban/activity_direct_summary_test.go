package kanban

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// directStubExecutor 是直连回合的执行端口：把固定文本当成 Agent 输出。
type directStubExecutor struct {
	out string
	err error
}

func (e directStubExecutor) RunDirectActivity(context.Context, Task, string) (string, error) {
	return e.out, e.err
}

// callsSnapshot 让断言在后台 goroutine 已退出后仍能安全读调用记录。
func (c *stubCompressor) callsSnapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.calls...)
}

// directSummaryFixture 准备一条直连回合需要的活动行与装配好的双端服务：
// ActivityService 负责跑回合，Service 负责压缩落地。
func directSummaryFixture(t *testing.T, compressor SummaryCompressor) (*activityServiceFixture, *Service, *TaskActivity) {
	t.Helper()
	ctx := context.Background()
	f := setupActivityService(t)
	svc := NewService(testRoots{root: f.root})
	svc.SetSummaryCompressor(compressor)
	f.service.SetTurnTracker(&svc.activityTurns)
	f.service.SetReplySummarizer(svc.scheduleDirectReplySummaryCompression)

	user := &TaskActivity{
		ID: "act-user", TaskID: "task-1", RootID: f.root.ID,
		Type: ActivityTypeUserMessage, Role: "user", Body: "把这三处改掉", Status: ActivityStatusCompleted,
		CreatedAt: time.Now().UTC().Add(-time.Minute),
	}
	act := &TaskActivity{
		ID: "act-reply", TaskID: "task-1", RootID: f.root.ID,
		Type: ActivityTypeAgentReply, Role: "agent", Status: ActivityStatusPending,
		ReplyToID: user.ID, Purpose: "reply", Ordinal: 1,
		CreatedAt: time.Now().UTC(),
	}
	for _, row := range []*TaskActivity{user, act} {
		if err := f.service.activities.Insert(ctx, row); err != nil {
			t.Fatalf("insert %s: %v", row.ID, err)
		}
	}
	return f, svc, act
}

func longDirectReply() string {
	return strings.Repeat("已改完三处并重跑门禁：ws.go 的分发改成 goroutine 派发，", 20)
}

// 直连回复（没有 WorkflowRun）落库后必须补一条压缩总结：这是「回复消息又不压缩
// 直接传出来」的回归点 —— 压缩只在 settleWorkflowReply 里做过，直连路径没有。
func TestDirectReplyGetsCompressedSummary(t *testing.T) {
	ctx := context.Background()
	raw := longDirectReply()
	compressor := &stubCompressor{out: "三处已改完，门禁全绿。"}
	f, svc, act := directSummaryFixture(t, compressor)
	f.service.SetDirectExecutor(directStubExecutor{out: raw})

	task, err := f.tasks.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	f.service.executeDirectActivity(ctx, act, task, "把这三处改掉")
	if err := svc.WaitActivityTurnsIdle(ctx); err != nil {
		t.Fatalf("WaitActivityTurnsIdle: %v", err)
	}

	stored, err := f.service.activities.Get(ctx, act.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Summary != "三处已改完，门禁全绿。" {
		t.Fatalf("summary = %q, want compressed text", stored.Summary)
	}
	// 原文一个字都不能丢：summary 只是展示层，body 仍是完整结果。
	if stored.Body != raw {
		t.Fatalf("body was overwritten: %d runes, want %d", len([]rune(stored.Body)), len([]rune(raw)))
	}
	loaded, err := f.tasks.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTask after: %v", err)
	}
	if loaded.ResultSummary != "三处已改完，门禁全绿。" {
		t.Fatalf("task result summary = %q, want compressed text", loaded.ResultSummary)
	}
	prompts := compressor.callsSnapshot()
	if len(prompts) != 1 || !strings.Contains(prompts[0], "直连对话") {
		t.Fatalf("compressor prompts = %d, want one direct-turn prompt", len(prompts))
	}
}

// 短回复本身就是一段总结：不调模型、summary 保持空，界面照常显示原文。
func TestDirectReplyShortTextSkipsCompression(t *testing.T) {
	ctx := context.Background()
	raw := "READY"
	compressor := &stubCompressor{out: "不该被调用"}
	f, svc, act := directSummaryFixture(t, compressor)
	f.service.SetDirectExecutor(directStubExecutor{out: raw})

	task, err := f.tasks.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	f.service.executeDirectActivity(ctx, act, task, "ping")
	if err := svc.WaitActivityTurnsIdle(ctx); err != nil {
		t.Fatalf("WaitActivityTurnsIdle: %v", err)
	}
	if n := compressor.callCount(); n != 0 {
		t.Fatalf("compressor called %d times for a short reply, want 0", n)
	}
	stored, err := f.service.activities.Get(ctx, act.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Summary != "" || stored.Body != raw {
		t.Fatalf("short reply changed: summary=%q body=%q", stored.Summary, stored.Body)
	}
}

// 压缩失败只意味着「没压成」：原文仍在，界面照常显示完整结果。
func TestDirectReplyCompressionFailureKeepsRaw(t *testing.T) {
	ctx := context.Background()
	raw := longDirectReply()
	compressor := &stubCompressor{err: errors.New("model down")}
	f, svc, act := directSummaryFixture(t, compressor)
	f.service.SetDirectExecutor(directStubExecutor{out: raw})

	task, err := f.tasks.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	f.service.executeDirectActivity(ctx, act, task, "把这三处改掉")
	if err := svc.WaitActivityTurnsIdle(ctx); err != nil {
		t.Fatalf("WaitActivityTurnsIdle: %v", err)
	}
	stored, err := f.service.activities.Get(ctx, act.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Summary != "" || stored.Body != raw {
		t.Fatalf("failure changed the reply: summary=%q body-len=%d", stored.Summary, len([]rune(stored.Body)))
	}
	if stored.Status != ActivityStatusCompleted {
		t.Fatalf("status = %q, want completed", stored.Status)
	}
}

// 同一段原文只压一次：重复结算（或回合重放）不能重复烧模型调用。
func TestDirectReplyCompressionIsNotRepeated(t *testing.T) {
	ctx := context.Background()
	raw := longDirectReply()
	compressor := &stubCompressor{out: "三处已改完，门禁全绿。"}
	f, svc, act := directSummaryFixture(t, compressor)

	svc.scheduleDirectReplySummaryCompression(ctx, f.tasks, act, raw)
	if err := svc.WaitActivityTurnsIdle(ctx); err != nil {
		t.Fatalf("WaitActivityTurnsIdle: %v", err)
	}
	svc.scheduleDirectReplySummaryCompression(ctx, f.tasks, act, raw)
	if err := svc.WaitActivityTurnsIdle(ctx); err != nil {
		t.Fatalf("WaitActivityTurnsIdle: %v", err)
	}
	if n := compressor.callCount(); n != 1 {
		t.Fatalf("compressor called %d times, want 1", n)
	}
}
