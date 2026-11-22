package kanban

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// M3：任务卡片的「结论」位置曾被系统自己的兜底说明占住。
//
// 失败回合会写一条活动正文解释故障，那已经够了；再把「本轮未生成总结（Router
// 未在收尾回合给出）· 状态：本轮失败：…」写进 tasks.result_summary，卡片上读到的
// 就不是任务结论而是兜底文案（实测 task_7280e0df4f5c8518）。
//
// 失败回合的结果摘要留空，由真正的总结（Router purpose=summary 或异步压缩）
// 后续填写；成功回合不变 —— 结构化摘要本身是真实结论，压缩不可用时它是卡片
// 唯一可读的内容。
func TestFailedDirectTurnDoesNotWriteFallbackSummary(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)
	done := make(chan struct{})
	f.service.SetDirectExecutor(&fakeDirectExecutor{err: errors.New("response stream ended unexpectedly"), done: done})

	if _, _, err := f.service.CreateUserActivity(ctx, "task-1", "交接文档已写好，两份内容一致", CommentWorkflowChoiceDisabled, nil); err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}

	waitTurnDone(t, done)
	task := waitTaskIdle(t, f, "task-1")
	if task.Status != StatusWaitingUser {
		t.Fatalf("失败回合应停在待审核，实际 %q", task.Status)
	}
	if strings.TrimSpace(task.ResultSummary) != "" {
		t.Fatalf("失败回合不该把兜底文案写成结论：%q", task.ResultSummary)
	}
}

// 成功回合的兜底摘要保留：压缩不可用时它是卡片唯一可读的内容。
func TestSuccessfulDirectTurnStillWritesDigestSummary(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)
	done := make(chan struct{})
	f.service.SetDirectExecutor(&fakeDirectExecutor{reply: longDirectReply(), done: done})

	if _, _, err := f.service.CreateUserActivity(ctx, "task-1", "把这三处改掉", CommentWorkflowChoiceDisabled, nil); err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}

	waitTurnDone(t, done)
	task := waitTaskIdle(t, f, "task-1")
	if strings.TrimSpace(task.ResultSummary) == "" {
		t.Fatalf("成功回合应写结构化兜底摘要")
	}
}

// waitTaskIdle 等回合收尾（任务不再处于 running）。
func waitTaskIdle(t *testing.T, f *activityServiceFixture, taskID string) Task {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		task, err := f.tasks.GetTask(ctx, taskID)
		if err != nil {
			t.Fatalf("GetTask: %v", err)
		}
		if task.Status != StatusRunning {
			return task
		}
		time.Sleep(20 * time.Millisecond)
	}
	task, err := f.tasks.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	t.Fatalf("回合未收尾，任务仍停在 %q", task.Status)
	return task
}

// waitTurnDone 等注入的执行器返回；回合收尾发生在它之后，所以还要配合
// waitTaskIdle 才能读到落库结果。
func waitTurnDone(t *testing.T, done chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("直连执行器从未被调用")
	}
}
