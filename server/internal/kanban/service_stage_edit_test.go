package kanban

import (
	"context"
	"strings"
	"testing"
	"time"
)

// 等待用户时追加 comment：当前段标记 approved、新增段默认命令名并立即进入执行。
func TestAddStageApprovesAndRunsWhenWaiting(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{userStage("Describe")},
		Input:  "第一版",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	// 新建任务是未开始态 → 先「开始」，让首段被执行体推进到等待用户。
	if _, err := svc.RunNow(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	waitForCondition(t, func() bool {
		got, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && got.Task.Status == StatusWaitingUser
	})
	detail, err = svc.AddStage(ctx, AddStageInput{
		RootID: root.ID,
		TaskID: detail.Task.ID,
		Stage:  agentStage("", "再走一遍流程：\n{previous_input}"),
	})
	if err != nil {
		t.Fatalf("AddStage: %v", err)
	}
	if len(detail.Task.Stages) != 2 {
		t.Fatalf("stages len=%d, want 2", len(detail.Task.Stages))
	}
	if detail.Task.Stages[1].Name != "阶段 2" {
		t.Fatalf("appended stage name=%q, want 阶段 2（缺省命名）", detail.Task.Stages[1].Name)
	}
	waitForCondition(t, func() bool {
		got, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && got.Task.Status == StatusWaitingUser && got.Task.CurrentStageIndex == 1
	})
	got, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	runner := svc.Runner.(*fakeRunner)
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.execs) != 1 {
		t.Fatalf("runner exec count=%d, want 1", len(runner.execs))
	}
	if !strings.Contains(runner.prompts[0], "再走一遍流程") || !strings.Contains(runner.prompts[0], "第一版") {
		t.Fatalf("appended prompt not rendered with previous input: %q", runner.prompts[0])
	}
	if got.Task.Status != StatusWaitingUser {
		t.Fatalf("task status=%s, want waiting_user", got.Task.Status)
	}
}

// 运行中追加：只排入流水尾，不立即执行；当前段结束后等待用户，验收再推进。
func TestAddStageQueuesTailWhileRunning(t *testing.T) {
	ctx := context.Background()
	gate := make(chan struct{})
	svc, root := newTestService(t, nil)
	runner := &fakeRunner{gate: gate}
	svc.SetRunner(runner)
	fix := agentStage("Fix", "Fix this:\n{previous_input}")
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{userStage("Describe"), fix},
		Input:  "第一版",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	waitForCondition(t, func() bool {
		runner.mu.Lock()
		defer runner.mu.Unlock()
		return len(runner.execs) > 0
	})

	// 任务 running：追加段应只入流水尾。
	updated, err := svc.AddStage(ctx, AddStageInput{
		RootID: root.ID,
		TaskID: detail.Task.ID,
		Stage:  agentStage("Follow-up", "Follow up."),
	})
	if err != nil {
		t.Fatalf("AddStage while running: %v", err)
	}
	if len(updated.Task.Stages) != 3 {
		t.Fatalf("stages len=%d, want 3", len(updated.Task.Stages))
	}
	if updated.Task.Status != StatusRunning {
		t.Fatalf("task status=%s, want running（追加不打断运行）", updated.Task.Status)
	}
	runner.mu.Lock()
	execBefore := len(runner.execs)
	runner.mu.Unlock()
	time.Sleep(50 * time.Millisecond)
	runner.mu.Lock()
	execAfter := len(runner.execs)
	runner.mu.Unlock()
	if execAfter != execBefore {
		t.Fatalf("appended stage started executing while running (exec %d->%d)", execBefore, execAfter)
	}
	close(gate)

	// Fix 段结束、非 auto_advance → 等待用户；此时追加过的段还没跑。
	waitForCondition(t, func() bool {
		got, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && got.Task.Status == StatusWaitingUser && got.Task.CurrentStageIndex == 1
	})
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID, Reason: "continue"}); err != nil {
		t.Fatalf("Next after approval: %v", err)
	}
	// Next 里那次 RunTask 可能被并发守卫吞掉（第一个执行体还没完全退出）；兜底再踢一次。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runner.mu.Lock()
		execCount := len(runner.execs)
		runner.mu.Unlock()
		if execCount >= 2 {
			break
		}
		svc.RunTask(root.ID, detail.Task.ID)
		time.Sleep(10 * time.Millisecond)
	}
	waitForCondition(t, func() bool {
		got, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && got.Task.CurrentStageIndex == 2
	})
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.execs) != 2 {
		t.Fatalf("runner exec count=%d, want 2（排队段未在验收后执行）", len(runner.execs))
	}
	if !strings.Contains(runner.prompts[1], "Follow up") {
		t.Fatalf("second exec prompt=%q, want appended stage prompt", runner.prompts[1])
	}
}

