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
		Input:          "broken save button",
		CreateWorktree: true,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	before, err := svc.ensureTaskWorktree(ctx, store, detail.Task)
	if err != nil {
		t.Fatalf("ensureTaskWorktree: %v", err)
	}
	if strings.TrimSpace(before.WorktreePath) == "" {
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

	// 勾了 worktree 就当场建树：建不出来必须整张卡失败，而不是先落库再补。
	_, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:         root.ID,
		Input:          "something to fix",
		CreateWorktree: true,
	})
	if err == nil {
		t.Fatal("worktree creation with no usable path must fail the create")
	}
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	if tasks, lerr := store.ListTasks(ctx, ListTasksOptions{Limit: 10}); lerr != nil {
		t.Fatalf("ListTasks: %v", lerr)
	} else if len(tasks) != 0 {
		t.Fatalf("failed worktree creation left %d task(s) behind", len(tasks))
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

	if _, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:         root.ID,
		Input:          "something to fix",
		CreateWorktree: true,
	}); err == nil {
		t.Fatal("a path that is not a directory must not be accepted as a worktree")
	}
	if !runner.worktreeCreateCalled {
		t.Fatal("the fixture must actually have reached worktree creation")
	}
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	if tasks, lerr := store.ListTasks(ctx, ListTasksOptions{Limit: 10}); lerr != nil {
		t.Fatalf("ListTasks: %v", lerr)
	} else if len(tasks) != 0 {
		t.Fatalf("failed worktree creation left %d task(s) behind", len(tasks))
	}
}
