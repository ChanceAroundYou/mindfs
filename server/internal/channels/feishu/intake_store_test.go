package feishu

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIntakeStorePersistsAndPrunesAtomically(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "state", "intake.json")
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	store, err := NewIntakeStore(path, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0700 {
		t.Errorf("目录权限 %o，want 0700", got)
	}
	if err := store.Record("om_1", "task_1", "created"); err != nil {
		t.Fatal(err)
	}
	if err := store.Record("om_2", "task_2", "picker"); err != nil {
		t.Fatal(err)
	}
	if !store.Seen("om_1") || !store.Seen("om_2") {
		t.Fatalf("新写入的消息应算已见：om_1=%v om_2=%v", store.Seen("om_1"), store.Seen("om_2"))
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fileInfo.Mode().Perm(); got != 0600 {
		t.Errorf("文件权限 %o，want 0600", got)
	}

	reopened, err := NewIntakeStore(path, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if !reopened.Seen("om_1") || !reopened.Seen("om_2") {
		t.Fatal("重新打开后消息去重记录丢了")
	}

	old := reopened.records["om_1"]
	old.CreatedAt = now.Add(-8 * 24 * time.Hour)
	reopened.records["om_1"] = old
	if err := reopened.Prune(); err != nil {
		t.Fatal(err)
	}
	if reopened.Seen("om_1") {
		t.Error("8 天前的消息应被清掉")
	}
	if !reopened.Seen("om_2") {
		t.Error("新消息不能被清掉")
	}
	again, err := NewIntakeStore(path, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if again.Seen("om_1") || !again.Seen("om_2") {
		t.Fatal("清理结果没有落盘")
	}
}

func TestIntakeStoreEnforcesSevenDayRetentionOnProductionPaths(t *testing.T) {
	base := t.TempDir()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

	// Construction must drop records that were already stale on disk.
	path := filepath.Join(base, "loaded", "intake.json")
	seed, err := NewIntakeStore(path, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Record("om_stale", "task_stale", "created"); err != nil {
		t.Fatal(err)
	}
	loaded, err := NewIntakeStore(path, func() time.Time { return now.Add(8 * 24 * time.Hour) })
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Seen("om_stale") {
		t.Fatal("加载时超过 7 天的消息仍算已见")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "om_stale") {
		t.Fatal("加载时的清理没有落盘")
	}

	// A long-running process must expire records without a restart.
	livePath := filepath.Join(base, "live", "intake.json")
	live, err := NewIntakeStore(livePath, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if err := live.Record("om_old", "task_old", "created"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(8 * 24 * time.Hour)
	if live.Seen("om_old") {
		t.Fatal("生产 Seen 路径没有按 7 天保留期过期")
	}
	if err := live.Record("om_old", "task_replayed", "created"); err != nil {
		t.Fatal(err)
	}
	if got := live.records["om_old"].TaskID; got != "task_replayed" {
		t.Fatalf("过期 ID 重放应可重新建任务，TaskID=%q", got)
	}
	if !live.Seen("om_old") {
		t.Fatal("重放后的消息应算已见")
	}
}

func TestIntakeStoreWriteFailureRollsBackMemory(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "intake.json")
	store, err := NewIntakeStore(path, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(base, "blocked")
	if err := os.Mkdir(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	store.path = blocked
	if err := store.Record("om_fail", "task_x", "created"); err == nil {
		t.Fatal("落盘失败应返回错误")
	}
	if store.Seen("om_fail") {
		t.Fatal("落盘失败后内存里不能留下这条记录，否则同一条消息会被永久吞掉")
	}
}

func TestIntakeStoreRecordIsIdempotentAndSetStatusOverwrites(t *testing.T) {
	store, err := NewIntakeStore(filepath.Join(t.TempDir(), "intake.json"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Record("om_9", "task_a", "picker"); err != nil {
		t.Fatal(err)
	}
	if err := store.Record("om_9", "task_b", "created"); err != nil {
		t.Fatal(err)
	}
	if got := store.records["om_9"]; got.TaskID != "task_a" || got.Status != "picker" {
		t.Fatalf("重复 Record 改写了原记录：%+v", got)
	}
	if err := store.SetStatus("om_9", "task_b", "created"); err != nil {
		t.Fatal(err)
	}
	if got := store.records["om_9"]; got.TaskID != "task_b" || got.Status != "created" {
		t.Fatalf("SetStatus 应更新已有的去重记录：%+v", got)
	}
}