func TestUpdateStageEditsFutureStagePrompt(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Describe"),
			agentStage("Fix", "v1 {previous_input}"),
		},
		Input: "input",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	newStage := agentStage("Fix improved", "v2 {previous_input}")
	if _, err := svc.UpdateStage(ctx, UpdateStageInput{RootID: root.ID, TaskID: detail.Task.ID, Index: 1, Stage: &newStage}); err != nil {
		t.Fatalf("UpdateStage: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	waitForCondition(t, func() bool {
		runner := svc.Runner.(*fakeRunner)
		runner.mu.Lock()
		defer runner.mu.Unlock()
		return len(runner.execs) > 0
	})
	runner := svc.Runner.(*fakeRunner)
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if !strings.Contains(runner.prompts[0], "v2") {
		t.Fatalf("edited prompt not used: %q", runner.prompts[0])
	}
	got, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Task.Stages[1].Name != "Fix improved" {
		t.Fatalf("stage name not updated: %q", got.Task.Stages[1].Name)
	}
}

// 删除未执行段：只允许删尚未产生 StageRun、且不在指针上的段；index 0 与当前段不可删。
func TestRemoveStageDeletesUnexecutedStage(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Describe"),
			agentStage("Fix", "Fix {previous_input}"),
			agentStage("Extra", "Extra {previous_input}"),
		},
		Input: "input",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	taskID := detail.Task.ID

	// index 0 是任务输入段，不可删。
	if _, err := svc.RemoveStage(ctx, RemoveStageInput{RootID: root.ID, TaskID: taskID, Index: 0}); err == nil {
		t.Fatal("RemoveStage(index 0) succeeded, want rejection")
	}
	// 越界。
	if _, err := svc.RemoveStage(ctx, RemoveStageInput{RootID: root.ID, TaskID: taskID, Index: 3}); err == nil {
		t.Fatal("RemoveStage(out of range) succeeded, want rejection")
	}
	// 新建任务的指针在 0，index 1/2 都还没跑过 → 指针之外的尾部可删。
	got, err := svc.RemoveStage(ctx, RemoveStageInput{RootID: root.ID, TaskID: taskID, Index: 2})
	if err != nil {
		t.Fatalf("RemoveStage(untouched tail): %v", err)
	}
	if len(got.Task.Stages) != 2 {
		t.Fatalf("stages len=%d, want 2", len(got.Task.Stages))
	}
	if got.Task.CurrentStageIndex != 0 {
		t.Fatalf("current_stage_index=%d, want 0（指针在删除点之前，不动）", got.Task.CurrentStageIndex)
	}

	// 跑到 index 1 → 等待用户，该段已产生 success 的 StageRun。
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: taskID}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	waitForCondition(t, func() bool {
		got, err := svc.GetTask(ctx, root.ID, taskID)
		return err == nil && got.Task.Status == StatusWaitingUser && got.Task.CurrentStageIndex == 1
	})
	// 已执行段不可删。
	if _, err := svc.RemoveStage(ctx, RemoveStageInput{RootID: root.ID, TaskID: taskID, Index: 1}); err == nil {
		t.Fatal("RemoveStage(executed stage) succeeded, want rejection")
	}

	// 尾段（index 1）现在是当前段：即便被 `Next` 判成末段收尾，也不能删当前指针段。
	if _, err := svc.RemoveStage(ctx, RemoveStageInput{RootID: root.ID, TaskID: taskID, Index: 1}); err == nil {
		t.Fatal("RemoveStage(current stage) succeeded, want rejection")
	}

	// 追加一段空 prompt 的段（UI「+ 新增阶段」就是这种）→ 只入流水尾，不推进。
	// 然后删掉它：指针仍停在 1。
	added, err := svc.AddStage(ctx, AddStageInput{
		RootID: root.ID,
		TaskID: taskID,
		Stage:  agentStage("Follow-up", ""),
	})
	if err != nil {
		t.Fatalf("AddStage: %v", err)
	}
	if len(added.Task.Stages) != 3 {
		t.Fatalf("stages len=%d, want 3", len(added.Task.Stages))
	}
	if added.Task.CurrentStageIndex != 1 {
		t.Fatalf("current_stage_index=%d, want 1（空 prompt 追加不推进）", added.Task.CurrentStageIndex)
	}
	got, err = svc.RemoveStage(ctx, RemoveStageInput{RootID: root.ID, TaskID: taskID, Index: 2})
	if err != nil {
		t.Fatalf("RemoveStage(appended tail): %v", err)
	}
	if len(got.Task.Stages) != 2 {
		t.Fatalf("stages len=%d, want 2", len(got.Task.Stages))
	}
	if got.Task.CurrentStageIndex != 1 {
		t.Fatalf("current_stage_index=%d, want 1", got.Task.CurrentStageIndex)
	}
}

