package kanban

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"mindfs/server/internal/fs"
)

func newRouterEventStore(t *testing.T) *RouterStore {
	t.Helper()
	root := fs.NewRootInfo("root", "root", t.TempDir())
	tasks, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	t.Cleanup(func() { _ = tasks.Close() })
	return NewRouterStore(tasks.db)
}

func baseRouterEvent(kind RouterEventKind, instanceKey string) RouterEvent {
	return RouterEvent{
		RootID:      "root-a",
		TaskID:      "task-1",
		RunID:       "run-1",
		Kind:        kind,
		InstanceKey: instanceKey,
		TurnID:      "turn-1",
		PayloadJSON: `{"summary":"ok"}`,
		Status:      RouterEventPending,
	}
}

func mustInsertRouterEvent(t *testing.T, store *RouterStore, ev RouterEvent) RouterEvent {
	t.Helper()
	out, inserted, err := store.InsertEvent(context.Background(), ev)
	if err != nil {
		t.Fatalf("InsertEvent(%s): %v", ev.Kind, err)
	}
	if !inserted {
		t.Fatalf("InsertEvent(%s) reported inserted=false, want true", ev.Kind)
	}
	return out
}

// 行为 1：同一 UniqueKey 插两次只留一行；第二次返回 inserted=false 且不覆盖已有行。
func TestRouterEventInsertDeduplicatesByUniqueKey(t *testing.T) {
	store := newRouterEventStore(t)
	ctx := context.Background()

	first := mustInsertRouterEvent(t, store, baseRouterEvent(RouterEventRunSettled, "run-1"))
	if first.ID == "" || !strings.HasPrefix(first.ID, "revt_") {
		t.Fatalf("generated id = %q, want revt_ prefix", first.ID)
	}
	if first.UniqueKey != RouterEventUniqueKey("task-1", "run-1", RouterEventRunSettled, "run-1") {
		t.Fatalf("unique key = %q", first.UniqueKey)
	}

	dup := baseRouterEvent(RouterEventRunSettled, "run-1")
	dup.ID = "revt-forced"
	dup.PayloadJSON = `{"summary":"second"}`
	dup.Status = RouterEventClaimed
	got, inserted, err := store.InsertEvent(ctx, dup)
	if err != nil {
		t.Fatalf("duplicate InsertEvent: %v", err)
	}
	if inserted {
		t.Fatal("duplicate unique key must not insert a second row")
	}
	if got.ID != first.ID {
		t.Fatalf("duplicate returned id %q, want existing %q", got.ID, first.ID)
	}
	if got.PayloadJSON != first.PayloadJSON || got.Status != RouterEventPending {
		t.Fatalf("existing row was overwritten: payload=%q status=%q", got.PayloadJSON, got.Status)
	}

	events, err := store.ListEventsByTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("ListEventsByTask: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("rows after duplicate insert = %d, want 1", len(events))
	}
}

// 行为 2：并发 ClaimEvent 同一行，恰好一个 true。
func TestRouterEventConcurrentClaimHasSingleWinner(t *testing.T) {
	store := newRouterEventStore(t)
	ctx := context.Background()
	ev := mustInsertRouterEvent(t, store, baseRouterEvent(RouterEventRunSettled, "run-1"))

	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]bool, 2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = store.ClaimEvent(ctx, ev.ID, fmt.Sprintf("claimer-%d", i))
		}(i)
	}
	close(start)
	wg.Wait()

	wins := 0
	for i, won := range results {
		if errs[i] != nil {
			t.Fatalf("claimer-%d error: %v", i, errs[i])
		}
		if won {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("claim winners = %d, want exactly 1 (%v)", wins, results)
	}
	again, err := store.ClaimEvent(ctx, ev.ID, "claimer-late")
	if err != nil {
		t.Fatalf("third claim: %v", err)
	}
	if again {
		t.Fatal("claim after claimed must return false")
	}
}

