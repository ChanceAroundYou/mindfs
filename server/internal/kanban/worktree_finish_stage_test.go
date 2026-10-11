package kanban

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
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

// parkOnWaitingAgentStage 把任务摆成「当前段是 agent 段、这一段停在 waiting_user」。
//
// 这是 2026-10-08 实测 task-9 的形状：agent 把活干完了但没输出 [STAGE-DONE:N]，任务停在
// 「等待你」，当前段正是 **agent 段的 waiting_user**。
//
// 走真 runner（静默结局）而不是直接改库：这样「停在 waiting_user」是引擎自己走出来的，
// 不是测试捏出来的。默认 fixture 的 fakeRunner 零值结果会被记成 success（StageOutcomeDone
// 是零值），正好绕开这条路 —— 必须显式给 Silent。
func parkOnWaitingAgentStage(t *testing.T, svc *Service, rootID, taskID string) {
	t.Helper()
	ctx := context.Background()
	svc.SetRunner(&fakeRunner{result: StageResult{Outcome: StageOutcomeSilent}})
	if _, err := svc.Next(ctx, MoveInput{RootID: rootID, TaskID: taskID}); err != nil {
		t.Fatalf("Next to the agent stage: %v", err)
	}
	waitForCondition(t, func() bool {
		d, err := svc.GetTask(ctx, rootID, taskID)
		return err == nil && d.Task.Status == StatusWaitingUser && d.Task.CurrentStageIndex == 1
	})
	d, err := svc.GetTask(ctx, rootID, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if d.Task.Stages[1].Role != RoleAgent {
		t.Fatalf("fixture shape changed: stage 1 must be an agent stage, got %q", d.Task.Stages[1].Role)
	}
}

// 收尾段必须能越过「停在 waiting_user 的 agent 段」—— 这是用户实测的那个拦路虎。
//
// 2026-10-08 task-9：任务停在「等待你」，当前段是 agent 段的 waiting_user。收尾段走的是
// AddStage 的**评论**那条路（canAdvanceFromStage），那里 agent 段的 waiting_user 是明确
// 拒绝的，于是用户点了收尾却拿到「current stage is waiting_user: 这一段没走完，追加评论
// 不能替代完成本段」+ 一长串清单 —— 而他点的本来就不是评论。
//
// 收尾是人/系统的显式动作，走 canLeaveStageOnRequest（与「下一段」按钮同一条规则）。
func TestBeginFinishWorktreeAppendsAFinishStageOverAnAgentStageParkedAtWaitingUser(t *testing.T) {
	ctx := context.Background()
	svc, root, _, _ := finishFixture(t, "work\n")
	taskID := finishTaskID(t, svc, root.ID)
	parkOnWaitingAgentStage(t, svc, root.ID, taskID)

	detail, err := svc.BeginFinishWorktree(ctx, BeginFinishInput{RootID: root.ID, TaskID: taskID})
	if err != nil {
		t.Fatalf("BeginFinishWorktree over a waiting_user agent stage: %v", err)
	}
	last := detail.Task.Stages[len(detail.Task.Stages)-1]
	if !IsFinishStage(last) {
		t.Fatalf("last stage must be the finish stage, got kind=%q", last.Kind)
	}
	if detail.Task.CurrentStageIndex != len(detail.Task.Stages)-1 {
		t.Fatalf("current stage = %d, want the finish stage %d", detail.Task.CurrentStageIndex, len(detail.Task.Stages)-1)
	}
}

// 同一局面下，**一句评论**仍然不许越过停在 waiting_user 的 agent 段。
//
// 与上一条是一对：放宽的只有「人/系统显式发起」那条路（ByRequest），不是 AddStage 整个。
// 少了这条，下次有人把 ByRequest 的分支删掉（或反过来把 canAdvanceFromStage 换掉）都不会红。
func TestAddStageWithoutByRequestStillRefusesToLeaveAWaitingAgentStage(t *testing.T) {
	ctx := context.Background()
	svc, root, _, _ := finishFixture(t, "work\n")
	taskID := finishTaskID(t, svc, root.ID)
	parkOnWaitingAgentStage(t, svc, root.ID, taskID)

	_, err := svc.AddStage(ctx, AddStageInput{
		RootID: root.ID,
		TaskID: taskID,
		Stage:  agentStage("More", "Do more."),
	})
	if err == nil {
		t.Fatal("a plain comment must not advance an agent stage parked at waiting_user")
	}
	if !strings.Contains(err.Error(), "追加评论不能替代完成本段") {
		t.Fatalf("want the comment-path refusal, got %v", err)
	}
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
// 这不是风格问题，是正确性：搬会话会读 main_session_key，而清场第 4 步
// ClearWorktreeRefs 会清掉任务的 worktree 归属。顺序一旦反过来，最直接的后果是
// 清场开头就判「路径为空 → 该任务还没有 worktree」直接返回 —— 目录不拆、分支不删、
// 还不报错，静默失败。
//
// 这里钉的是「清场自己会清路径」这个不变量（顺序依赖它），而不是钉某个具体调用者：
// 2026-10-01 之前 repoint 也会清路径，于是「先搬会话」会同样触发静默失败。
// repoint 那条路已经拆掉了（搬会话与任务收工不再焊死），本测试守住的是清场自身。
func TestRepointingFirstWouldStrandTheWorktree(t *testing.T) {
	svc, root, _, worktreePath := finishFixture(t, "")
	task := parkAtWaitingUser(t, svc, root.ID, finishTaskID(t, svc, root.ID))

	// 清场的第 4 步会清掉归属。清完路径就没了。
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

// 搬会话**不许**清掉任务的 worktree 归属 —— 这条是 2026-10-01 修的真 bug：
// 一次纯搬会话的 repoint 把还在用的任务目录记录清成空，卡片随即显示「已收尾」，
// 一个从未收过尾的任务声称收工了。
//
// 收尾该清的由清场自己清（ClearWorktreeRefs），两边语义对等，谁也不替谁代劳。
func TestRepointDoesNotStealTheTasksWorktreeRecord(t *testing.T) {
	svc, root, _, worktreePath := finishFixture(t, "")
	task := parkAtWaitingUser(t, svc, root.ID, finishTaskID(t, svc, root.ID))

	before := mustGetTask(t, svc, root.ID, task.ID)
	if strings.TrimSpace(before.WorktreePath) == "" {
		t.Fatal("fixture must start with a live worktree path")
	}

	// 走一遍任务侧那个「清归属」的公开出口，确认**只有**清场在用它：repoint 的
	// 源码里不许再出现 ClearTaskWorktree。这里解析 AST 而不是 grep 文本 ——
	// grep 会把注释里那句「刻意不调 ClearTaskWorktree」也算成命中。
	if callsClearTaskWorktree(t) {
		t.Fatal("repoint must not clear the task's worktree record: it moves a session, it does not retire a task")
	}
	// 反过来确认：清场那条路**要**清 —— 别把修复做成「repoint 不清、于是谁都不清了」。
	if !strings.Contains(readRepoFile(t, "worktree_finish.go"), "ClearWorktreeRefs") {
		t.Fatal("the teardown must still clear worktree refs; otherwise nothing ever retires a task's worktree")
	}
	// 目录也还在，没被谁顺手拆掉。
	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatalf("the worktree directory should be untouched: %v", err)
	}
	_ = task
}

// callsClearTaskWorktree 解析 repoint 的源码，找有没有**真的调用**清归属那个出口。
//
// 用 AST 而不是字符串查找：session_repoint.go 里现在有一整段注释在解释「为什么这里
// 刻意不调 ClearTaskWorktree」，grep 'ClearTaskWorktree' 必然命中，测试就成了永远绿。
func callsClearTaskWorktree(t *testing.T) bool {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), repointSourcePath(t), nil, 0)
	if err != nil {
		t.Fatalf("parse repoint source: %v", err)
	}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.SelectorExpr:
			if fun.Sel.Name == "ClearTaskWorktree" {
				found = true
			}
		case *ast.Ident:
			if fun.Name == "ClearTaskWorktree" || fun.Name == "clearTaskWorktree" {
				found = true
			}
		}
		return true
	})
	return found
}

