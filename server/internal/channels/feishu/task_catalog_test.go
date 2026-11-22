package feishu

import (
	"context"
	"testing"
	"time"
)

type fakeTaskFinder struct {
	tasks []TaskRef
	calls int
	err   error
}

func (f *fakeTaskFinder) List(_ context.Context, _, _, _ string) ([]TaskRef, error) {
	f.calls++
	return f.tasks, f.err
}

func taskRef(number int, title, status string) TaskRef {
	return TaskRef{
		NodeID: "fn", RootID: "/srv/docs", TaskID: "task_" + title,
		TaskNumber: number, Title: title, Status: status,
	}
}

func TestTaskCatalogMatchesTitleCaseInsensitively(t *testing.T) {
	finder := &fakeTaskFinder{tasks: []TaskRef{
		taskRef(1, "Fix Login Bug", "running"),
		taskRef(2, "写文档", "waiting_user"),
		taskRef(3, "登录超时", "running"),
	}}
	catalog := NewTaskCatalog(finder, time.Minute, time.Now)

	hits, err := catalog.Match(context.Background(), "fn", "/srv/docs", "login")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].TaskNumber != 1 {
		t.Fatalf("大小写不敏感的标题匹配失败: %+v", hits)
	}
	hits, err = catalog.Match(context.Background(), "fn", "/srv/docs", "登录")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].TaskNumber != 3 {
		t.Fatalf("中文标题匹配失败: %+v", hits)
	}
	hits, err = catalog.Match(context.Background(), "fn", "/srv/docs", "不存在")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("未命中应返回空: %+v", hits)
	}
}

// 「#42」必须精确命中的排在最前，不能因为别的任务标题里带 42 被挤掉。
func TestTaskCatalogPrefersExactTaskNumber(t *testing.T) {
	finder := &fakeTaskFinder{tasks: []TaskRef{
		taskRef(7, "重构 42 号模块", "running"),
		taskRef(42, "修登录", "waiting_user"),
	}}
	catalog := NewTaskCatalog(finder, time.Minute, time.Now)

	for _, keyword := range []string{"#42", "42"} {
		hits, err := catalog.Match(context.Background(), "fn", "/srv/docs", keyword)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) == 0 || hits[0].TaskNumber != 42 {
			t.Fatalf("keyword %q 没把精确编号排在最前: %+v", keyword, hits)
		}
	}
	if hits, _ := catalog.Match(context.Background(), "fn", "/srv/docs", "#999"); len(hits) != 0 {
		t.Fatalf("不存在的编号不应命中: %+v", hits)
	}
}

func TestTaskCatalogCapsCandidatesAtFive(t *testing.T) {
	var tasks []TaskRef
	for i := 1; i <= 9; i++ {
		tasks = append(tasks, taskRef(i, "任务", "running"))
	}
	catalog := NewTaskCatalog(&fakeTaskFinder{tasks: tasks}, time.Minute, time.Now)
	hits, err := catalog.Match(context.Background(), "fn", "/srv/docs", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != taskCatalogLimit {
		t.Fatalf("候选数 = %d, want %d", len(hits), taskCatalogLimit)
	}
}

func TestTaskCatalogCachesPerKeywordUntilTTL(t *testing.T) {
	finder := &fakeTaskFinder{tasks: []TaskRef{taskRef(1, "修登录", "running")}}
	now := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	catalog := NewTaskCatalog(finder, time.Minute, func() time.Time { return now })

	for i := 0; i < 3; i++ {
		if _, err := catalog.Match(context.Background(), "fn", "/srv/docs", "登录"); err != nil {
			t.Fatal(err)
		}
	}
	if finder.calls != 1 {
		t.Fatalf("TTL 内重复查询打了 %d 次任务源, want 1", finder.calls)
	}
	// 关键词不同是另一次查询；超过 TTL 后原来的关键词也要重取。
	if _, err := catalog.Match(context.Background(), "fn", "/srv/docs", "文档"); err != nil {
		t.Fatal(err)
	}
	if finder.calls != 2 {
		t.Fatalf("换关键词应重新查询, calls=%d", finder.calls)
	}
	now = now.Add(2 * time.Minute)
	if _, err := catalog.Match(context.Background(), "fn", "/srv/docs", "登录"); err != nil {
		t.Fatal(err)
	}
	if finder.calls != 3 {
		t.Fatalf("TTL 过期后应重新查询, calls=%d", finder.calls)
	}
}

func TestTaskCatalogWithoutFinderHasNoCandidates(t *testing.T) {
	catalog := NewTaskCatalog(nil, time.Minute, time.Now)
	hits, err := catalog.Match(context.Background(), "fn", "/srv/docs", "登录")
	if err != nil || len(hits) != 0 {
		t.Fatalf("没有任务源时应返回空而不是报错: %+v err=%v", hits, err)
	}
	if _, err := catalog.Match(context.Background(), "fn", "  ", "登录"); err == nil {
		t.Fatal("没有项目时应该报错，否则会去查整台机器的所有任务")
	}
}
