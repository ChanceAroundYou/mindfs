package kanban

import (
	"context"
	"testing"
)

func TestUpdateCurrentInputKeepsPreviousStageInput(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Describe"),
			agentStage("Fix", "Fix this:\n{previous_input}"),
		},
		Input: "first input",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	detail, err = svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID})
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	for _, run := range detail.StageRuns {
		if run.StageIndex == 1 && run.Input != "" {
			t.Fatalf("new current stage input=%q, want empty", run.Input)
		}
	}
	updated, err := svc.UpdateCurrentInput(ctx, UpdateTaskInput{RootID: root.ID, TaskID: detail.Task.ID, Input: "current input"})
	if err != nil {
		t.Fatalf("UpdateCurrentInput: %v", err)
	}
	firstRun := StageRun{}
	currentRun := StageRun{}
	for _, run := range updated.StageRuns {
		if run.StageIndex == 0 {
			firstRun = run
		}
		if run.StageIndex == 1 {
			currentRun = run
		}
	}
	if firstRun.Input != "first input" {
		t.Fatalf("first stage input=%q, want first input", firstRun.Input)
	}
	if currentRun.Input != "current input" {
		t.Fatalf("current stage input=%q, want current input", currentRun.Input)
	}
}

func TestCompleteFinalWaitingTask(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{userStage("Describe")},
		Input:  "done",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if detail.Task.Status != StatusPending {
		t.Fatalf("task status=%s, want pending（新建任务未开始）", detail.Task.Status)
	}
	// Complete 只接受等待用户态；未开始态先「开始」，由执行体把单段 user 任务推到等待用户。
	if _, err := svc.RunNow(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	waitForCondition(t, func() bool {
		got, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && got.Task.Status == StatusWaitingUser
	})
	completed, err := svc.Complete(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID, Reason: "approved"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if completed.Task.Status != StatusSuccess {
		t.Fatalf("task status=%s, want success", completed.Task.Status)
	}
	if completed.Task.CompletedAt == "" {
		t.Fatalf("completed_at empty")
	}
}

// 任务状态不跟会话/worktree 绑死：会话被删、worktree 丢失后任务会一直卡在 running，
// 此时必须还能被人工完成，否则这些任务永远动不了。
func TestCompleteStuckRunningTaskAfterSessionLost(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Describe"),
			agentStage("Fix", "Fix this:\n{previous_input}"),
		},
		Input: "broken save button",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	// 任务已经进入 agent 段（running）后，会话/worktree 丢失：执行体不再回来。
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
			d, err := svc.CreateTask(ctx, CreateTaskInput{RootID: root.ID, Stages: []StageTemplate{userStage("Describe")}, Input: "x"})
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
	d, err := svc.CreateTask(ctx, CreateTaskInput{RootID: root.ID,
		Stages: []StageTemplate{userStage("A"), agentStage("B", "do {previous_input}")}, Input: "x"})
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
// setTaskStatus（Pause/Resume/Cancel/Fail）与 moveTo（换段）以前都只写库、
// 由 HTTP handler 事后补一次，于是 KickPending 等非 HTTP 入口推不出去。
func TestStatusChangeAlwaysBroadcasts(t *testing.T) {
	ctx := context.Background()
	runner := &fakeRunner{}
	svc, root := newTestService(t, runner)
	d, err := svc.CreateTask(ctx, CreateTaskInput{RootID: root.ID,
		Stages: []StageTemplate{userStage("A"), agentStage("B", "do {previous_input}")}, Input: "x"})
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

// moveTo 换段指针后必须播：卡片上的「当前在第几段」就挂在这条上。
func TestAdvanceBroadcastsNewStage(t *testing.T) {
	ctx := context.Background()
	runner := &fakeRunner{}
	svc, root := newTestService(t, runner)
	d, err := svc.CreateTask(ctx, CreateTaskInput{RootID: root.ID,
		Stages: []StageTemplate{userStage("A"), agentStage("B", "do {previous_input}")}, Input: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	before := runner.updateCount()
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: d.Task.ID}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	if got := runner.updateCount(); got <= before {
		t.Fatalf("broadcasts after Next = %d, want > %d (moveTo must broadcast)", got, before)
	}
}

func TestTaskNumbersIncrement(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	first, err := svc.CreateTask(ctx, CreateTaskInput{RootID: root.ID, Stages: []StageTemplate{userStage("Describe")}, Input: "one"})
	if err != nil {
		t.Fatalf("CreateTask first: %v", err)
	}
	second, err := svc.CreateTask(ctx, CreateTaskInput{RootID: root.ID, Stages: []StageTemplate{userStage("Describe")}, Input: "two"})
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

// running / paused 下的「立即执行」不再静默回 200：调用方拿得到明确错误，
// 而不是 apply 完一个没变的详情、看着像按钮坏了。
func TestRunNowRejectsRunningTaskWithExplicitError(t *testing.T) {
	ctx := context.Background()
	runner := &fakeRunner{}
	runner.gate = make(chan struct{})
	svc, root := newTestService(t, runner)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Describe"),
			agentStage("Fix", "Fix:\n{previous_input}"),
			agentStage("Review", "Review."),
		},
		Input: "change",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next to agent: %v", err)
	}
	waitForCondition(t, func() bool {
		d, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && d.Task.Status == StatusRunning
	})
	if _, err := svc.RunNow(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err == nil {
		t.Fatal("RunNow on a running task must return an explicit error, not a silent 200")
	}
	close(runner.gate)
}
