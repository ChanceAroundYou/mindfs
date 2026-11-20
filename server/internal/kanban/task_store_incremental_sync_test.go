package kanban

import (
	"context"
	"testing"
	"time"
)

// 增量同步必须能看见「归档状态」的变化。
//
// 症状（丢了会怎样）：自动归档刻意不推动 updated_at（它既是看板排序键，也是 7 天
// 自动归档的兜底起算点），而看板的增量拉取只按 updated_at > after 过滤 —— 服务端
// 归档之后，这些行再也不会出现在任何响应里。客户端缓存（IndexedDB）里那张卡于是
// 永远停在「完成」列，用户看到的就是「陈旧任务并没有被自动归档」。
//
// 修法：归档状态有自己的一条水位（archived_at > archived_after），与任务自身的
// 更新水位分开算 —— 混成一个 max 水位会让「先归档、后有别处更新」的行漏掉。
func TestListTasksArchivedAfterSeesArchiveChanges(t *testing.T) {
	store := newArchiveStore(t)
	ctx := context.Background()
	seeded := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	seedArchiveTask(t, store, "task-archived", StatusSuccess, seeded, seeded.Format(time.RFC3339Nano))
	seedArchiveTask(t, store, "task-quiet", StatusSuccess, seeded, seeded.Format(time.RFC3339Nano))

	store.now = func() time.Time { return time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC) }
	if _, err := store.SetTaskArchived(ctx, "task-archived", true); err != nil {
		t.Fatalf("SetTaskArchived: %v", err)
	}

	watermark := seeded.Format(time.RFC3339Nano)
	rows, err := store.ListTasks(ctx, ListTasksOptions{ArchivedAfter: watermark})
	if err != nil {
		t.Fatalf("ListTasks(archived_after): %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "task-archived" {
		ids := make([]string, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		t.Fatalf("rows = %v, want [task-archived] (archive state changes must reach incremental clients)", ids)
	}
	if rows[0].ArchivedAt == "" {
		t.Fatal("incremental row lost archived_at: the client would still render it as live")
	}

	// 客户端实际发出的就是两条水位一起（它既有任务更新水位、也有归档水位）。
	// 服务端必须取 OR：AND 起来几乎永远为空，等于什么都没同步到。
	combined, err := store.ListTasks(ctx, ListTasksOptions{After: watermark, ArchivedAfter: watermark})
	if err != nil {
		t.Fatalf("ListTasks(after+archived_after): %v", err)
	}
	if len(combined) != 1 || combined[0].ID != "task-archived" {
		t.Fatalf("combined rows = %v, want [task-archived] (two watermarks must be OR-ed)", combined)
	}

	// 水位推到归档之后：同一行不能再回来，否则每次增量拉取都会重发全部归档行。
	later, err := store.ListTasks(ctx, ListTasksOptions{ArchivedAfter: "2026-11-20T00:00:00Z"})
	if err != nil {
		t.Fatalf("ListTasks(archived_after later): %v", err)
	}
	if len(later) != 0 {
		t.Fatalf("rows = %v after the archive watermark moved past it, want none", later)
	}

	// 未归档的行 archived_at 为空串，不能因为字符串比较被卷进来。
	if got := mustTask(t, store, "task-quiet").ArchivedAt; got != "" {
		t.Fatalf("task-quiet archived_at = %q, want empty", got)
	}
}
