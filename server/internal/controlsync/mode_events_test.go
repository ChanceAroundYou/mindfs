package controlsync

import (
	"testing"
	"time"
)

// 同一次迁移重放两次只能留一条：事件 id 由 (from,to,at) 决定，不是随手生成的随机串。
// 随机 id 会让「重启后又观察到同一次降级」在界面上变成两条通知。
func TestRecordModeEventIsIdempotent(t *testing.T) {
	store := openTestStore(t)
	ctx := t.Context()
	at := time.Date(2026, 11, 22, 10, 0, 0, 400_000_000, time.UTC)
	ev := ModeEvent{From: ModeOnline, To: ModeOfflineLocal, Reason: "primary unreachable", At: at}

	created, err := store.RecordModeEvent(ctx, ev)
	if err != nil {
		t.Fatalf("RecordModeEvent: %v", err)
	}
	if !created {
		t.Fatalf("第一次写事件就报「已存在」")
	}
	created, err = store.RecordModeEvent(ctx, ev)
	if err != nil {
		t.Fatalf("重放 RecordModeEvent: %v", err)
	}
	if created {
		t.Fatalf("同一次迁移被写了第二条")
	}
	events, err := store.ListModeEvents(ctx, 10)
	if err != nil {
		t.Fatalf("ListModeEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("重放后事件条数 = %d，应为 1：%+v", len(events), events)
	}
	if events[0].ID != ModeEventID(ModeOnline, ModeOfflineLocal, at) {
		t.Fatalf("事件 id = %q，应由 from/to/at 决定", events[0].ID)
	}
	if events[0].Reason != "primary unreachable" || events[0].From != ModeOnline || events[0].To != ModeOfflineLocal {
		t.Fatalf("事件内容 = %+v", events[0])
	}

	// 下一次迁移（时间不同）必须是另一条。
	if _, err := store.RecordModeEvent(ctx, ModeEvent{
		From: ModeOfflineLocal, To: ModeReconciling, At: at.Add(time.Second),
	}); err != nil {
		t.Fatalf("RecordModeEvent: %v", err)
	}
	events, err = store.ListModeEvents(ctx, 10)
	if err != nil {
		t.Fatalf("ListModeEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("事件条数 = %d，应为 2", len(events))
	}
	// 最新的在前：界面上第一眼看到的就是刚刚发生的那次。
	if events[0].To != ModeReconciling || events[1].To != ModeOfflineLocal {
		t.Fatalf("排序 = %+v，应为最新在前", events)
	}
}

// spec §7：降级与升级各留一条可见事件，且只有真翻转才写。
func TestModeControllerRecordsTransitionEvents(t *testing.T) {
	prober := &fakeProber{errs: []error{
		errProbe, errProbe, errProbe, errProbe, // 3 次失败降级，第 4 次不再写
		nil, nil, // 2 次成功升级
	}}
	store, mc := newModeFixture(t, prober)
	ctx := t.Context()

	assertEventCount(t, store, 0)
	for i := 0; i < 4; i++ {
		mc.Observe(mc.probe.Probe(ctx) == nil)
	}
	events := assertEventCount(t, store, 1)
	if events[0].From != ModeOnline || events[0].To != ModeOfflineLocal {
		t.Fatalf("降级事件 = %s → %s", events[0].From, events[0].To)
	}

	for i := 0; i < 2; i++ {
		mc.Observe(mc.probe.Probe(ctx) == nil)
	}
	events = assertEventCount(t, store, 2)
	if events[0].From != ModeOfflineLocal || events[0].To != ModeReconciling {
		t.Fatalf("升级事件 = %s → %s", events[0].From, events[0].To)
	}
	// 状态接口要能读到同一份事件：横幅据此显示「上次变化」与一次性提醒。
	st, err := mc.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(st.RecentEvents) != 2 || st.RecentEvents[0].To != ModeReconciling {
		t.Fatalf("status.recent_events = %+v", st.RecentEvents)
	}
}

// 追平锁触发的那次降级要带上原因：用户看到的横幅得说清「为什么明明能连上却是本地模式」。
func TestModeControllerRecordsBlockReason(t *testing.T) {
	store, mc := newModeFixture(t, &fakeProber{})
	ctx := t.Context()
	if _, err := mc.BlockLocal(ctx, "primary instance changed: recorded=home primary=hasee2"); err != nil {
		t.Fatalf("BlockLocal: %v", err)
	}
	events := assertEventCount(t, store, 1)
	if events[0].To != ModeOfflineLocal || events[0].Reason == "" {
		t.Fatalf("锁事件 = %+v，应带原因", events[0])
	}
}

// conflict-review 只提醒一次：停在同一个模式上反复收尾不能一次次追加事件；
// 只有真的离开又回来才算新的一次。
func TestModeControllerConflictReviewEventOnce(t *testing.T) {
	store, mc := newModeFixture(t, &fakeProber{errs: []error{nil, nil}})
	ctx := t.Context()
	if mode, err := mc.Retry(ctx); err != nil || mode != ModeReconciling {
		t.Fatalf("Retry = %s, %v，应为 reconciling", mode, err)
	}
	assertEventCount(t, store, 1) // online → reconciling

	for i := 0; i < 3; i++ {
		if _, err := mc.CompleteReconcile(ctx, true); err != nil {
			t.Fatalf("CompleteReconcile: %v", err)
		}
	}
	events := assertEventCount(t, store, 2)
	if events[0].To != ModeConflictReview {
		t.Fatalf("最新事件 = %+v，应为进入 conflict-review", events[0])
	}

	// 裁决完回到 online，再撞一次冲突：这是一次新的进出，要留新事件。
	if _, err := mc.CompleteReconcile(ctx, false); err != nil {
		t.Fatalf("CompleteReconcile: %v", err)
	}
	if mode, err := mc.Retry(ctx); err != nil || mode != ModeReconciling {
		t.Fatalf("第二轮 Retry = %s, %v", mode, err)
	}
	if _, err := mc.CompleteReconcile(ctx, true); err != nil {
		t.Fatalf("CompleteReconcile: %v", err)
	}
	events = assertEventCount(t, store, 5)
	if events[0].To != ModeConflictReview || events[1].To != ModeReconciling {
		t.Fatalf("第二轮事件 = %+v", events)
	}
}

var errProbe = probeError("primary unreachable")

type probeError string

func (e probeError) Error() string { return string(e) }

func assertEventCount(t *testing.T, store *Store, want int) []ModeEvent {
	t.Helper()
	events, err := store.ListModeEvents(t.Context(), 10)
	if err != nil {
		t.Fatalf("ListModeEvents: %v", err)
	}
	if len(events) != want {
		t.Fatalf("事件条数 = %d，应为 %d：%+v", len(events), want, events)
	}
	return events
}