// 行为 3：关闭 TaskStore 后重开（新连接、再次启动迁移）事件仍在、状态不变；
// 迁移再跑一次不改历史行。
func TestRouterEventSurvivesReopenAndMigrationRerun(t *testing.T) {
	dir := t.TempDir()
	root := fs.NewRootInfo("root", "root", dir)
	ctx := context.Background()

	tasks, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	store := NewRouterStore(tasks.db)
	done := mustInsertRouterEvent(t, store, baseRouterEvent(RouterEventRunFailed, "run-1"))
	if _, err := store.ClaimEvent(ctx, done.ID, "worker-a"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := store.CompleteEvent(ctx, done.ID, RouterEventDone, ""); err != nil {
		t.Fatalf("complete done: %v", err)
	}
	mustInsertRouterEvent(t, store, baseRouterEvent(RouterEventGateWaiting, "node-1:1"))
	if err := tasks.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("reopen NewTaskStore: %v", err)
	}
	defer reopened.Close()
	store2 := NewRouterStore(reopened.db)
	events, err := store2.ListEventsByTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("ListEventsByTask after reopen: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events after reopen = %d, want 2", len(events))
	}
	byID := map[string]RouterEvent{}
	for _, ev := range events {
		byID[ev.ID] = ev
	}
	if byID[done.ID].Status != RouterEventDone || byID[done.ID].CompletedAt.IsZero() {
		t.Fatalf("done row after reopen = %+v", byID[done.ID])
	}

	if err := migrateRouterEventTable(reopened.db); err != nil {
		t.Fatalf("migration rerun: %v", err)
	}
	after, err := store2.ListEventsByTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("list after migration rerun: %v", err)
	}
	if len(after) != 2 || after[0].ID != events[0].ID || after[1].ID != events[1].ID {
		t.Fatalf("migration rerun changed history rows: %+v", after)
	}
	if byID[done.ID].PayloadJSON != `{"summary":"ok"}` {
		t.Fatalf("payload changed: %q", byID[done.ID].PayloadJSON)
	}
}

// 行为 4：failed 终态不再出现在 pending 列表，也不会被 RecoverClaimedEvents 复活。
func TestRouterEventFailedTerminalIsNotRevived(t *testing.T) {
	store := newRouterEventStore(t)
	ctx := context.Background()
	ev := mustInsertRouterEvent(t, store, baseRouterEvent(RouterEventRunFailed, "run-1"))
	if _, err := store.ClaimEvent(ctx, ev.ID, "worker-a"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := store.CompleteEvent(ctx, ev.ID, RouterEventFailed, "boom"); err != nil {
		t.Fatalf("complete failed: %v", err)
	}

	pending, err := store.ListPendingEvents(ctx, 10)
	if err != nil {
		t.Fatalf("ListPendingEvents: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("failed event still pending: %+v", pending)
	}
	recovered, err := store.RecoverClaimedEvents(ctx, "startup restart")
	if err != nil {
		t.Fatalf("RecoverClaimedEvents: %v", err)
	}
	if recovered != 0 {
		t.Fatalf("recovered = %d, want 0 (failed must stay terminal)", recovered)
	}
	events, err := store.ListEventsByTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("ListEventsByTask: %v", err)
	}
	if len(events) != 1 || events[0].Status != RouterEventFailed || events[0].LastError != "boom" {
		t.Fatalf("terminal row = %+v", events)
	}
}

// 行为 5：claim/effect/complete 分界——pending 行不能直接 complete，先 claim 再 done。
func TestRouterEventCompleteRequiresClaim(t *testing.T) {
	store := newRouterEventStore(t)
	ctx := context.Background()
	ev := mustInsertRouterEvent(t, store, baseRouterEvent(RouterEventRunSettled, "run-1"))

	if err := store.CompleteEvent(ctx, ev.ID, RouterEventDone, ""); err == nil {
		t.Fatal("complete on pending row must fail")
	}
	events, err := store.ListEventsByTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("ListEventsByTask: %v", err)
	}
	if events[0].Status != RouterEventPending {
		t.Fatalf("status after rejected complete = %q, want pending", events[0].Status)
	}

	if _, err := store.ClaimEvent(ctx, ev.ID, "worker-a"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := store.CompleteEvent(ctx, ev.ID, RouterEventPending, ""); err == nil {
		t.Fatal("completing to pending must fail: only done|failed|skipped are terminal")
	}
	if err := store.CompleteEvent(ctx, ev.ID, RouterEventDone, ""); err != nil {
		t.Fatalf("complete done after claim: %v", err)
	}
	events, err = store.ListEventsByTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("ListEventsByTask: %v", err)
	}
	if events[0].Status != RouterEventDone || events[0].CompletedAt.IsZero() {
		t.Fatalf("done row = %+v", events[0])
	}
	if err := store.CompleteEvent(ctx, ev.ID, RouterEventDone, ""); err == nil {
		t.Fatal("completing a terminal row must fail")
	}
}