// repointSourcePath 定位 session_repoint.go：本测试在 kanban 包里，包目录是
// server/internal/kanban，而目标在隔壁的 api/usecase。
func repointSourcePath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test source file")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "api", "usecase", "session_repoint.go")
}

// readRepoFile 读 kanban 包内的源文件，供「这一侧**要**有那个调用」的反向断言用。
func readRepoFile(t *testing.T, name string) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test source file")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(raw)
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

// 终态任务也要能收尾（2026-10-04 用户要求）：活跑完了但 worktree 还留着没收，
// 那正是收尾唯一有意义的时刻。原来这里直接返回「任务已结束」。
//
// 顺带钉住 completed_at 被清掉 —— 留着它看板会继续把任务渲染成「已完成」
// （列归位、卡片打勾），而它实际正在跑收尾段，那种「显示已完成、实际在动」
// 的状态最容易让人以为按钮没生效。
func TestBeginFinishWorktreeRevivesTerminalTasks(t *testing.T) {
	for _, status := range []string{StatusSuccess, StatusCancelled, StatusFail} {
		t.Run(status, func(t *testing.T) {
			svc, root, _, _ := finishFixture(t, "")
			store, err := svc.taskStore(root.ID)
			if err != nil {
				t.Fatalf("taskStore: %v", err)
			}
			task := mustGetTask(t, svc, root.ID, finishTaskID(t, svc, root.ID))
			terminalAt := time.Now().UTC()
			task.Status = status
			task.CompletedAt = terminalAt.Format(time.RFC3339Nano)
			if err := store.UpdateTask(context.Background(), task); err != nil {
				t.Fatalf("park terminal: %v", err)
			}

			if _, err := svc.BeginFinishWorktree(context.Background(), BeginFinishInput{
				RootID: root.ID, TaskID: task.ID,
			}); err != nil {
				t.Fatalf("BeginFinishWorktree on a %s task must work, got %v", status, err)
			}

			after, err := store.GetTask(context.Background(), task.ID)
			if err != nil {
				t.Fatalf("GetTask after: %v", err)
			}
			if after.CompletedAt != "" {
				t.Fatalf("completed_at must be cleared on revive, got %q", after.CompletedAt)
			}
			// 收尾段必须真的挂上并成为当前段 —— 只改 status 不追加段的话，
			// AddStage 会把它排到流水尾不动，用户点了等于没点。
			last := after.Stages[len(after.Stages)-1]
			if !IsFinishStage(last) {
				t.Fatalf("a finish stage must be appended, got kind=%q", last.Kind)
			}
			if after.Status == StatusSuccess || after.Status == StatusCancelled || after.Status == StatusFail {
				t.Fatalf("task must leave the terminal state, still %q", after.Status)
			}
		})
	}
}

