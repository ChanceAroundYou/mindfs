package kanban

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mindfs/server/internal/fs"
)

// 收尾段（BeginFinishWorktree）与它的完成钩子。
//
// 用真 git 仓库而不是 fakeRunner 造的空目录：finishFixture 已经把任务钉在真的
// linked worktree 上，而「worktree 目录还在不在」正是这一层唯一的判据。

// parkAtWaitingUser 把任务停在 waiting_user —— AddStage 只在这个状态才真正推进并起
// agent，其它状态会把段追加到尾巴上、不执行也不报错。
func parkAtWaitingUser(t *testing.T, svc *Service, rootID, taskID string) Task {
	t.Helper()
	ctx := context.Background()
	store, err := svc.taskStore(rootID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	task.Status = StatusWaitingUser
	task.UpdatedAt = time.Now().UTC()
	if err := store.UpdateTask(ctx, task); err != nil {
		t.Fatalf("park task: %v", err)
	}
	return task
}

func TestBeginFinishWorktreeAppendsAFinishStageAndRunsIt(t *testing.T) {
	svc, root, _, _ := finishFixture(t, "")
	task := parkAtWaitingUser(t, svc, root.ID, mustGetTask(t, svc, root.ID, finishTaskID(t, svc, root.ID)).ID)

	detail, err := svc.BeginFinishWorktree(context.Background(), BeginFinishInput{
		RootID: root.ID,
		TaskID: task.ID,
	})
	if err != nil {
		t.Fatalf("BeginFinishWorktree: %v", err)
	}

	last := detail.Task.Stages[len(detail.Task.Stages)-1]
	if !IsFinishStage(last) {
		t.Fatalf("last stage must be the finish stage, got kind=%q role=%q", last.Kind, last.Role)
	}
	if last.Role != RoleAgent {
		t.Fatalf("the finish stage must be an agent stage, got %q", last.Role)
	}
	// 继承上一个 agent 段的 agent，而不是服务端硬编码一个：后端没有「默认 agent」
	// 概念，runAgentStage 碰到空 agent 直接 failTask。
	if last.Agent != "codex" {
		t.Fatalf("finish stage must inherit the previous agent, got %q", last.Agent)
	}
	// AutoAdvance 必须为 false：成功之后要停在待审核等清场钩子，不能被自动推进走。
	if last.AutoAdvance {
		t.Fatal("the finish stage must not auto-advance; the teardown hook needs it parked")
	}
	// 复用任务主会话 —— 这正是之后要 repoint 回主 checkout 的那个会话。
	if last.SessionReusePolicy != SessionReuseTaskMain {
		t.Fatalf("finish stage must reuse the task main session, got %q", last.SessionReusePolicy)
	}
}

func TestBeginFinishWorktreeRefusesToStackASecondFinishStage(t *testing.T) {
	svc, root, _, _ := finishFixture(t, "")
	task := parkAtWaitingUser(t, svc, root.ID, finishTaskID(t, svc, root.ID))
	if _, err := svc.BeginFinishWorktree(context.Background(), BeginFinishInput{
		RootID: root.ID, TaskID: task.ID,
	}); err != nil {
		t.Fatalf("first BeginFinishWorktree: %v", err)
	}
	// 反复点是用户会做的事（收尾失败后想再来一次）。堆出一串收尾段会让每一段
	// 成功都触发一次清场，第二段开始就是对着已经不存在的 worktree 干活。
	parkAtWaitingUser(t, svc, root.ID, task.ID)
	if _, err := svc.BeginFinishWorktree(context.Background(), BeginFinishInput{
		RootID: root.ID, TaskID: task.ID,
	}); err == nil || !strings.Contains(err.Error(), "已在收尾流程中") {
		t.Fatalf("a second begin-finish must be refused, got %v", err)
	}
}

func TestBeginFinishWorktreeRefusesStatesItCannotFinish(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, svc *Service, rootID, taskID string)
		want  string
	}{
		{
			name:  "worktree directory is gone",
			setup: func(t *testing.T, svc *Service, rootID, taskID string) {},
			want:  "worktree 已不存在",
		},
		{
			name: "task is running",
			setup: func(t *testing.T, svc *Service, rootID, taskID string) {
				store, err := svc.taskStore(rootID)
				if err != nil {
					t.Fatalf("taskStore: %v", err)
				}
				task, err := store.GetTask(context.Background(), taskID)
				if err != nil {
					t.Fatalf("GetTask: %v", err)
				}
				task.Status = StatusRunning
				if err := store.UpdateTask(context.Background(), task); err != nil {
					t.Fatalf("set running: %v", err)
				}
			},
			want: "任务正在执行中",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, root, _, worktreePath := finishFixture(t, "")
			task := parkAtWaitingUser(t, svc, root.ID, finishTaskID(t, svc, root.ID))
			tc.setup(t, svc, root.ID, task.ID)
			if tc.name == "worktree directory is gone" {
				if err := os.RemoveAll(worktreePath); err != nil {
					t.Fatalf("remove worktree: %v", err)
				}
			}
			if _, err := svc.BeginFinishWorktree(context.Background(), BeginFinishInput{
				RootID: root.ID, TaskID: task.ID,
			}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("BeginFinishWorktree must refuse with %q, got %v", tc.want, err)
			}
		})
	}
}

