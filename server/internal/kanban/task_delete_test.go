package kanban

import (
	"context"
	"strings"
	"testing"
	"time"

	rootfs "mindfs/server/internal/fs"
)

// 守「任务卡片可以被真正删掉」这条定制（G-AQ 的一部分，2026-10-06 用户要求）。
//
// 症状：看板上的终态卡片**只增不减**。取消改的是状态、卡片还留在终态列里，
// 用户翻历史翻到满屏也没法清掉任何一张。所以补了删除键 —— 但它删的**只是卡片**：
// worktree、分支、会话都不动（那是「收尾」的职责，不是「删除」的）。
//
// 这里钉三件事，缺一件这套语义就不成立：
//  1. 卡片和它的从属行（stage_runs / task_events）一起走 —— 留孤儿行会让
//     LatestStageRun 之类查询拿到一张已不存在的任务的记录。
//  2. 同库的其它任务不受影响（DELETE 不能写成「清空整表」）。
//  3. 正在跑的任务拒绝删除（活还在动，删了卡片就没人能停它了）。

func TestTaskStoreDeleteTaskRemovesCardAndItsRows(t *testing.T) {
	ctx := context.Background()
	store, err := NewTaskStore(rootfs.NewRootInfo("test", "test", t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()

	now := time.Now().UTC()
	seeded, err := store.CreateTask(ctx, Task{
		ID:        "keep-me-company",
		RootID:    "test",
		Status:    StatusWaitingUser,
		CreatedAt: now,
		UpdatedAt: now,
	}, StageRun{}, TaskEvent{})
	if err != nil {
		t.Fatal(err)
	}
	doomed, err := store.CreateTask(ctx, Task{
		ID:        "doomed",
		RootID:    "test",
		Status:    StatusWaitingUser,
		CreatedAt: now,
		UpdatedAt: now,
	}, StageRun{ID: "run-doomed-0", TaskID: "doomed", StageIndex: 0, Status: StageStatusSuccess}, TaskEvent{ID: "evt-doomed-1", TaskID: "doomed", Type: "created"})
	if err != nil {
		t.Fatal(err)
	}
	// 前置条件：从属行真的落进去了，否则下面的「一起走」是空断言。
	if _, err := store.LatestStageRun(ctx, doomed.ID, 0); err != nil {
		t.Fatalf("前置条件不成立：stage_runs 没播进去：%v", err)
	}

	if err := store.DeleteTask(ctx, doomed.ID); err != nil {
		t.Fatalf("DeleteTask 报错：%v", err)
	}
	if _, err := store.GetTask(ctx, doomed.ID); err == nil {
		t.Fatal("任务卡片还在 —— DeleteTask 必须把 tasks 行删掉")
	}
	var runs, events int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stage_runs WHERE task_id = ?`, doomed.ID).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_events WHERE task_id = ?`, doomed.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if runs != 0 || events != 0 {
		t.Fatalf("从属行成了孤儿：stage_runs=%d task_events=%d，都该是 0", runs, events)
	}
	// 同库的邻居必须原样在。
	if _, err := store.GetTask(ctx, seeded.ID); err != nil {
		t.Fatalf("删一张卡把邻居也删了：%v", err)
	}
}

func TestTaskStoreDeleteTaskRejectsUnknownID(t *testing.T) {
	ctx := context.Background()
	store, err := NewTaskStore(rootfs.NewRootInfo("test", "test", t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()

	// 空 id / 不存在的 id 都必须报错，不能静默成功 —— 静默成功会让 API 回 200，
	// 前端以为删掉了，刷新之后卡片还在。
	if err := store.DeleteTask(ctx, "  "); err == nil {
		t.Fatal("空 id 必须报错")
	}
	if err := store.DeleteTask(ctx, "no-such-task"); err == nil {
		t.Fatal("不存在的 id 必须报错")
	}
}

func TestServiceDeleteTaskRemovesOnlyTheCard(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)

	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Input:  "删我",
		Stages: []StageTemplate{userStage("起"), agentStage("干活", "干")},
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 摆上 worktree 与主会话，验「只删卡片」不是空话：这两个字段随卡片一起消失，
	// 但服务端不去动磁盘上的目录 / 分支 / 会话（那要真建树，属于收尾的测试面）。
	task, err := store.GetTask(ctx, detail.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	task.CreateWorktree = true
	task.WorktreePath = "/tmp/does-not-matter/.worktree/t1"
	task.MainSessionKey = "sess-1"
	if err := store.UpdateTask(ctx, task); err != nil {
		t.Fatal(err)
	}

	if err := svc.DeleteTask(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("DeleteTask 报错：%v", err)
	}
	if _, err := store.GetTask(ctx, detail.Task.ID); err == nil {
		t.Fatal("卡片还在 —— 删除没生效")
	}
	// 任务库本身还得能用（删完再建一张）。
	if _, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Input:  "下一张",
		Stages: []StageTemplate{userStage("起"), agentStage("干活", "干")},
	}); err != nil {
		t.Fatalf("删完之后建不出新任务：%v", err)
	}
}

func TestServiceDeleteTaskRefusesWhileRunning(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)

	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Input:  "别删我",
		Stages: []StageTemplate{userStage("起"), agentStage("干活", "干")},
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatal(err)
	}

	// ① 状态 running：活还在动。
	task, err := store.GetTask(ctx, detail.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	task.Status = StatusRunning
	if err := store.UpdateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteTask(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err == nil {
		t.Fatal("running 的任务必须拒绝删除 —— 删了卡片就没人能停它了")
	}

	// ② 已排进执行队列、还没落 running 的那个窗口：taskRun 标记说了算。
	task.Status = StatusWaitingUser
	if err := store.UpdateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	key := strings.TrimSpace(root.ID) + "\x00" + strings.TrimSpace(detail.Task.ID)
	svc.mu.Lock()
	svc.taskRun[key] = true
	svc.mu.Unlock()
	err = svc.DeleteTask(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID})
	if err == nil {
		t.Fatal("已排进执行队列的任务必须拒绝删除")
	}
	if !strings.Contains(err.Error(), "执行") {
		t.Fatalf("拒绝的理由该说清是「正在执行」：%v", err)
	}
	// 摘掉标记之后必须能删 —— 否则这个拒绝就成了永久锁。
	svc.mu.Lock()
	delete(svc.taskRun, key)
	svc.mu.Unlock()
	if err := svc.DeleteTask(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("执行标记摘掉之后应当能删：%v", err)
	}
}