// 复活只解决「AddStage 不推进」；worktree 目录不在了仍然无从收尾。
// 这条不能因为放开终态而丢掉 —— 该点的是重建按钮。
func TestBeginFinishWorktreeStillRefusesTerminalTaskWithoutWorktree(t *testing.T) {
	svc, root, _, worktreePath := finishFixture(t, "")
	task := mustGetTask(t, svc, root.ID, finishTaskID(t, svc, root.ID))
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	task.Status = StatusSuccess
	if err := store.UpdateTask(context.Background(), task); err != nil {
		t.Fatalf("park terminal: %v", err)
	}
	if err := os.RemoveAll(worktreePath); err != nil {
		t.Fatalf("remove worktree: %v", err)
	}

	if _, err := svc.BeginFinishWorktree(context.Background(), BeginFinishInput{
		RootID: root.ID, TaskID: task.ID,
	}); err == nil || !strings.Contains(err.Error(), "worktree 已不存在") {
		t.Fatalf("a terminal task without a worktree must still be refused, got %v", err)
	}
}

// 被拒绝的终态任务必须**原样躺着**：复活发生在所有校验之后，早一步就会先把
// 状态改成 waiting_user 再返回错误 —— 板上于是多出一个「在动」却没人跑的任务，
// 而用户连一个明确的报错都没拿到。这条钉的是**顺序**，不是判据。
func TestBeginFinishWorktreeLeavesTerminalTaskUntouchedWhenRefused(t *testing.T) {
	cases := []struct {
		name    string
		wantErr string
		prepare func(task *Task)
	}{
		{
			name:    "already in a finish flow",
			wantErr: "已在收尾流程中",
			prepare: func(task *Task) {
				task.Stages = append(task.Stages, StageTemplate{
					Name: finishStageName, Role: RoleAgent, Kind: StageKindWorktreeFinish,
				})
			},
		},
		{
			name:    "no agent stage to inherit from",
			wantErr: "还没有 agent 阶段",
			prepare: func(task *Task) {
				task.Stages = []StageTemplate{{Name: "只有 user 段", Role: RoleUser}}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, root, _, _ := finishFixture(t, "")
			store, err := svc.taskStore(root.ID)
			if err != nil {
				t.Fatalf("taskStore: %v", err)
			}
			task := mustGetTask(t, svc, root.ID, finishTaskID(t, svc, root.ID))
			completedAt := time.Now().UTC().Format(time.RFC3339Nano)
			task.Status = StatusSuccess
			task.CompletedAt = completedAt
			tc.prepare(&task)
			if err := store.UpdateTask(context.Background(), task); err != nil {
				t.Fatalf("park terminal: %v", err)
			}
			before, err := store.GetTask(context.Background(), task.ID)
			if err != nil {
				t.Fatalf("GetTask before: %v", err)
			}

			if _, err := svc.BeginFinishWorktree(context.Background(), BeginFinishInput{
				RootID: root.ID, TaskID: task.ID,
			}); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want a %q refusal, got %v", tc.wantErr, err)
			}

			after, err := store.GetTask(context.Background(), task.ID)
			if err != nil {
				t.Fatalf("GetTask after: %v", err)
			}
			if after.Status != StatusSuccess || after.CompletedAt == "" {
				t.Fatalf("a refused terminal task must not be revived: status=%q completed_at=%q",
					after.Status, after.CompletedAt)
			}
			if len(after.Stages) != len(before.Stages) {
				t.Fatalf("a refused call must not append stages: %d → %d", len(before.Stages), len(after.Stages))
			}
		})
	}
}
