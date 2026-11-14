package kanban

import (
	"context"
	"testing"
)

// 任务状态不跟会话/worktree 绑死：会话被删、worktree 丢失后任务会一直卡在 running，
// 此时必须还能被人工完成，否则这些任务永远动不了。
func TestCompleteStuckRunningTask(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{RootID: root.ID, Input: "broken save button"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	if err := store.UpdateTaskStatus(ctx, detail.Task.ID, StatusRunning, false); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}
	stuck, err := store.GetTask(ctx, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if stuck.Status != StatusRunning {
		t.Fatalf("precondition status=%s, want running", stuck.Status)
	}

	completed, err := svc.Complete(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID, Reason: "session gone"})
	if err != nil {
		t.Fatalf("Complete on a stuck running task must succeed, got: %v", err)
	}
	if completed.Task.Status != StatusSuccess {
		t.Fatalf("task status=%s, want success", completed.Task.Status)
	}
	if completed.Task.CompletedAt == "" {
		t.Fatalf("completed_at empty")
	}
}

// 终态任务不能被状态操作复活：Pause/Resume 走 setTaskStatus，终态时必须原样返回。
func TestTerminalTaskCannotBeResurrected(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		kill  func(*Service, string, string) error
		after string
	}{
		{"success+resume", func(s *Service, r, id string) error {
			_, err := s.Resume(ctx, MoveInput{RootID: r, TaskID: id})
			return err
		}, StatusSuccess},
		{"success+pause", func(s *Service, r, id string) error {
			_, err := s.Pause(ctx, MoveInput{RootID: r, TaskID: id})
			return err
		}, StatusSuccess},
		{"cancelled+pause", func(s *Service, r, id string) error {
			_, err := s.Pause(ctx, MoveInput{RootID: r, TaskID: id})
			return err
		}, StatusCancelled},
		{"cancelled+resume", func(s *Service, r, id string) error {
			_, err := s.Resume(ctx, MoveInput{RootID: r, TaskID: id})
			return err
		}, StatusCancelled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, root := newTestService(t, nil)
			d, err := svc.CreateTask(ctx, CreateTaskInput{RootID: root.ID, Input: "x"})
			if err != nil {
				t.Fatalf("CreateTask: %v", err)
			}
			// 从非终态进入终态：cancelled 用 Cancel，success 用 Complete。
			// 不能拿 success 再 Cancel——终态已不可改写，那是本测试要守的行为本身。
			if tc.after == StatusCancelled {
				if _, err := svc.Cancel(ctx, MoveInput{RootID: root.ID, TaskID: d.Task.ID}); err != nil {
					t.Fatalf("Cancel: %v", err)
				}
			} else if _, err := svc.Complete(ctx, MoveInput{RootID: root.ID, TaskID: d.Task.ID}); err != nil {
				t.Fatalf("Complete: %v", err)
			}
			if err := tc.kill(svc, root.ID, d.Task.ID); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got, err := svc.GetTask(ctx, root.ID, d.Task.ID)
			if err != nil {
				t.Fatalf("GetTask: %v", err)
			}
			if got.Task.Status != tc.after {
				t.Fatalf("terminal task was resurrected: status=%s, want %s", got.Task.Status, tc.after)
			}
		})
	}
}

// 暂停/恢复往返：暂停后状态保持，恢复后继续跑（非终态才允许）。
func TestPauseResumeRoundTrip(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	d, err := svc.CreateTask(ctx, CreateTaskInput{RootID: root.ID, Input: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	paused, err := svc.Pause(ctx, MoveInput{RootID: root.ID, TaskID: d.Task.ID})
	if err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if paused.Task.Status != StatusPaused {
		t.Fatalf("status=%s, want paused", paused.Task.Status)
	}
	resumed, err := svc.Resume(ctx, MoveInput{RootID: root.ID, TaskID: d.Task.ID})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if resumed.Task.Status != StatusRunning {
		t.Fatalf("status=%s, want running", resumed.Task.Status)
	}
}

// 状态变更必须发 task.updated，否则别的客户端看不到：
// setTaskStatus（Pause/Resume/Cancel/Fail）以前只写库、由 HTTP handler 事后补一次，
// 于是 KickPending 等非 HTTP 入口推不出去。
func TestStatusChangeAlwaysBroadcasts(t *testing.T) {
	ctx := context.Background()
	runner := &fakeRunner{}
	svc, root := newTestService(t, runner)
	d, err := svc.CreateTask(ctx, CreateTaskInput{RootID: root.ID, Input: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	// Pause 走 setTaskStatus
	if _, err := svc.Pause(ctx, MoveInput{RootID: root.ID, TaskID: d.Task.ID, Reason: "r"}); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if got := runner.updateCount(); got != 1 {
		t.Fatalf("broadcasts after Pause = %d, want 1 (setTaskStatus must broadcast)", got)
	}
	// Cancel 也走 setTaskStatus：任务变终态，卡片要从板上消失
	if _, err := svc.Cancel(ctx, MoveInput{RootID: root.ID, TaskID: d.Task.ID}); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if got := runner.updateCount(); got != 2 {
		t.Fatalf("broadcasts after Cancel = %d, want 2", got)
	}
	// 已是终态再 Cancel：状态没变，就不该再推一条一模一样的（否则每个客户端白重渲染一次）
	if _, err := svc.Cancel(ctx, MoveInput{RootID: root.ID, TaskID: d.Task.ID}); err != nil {
		t.Fatalf("Cancel on terminal task: %v", err)
	}
	if got := runner.updateCount(); got != 2 {
		t.Fatalf("broadcasts after redundant Cancel = %d, want 2 (unchanged status must stay silent)", got)
	}
}

func TestTaskNumbersIncrement(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	first, err := svc.CreateTask(ctx, CreateTaskInput{RootID: root.ID, Input: "one"})
	if err != nil {
		t.Fatalf("CreateTask first: %v", err)
	}
	second, err := svc.CreateTask(ctx, CreateTaskInput{RootID: root.ID, Input: "two"})
	if err != nil {
		t.Fatalf("CreateTask second: %v", err)
	}
	if first.Task.TaskNumber != 1 || second.Task.TaskNumber != 2 {
		t.Fatalf("task numbers = %d/%d, want 1/2", first.Task.TaskNumber, second.Task.TaskNumber)
	}
	numbered, err := svc.ListTaskDetails(ctx, root.ID, ListTasksOptions{TaskNumber: 2})
	if err != nil {
		t.Fatalf("ListTaskDetails by task number: %v", err)
	}
	if len(numbered) != 1 || numbered[0].Task.ID != second.Task.ID {
		t.Fatalf("task number lookup = %#v, want second task", numbered)
	}
}