// 收尾段成功之后钩子必须被叫，且只叫一次。
func TestFinishStageHookFiresOnceWhenTheStageSucceeds(t *testing.T) {
	svc, root, _, _ := finishFixture(t, "")
	task := parkAtWaitingUser(t, svc, root.ID, finishTaskID(t, svc, root.ID))

	var calls atomic.Int32
	svc.SetFinishStageFinished(func(_ string, outcome FinishStageOutcome) {
		if !outcome.Succeeded {
			t.Errorf("hook fired with Succeeded=false; a blocked stage must not tear anything down")
		}
		calls.Add(1)
	})

	if _, err := svc.BeginFinishWorktree(context.Background(), BeginFinishInput{
		RootID: root.ID, TaskID: task.ID,
	}); err != nil {
		t.Fatalf("BeginFinishWorktree: %v", err)
	}
	// fakeRunner 默认回报 Done（StageResult 的零值就是 Done），所以这一段会成功。
	waitForCondition(t, func() bool { return calls.Load() > 0 })
	// 只该有一次：补跑循环（taskPend）会再走一遍 executeTask，但那时段已经不是
	// 收尾段了，或者状态已经变过 —— 两种情况都不该再触发清场。
	time.Sleep(150 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("finish hook must fire exactly once, got %d", got)
	}
}

// 受阻的收尾段**不许**触发清场：那正是「停下来问用户」的形态，合进去等于替用户
// 把没做完的活并进主干。
func TestFinishStageHookStaysSilentWhenTheStageIsBlocked(t *testing.T) {
	svc, root, _, _ := finishFixture(t, "")
	task := parkAtWaitingUser(t, svc, root.ID, finishTaskID(t, svc, root.ID))

	var calls atomic.Int32
	svc.SetFinishStageFinished(func(string, FinishStageOutcome) { calls.Add(1) })
	runner := svc.Runner.(*fakeRunner)
	runner.mu.Lock()
	runner.result = StageResult{Outcome: StageOutcomeBlocked, Reason: "合并撞上冲突"}
	runner.mu.Unlock()

	if _, err := svc.BeginFinishWorktree(context.Background(), BeginFinishInput{
		RootID: root.ID, TaskID: task.ID,
	}); err != nil {
		t.Fatalf("BeginFinishWorktree: %v", err)
	}
	waitForCondition(t, func() bool { return calls.Load() > 0 || finishStageStopped(svc, root.ID, task.ID) })
	time.Sleep(150 * time.Millisecond)
	if got := calls.Load(); got != 0 {
		t.Fatalf("a blocked finish stage must not tear anything down, hook fired %d times", got)
	}
}

// finishStageStopped 报收尾段已经跑完（无论结论）。
func finishStageStopped(svc *Service, rootID, taskID string) bool {
	store, err := svc.taskStore(rootID)
	if err != nil {
		return false
	}
	task, err := store.GetTask(context.Background(), taskID)
	if err != nil {
		return false
	}
	index := task.CurrentStageIndex
	if index < 0 || index >= len(task.Stages) || !IsFinishStage(task.Stages[index]) {
		return false
	}
	run, err := store.LatestStageRun(context.Background(), taskID, index)
	if err != nil {
		return false
	}
	return run.Status == StageStatusWaitingUser || run.Status == StageStatusSuccess
}

