package feishu

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func targetFor(chatID, taskID string) ActiveTarget {
	return ActiveTarget{
		ChatID: chatID, NodeID: "fn", RootID: "/srv/docs",
		TaskID: taskID, TaskNumber: 42, TaskTitle: "修登录",
	}
}

func TestIntakeStoreActiveTargetRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "intake.json")
	now := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	store, err := NewIntakeStore(path, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetActiveTarget(targetFor("oc_1", "task_42")); err != nil {
		t.Fatal(err)
	}
	got, ok := store.ActiveTarget("oc_1")
	if !ok {
		t.Fatal("刚写入的活跃目标读不到")
	}
	if got.NodeID != "fn" || got.RootID != "/srv/docs" || got.TaskID != "task_42" || got.TaskNumber != 42 {
		t.Fatalf("活跃目标内容不对: %+v", got)
	}
	if got.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt 应记写入时刻")
	}
	if _, ok := store.ActiveTarget("oc_2"); ok {
		t.Fatal("没写过的 chat 不应有活跃目标")
	}

	reopened, err := NewIntakeStore(path, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if again, ok := reopened.ActiveTarget("oc_1"); !ok || again.TaskID != "task_42" {
		t.Fatalf("重新打开后活跃目标丢了: %+v ok=%v", again, ok)
	}
}

func TestIntakeStoreActiveTargetExpiresAfterSevenDays(t *testing.T) {
	path := filepath.Join(t.TempDir(), "intake.json")
	now := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	store, err := NewIntakeStore(path, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetActiveTarget(targetFor("oc_1", "task_42")); err != nil {
		t.Fatal(err)
	}
	now = now.Add(8 * 24 * time.Hour)
	if _, ok := store.ActiveTarget("oc_1"); ok {
		t.Fatal("超过 7 天的活跃目标必须读不到，否则消息会落进早就结束的任务")
	}
	if err := store.SetActiveTarget(targetFor("oc_2", "task_42")); err != nil {
		t.Fatal(err)
	}

	// 过期也要落盘：重启后不能把过期目标带回来。
	now = time.Date(2026, 12, 10, 9, 0, 0, 0, time.UTC)
	reopened, err := NewIntakeStore(path, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reopened.ActiveTarget("oc_2"); ok {
		t.Fatal("过期目标被重新加载回来了")
	}
}

func TestIntakeStoreActiveTargetOverwritesSameChat(t *testing.T) {
	store, err := NewIntakeStore(filepath.Join(t.TempDir(), "intake.json"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetActiveTarget(targetFor("oc_1", "task_42")); err != nil {
		t.Fatal(err)
	}
	next := targetFor("oc_1", "")
	next.TaskNumber, next.TaskTitle = 0, ""
	if err := store.SetActiveTarget(next); err != nil {
		t.Fatal(err)
	}
	got, ok := store.ActiveTarget("oc_1")
	if !ok {
		t.Fatal("覆盖后读不到")
	}
	if got.TaskID != "" || got.TaskNumber != 0 {
		t.Fatalf("覆盖没有生效，旧任务还留着: %+v", got)
	}
	if err := store.ClearActiveTarget("oc_1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.ActiveTarget("oc_1"); ok {
		t.Fatal("Clear 之后仍能读到活跃目标")
	}
}

// 每个 chat 一条记录，一次写入不能把别的 chat 一起抹掉；并发写也不能。
func TestIntakeStoreActiveTargetKeepsOtherChats(t *testing.T) {
	store, err := NewIntakeStore(filepath.Join(t.TempDir(), "intake.json"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i, chat := range []string{"oc_a", "oc_b", "oc_c", "oc_d"} {
		wg.Add(1)
		go func(i int, chat string) {
			defer wg.Done()
			if err := store.SetActiveTarget(targetFor(chat, "task_"+chat)); err != nil {
				t.Errorf("SetActiveTarget(%s): %v", chat, err)
			}
		}(i, chat)
	}
	wg.Wait()
	for _, chat := range []string{"oc_a", "oc_b", "oc_c", "oc_d"} {
		if got, ok := store.ActiveTarget(chat); !ok || got.TaskID != "task_"+chat {
			t.Fatalf("chat %s 的活跃目标丢了: %+v ok=%v", chat, got, ok)
		}
	}
}
