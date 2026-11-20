package kanban

import (
	"context"
	"errors"
	"testing"
	"time"
)

// 回合用量差分（Plan 2026-11-20 Task 13 / B4）。语义只有一条：把这一轮 Run
// 绑定会话在 Run 时间窗内的账本行加起来。读不到就是读不到 —— 返回 false，界面
// 给 null；绝不能写 0，因为「没有数」被读成「没花钱」正是这类遥测最容易骗人的地方。

type fakeFoldUsage struct {
	samples map[string][]FoldUsageSample
	err     error
	calls   []string
}

func (f *fakeFoldUsage) SessionFoldUsage(_ context.Context, rootID, sessionKey string) ([]FoldUsageSample, error) {
	f.calls = append(f.calls, rootID+"/"+sessionKey)
	if f.err != nil {
		return nil, f.err
	}
	return f.samples[sessionKey], nil
}

func (f *completionFixture) bindSession(t *testing.T, nodeID, sessionKey string) {
	t.Helper()
	ctx := context.Background()
	rows, err := f.store.ListWorkflowNodeRuns(ctx, f.run.ID)
	must(t, err, "ListWorkflowNodeRuns")
	var target *NodeRun
	for i := range rows {
		if rows[i].NodeID == nodeID {
			target = &rows[i]
		}
	}
	if target == nil {
		t.Fatalf("节点 %s 还没有 NodeRun", nodeID)
	}
	target.SessionKey = sessionKey
	must(t, f.store.UpsertWorkflowNodeRun(ctx, *target), "UpsertWorkflowNodeRun")
}

func TestCompletionUsageSumsRowsInsideRunWindow(t *testing.T) {
	fix := newCompletionFixture(t, nil)
	fix.addAttempt(t, "build", NodeRunSuccess, nil, "")
	fix.bindSession(t, "build", "sess-usage")
	now := time.Now()
	fix.run.CreatedAt = now.Add(-time.Hour)
	fix.run.UpdatedAt = now.Add(-30 * time.Minute) // 任务已终态：窗口右界取 Run 最后一次写入
	reader := &fakeFoldUsage{samples: map[string][]FoldUsageSample{
		"sess-usage": {
			{At: now.Add(-90 * time.Minute), Input: 999, Output: 999, Cached: 999}, // Run 之前的回合不算
			{At: now.Add(-50 * time.Minute), Input: 100, Output: 10, Cached: 7},
			{At: now.Add(-45 * time.Minute), Input: 200, Output: 20, Cached: 8},
			{At: now.Add(-40 * time.Minute), Input: 300, Output: 30, Cached: 9},
			{At: now.Add(-10 * time.Minute), Input: 500, Output: 50, Cached: 5}, // 窗口之后不算
		},
	}}
	fix.svc.SetFoldUsageReader(reader)

	usage, ok := fix.svc.completionUsage(context.Background(), fix.task, fix.run)
	if !ok {
		t.Fatal("窗口内有账本行，必须给出用量")
	}
	if usage.Input != 600 || usage.Output != 60 || usage.Cached != 24 {
		t.Fatalf("usage = %+v, want input 600 / output 60 / cached 24", usage)
	}
	if usage.Samples != 3 {
		t.Fatalf("samples = %d, want 3", usage.Samples)
	}
}

func TestCompletionUsageOpenRunCountsRowsUpToNow(t *testing.T) {
	fix := newCompletionFixture(t, nil)
	fix.addAttempt(t, "build", NodeRunRunning, nil, "")
	fix.bindSession(t, "build", "sess-open")
	now := time.Now()
	fix.run.CreatedAt = now.Add(-time.Hour)
	// 运行中的 Run，投影停在最后一次节点写入上；这一轮刚花的钱在那之后，
	// 用 UpdatedAt 当右界会把「现在的花费」整段裁掉。
	fix.run.UpdatedAt = now.Add(-40 * time.Minute)
	fix.svc.SetFoldUsageReader(&fakeFoldUsage{samples: map[string][]FoldUsageSample{
		"sess-open": {
			{At: now.Add(-50 * time.Minute), Input: 10},
			{At: now.Add(-5 * time.Minute), Input: 20},
		},
	}})

	usage, ok := fix.svc.completionUsage(context.Background(), fix.task, fix.run)
	if !ok {
		t.Fatal("运行中的 Run 有账本行时必须给出用量")
	}
	if usage.Input != 30 || usage.Samples != 2 {
		t.Fatalf("usage = %+v, want 两条都算（input 30 / samples 2）", usage)
	}
}

func TestCompletionUsageReportsNoDataInsteadOfZero(t *testing.T) {
	fix := newCompletionFixture(t, nil)
	fix.addAttempt(t, "build", NodeRunSuccess, nil, "")
	fix.bindSession(t, "build", "sess-empty")
	fix.svc.SetFoldUsageReader(&fakeFoldUsage{samples: map[string][]FoldUsageSample{}})

	if usage, ok := fix.svc.completionUsage(context.Background(), fix.task, fix.run); ok {
		t.Fatalf("没有账本行时必须返回 ok=false，实际给出 %+v（0 会被读成「没花钱」）", usage)
	}
}

func TestCompletionUsageSkipsMissingSamples(t *testing.T) {
	fix := newCompletionFixture(t, nil)
	fix.addAttempt(t, "build", NodeRunSuccess, nil, "")
	fix.bindSession(t, "build", "sess-missing")
	now := time.Now()
	fix.run.CreatedAt = now.Add(-time.Hour)
	fix.svc.SetFoldUsageReader(&fakeFoldUsage{samples: map[string][]FoldUsageSample{
		"sess-missing": {
			{At: now.Add(-50 * time.Minute), Missing: true},
			{At: now.Add(-40 * time.Minute), Input: 42, Output: 4, Cached: 2},
		},
	}})

	usage, ok := fix.svc.completionUsage(context.Background(), fix.task, fix.run)
	if !ok {
		t.Fatal("有一条真实读数就必须给出用量")
	}
	if usage.Samples != 1 || usage.Input != 42 {
		t.Fatalf("usage = %+v, want 只用那一条真实读数（samples=1 input=42）", usage)
	}
}

func TestCompletionUsageUnreadableLedgerIsNoData(t *testing.T) {
	fix := newCompletionFixture(t, nil)
	fix.addAttempt(t, "build", NodeRunSuccess, nil, "")
	fix.bindSession(t, "build", "sess-broken")
	fix.svc.SetFoldUsageReader(&fakeFoldUsage{err: errors.New("permission denied")})

	if usage, ok := fix.svc.completionUsage(context.Background(), fix.task, fix.run); ok {
		t.Fatalf("账本读不出来时必须返回 ok=false，实际给出 %+v", usage)
	}
}

func TestCompletionUsageNeedsARunBoundSession(t *testing.T) {
	fix := newCompletionFixture(t, nil)
	fix.addAttempt(t, "build", NodeRunSuccess, nil, "")
	reader := &fakeFoldUsage{}
	fix.svc.SetFoldUsageReader(reader)

	if _, ok := fix.svc.completionUsage(context.Background(), fix.task, fix.run); ok {
		t.Fatal("Run 上没有绑定会话时不该编一个用量出来")
	}
	if len(reader.calls) != 0 {
		t.Fatalf("没有会话就不该去读账本：%v", reader.calls)
	}
}