// 行为 6：ListEventsByTask 只返回本 task；同 ID 不同 root 库互不串。
func TestRouterEventListByTaskScopesRows(t *testing.T) {
	storeA := newRouterEventStore(t)
	ctx := context.Background()
	sharedID := "revt-shared"
	evA := baseRouterEvent(RouterEventRunSettled, "run-1")
	evA.ID = sharedID
	evA.RootID = "root-a"
	evA.TaskID = "task-1"
	if _, _, err := storeA.InsertEvent(ctx, evA); err != nil {
		t.Fatalf("insert A: %v", err)
	}
	// 另一个 root（独立账户库）用同一个事件 ID，不得串到本库。
	rootB := fs.NewRootInfo("root", "root", t.TempDir())
	tasksB, err := NewTaskStore(rootB)
	if err != nil {
		t.Fatalf("NewTaskStore B: %v", err)
	}
	defer tasksB.Close()
	storeB := NewRouterStore(tasksB.db)
	evB := baseRouterEvent(RouterEventRunSettled, "run-1")
	evB.ID = sharedID
	evB.RootID = "root-b"
	evB.TaskID = "task-2"
	if _, _, err := storeB.InsertEvent(ctx, evB); err != nil {
		t.Fatalf("insert B: %v", err)
	}

	rowsA, err := storeA.ListEventsByTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("list A: %v", err)
	}
	if len(rowsA) != 1 || rowsA[0].RootID != "root-a" {
		t.Fatalf("root-a rows = %+v", rowsA)
	}
	rowsB, err := storeB.ListEventsByTask(ctx, "task-2")
	if err != nil {
		t.Fatalf("list B: %v", err)
	}
	if len(rowsB) != 1 || rowsB[0].RootID != "root-b" {
		t.Fatalf("root-b rows = %+v", rowsB)
	}
	leaked, err := storeA.ListEventsByTask(ctx, "task-2")
	if err != nil {
		t.Fatalf("list A task-2: %v", err)
	}
	if len(leaked) != 0 {
		t.Fatalf("root-a leaked task-2 rows: %+v", leaked)
	}
}

// 行为 7：唯一键包含 kind 与实例——同 task/run 下 settled/failed 各一行，
// 同 run 不同 gate attempt 各一行。
func TestRouterEventUniqueKeySeparatesKindAndInstance(t *testing.T) {
	store := newRouterEventStore(t)
	ctx := context.Background()

	settled := mustInsertRouterEvent(t, store, baseRouterEvent(RouterEventRunSettled, "run-1"))
	failed := mustInsertRouterEvent(t, store, baseRouterEvent(RouterEventRunFailed, "run-1"))

	gate1 := baseRouterEvent(RouterEventGateWaiting, "node-1:1")
	gate1.Attempt = 1
	gate1Ev := mustInsertRouterEvent(t, store, gate1)
	gate2 := baseRouterEvent(RouterEventGateWaiting, "node-1:2")
	gate2.Attempt = 2
	gate2Ev := mustInsertRouterEvent(t, store, gate2)

	if settled.UniqueKey == failed.UniqueKey {
		t.Fatalf("settled/failed share unique key %q", settled.UniqueKey)
	}
	if gate1Ev.UniqueKey == gate2Ev.UniqueKey {
		t.Fatalf("gate attempts share unique key %q", gate1Ev.UniqueKey)
	}
	if _, inserted, err := store.InsertEvent(ctx, gate1); err != nil || inserted {
		t.Fatalf("duplicate gate insert: inserted=%v err=%v", inserted, err)
	}
	events, err := store.ListEventsByTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("ListEventsByTask: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("rows = %d, want 4 (settled, failed, gate1, gate2)", len(events))
	}
}

