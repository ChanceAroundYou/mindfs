package controlsync

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"mindfs/server/internal/preferences"
)

func TestPreferencesDomainSnapshotOneRecordPerField(t *testing.T) {
	dir := t.TempDir()
	store, err := preferences.NewStoreAt(dir)
	if err != nil {
		t.Fatalf("NewStoreAt: %v", err)
	}
	if err := store.UpdateIdleSessionResourceReleaseHours(6); err != nil {
		t.Fatalf("UpdateIdleSessionResourceReleaseHours: %v", err)
	}
	if _, err := store.UpdateAgentDefaultsIfChanged("claude", "sonnet", "low", ""); err != nil {
		t.Fatalf("UpdateAgentDefaultsIfChanged: %v", err)
	}
	recs, err := NewPreferencesDomain(store).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	keys := recordKeys(recs)
	for _, want := range []string{"agents", "idle_session_resource_release_hours"} {
		found := false
		for _, key := range keys {
			if key == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("缺少顶层字段记录 %q：%v（整文件单 key 会让任意一处修改都覆盖整份偏好）", want, keys)
		}
	}
}

func TestPreferencesDomainApplyKeepsOtherFields(t *testing.T) {
	sourceDir, targetDir := t.TempDir(), t.TempDir()
	source, err := preferences.NewStoreAt(sourceDir)
	if err != nil {
		t.Fatalf("NewStoreAt(source): %v", err)
	}
	if _, err := source.UpdateAgentDefaultsIfChanged("claude", "sonnet", "", ""); err != nil {
		t.Fatalf("UpdateAgentDefaultsIfChanged: %v", err)
	}
	target, err := preferences.NewStoreAt(targetDir)
	if err != nil {
		t.Fatalf("NewStoreAt(target): %v", err)
	}
	if err := target.UpdateIdleSessionResourceReleaseHours(3); err != nil {
		t.Fatalf("UpdateIdleSessionResourceReleaseHours: %v", err)
	}
	recs, err := NewPreferencesDomain(source).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	domain := NewPreferencesDomain(target)
	for _, rec := range recs {
		if err := domain.Apply(context.Background(), rec); err != nil {
			t.Fatalf("Apply(%s): %v", rec.Key, err)
		}
	}
	if got := target.IdleSessionResourceReleaseHours(); got != 3 {
		t.Fatalf("应用 agents 记录后 idle hours 变成了 %d：字段级合并被整文件覆盖", got)
	}
}

func TestPreferencesDomainUnknownFieldsSurviveAndSync(t *testing.T) {
	dir := t.TempDir()
	raw := map[string]any{
		"agents":       map[string]any{"claude": map[string]any{"model": "sonnet"}},
		"future_field": map[string]any{"enabled": true},
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "preferences.json"), encoded, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	store, err := preferences.NewStoreAt(dir)
	if err != nil {
		t.Fatalf("NewStoreAt: %v", err)
	}
	domain := NewPreferencesDomain(store)
	recs, err := domain.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	seen := false
	for _, rec := range recs {
		if rec.Key == "future_field" {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("未知字段没进快照：新版本写的字段会在老版本上被静默删掉")
	}
	// 再写一次已知字段，未知字段必须还在文件里。
	if err := store.UpdateIdleSessionResourceReleaseHours(9); err != nil {
		t.Fatalf("UpdateIdleSessionResourceReleaseHours: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "preferences.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var after map[string]json.RawMessage
	if err := json.Unmarshal(b, &after); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, ok := after["future_field"]; !ok {
		t.Fatalf("写一次已知字段就把未知字段抹掉了：%s", string(b))
	}
}

func TestPreferencesDomainTombstoneDeletesField(t *testing.T) {
	dir := t.TempDir()
	store, err := preferences.NewStoreAt(dir)
	if err != nil {
		t.Fatalf("NewStoreAt: %v", err)
	}
	if err := store.UpdateNewProjectMetaLocation("home"); err != nil {
		t.Fatalf("UpdateNewProjectMetaLocation: %v", err)
	}
	domain := NewPreferencesDomain(store)
	recs, err := domain.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	var target Record
	for _, rec := range recs {
		if rec.Key == "new_project_meta_location" {
			target = rec
		}
	}
	if target.Key == "" {
		t.Fatalf("缺少 new_project_meta_location 记录：%v", recordKeys(recs))
	}
	target.Deleted = true
	target.Payload = nil
	if err := domain.Apply(context.Background(), target); err != nil {
		t.Fatalf("Apply tombstone: %v", err)
	}
	if got := store.NewProjectMetaLocation(); got != "project" {
		t.Fatalf("墓碑之后取值 = %q，应回落到默认 project", got)
	}
}
