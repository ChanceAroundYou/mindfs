package kanban

import (
	"context"
	"testing"
	"time"
)

// 领取租约必须接在循环里，否则 ReclaimStaleEvents 只是个没人调的方法 ——
// 与它要修的「事件永久 claimed」是同一个症状。这里直接打一拍 coordinateTick。

func TestCoordinatorTickReclaimsStaleClaims(t *testing.T) {
	ctx := context.Background()
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)
	ev := f.addEvent(t, RouterEvent{Kind: RouterEventRunSettled, TurnID: "lease-tick", UserActivityID: "uact-lease"})
	if _, err := f.events.ClaimEvent(ctx, ev.ID, "worker-stuck"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	old := time.Now().UTC().Add(-2 * routerEventClaimLease).Format(routerTimeLayout)
	if _, err := f.store.db.ExecContext(ctx, `UPDATE router_events SET claimed_at = ? WHERE id = ?`, old, ev.ID); err != nil {
		t.Fatalf("backdate claim: %v", err)
	}

	f.router.coordinateTick()

	got, err := f.events.eventStatus(ctx, ev.ID)
	if err != nil {
		t.Fatalf("eventStatus: %v", err)
	}
	// 收回之后同一拍就会被消费掉（fixture 的 agent 会正常汇报），所以终态或
	// pending 都算「不再是卡死的 claimed」；唯一不能接受的是仍停在 claimed。
	if got == RouterEventClaimed {
		t.Fatalf("事件仍停在 claimed —— 租约没有接进循环")
	}
}

// 反例：租约内的事件不许被这一拍抢走（否则会把正常回合的事件重复投递）。
func TestCoordinatorTickLeavesFreshClaims(t *testing.T) {
	ctx := context.Background()
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)
	ev := f.addEvent(t, RouterEvent{Kind: RouterEventRunSettled, TurnID: "lease-fresh", UserActivityID: "uact-fresh"})
	if _, err := f.events.ClaimEvent(ctx, ev.ID, "worker-alive"); err != nil {
		t.Fatalf("claim: %v", err)
	}

	f.router.reclaimStaleEventClaims(ctx, time.Now().UTC())

	got, err := f.events.eventStatus(ctx, ev.ID)
	if err != nil {
		t.Fatalf("eventStatus: %v", err)
	}
	if got != RouterEventClaimed {
		t.Fatalf("新鲜领取被收回：status = %q", got)
	}
}
