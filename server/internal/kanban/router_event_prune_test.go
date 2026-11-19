package kanban

import (
	"context"
	"testing"
	"time"
)

// 账本保留策略（R1 留存缺口）：router_events 只增不删。每条 Run 结算/失败/gate/
// 停摆/启动对账都落一行，长期运行的节点上这张表会一直长，而没有任何东西回收它。
//
// 判据必须窄：只删「已终态且够老」的行。pending/claimed 是**还没办完的事** ——
// 再老也得留着；终态但新鲜的还要给诊断看现场。

func insertTerminalEvent(t *testing.T, store *RouterStore, kind RouterEventKind, instanceKey, status string, age time.Duration, claimer string) RouterEvent {
	t.Helper()
	ctx := context.Background()
	ev := mustInsertRouterEvent(t, store, baseRouterEvent(kind, instanceKey))
	if _, err := store.ClaimEvent(ctx, ev.ID, claimer); err != nil {
		t.Fatalf("claim %s: %v", instanceKey, err)
	}
	if err := store.CompleteEvent(ctx, ev.ID, status, ""); err != nil {
		t.Fatalf("complete %s: %v", instanceKey, err)
	}
	stamp := time.Now().UTC().Add(-age).Format(routerTimeLayout)
	if _, err := store.db.ExecContext(ctx,
		`UPDATE router_events SET created_at = ?, completed_at = ? WHERE id = ?`, stamp, stamp, ev.ID); err != nil {
		t.Fatalf("backdate %s: %v", instanceKey, err)
	}
	return ev
}

func backdatePendingEvent(t *testing.T, store *RouterStore, ev RouterEvent, age time.Duration) {
	t.Helper()
	stamp := time.Now().UTC().Add(-age).Format(routerTimeLayout)
	if _, err := store.db.ExecContext(context.Background(),
		`UPDATE router_events SET created_at = ? WHERE id = ?`, stamp, ev.ID); err != nil {
		t.Fatalf("backdate pending: %v", err)
	}
}

// 行为 1：过期的终态行被删，新鲜终态/过期未办完的行一条不动。
func TestRouterEventPruneRemovesOnlyOldTerminalRows(t *testing.T) {
	store := newRouterEventStore(t)
	ctx := context.Background()
	oldDone := insertTerminalEvent(t, store, RouterEventRunSettled, "old-done", RouterEventDone, 30*24*time.Hour, "worker-a")
	oldFailed := insertTerminalEvent(t, store, RouterEventRunFailed, "old-failed", RouterEventFailed, 30*24*time.Hour, "worker-a")
	oldSkipped := insertTerminalEvent(t, store, RouterEventGateWaiting, "old-skipped", RouterEventSkipped, 30*24*time.Hour, "worker-a")
	freshDone := insertTerminalEvent(t, store, RouterEventRunSettled, "fresh-done", RouterEventDone, time.Hour, "worker-a")
	stalePending := mustInsertRouterEvent(t, store, baseRouterEvent(RouterEventRunStalled, "stale-pending"))
	backdatePendingEvent(t, store, stalePending, 30*24*time.Hour)
	staleClaimed := mustInsertRouterEvent(t, store, baseRouterEvent(RouterEventRunStalled, "stale-claimed"))
	backdatePendingEvent(t, store, staleClaimed, 30*24*time.Hour)
	if _, err := store.ClaimEvent(ctx, staleClaimed.ID, "worker-b"); err != nil {
		t.Fatalf("claim stale-claimed: %v", err)
	}

	n, err := store.PruneEvents(ctx, time.Now().UTC().Add(-7*24*time.Hour), 100)
	if err != nil {
		t.Fatalf("PruneEvents: %v", err)
	}
	if n != 3 {
		t.Fatalf("pruned = %d, want 3 (old done/failed/skipped)", n)
	}
	for _, id := range []string{oldDone.ID, oldFailed.ID, oldSkipped.ID} {
		if _, err := store.eventStatus(ctx, id); err == nil {
			t.Fatalf("过期终态行还在：%s", id)
		}
	}
	for _, want := range []struct{ id, status string }{
		{freshDone.ID, RouterEventDone},
		{stalePending.ID, RouterEventPending},
		{staleClaimed.ID, RouterEventClaimed},
	} {
		got, err := store.eventStatus(ctx, want.id)
		if err != nil {
			t.Fatalf("被误删的行 %s: %v", want.id, err)
		}
		if got != want.status {
			t.Fatalf("行 %s 状态变成 %q，want %q", want.id, got, want.status)
		}
	}
}

// 行为 2：limit 是硬上限 —— 一次删不完就分批，绝不长事务扫表。
func TestRouterEventPruneRespectsLimit(t *testing.T) {
	store := newRouterEventStore(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		insertTerminalEvent(t, store, RouterEventRunSettled, "old-"+string(rune('a'+i)), RouterEventDone, 30*24*time.Hour, "worker-a")
	}
	n, err := store.PruneEvents(ctx, time.Now().UTC().Add(-7*24*time.Hour), 2)
	if err != nil {
		t.Fatalf("PruneEvents: %v", err)
	}
	if n != 2 {
		t.Fatalf("pruned = %d, want 2", n)
	}
	pending, err := store.ListPendingEvents(ctx, 10)
	if err != nil {
		t.Fatalf("ListPendingEvents: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("prune 不该把行打回 pending: %+v", pending)
	}
	var left int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM router_events`).Scan(&left); err != nil {
		t.Fatalf("count: %v", err)
	}
	if left != 3 {
		t.Fatalf("remaining = %d, want 3", left)
	}
}
