package controlsync

import (
	"context"
	"path/filepath"
	"testing"

	"mindfs/server/internal/pins"
)

func newTestPinsDomain(t *testing.T, configDir string) *PinsDomain {
	t.Helper()
	domain, err := NewPinsDomain(configDir)
	if err != nil {
		t.Fatalf("NewPinsDomain: %v", err)
	}
	return domain
}

func TestPinsDomainSnapshotCarriesAccountAndExactTimestamps(t *testing.T) {
	dir := t.TempDir()
	store, err := pins.NewStoreAt(filepath.Join(dir, "users", "u_abc"))
	if err != nil {
		t.Fatalf("NewStoreAt: %v", err)
	}
	if _, _, err := store.SetProjectPin("home::root-1", true); err != nil {
		t.Fatalf("SetProjectPin: %v", err)
	}
	if _, _, err := store.SetSessionPin("home::root-1::sess-1", true); err != nil {
		t.Fatalf("SetSessionPin: %v", err)
	}
	recs, err := newTestPinsDomain(t, dir).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("records = %d, want 2", len(recs))
	}
	keys := map[string]bool{}
	for _, rec := range recs {
		keys[rec.Key] = true
	}
	for _, want := range []string{"u_abc/project:home::root-1", "u_abc/session:home::root-1::sess-1"} {
		if !keys[want] {
			t.Fatalf("缺少 key %q：%v", want, keys)
		}
	}
}

func TestPinsDomainApplyRestoresExactValueAndTombstone(t *testing.T) {
	sourceDir, targetDir := t.TempDir(), t.TempDir()
	source, err := pins.NewStoreAt(sourceDir)
	if err != nil {
		t.Fatalf("NewStoreAt: %v", err)
	}
	if err := source.RestoreProjectPin("home::root-1", 1700000000123); err != nil {
		t.Fatalf("RestoreProjectPin: %v", err)
	}
	if err := source.RestoreSessionPin("home::root-1::sess-1", "2026-11-21T10:00:00Z"); err != nil {
		t.Fatalf("RestoreSessionPin: %v", err)
	}
	recs, err := newTestPinsDomain(t, sourceDir).Snapshot(context.Background())
	if err != nil || len(recs) != 2 {
		t.Fatalf("Snapshot = %v, %v", recs, err)
	}

	domain := newTestPinsDomain(t, targetDir)
	for _, rec := range recs {
		if err := domain.Apply(context.Background(), rec); err != nil {
			t.Fatalf("Apply(%s): %v", rec.Key, err)
		}
	}
	// 应用后再快照：hash 必须逐条相等 —— 时间戳被写成「现在」就会在这里红。
	targetRecs, err := domain.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot(target): %v", err)
	}
	if !sameRecordHashes(recs, targetRecs) {
		t.Fatalf("两侧 hash 不一致：%v vs %v", recordKeyHashes(recs), recordKeyHashes(targetRecs))
	}

	for _, rec := range recs {
		dead := rec
		dead.Deleted = true
		dead.Payload = nil
		if err := domain.Apply(context.Background(), dead); err != nil {
			t.Fatalf("Apply tombstone(%s): %v", rec.Key, err)
		}
	}
	if left, err := domain.Snapshot(context.Background()); err != nil || len(left) != 0 {
		t.Fatalf("墓碑之后仍有 %v（err=%v）", recordKeys(left), err)
	}
}

func TestPinsDomainRejectsScopeMismatch(t *testing.T) {
	dir := t.TempDir()
	domain := newTestPinsDomain(t, dir)
	rec, err := newRecord(DomainPins, "main/project:home::root-1", projectPinPayload{Scope: "home::root-2", PinnedAtMs: 1})
	if err != nil {
		t.Fatalf("newRecord: %v", err)
	}
	if err := domain.Apply(context.Background(), rec); err == nil {
		t.Fatalf("scope 与 key 不一致居然被接受：置顶会落到另一个项目上")
	}
	store, err := pins.NewStoreAt(dir)
	if err != nil {
		t.Fatalf("NewStoreAt: %v", err)
	}
	if got := store.ProjectPins(); len(got) != 0 {
		t.Fatalf("被拒的记录写进了存储：%v", got)
	}
}
