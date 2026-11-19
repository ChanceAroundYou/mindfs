package kanban

import (
	"context"
	"strings"
	"testing"
	"time"
)

// 领取租约（R1 留存缺口）：claimed 行原来只有进程重启才能救回来 ——
// startCoordinate 跑一次 RecoverClaimedEvents 之后，只要进程不死，任何卡在
// 领取里的事件（回合 panic 被吞、超时后提前 return 漏掉 complete）就永久
// claimed：账本上既不 pending 也不终态，消费端看不到，人也查不出。
//
// 判据是「领取时间」而不是「事件创建时间」：排队很久但刚被领走的事件是活的，
// 不能因为老就被打回 pending 让第二个回合抢同一事件。

func leasedEvent(t *testing.T, store *RouterStore) RouterEvent {
	t.Helper()
	return mustInsertRouterEvent(t, store, baseRouterEvent(RouterEventRunSettled, "run-lease"))
}

// 行为 1：领取超过租约的事件被打回 pending，并保留原领取者。
func TestRouterEventReclaimStaleEvents(t *testing.T) {
	store := newRouterEventStore(t)
	ctx := context.Background()
	ev := leasedEvent(t, store)
	if _, err := store.ClaimEvent(ctx, ev.ID, "worker-stuck"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	// 把领取时间挪到租约之外：比 wait 杀进程再重启更确定，也不需要假时钟。
	old := time.Now().UTC().Add(-time.Hour).Format(routerTimeLayout)
	if _, err := store.db.ExecContext(ctx, `UPDATE router_events SET claimed_at = ? WHERE id = ?`, old, ev.ID); err != nil {
		t.Fatalf("backdate claim: %v", err)
	}

	n, err := store.ReclaimStaleEvents(ctx, time.Now().UTC().Add(-10*time.Minute), "lease expired")
	if err != nil {
		t.Fatalf("ReclaimStaleEvents: %v", err)
	}
	if n != 1 {
		t.Fatalf("reclaimed = %d, want 1", n)
	}
	pending, err := store.ListPendingEvents(ctx, 10)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != ev.ID {
		t.Fatalf("pending after reclaim = %+v", pending)
	}
	if !strings.Contains(pending[0].LastError, "lease expired") ||
		!strings.Contains(pending[0].LastError, "worker-stuck") {
		t.Fatalf("reclaim reason lost claimer: %q", pending[0].LastError)
	}
}

// 行为 2：租约内的事件、以及任何终态行，一条都不许动。
func TestRouterEventReclaimLeavesFreshAndTerminalEvents(t *testing.T) {
	store := newRouterEventStore(t)
	ctx := context.Background()
	fresh := leasedEvent(t, store)
	if _, err := store.ClaimEvent(ctx, fresh.ID, "worker-alive"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	// 另一个事件：领了、跑完了、落了 done —— 老也不能复活。
	done := mustInsertRouterEvent(t, store, baseRouterEvent(RouterEventRunFailed, "run-done"))
	if _, err := store.ClaimEvent(ctx, done.ID, "worker-done"); err != nil {
		t.Fatalf("claim done: %v", err)
	}
	if err := store.CompleteEvent(ctx, done.ID, RouterEventDone, ""); err != nil {
		t.Fatalf("complete: %v", err)
	}
	old := time.Now().UTC().Add(-time.Hour).Format(routerTimeLayout)
	if _, err := store.db.ExecContext(ctx, `UPDATE router_events SET claimed_at = ?, completed_at = ? WHERE id IN (?, ?)`,
		old, old, done.ID, done.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	n, err := store.ReclaimStaleEvents(ctx, time.Now().UTC().Add(-10*time.Minute), "lease expired")
	if err != nil {
		t.Fatalf("ReclaimStaleEvents: %v", err)
	}
	if n != 0 {
		t.Fatalf("reclaimed = %d, want 0 (新领取 + 已终态都不该动)", n)
	}
	got, err := store.eventStatus(ctx, fresh.ID)
	if err != nil {
		t.Fatalf("status fresh: %v", err)
	}
	if got != RouterEventClaimed {
		t.Fatalf("fresh event status = %q, want claimed", got)
	}
	got, err = store.eventStatus(ctx, done.ID)
	if err != nil {
		t.Fatalf("status done: %v", err)
	}
	if got != RouterEventDone {
		t.Fatalf("terminal event status = %q, want done", got)
	}
}
