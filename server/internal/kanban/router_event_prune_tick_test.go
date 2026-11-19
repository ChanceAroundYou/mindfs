package kanban

import (
	"context"
	"testing"
	"time"
)

// 回收必须接在循环里，且不能每拍都打一条 DELETE —— 否则 PruneEvents 只是
// 一个没人调（或每 30s 锁一次表）的方法，与保留策略想解决的「账本无限长」
// 是两件事。

// 行为 1：启动那一拍要回收；过期终态行删掉，新鲜行与未办完的行留着。
func TestCoordinatorTickPrunesExpiredEvents(t *testing.T) {
	ctx := context.Background()
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)
	// fixture 装配时循环已经跑过一拍，计数器不为 0；这里要的正是「启动拍」。
	f.router.coordinateTicks = 0
	old := insertTerminalEvent(t, f.events, RouterEventRunSettled, "prune-old", RouterEventDone, 30*24*time.Hour, "worker-a")
	fresh := insertTerminalEvent(t, f.events, RouterEventRunSettled, "prune-fresh", RouterEventDone, time.Hour, "worker-a")

	f.router.coordinateTick()

	if _, err := f.events.eventStatus(ctx, old.ID); err == nil {
		t.Fatalf("过期终态行还在 —— 回收没有接进循环")
	}
	if got, err := f.events.eventStatus(ctx, fresh.ID); err != nil || got != RouterEventDone {
		t.Fatalf("新鲜终态行被误删：status=%q err=%v", got, err)
	}
}

// 行为 2：只有第 0 拍和每 routerEventPruneEveryTicks 拍回收，中间那些拍
// 不碰账本 —— 回收不是热路径。这里直接驱动计数，比等 120 拍真实运转快。
func TestCoordinatorTickPruneRunsEveryNthTick(t *testing.T) {
	ctx := context.Background()
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)
	// 从「启动拍之后」起算：0 号拍已经回收过了。
	f.router.coordinateTicks = 1
	ev := insertTerminalEvent(t, f.events, RouterEventRunSettled, "prune-cadence", RouterEventDone, 30*24*time.Hour, "worker-a")

	// 第 1、2 拍不回收，行必须还在。
	f.router.coordinateTick()
	f.router.coordinateTick()
	if _, err := f.events.eventStatus(ctx, ev.ID); err != nil {
		t.Fatalf("非回收拍就把行删了：%v", err)
	}

	// 拨到回收拍（tick % routerEventPruneEveryTicks == 0），再打一拍。
	f.router.coordinateTicks = routerEventPruneEveryTicks
	f.router.coordinateTick()
	if _, err := f.events.eventStatus(ctx, ev.ID); err == nil {
		t.Fatalf("到点那一拍没有回收")
	}
}
