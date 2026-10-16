package kanban

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"mindfs/server/internal/fs"
)

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