// 删掉指针之前的段时，指针要跟着回移，不能指到别的段上。
func TestRemoveStageBeforeCurrentShiftsPointer(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Describe"),
			agentStage("Fix", "Fix {previous_input}"),
			userStage("Review"),
			agentStage("Polish", "Polish {previous_input}"),
		},
		Input: "input",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	taskID := detail.Task.ID

	// 走到 index 2（user 段，等待用户）。RerunStage 顶替了 Jump（d1f0e6e 删了后者）。
	if _, err := svc.RerunStage(ctx, MoveInput{RootID: root.ID, TaskID: taskID, StageIndex: 2}); err != nil {
		t.Fatalf("RerunStage: %v", err)
	}
	before, err := svc.GetTask(ctx, root.ID, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if before.Task.CurrentStageIndex != 2 {
		t.Fatalf("current_stage_index=%d, want 2", before.Task.CurrentStageIndex)
	}

	// index 1 在指针之前且未执行 → 可删，指针 2 回移到 1。
	got, err := svc.RemoveStage(ctx, RemoveStageInput{RootID: root.ID, TaskID: taskID, Index: 1})
	if err != nil {
		t.Fatalf("RemoveStage(before current): %v", err)
	}
	if len(got.Task.Stages) != 3 {
		t.Fatalf("stages len=%d, want 3", len(got.Task.Stages))
	}
	if got.Task.CurrentStageIndex != 1 {
		t.Fatalf("current_stage_index=%d, want 1（指针随删除回移）", got.Task.CurrentStageIndex)
	}
	if got.Task.Stages[1].Name != "Review" {
		t.Fatalf("stages[1].name=%q, want Review", got.Task.Stages[1].Name)
	}
}

// 重跑：把指针移回某段并再次执行（任务不在运行中时）。
func TestRerunStageReexecutesStage(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{userStage("Describe"), agentStage("Fix", "Fix {previous_input}")},
		Input:  "input",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	waitForCondition(t, func() bool {
		got, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && got.Task.Status == StatusWaitingUser && got.Task.CurrentStageIndex == 1
	})
	if _, err := svc.RerunStage(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID, StageIndex: 1}); err != nil {
		t.Fatalf("RerunStage: %v", err)
	}
	// 等 goroutine 把任务状态落定（RerunStage 失败 → waiting_user），再断言确实执行了第二段。
	// 不能等 len(runner.execs)==2：那在 goroutine 头几条语句就满足，同步 GetTask 会跑赢
	// 剩余的 SQLite 落库尾巴，读到中间态（-count=10 下约 9/10 复现）。
	waitForCondition(t, func() bool {
		got, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		runner := svc.Runner.(*fakeRunner)
		return err == nil && got.Task.Status == StatusWaitingUser &&
			func() bool {
				runner.mu.Lock()
				defer runner.mu.Unlock()
				return len(runner.execs) == 2
			}()
	})
	// 重跑再次失败后应回到等待用户。
	got, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Task.Status != StatusWaitingUser || got.Task.CurrentStageIndex != 1 {
		t.Fatalf("after rerun status/stage = %s/%d, want waiting_user/1", got.Task.Status, got.Task.CurrentStageIndex)
	}
}

func containsAll(value string, parts []string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}
