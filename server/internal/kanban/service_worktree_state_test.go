package kanban

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// worktree_missing 是派生的：目录没了转 true，重建后转回 false。
//
// 判据是 WorktreeMissingNow() 而不是 WorktreeMissing 字段：后者是 json:"-" 的派生槽位，
// 只在 MarshalJSON 内部被赋值，从库里读出来的 Task 上恒为零值。拿它断言等于什么都没测。
func TestTaskWorktreeMissingIsDerivedFromDirectory(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "afile")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	cases := []struct {
		name string
		task Task
		want bool
	}{
		{"没建过 worktree", Task{CreateWorktree: true}, false},
		{"目录还在", Task{CreateWorktree: true, WorktreePath: dir}, false},
		{"目录已删", Task{CreateWorktree: true, WorktreePath: filepath.Join(dir, "deleted")}, true},
		// 没开 worktree 的任务不该被报成失效（那不是它的 worktree）。
		{"任务本身没开 worktree", Task{CreateWorktree: false, WorktreePath: filepath.Join(dir, "deleted")}, false},
		// 路径存在但不是目录（cwd 必须是目录）同样算失效。
		{"路径是文件", Task{CreateWorktree: true, WorktreePath: filePath}, true},
	}
	for _, tc := range cases {
		if got := tc.task.WorktreeMissingNow(); got != tc.want {
			t.Errorf("%s: WorktreeMissingNow() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// 序列化后仍带着派生的 worktree_missing：前端靠这个字段改徽标。
func TestTaskJSONIncludesDerivedWorktreeMissing(t *testing.T) {
	gone := Task{ID: "t1", CreateWorktree: true, WorktreePath: filepath.Join(t.TempDir(), "deleted")}
	raw, err := json.Marshal(gone)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out["worktree_missing"] != true {
		t.Fatalf("worktree_missing=%v, want true", out["worktree_missing"])
	}
	// 反序列化不能把派生字段当持久化字段读回来（它每次都该重算）。
	var back Task
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal into Task: %v", err)
	}
	live := Task{ID: "t2", CreateWorktree: true, WorktreePath: t.TempDir()}
	rawLive, err := json.Marshal(live)
	if err != nil {
		t.Fatalf("marshal live: %v", err)
	}
	if err := json.Unmarshal(rawLive, &out); err != nil {
		t.Fatalf("unmarshal live: %v", err)
	}
	if out["worktree_missing"] != false {
		t.Fatalf("worktree_missing=%v, want false", out["worktree_missing"])
	}
}

// ClearTaskWorktree：清掉任务的 worktree 归属（路径 + root id），且幂等。
// 生产已无调用方（收尾走 store.ClearWorktreeRefs；repoint 那条路 2026-10-01 拆掉了），
// 本用例守住方法自身的语义。
func TestClearTaskWorktreeDetachesTask(t *testing.T) {
	ctx := context.Background()
	runner := &fakeRunner{}
	svc, root := newTestService(t, runner)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:         root.ID,
		Stages:         []StageTemplate{userStage("Describe"), agentStage("Fix", "Fix this:\n{previous_input}")},
		Input:          "broken save button",
		CreateWorktree: true,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	// 等**执行体收工**（Status==waiting_user），不能只等路径落库。
	//
	// 路径是 moveRelative 在 Next 里**同步**写的，所以「等到路径非空」几乎立刻成立；
	// 但 Next 之后还有一个**异步**的 executeTask 在跑。它开头的 loadForMove 若读在
	// 清归属**之后**，快照里路径已空，于是又走一次建树分支把路径**写回**去 —— 断言
	// 随即看到「清完路径又回来」。高并行负载（go test ./... 各包并行）拉宽这个窗口，
	// 就是那个 flaky；单独跑 / -race 跑不出来。
	//
	// 等 waiting_user 表示 agent 段已跑完、executeTask 已返回，不会再写归属。这也是
	// 本包「等 Next 的异步执行落地」的既有范式（见 parkOnWaitingAgentStage）。
	var before TaskDetail
	waitForCondition(t, func() bool {
		got, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		if err != nil {
			return false
		}
		before = got
		return strings.TrimSpace(got.Task.WorktreePath) != "" && got.Task.Status == StatusWaitingUser
	})
	if strings.TrimSpace(before.Task.WorktreePath) == "" {
		t.Fatalf("worktree path should be set before clearing")
	}

	if err := svc.ClearTaskWorktree(ctx, root.ID, detail.Task.ID); err != nil {
		t.Fatalf("ClearTaskWorktree: %v", err)
	}
	after, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask after clear: %v", err)
	}
	if after.Task.WorktreePath != "" || after.Task.WorktreeRootID != "" {
		t.Fatalf("worktree refs should be cleared, got path=%q rootID=%q",
			after.Task.WorktreePath, after.Task.WorktreeRootID)
	}
	// 幂等：再清一次不报错。
	if err := svc.ClearTaskWorktree(ctx, root.ID, detail.Task.ID); err != nil {
		t.Fatalf("ClearTaskWorktree should be idempotent, got %v", err)
	}
}

// 建树返回「成功」但产出不可用时，必须当失败，绝不能当成建过了。
//
// err == nil 不等于树建起来了。空路径一旦被当成成功写进库，两个不可逆的后果同时
// 发生：agent 拿到空的 RuntimeRootPath（见 service.go 取的就是 task.WorktreePath），
// 于是在**主 checkout** 里跑、改的是主干；界面那边看到「建过 + 路径为空」，把一棵
// 根本没建出来的活说成「已收尾」。
func TestBuildingAnUnusableWorktreeIsNotRecordedAsBuilt(t *testing.T) {
	runner := &fakeRunner{worktreeInfo: WorktreeInfo{RootID: "wt-root"}} // 成功，但没有 Path
	svc, root := newTestService(t, runner)
	ctx := context.Background()

	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:         root.ID,
		Stages:         []StageTemplate{userStage("Describe"), agentStage("Fix", "Fix:\n{previous_input}")},
		Input:          "something to fix",
		CreateWorktree: true,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	// Next 必须当场拒绝：建树没产出可用目录，推到 agent 段就会在主 checkout 里跑。
	_, err = svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID})
	if err == nil {
		t.Fatal("advancing into an agent stage must fail when the worktree creation produced nothing usable")
	}

	after := mustGetTask(t, svc, root.ID, detail.Task.ID)
	if strings.TrimSpace(after.WorktreePath) != "" {
		t.Fatalf("an unusable path must not be persisted as the task's worktree, got %q", after.WorktreePath)
	}
	if after.WorktreeBuilt {
		t.Fatal("a worktree that was never actually created must not be recorded as built — that flag drives the \"finished\" badge")
	}
	// 更要紧的：agent 绝不能因此在主 checkout 里跑起来。
	runner.mu.Lock()
	execs := append([]AgentStageExecution(nil), runner.execs...)
	runner.mu.Unlock()
	for _, exec := range execs {
		if strings.TrimSpace(exec.RuntimeRootPath) == "" && after.CreateWorktree {
			t.Fatalf("the agent ran with no worktree cwd (task=%s): it would edit the main checkout", exec.Task.ID)
		}
	}
}

// 路径指向文件而不是目录时同样不可用：cwd 必须是目录。
func TestBuildingAWorktreeThatIsAFileIsNotRecordedAsBuilt(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write decoy file: %v", err)
	}
	runner := &fakeRunner{worktreeInfo: WorktreeInfo{RootID: "wt-root", Path: file}}
	svc, root := newTestService(t, runner)
	ctx := context.Background()

	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:         root.ID,
		Stages:         []StageTemplate{userStage("Describe"), agentStage("Fix", "Fix:\n{previous_input}")},
		Input:          "something to fix",
		CreateWorktree: true,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err == nil {
		t.Fatal("a path that is not a directory must not be accepted as a worktree")
	}
	if !runner.worktreeCreateCalled {
		t.Fatal("the fixture must actually have reached worktree creation")
	}

	after := mustGetTask(t, svc, root.ID, detail.Task.ID)
	if after.WorktreeBuilt {
		t.Fatal("a path that is not a directory must not be recorded as a built worktree")
	}
}
