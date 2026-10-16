package kanban

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNextFinishesFinalStageWaitingUser(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Describe"),
			agentStage("Do it", "Do this:\n{previous_input}"),
		},
		Input: "hello",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	waitForCondition(t, func() bool {
		detail, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && detail.Task.Status == StatusWaitingUser && detail.Task.CurrentStageIndex == 1
	})
	if detail.Task.Status != StatusWaitingUser || detail.Task.CurrentStageIndex != 1 {
		t.Fatalf("status = %s stage = %d, want waiting_user @1", detail.Task.Status, detail.Task.CurrentStageIndex)
	}
	// 最后一阶段等待用户时 Next 应完成而不是报 stage out of range。
	detail, err = svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID, Reason: "done"})
	if err != nil {
		t.Fatalf("Next at final stage: %v", err)
	}
	if detail.Task.Status != StatusSuccess {
		t.Fatalf("status = %s, want success", detail.Task.Status)
	}
}

func TestNextRequiresCurrentUserInputWhenTargetReferencesIt(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Describe"),
			agentStage("Fix", "Fix this:\n{previous_input}"),
		},
		Input: "",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err == nil {
		t.Fatalf("Next with empty input succeeded, want error")
	}
	detail, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	// 校验失败不改写状态：仍停在未开始态的第 0 段。
	if detail.Task.CurrentStageIndex != 0 || detail.Task.Status != StatusPending {
		t.Fatalf("task stage/status = %d/%s, want 0/%s", detail.Task.CurrentStageIndex, detail.Task.Status, StatusPending)
	}
	runner := svc.Runner.(*fakeRunner)
	runner.mu.Lock()
	execCount := len(runner.execs)
	runner.mu.Unlock()
	if execCount != 0 {
		t.Fatalf("agent exec count = %d, want 0", execCount)
	}
}

func TestNextAllowsEmptyInputWhenTargetDoesNotReferenceIt(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Gate"),
			agentStage("Static", "Run the static check."),
		},
		Input: "",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	detail, err = svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID})
	if err != nil {
		t.Fatalf("Next with empty input: %v", err)
	}
	if detail.Task.CurrentStageIndex != 1 {
		t.Fatalf("current stage = %d, want 1", detail.Task.CurrentStageIndex)
	}
}

func TestNextRequiresCurrentInputFromAgentStageWhenTargetReferencesIt(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Describe"),
			agentStage("Fix", "Fix this:\n{previous_input}"),
			agentStage("Review", "Review this:\n{previous_input}"),
		},
		Input: "first",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	detail, err = svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID})
	if err != nil {
		t.Fatalf("Next to agent: %v", err)
	}
	waitForCondition(t, func() bool {
		detail, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && detail.Task.CurrentStageIndex == 1 && detail.Task.Status == StatusWaitingUser
	})
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err == nil {
		t.Fatalf("Next from empty agent input succeeded, want error")
	}
	detail, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask after failed next: %v", err)
	}
	if detail.Task.CurrentStageIndex != 1 {
		t.Fatalf("current stage = %d, want 1", detail.Task.CurrentStageIndex)
	}
}

// Next/Resume/RunNow 等处都会触发 RunTask：守卫防止同一个 agent 阶段被并发跑两次。
// 用一个会阻塞的 runner 把第一个执行体钉在阶段内部，再补一次 RunTask —— 没有守卫时 exec 会变成 2。
func TestRunTaskExecutesStageOnceWhenKickedTwice(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	gate := make(chan struct{})
	runner := &fakeRunner{gate: gate}
	svc.SetRunner(runner)

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
	// 等第一个执行体真正进入 agent 阶段（gate 会把它留在里面）。
	waitForCondition(t, func() bool {
		runner.mu.Lock()
		defer runner.mu.Unlock()
		return len(runner.execs) > 0
	})
	runner.mu.Lock()
	entered := len(runner.execs) > 0
	runner.mu.Unlock()
	if !entered {
		close(gate)
		t.Fatalf("agent stage never started")
	}
	// 第二个执行体：守卫生效时应被挡下。
	svc.RunTask(root.ID, detail.Task.ID)
	time.Sleep(50 * time.Millisecond)
	close(gate)

	waitForCondition(t, func() bool {
		got, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && got.Task.Status == StatusWaitingUser
	})
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.execs) != 1 {
		t.Fatalf("runner exec count=%d, want 1（同一阶段被执行了多次）", len(runner.execs))
	}
}

