package controlsync

import (
	"errors"
	"testing"
	"time"
)

// 状态接口要能直接回答「主节点现在通不通、还有多少待推送、有多少冲突卡」——
// 光有模式名不够：offline-local 里也可能刚一探测成功，online 里也可能刚掉线。
func TestModeStatusReportsReachability(t *testing.T) {
	prober := &fakeProber{}
	_, mc := newModeFixture(t, prober)
	ctx := t.Context()

	status, err := mc.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.PrimaryReachable {
		t.Fatalf("还没探测过就报主节点可达")
	}

	mc.Observe(true)
	if status, err = mc.Status(ctx); err != nil {
		t.Fatalf("Status: %v", err)
	} else if !status.PrimaryReachable {
		t.Fatalf("探测成功后仍报不可达")
	}

	// 一次失败还没降级，但「可达」已经是假的：横幅据此说明原因。
	mc.Observe(false)
	if status, err = mc.Status(ctx); err != nil {
		t.Fatalf("Status: %v", err)
	} else if status.PrimaryReachable {
		t.Fatalf("探测失败后仍报可达")
	}
	if status.Mode != ModeOnline {
		t.Fatalf("一次失败就降级了：%s", status.Mode)
	}
}

// 计数来自本机两张表：pending 是待推送，conflicts 是未裁决。
// 它们必须是查询时的实时值，不能在启动时算一次缓存下来。
func TestModeStatusReportsPendingAndConflictCounts(t *testing.T) {
	prober := &fakeProber{}
	store, mc := newModeFixture(t, prober)
	ctx := t.Context()

	if err := store.PutPending(ctx, PendingChange{
		ChangeID:  "edge-01:preferences:agents:1",
		Domain:    DomainPreferences,
		Key:       "agents",
		Payload:   []byte(`{"v":1}`),
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("PutPending: %v", err)
	}
	if _, err := store.InsertConflict(ctx, Conflict{
		ID:            "pull:preferences:agents",
		Domain:        DomainPreferences,
		Key:           "agents",
		LocalPayload:  []byte(`{"v":1}`),
		RemotePayload: []byte(`{"v":2}`),
		LocalHash:     "h1",
		RemoteHash:    "h2",
		CreatedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatalf("InsertConflict: %v", err)
	}

	status, err := mc.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.PendingCount != 1 {
		t.Fatalf("pending_count = %d，应为 1", status.PendingCount)
	}
	if status.ConflictCount != 1 {
		t.Fatalf("conflict_count = %d，应为 1", status.ConflictCount)
	}

	// 清空后下一拍必须归零：缓存值会让用户看到一张早就不存在的冲突卡。
	if err := store.DeletePending(ctx, DomainPreferences, "agents"); err != nil {
		t.Fatalf("DeletePending: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE conflicts SET resolved_at = ?`,
		time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("标记冲突已裁决: %v", err)
	}
	if status, err = mc.Status(ctx); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.PendingCount != 0 || status.ConflictCount != 0 {
		t.Fatalf("清空后仍报 pending=%d conflicts=%d", status.PendingCount, status.ConflictCount)
	}
}

// 查不出来就报错，不能把「读失败」显示成 0 条冲突 —— 那会让用户以为没有要裁决的东西。
func TestModeStatusFailsWhenCountsUnreadable(t *testing.T) {
	prober := &fakeProber{}
	store, mc := newModeFixture(t, prober)
	ctx := t.Context()
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := mc.Status(ctx); err == nil {
		t.Fatalf("库已关闭，Status 却成功了")
	} else if errors.Is(err, ErrConflictNotFound) {
		t.Fatalf("错误类型不对：%v", err)
	}
}