func mustGetTask(t *testing.T, svc *Service, rootID, taskID string) Task {
	t.Helper()
	store, err := svc.taskStore(rootID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	task, err := store.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	return task
}

// finishTaskID 报这个 root 下唯一一个任务的 id（finishFixture 只造一个）。
func finishTaskID(t *testing.T, svc *Service, rootID string) string {
	t.Helper()
	store, err := svc.taskStore(rootID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	tasks, err := store.ListTasks(context.Background(), ListTasksOptions{})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("fixture should hold exactly one task, got %d", len(tasks))
	}
	return tasks[0].ID
}

// 清场判据逐条钉住。三条守卫少一条，这个表就会红 —— 变异测试确认过：
// 去掉 running 守卫，全套其它测试照样绿（那个分支平时走不到），只有这张表拦得住。
func TestFinishStageTeardownDue(t *testing.T) {
	finish := StageTemplate{Name: finishStageName, Role: RoleAgent, Kind: StageKindWorktreeFinish}
	plain := StageTemplate{Name: "Fix", Role: RoleAgent}

	cases := []struct {
		name   string
		task   Task
		status string
		want   bool
	}{
		{
			name:   "finish stage succeeded and the task settled",
			task:   Task{Status: StatusWaitingUser, CurrentStageIndex: 1, Stages: []StageTemplate{plain, finish}},
			status: StageStatusSuccess,
			want:   true,
		},
		{
			// 「停下来问用户」：受阻的收尾段绝不能清场，否则替用户把没做完的活合进主干。
			name:   "finish stage blocked",
			task:   Task{Status: StatusWaitingUser, CurrentStageIndex: 1, Stages: []StageTemplate{plain, finish}},
			status: StageStatusWaitingUser,
			want:   false,
		},
		{
			// agent 没输出任何结论（静默）：同样不清场。
			name:   "finish stage silent",
			task:   Task{Status: StatusWaitingUser, CurrentStageIndex: 1, Stages: []StageTemplate{plain, finish}},
			status: StageStatusPending,
			want:   false,
		},
		{
			name:   "finish stage failed",
			task:   Task{Status: StatusWaitingUser, CurrentStageIndex: 1, Stages: []StageTemplate{plain, finish}},
			status: StageStatusFail,
			want:   false,
		},
		{
			// 还在跑：结果没定。清场会拆掉 agent 脚下的目录。
			name:   "still running",
			task:   Task{Status: StatusRunning, CurrentStageIndex: 1, Stages: []StageTemplate{plain, finish}},
			status: StageStatusSuccess,
			want:   false,
		},
		{
			// 普通 agent 段收工同样是 success + waiting_user。少了「指针在收尾段上」
			// 这条，每一次 agent 段跑完都会触发一次清场。
			name:   "an ordinary agent stage succeeded",
			task:   Task{Status: StatusWaitingUser, CurrentStageIndex: 0, Stages: []StageTemplate{plain}},
			status: StageStatusSuccess,
			want:   false,
		},
		{
			name:   "pointer out of range",
			task:   Task{Status: StatusWaitingUser, CurrentStageIndex: 5, Stages: []StageTemplate{plain, finish}},
			status: StageStatusSuccess,
			want:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := finishStageTeardownDue(tc.task, tc.status); got != tc.want {
				t.Fatalf("finishStageTeardownDue = %v, want %v", got, tc.want)
			}
		})
	}
}

// 清场必须发生在搬会话**之前**。
//
// 这不是风格问题，是正确性：RepointSession 的收尾会调 ClearTaskWorktree 清掉任务的
// worktree_path，而 FinishTaskWorktree 开头就判「路径为空 → 该任务还没有 worktree」
// 直接返回。于是顺序一旦反过来，目录不拆、分支不删、还不报错 —— 静默失败。
// 这里把「先清场会清掉路径」这件事本身钉住：谁改动 ClearTaskWorktree 让它不清路径，
// 这个测试就得跟着更新，好过悄悄换语义。
func TestRepointingFirstWouldStrandTheWorktree(t *testing.T) {
	svc, root, _, worktreePath := finishFixture(t, "")
	task := parkAtWaitingUser(t, svc, root.ID, finishTaskID(t, svc, root.ID))

	// 搬会话的最后一件事：清任务侧归属。清完路径就没了。
	if err := svc.ClearTaskWorktree(context.Background(), root.ID, task.ID); err != nil {
		t.Fatalf("ClearTaskWorktree: %v", err)
	}
	after := mustGetTask(t, svc, root.ID, task.ID)
	if strings.TrimSpace(after.WorktreePath) != "" {
		t.Fatal("ClearTaskWorktree is expected to drop worktree_path; the ordering above depends on it")
	}
	// 目录还在（没拆），但任务已经不认识它了 —— 这就是顺序反了之后的样子。
	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatalf("the worktree directory should still be there: %v", err)
	}
	if _, err := svc.FinishTaskWorktree(context.Background(), FinishWorktreeInput{
		RootID: root.ID, TaskID: task.ID, DeleteBranch: true,
	}); err == nil || !strings.Contains(err.Error(), "该任务还没有 worktree") {
		t.Fatalf("finish after clear must bail out, not silently no-op; got %v", err)
	}
}

// 收尾段要继承 agent，但一个 agent 段都没有的任务不许硬造一个：后端没有「默认
// agent」，runAgentStage 碰到空 agent 直接 failTask，那会表现成一次莫名其妙的失败。
func TestBeginFinishWorktreeRefusesWhenThereIsNoAgentStageToInheritFrom(t *testing.T) {
	mainDir := newGitRoot(t)
	worktreePath := filepath.Join(mainDir, ".worktree", "task-1")
	gitForTest(t, mainDir, "worktree", "add", "-q", "-b", "task-1", worktreePath)

	root := fs.NewRootInfo("root", "root", mainDir)
	svc := NewService(NewTemplateStoreAt(t.TempDir()), testRoots{root: root})
	svc.SetRunner(&fakeRunner{})
	detail, err := svc.CreateTask(context.Background(), CreateTaskInput{
		RootID:             root.ID,
		Stages:             []StageTemplate{userStage("Describe")},
		Input:              "just a description",
		CreateWorktree:     true,
		WorktreeBranchMode: "existing",
		WorktreeBranch:     "task-1",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	pinWorktree(t, store, detail.Task, root.ID, worktreePath)
	parkAtWaitingUser(t, svc, root.ID, detail.Task.ID)

	if _, err := svc.BeginFinishWorktree(context.Background(), BeginFinishInput{
		RootID: root.ID, TaskID: detail.Task.ID,
	}); err == nil || !strings.Contains(err.Error(), "还没有 agent 阶段") {
		t.Fatalf("a task with no agent stage must be refused, got %v", err)
	}
}
