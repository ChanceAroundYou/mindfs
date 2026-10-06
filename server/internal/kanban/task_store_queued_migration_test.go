package kanban

import (
	"context"
	"testing"
	"time"

	rootfs "mindfs/server/internal/fs"
)

// 守「queued 状态已退役」这条定制（G-Y 看板重做的一部分）。
//
// 这是前端能安全删掉 queued 分支的**前提**：migrate() 在 store 打开时无条件把存量
// queued 归入 pending，所以本二进制服务的节点不可能再给出 queued。
// 一旦这条迁移被删掉或加了条件（比如「只在新库上跑」），老项目里的 queued 行就会
// 原样送往前端 —— 那时前端已按「不可能出现」把分支删了，卡片上那个状态就没有读数。
//
// 所以这里直接播一条 queued 行，再跑一次 migrate，断言它变成 pending。
//
// 注意 "queued" 是**字面量**而不是常量：types.go 里已经没有 StatusQueued 了
// （这正是退役的证据）。用字面量是刻意的 —— 模拟的是旧版本写进库里的历史值，
// 不是当前词表里的东西。
func TestMigrateFoldsQueuedIntoPending(t *testing.T) {
	ctx := context.Background()
	store, err := NewTaskStore(rootfs.NewRootInfo("test", "test", t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()

	now := time.Now().UTC()
	seeded, err := store.CreateTask(ctx, Task{
		ID:        "legacy-queued",
		RootID:    "test",
		Status:    "queued", // 历史值，不在当前词表里
		CreatedAt: now,
		UpdatedAt: now,
	}, StageRun{}, TaskEvent{})
	if err != nil {
		t.Fatal(err)
	}

	// 确认播进去的确实是 queued（否则下面的断言会因为「本来就是 pending」而假绿）
	if seeded.Status != "queued" {
		t.Fatalf("前置条件不成立：播入的 status = %q，期望 queued", seeded.Status)
	}

	if err := store.migrate(); err != nil {
		t.Fatal(err)
	}

	task, err := store.GetTask(ctx, seeded.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != StatusPending {
		t.Fatalf("migrate 后 status = %q，期望 %q —— queued 必须被归入 pending，不能原样送往前端",
			task.Status, StatusPending)
	}
}
