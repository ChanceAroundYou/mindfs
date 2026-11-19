package kanban

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"mindfs/server/internal/fs"
)

// 看板归档（#86）的行为钉子。用户裁定 A：归档只打标记、不删任何后代会话；
// 自动归档只碰「超过 7 天的终态任务」，起算点 completed_at，空则回退 updated_at。

func newArchiveStore(t *testing.T) *TaskStore {
	t.Helper()
	store, err := NewTaskStore(fs.NewRootInfo("root", "root", t.TempDir()))
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func seedArchiveTask(t *testing.T, store *TaskStore, id, status string, updatedAt time.Time, completedAt string) Task {
	t.Helper()
	task, err := store.CreateTask(context.Background(),
		Task{ID: id, RootID: "root", Status: status, CreatedAt: updatedAt, UpdatedAt: updatedAt, CompletedAt: completedAt},
		TaskEvent{ID: "event-" + id, TaskID: id, Type: "created", CreatedAt: updatedAt})
	if err != nil {
		t.Fatalf("CreateTask(%s): %v", id, err)
	}
	return task
}

func mustTask(t *testing.T, store *TaskStore, id string) Task {
	t.Helper()
	task, err := store.GetTask(context.Background(), id)
	if err != nil {
		t.Fatalf("GetTask(%s): %v", id, err)
	}
	return task
}

func TestSetTaskArchivedRejectsNonTerminalTask(t *testing.T) {
	store := newArchiveStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	for _, status := range []string{StatusPending, StatusRunning, StatusWaitingUser, StatusPaused} {
		seedArchiveTask(t, store, "task-"+status, status, now, "")
		if _, err := store.SetTaskArchived(ctx, "task-"+status, true); !errors.Is(err, ErrTaskNotArchivable) {
			t.Fatalf("status %q archive err = %v, want ErrTaskNotArchivable (a running task must never disappear from the board)", status, err)
		}
		if got := mustTask(t, store, "task-"+status).ArchivedAt; got != "" {
			t.Fatalf("status %q archived_at = %q after rejected archive, want empty", status, got)
		}
	}
}

func TestSetTaskArchivedIsIdempotentAndKeepsUpdatedAt(t *testing.T) {
	store := newArchiveStore(t)
	ctx := context.Background()
	seeded := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	seedArchiveTask(t, store, "task-1", StatusSuccess, seeded, seeded.Format(time.RFC3339Nano))

	first := time.Date(2026, 11, 19, 1, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return first }
	archived, err := store.SetTaskArchived(ctx, "task-1", true)
	if err != nil {
		t.Fatalf("SetTaskArchived: %v", err)
	}
	if archived.ArchivedAt != first.Format(time.RFC3339Nano) {
		t.Fatalf("archived_at = %q, want %q", archived.ArchivedAt, first.Format(time.RFC3339Nano))
	}

	// 再归档一次：保留首次时间，不重写。
	store.now = func() time.Time { return first.Add(48 * time.Hour) }
	again, err := store.SetTaskArchived(ctx, "task-1", true)
	if err != nil {
		t.Fatalf("re-archive: %v", err)
	}
	if again.ArchivedAt != archived.ArchivedAt {
		t.Fatalf("re-archive moved stamp %q -> %q, want idempotent", archived.ArchivedAt, again.ArchivedAt)
	}

	// updated_at 不能被归档推动：它既是看板排序键，也是 7 天自动归档的兜底起算点。
	stored := mustTask(t, store, "task-1")
	if !stored.UpdatedAt.Equal(seeded) {
		t.Fatalf("updated_at = %v after archive, want unchanged %v", stored.UpdatedAt, seeded)
	}

	if _, err := store.SetTaskArchived(ctx, "task-1", false); err != nil {
		t.Fatalf("unarchive: %v", err)
	}
	if got := mustTask(t, store, "task-1").ArchivedAt; got != "" {
		t.Fatalf("archived_at = %q after unarchive, want empty", got)
	}
	// 未归档再取消归档是空操作，不报错。
	if _, err := store.SetTaskArchived(ctx, "task-1", false); err != nil {
		t.Fatalf("no-op unarchive: %v", err)
	}
}

func TestListTasksDueForArchiveUsesCompletedAtFallsBackToUpdatedAt(t *testing.T) {
	store := newArchiveStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	old := now.Add(-8 * 24 * time.Hour)
	fresh := now.Add(-2 * 24 * time.Hour)

	seedArchiveTask(t, store, "due-by-completed", StatusSuccess, now.Add(-time.Hour), old.Format(time.RFC3339Nano))
	seedArchiveTask(t, store, "due-by-updated", StatusCancelled, old, "")
	seedArchiveTask(t, store, "fresh", StatusSuccess, now.Add(-time.Hour), fresh.Format(time.RFC3339Nano))
	seedArchiveTask(t, store, "active", StatusWaitingUser, now.Add(-30*24*time.Hour), "")
	// 已归档的不再返回，否则每次扫描都会重复级联。
	seedArchiveTask(t, store, "already", StatusFail, old, old.Format(time.RFC3339Nano))
	if _, err := store.SetTaskArchived(ctx, "already", true); err != nil {
		t.Fatalf("pre-archive: %v", err)
	}

	due, err := store.ListTasksDueForArchive(ctx, now.Add(-TaskArchiveAfter), 50)
	if err != nil {
		t.Fatalf("ListTasksDueForArchive: %v", err)
	}
	ids := make([]string, 0, len(due))
	for _, task := range due {
		ids = append(ids, task.ID)
	}
	sort.Strings(ids)
	if len(ids) != 2 || ids[0] != "due-by-completed" || ids[1] != "due-by-updated" {
		t.Fatalf("due = %v, want [due-by-completed due-by-updated]", ids)
	}
}

func TestTaskRelatedSessionKeysCollectsAndDedupes(t *testing.T) {
	store := newArchiveStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	seedArchiveTask(t, store, "task-1", StatusSuccess, now, now.Format(time.RFC3339Nano))
	if _, err := store.db.ExecContext(ctx, `UPDATE tasks SET main_session_key = ? WHERE id = ?`, "s-main", "task-1"); err != nil {
		t.Fatalf("set main session: %v", err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := store.db.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("seed %q: %v", query, err)
		}
	}
	stamp := now.Format(time.RFC3339Nano)
	exec(`INSERT INTO workflow_runs (id, task_id, graph_json, created_at, updated_at) VALUES (?, ?, '{}', ?, ?)`, "run-1", "task-1", stamp, stamp)
	exec(`INSERT INTO workflow_node_runs (id, run_id, node_id, session_key, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"nr-1", "run-1", "agent-1", "s-node", NodeRunSuccess, stamp, stamp)
	exec(`INSERT INTO task_activities (id, task_id, session_key, created_at) VALUES (?, ?, ?, ?)`, "act-1", "task-1", "s-act", stamp)
	// 同一个 key 出现在两处只能算一次。
	exec(`INSERT INTO task_activities (id, task_id, session_key, created_at) VALUES (?, ?, ?, ?)`, "act-2", "task-1", "s-main", stamp)
	exec(`INSERT INTO router_sessions (scope, owner_id, root_id, session_key, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		string(RouterScopeTask), "task-1", "root", "s-router", "idle", stamp, stamp)

	keys, err := store.TaskRelatedSessionKeys(ctx, "task-1")
	if err != nil {
		t.Fatalf("TaskRelatedSessionKeys: %v", err)
	}
	sort.Strings(keys)
	want := []string{"s-act", "s-main", "s-node", "s-router"}
	if len(keys) != len(want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("keys = %v, want %v", keys, want)
		}
	}
	// 汇总只是读：会话行、JSONL 都不该被这次调用动到（级联由上层只调 SetArchived）。
	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM router_sessions`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("router_sessions rows = %d err=%v, want 1 (collecting keys must not delete anything)", count, err)
	}
}

// 归档的任务不在工作台显示（#86 第 5 条）：过滤必须在 SQL 层，否则已归档的行
// 会先占掉 Limit 名额、把还在流转的任务挤出工作台。
func TestOverviewExcludesArchivedTasks(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t, nil)
	store, err := svc.taskStore("root")
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	now := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	seedArchiveTask(t, store, "task-live", StatusSuccess, now, now.Format(time.RFC3339Nano))
	seedArchiveTask(t, store, "task-hidden", StatusSuccess, now, now.Format(time.RFC3339Nano))
	if _, err := store.SetTaskArchived(ctx, "task-hidden", true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	items, err := svc.Overview(ctx)
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.Task.ID)
	}
	sort.Strings(ids)
	if len(ids) != 1 || ids[0] != "task-live" {
		t.Fatalf("overview ids = %v, want [task-live] (archived tasks must not show on the workbench)", ids)
	}
}

type recordingSessionArchiver struct {
	calls   [][]string
	taskIDs []string
}

func (r *recordingSessionArchiver) ArchiveSessions(_ context.Context, _ string, taskID string, keys []string) error {
	r.calls = append(r.calls, append([]string(nil), keys...))
	r.taskIDs = append(r.taskIDs, taskID)
	return nil
}

func TestArchiveTaskCascadesMarksAndUnarchiveLeavesSessionsAlone(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	now := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	seedArchiveTask(t, store, "task-1", StatusSuccess, now, now.Format(time.RFC3339Nano))
	if _, err := store.db.ExecContext(ctx, `UPDATE tasks SET main_session_key = ? WHERE id = ?`, "s-main", "task-1"); err != nil {
		t.Fatalf("set main session: %v", err)
	}
	archiver := &recordingSessionArchiver{}
	svc.SetSessionArchiver(archiver)

	detail, err := svc.ArchiveTask(ctx, ArchiveTaskInput{RootID: root.ID, TaskID: "task-1", Archived: true})
	if err != nil {
		t.Fatalf("ArchiveTask: %v", err)
	}
	if detail.Task.ArchivedAt == "" {
		t.Fatal("archived_at empty after ArchiveTask")
	}
	if len(archiver.calls) != 1 || len(archiver.calls[0]) != 1 || archiver.calls[0][0] != "s-main" {
		t.Fatalf("cascade calls = %v, want one call with [s-main]", archiver.calls)
	}
	if len(archiver.taskIDs) != 1 || archiver.taskIDs[0] != "task-1" {
		t.Fatalf("cascade taskIDs = %v, want [task-1] (archiver needs taskID to find all owned sessions)", archiver.taskIDs)
	}

	// 取消归档只清任务自己的标记：会话可能是用户自己归档的，不能被反向解开。
	if _, err := svc.ArchiveTask(ctx, ArchiveTaskInput{RootID: root.ID, TaskID: "task-1", Archived: false}); err != nil {
		t.Fatalf("unarchive: %v", err)
	}
	if got := mustTask(t, store, "task-1").ArchivedAt; got != "" {
		t.Fatalf("archived_at = %q after unarchive, want empty", got)
	}
	if len(archiver.calls) != 1 {
		t.Fatalf("unarchive touched sessions (%d cascade calls), want none", len(archiver.calls))
	}
}

func TestSweepAutoArchivedTasksOnlyTouchesExpiredTerminalTasks(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	now := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	old := now.Add(-8 * 24 * time.Hour)
	seedArchiveTask(t, store, "old-success", StatusSuccess, now, old.Format(time.RFC3339Nano))
	if _, err := store.db.ExecContext(ctx, `UPDATE tasks SET main_session_key = ? WHERE id = ?`, "s-old", "old-success"); err != nil {
		t.Fatalf("set main session: %v", err)
	}
	seedArchiveTask(t, store, "fresh-success", StatusSuccess, now, now.Add(-24*time.Hour).Format(time.RFC3339Nano))
	seedArchiveTask(t, store, "old-running", StatusRunning, old, "")
	archiver := &recordingSessionArchiver{}
	svc.SetSessionArchiver(archiver)

	if n := svc.SweepAutoArchivedTasks(ctx); n != 1 {
		t.Fatalf("sweep archived %d, want 1", n)
	}
	if got := mustTask(t, store, "old-success").ArchivedAt; got == "" {
		t.Fatal("old terminal task not archived (sweep must really write archived_at)")
	}
	if got := mustTask(t, store, "fresh-success").ArchivedAt; got != "" {
		t.Fatalf("fresh task archived_at = %q, want empty", got)
	}
	if got := mustTask(t, store, "old-running").ArchivedAt; got != "" {
		t.Fatalf("running task archived_at = %q, want empty", got)
	}
	if len(archiver.calls) != 1 || len(archiver.calls[0]) != 1 || archiver.calls[0][0] != "s-old" {
		t.Fatalf("auto-archive cascade calls = %v, want one call with [s-old]", archiver.calls)
	}
	// 再扫一次不重复归档（幂等），否则每小时都会白写一遍。
	if n := svc.SweepAutoArchivedTasks(ctx); n != 0 {
		t.Fatalf("second sweep archived %d, want 0", n)
	}
}

// 归档标记只属于终态：重开/恢复/新的执行轮把任务拉回活动列时，标记必须一起清掉，
// 否则卡片会「明明在跑却躺在归档组里」，而归档组按语义是只读证据。
func TestUpdateTaskStatusClearsArchiveWhenTaskReturnsToActive(t *testing.T) {
	store := newArchiveStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	seedArchiveTask(t, store, "task-1", StatusSuccess, now, now.Format(time.RFC3339Nano))
	if _, err := store.SetTaskArchived(ctx, "task-1", true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if err := store.UpdateTaskStatus(ctx, "task-1", StatusWaitingUser, false); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}
	if got := mustTask(t, store, "task-1").ArchivedAt; got != "" {
		t.Fatalf("archived_at = %q after returning to waiting_user, want empty", got)
	}

	// ReopenTask 走自己的 SQL（不经 UpdateTaskStatus），同样要清。
	seedArchiveTask(t, store, "task-2", StatusSuccess, now, now.Format(time.RFC3339Nano))
	if _, err := store.SetTaskArchived(ctx, "task-2", true); err != nil {
		t.Fatalf("archive task-2: %v", err)
	}
	if err := store.ReopenTask(ctx, "task-2"); err != nil {
		t.Fatalf("ReopenTask: %v", err)
	}
	if got := mustTask(t, store, "task-2").ArchivedAt; got != "" {
		t.Fatalf("archived_at = %q after reopen, want empty", got)
	}
}

func archiveDismissedAt(t *testing.T, store *TaskStore, id string) string {
	t.Helper()
	var got string
	if err := store.db.QueryRow(`SELECT archive_dismissed_at FROM tasks WHERE id = ?`, id).Scan(&got); err != nil {
		t.Fatalf("read archive_dismissed_at(%s): %v", id, err)
	}
	return got
}

// 「取消归档后别再自动收我」的留痕要有完整的生命周期：取消归档打戳、显式归档
// 清空、任务回到活动列清空。只写不清会变成一个永久豁免 —— 用户重开、再完成后
// 这张卡就再也不会被自动归档了，和没做这个功能一样。
//
// 「打完戳后扫描不再收它」由 task_archive_dismissal_test.go 钉住行为；这里钉住
// 标记本身的写入与清理。
func TestArchiveDismissalLifetime(t *testing.T) {
	store := newArchiveStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	old := now.Add(-8 * 24 * time.Hour)

	seedArchiveTask(t, store, "task-1", StatusSuccess, old, old.Format(time.RFC3339Nano))
	if _, err := store.SetTaskArchived(ctx, "task-1", true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if got := archiveDismissedAt(t, store, "task-1"); got != "" {
		t.Fatalf("dismissal = %q right after archive, want empty", got)
	}
	if _, err := store.SetTaskArchived(ctx, "task-1", false); err != nil {
		t.Fatalf("unarchive: %v", err)
	}
	if got := archiveDismissedAt(t, store, "task-1"); got == "" {
		t.Fatal("manual unarchive must leave a dismissal stamp, otherwise the next sweep re-archives it")
	}
	// 显式重新归档：用户重新做了决定，留痕清空。
	if _, err := store.SetTaskArchived(ctx, "task-1", true); err != nil {
		t.Fatalf("re-archive: %v", err)
	}
	if got := archiveDismissedAt(t, store, "task-1"); got != "" {
		t.Fatalf("dismissal = %q after explicit re-archive, want empty", got)
	}
	// 回到非终态：旧一轮的取舍失效，自动归档重新按新的完成时间生效。
	if _, err := store.SetTaskArchived(ctx, "task-1", false); err != nil {
		t.Fatalf("unarchive again: %v", err)
	}
	if err := store.UpdateTaskStatus(ctx, "task-1", StatusWaitingUser, false); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}
	if got := archiveDismissedAt(t, store, "task-1"); got != "" {
		t.Fatalf("dismissal = %q after returning to waiting_user, want empty", got)
	}

	// ReopenTask 走自己的 SQL，同样要清。
	seedArchiveTask(t, store, "task-2", StatusSuccess, old, old.Format(time.RFC3339Nano))
	if _, err := store.SetTaskArchived(ctx, "task-2", true); err != nil {
		t.Fatalf("archive task-2: %v", err)
	}
	if _, err := store.SetTaskArchived(ctx, "task-2", false); err != nil {
		t.Fatalf("unarchive task-2: %v", err)
	}
	if err := store.ReopenTask(ctx, "task-2"); err != nil {
		t.Fatalf("ReopenTask: %v", err)
	}
	if got := archiveDismissedAt(t, store, "task-2"); got != "" {
		t.Fatalf("dismissal = %q after reopen, want empty", got)
	}
}