// 待开始态点「开始」必须直接进执行中：先过掉 user 段再跑 agent 段。
// 曾经 RunNow 对 pending 只调 RunTask 不推进阶段，任务卡在待审核，
// 用户得再点一次「执行」才真的跑起来。
func TestRunNowFromPendingStartsAgentStage(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{userStage("Describe"), agentStage("Fix", "Fix this:\n{previous_input}")},
		Input:  "broken save button",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if detail.Task.Status != StatusPending || detail.Task.CurrentStageIndex != 0 {
		t.Fatalf("new task stage/status = %d/%s, want 0/%s", detail.Task.CurrentStageIndex, detail.Task.Status, StatusPending)
	}

	if _, err := svc.RunNow(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	// 等**自动落到 agent 段**（index==1）这个稳定结果，别等 Status==running。
	// running 是瞬态：推进阶段时同步置 running，随后异步 executeTask 跑完 agent 段
	// 就转 waiting_user。高并行负载下（go test ./... 各包并行）它可能在首轮轮询
	// （动作返回后 ~10ms）之前就翻过去 —— 实测 300 次高压下 12 次从头到尾只见
	// waiting_user，条件永不成立 → 2s 超时。index==1 在 running 与 waiting_user
	// 两态下都成立，且正是本用例要断言的结果。
	waitForCondition(t, func() bool {
		d, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && d.Task.CurrentStageIndex == 1
	})
	detail, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if detail.Task.CurrentStageIndex != 1 {
		t.Fatalf("current stage=%d, want 1（「开始」应直接进入 agent 段）", detail.Task.CurrentStageIndex)
	}
	// user 段被「开始」这一次动作批准掉，不该留在待审核。
	for _, run := range detail.StageRuns {
		if run.StageIndex == 0 && run.Status == StageStatusWaitingUser {
			t.Fatalf("stage 0 still waiting_user after one RunNow")
		}
	}
}

// agent 段停在 waiting_user（没输出 [STAGE-DONE:N]）时，用户手动点「立即执行」要能推进。
//
// 2026-10-03 实测（mindfs 任务 26）：定位段把活干完了但没回报完成，于是
//   - 详情面板按 canAdvanceFromCurrentStage 不给按钮，
//   - 看板卡片只查 hasLaterStage，给了个「立即执行」，
//   - 点下去 moveRelative 报错、RunNow 又把错吞掉只回未变的详情。
//
// 结果任务彻底卡死，用户唯一想做的事（就这样，推进到下一段）没有任何入口能达成。
//
// 自动推进那条防线不在此处：引擎走 moveTo（auto_advanced 事件），根本不经过
// canLeaveStageOnRequest —— TestAgentStageWithoutDoneMarkerStopsAtCurrentStage 守着它。
func TestRunNowAdvancesAgentStageThatStoppedWaitingForUser(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{result: StageResult{Outcome: StageOutcomeSilent}})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Describe"),
			agentStage("Locate", "Locate:\n{previous_input}"),
			agentStage("Fix", "Fix it."),
		},
		Input: "find it",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next to agent: %v", err)
	}
	waitForCondition(t, func() bool {
		d, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && d.Task.Status == StatusWaitingUser && d.Task.CurrentStageIndex == 1
	})

	// 复现形状：RunNow 在 waiting_user 下走 Next；以前 moveRelative 拒绝、错误被吞掉。
	after := mustGetDetail(t, svc, root.ID, detail.Task.ID)
	if after.Task.Status != StatusWaitingUser {
		t.Fatalf("precondition: status = %s, want waiting_user", after.Task.Status)
	}
	if _, err := svc.RunNow(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("RunNow on an unreported agent stage must advance, got %v", err)
	}
	moved := mustGetDetail(t, svc, root.ID, detail.Task.ID)
	if moved.Task.CurrentStageIndex != 2 {
		t.Fatalf("current stage = %d, want 2（用户点了就该推进到下一段）", moved.Task.CurrentStageIndex)
	}
	if len(moved.StageRuns) != 3 {
		t.Fatalf("stage run count = %d, want 3（下一段被植进来了）", len(moved.StageRuns))
	}
}

// 引擎的自动推进仍然不许跳过没回报完成的 agent 段：它走 moveTo，不走
// canLeaveStageOnRequest。这条与上一条是一对 —— 放宽的只有人点的那条路。
func TestAutoAdvanceStillStopsAtUnreportedAgentStage(t *testing.T) {
	ctx := context.Background()
	// auto_advance=true 但 agent 静默：引擎跑完这一段后必须停在原地。
	svc, root := newTestService(t, &fakeRunner{result: StageResult{Outcome: StageOutcomeSilent}})
	implement := agentStage("Implement", "Implement {previous_input}")
	implement.AutoAdvance = true
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{userStage("Describe"), implement, agentStage("Review", "Review.")},
		Input:  "change",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	waitForCondition(t, func() bool {
		d, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && d.Task.Status == StatusWaitingUser && d.Task.AuxFlags.SessionError != ""
	})
	d := mustGetDetail(t, svc, root.ID, detail.Task.ID)
	if d.Task.CurrentStageIndex != 1 {
		t.Fatalf("current stage = %d, want 1（引擎不许自己跳过没回报的段）", d.Task.CurrentStageIndex)
	}
	if len(d.StageRuns) != 2 {
		t.Fatalf("stage run count = %d, want 2（下一段没被植进来）", len(d.StageRuns))
	}
}

// fail / cancelled / rejected 仍然拦得住：放宽的只有 waiting_user 一种。
func TestRunNowStillRejectsFailedAgentStage(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{runErr: errors.New("agent unavailable")})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Describe"),
			agentStage("Fix", "Fix:\n{previous_input}"),
			agentStage("Review", "Review."),
		},
		Input: "broken",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next to agent: %v", err)
	}
	waitForCondition(t, func() bool {
		d, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && d.Task.CurrentStageIndex == 1 && d.Task.AuxFlags.SessionError != ""
	})
	// RunNow 的 waiting_user 分支照旧把错误吞掉回详情（前端有那条人话可看），
	// 所以断言的是**指针没动**，而不是 HTTP 层报错。
	if _, err := svc.RunNow(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	d := mustGetDetail(t, svc, root.ID, detail.Task.ID)
	if d.Task.CurrentStageIndex != 1 {
		t.Fatalf("current stage = %d, want 1（失败段仍不许被推走）", d.Task.CurrentStageIndex)
	}
}