// 行为 8：旧库（无 router_events）上迁移可重复执行，且不改历史行。
func TestRouterEventMigrationRepeatableOnOldDatabase(t *testing.T) {
	root := fs.NewRootInfo("root", "root", t.TempDir())
	tasks, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	defer tasks.Close()
	db := tasks.db

	var name string
	err = db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='router_events'`).Scan(&name)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("old database already has router_events (err=%v); migration must be the only creator", err)
	}
	if err := migrateRouterEventTable(db); err != nil {
		t.Fatalf("first migration: %v", err)
	}
	store := NewRouterStore(db)
	ev := mustInsertRouterEvent(t, store, baseRouterEvent(RouterEventStartupReconcile, "run-1"))
	if err := migrateRouterEventTable(db); err != nil {
		t.Fatalf("second migration: %v", err)
	}
	rows, err := store.ListEventsByTask(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("ListEventsByTask: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != ev.ID || rows[0].Status != RouterEventPending {
		t.Fatalf("history after second migration = %+v", rows)
	}
}

// 边界：PayloadJSON 超过 8KB 直接拒绝，不静默截断落库。
func TestRouterEventPayloadBoundRejected(t *testing.T) {
	store := newRouterEventStore(t)
	ctx := context.Background()
	ev := baseRouterEvent(RouterEventRunSettled, "run-1")
	ev.PayloadJSON = `{"summary":"` + strings.Repeat("x", RouterEventMaxPayloadBytes) + `"}`
	if _, _, err := store.InsertEvent(ctx, ev); err == nil {
		t.Fatal("oversize payload must be rejected")
	}
	rows, err := store.ListEventsByTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("ListEventsByTask: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("oversize payload was written: %+v", rows)
	}
}

// RecoverClaimedEvents 把上次进程遗留的 claimed 行恢复为 pending 并记录原因。
func TestRouterEventRecoverClaimedEvents(t *testing.T) {
	store := newRouterEventStore(t)
	ctx := context.Background()
	ev := mustInsertRouterEvent(t, store, baseRouterEvent(RouterEventGateWaiting, "node-1:1"))
	if _, err := store.ClaimEvent(ctx, ev.ID, "worker-crashed"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	recovered, err := store.RecoverClaimedEvents(ctx, "process restarted")
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("recovered = %d, want 1", recovered)
	}
	pending, err := store.ListPendingEvents(ctx, 10)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != ev.ID {
		t.Fatalf("pending after recover = %+v", pending)
	}
	if !strings.Contains(pending[0].LastError, "process restarted") ||
		!strings.Contains(pending[0].LastError, "worker-crashed") {
		t.Fatalf("recover reason lost claimer: %q", pending[0].LastError)
	}
}

// ListPendingEvents 按 created_at 稳定排序并遵守 limit。
func TestRouterEventListPendingStableOrder(t *testing.T) {
	store := newRouterEventStore(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	for _, offset := range []int{2, 0, 1} {
		ev := baseRouterEvent(RouterEventRunSettled, fmt.Sprintf("run-%d", offset))
		ev.RunID = fmt.Sprintf("run-%d", offset)
		ev.CreatedAt = base.Add(time.Duration(offset) * time.Second)
		mustInsertRouterEvent(t, store, ev)
	}
	pending, err := store.ListPendingEvents(ctx, 10)
	if err != nil {
		t.Fatalf("ListPendingEvents: %v", err)
	}
	want := []string{"run-0", "run-1", "run-2"}
	if len(pending) != len(want) {
		t.Fatalf("pending rows = %d, want %d", len(pending), len(want))
	}
	for i, runID := range want {
		if pending[i].RunID != runID {
			t.Fatalf("pending[%d] = %q, want %q", i, pending[i].RunID, runID)
		}
	}
	limited, err := store.ListPendingEvents(ctx, 2)
	if err != nil {
		t.Fatalf("limited list: %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("limit 2 returned %d rows", len(limited))
	}
}
