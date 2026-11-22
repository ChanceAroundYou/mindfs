package kanban

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"
)

// H1：任务摘要与活动 summary 是全侧栏/看板的可见文案，此前四个写入点里只有
// 两个做了截断 —— `commitCompletion`（操作员直接完成）与 Router 的 summary
// 分支直接透传，线上因此长出 9001 字的卡片摘要。落库层必须自己兜底：
// 调用方漏了截断不能变成库里一条读不完的摘要。
//
// 这里的判据是「写进去再多，读出来也不超 maxStoredSummaryRunes」，
// 不关心在哪一层被砍。
func TestUpdateTaskResultSummaryTruncatesOversizeBody(t *testing.T) {
	store, _, _ := newDirectReplyRecoveryFixture(t, "task-summary-trunc", "sess-summary-trunc", StatusRunning)
	ctx := context.Background()

	long := strings.Repeat("这是一段很长的收尾说明，用来验证摘要落库时的截断兜底。", 400)
	if utf8.RuneCountInString(long) <= maxStoredSummaryRunes {
		t.Fatalf("夹具太短，测不出截断：%d", utf8.RuneCountInString(long))
	}
	if err := store.UpdateTaskResultSummary(ctx, "task-summary-trunc", long); err != nil {
		t.Fatalf("UpdateTaskResultSummary: %v", err)
	}
	got, err := store.GetTask(ctx, "task-summary-trunc")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if n := utf8.RuneCountInString(got.ResultSummary); n > maxStoredSummaryRunes {
		t.Fatalf("任务摘要未截断：%d 字 > 上限 %d", n, maxStoredSummaryRunes)
	}
	if !strings.HasPrefix(got.ResultSummary, "这是一段很长的收尾说明") {
		t.Fatalf("截断不能改写开头：%q", got.ResultSummary)
	}
}

// 异步压缩落地走的 CAS 写口是另一条路 —— 只加一处兜底就等于留一条后门。
func TestReplaceTaskResultSummaryIfUnchangedTruncatesOversizeBody(t *testing.T) {
	store, _, _ := newDirectReplyRecoveryFixture(t, "task-summary-cas", "sess-summary-cas", StatusRunning)
	ctx := context.Background()
	if err := store.UpdateTaskResultSummary(ctx, "task-summary-cas", "旧摘要"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	long := strings.Repeat("压缩器回吐的超长总结，同样必须服从落库上限。", 400)
	ok, err := store.ReplaceTaskResultSummaryIfUnchanged(ctx, "task-summary-cas", "旧摘要", long)
	if err != nil {
		t.Fatalf("ReplaceTaskResultSummaryIfUnchanged: %v", err)
	}
	if !ok {
		t.Fatalf("CAS 未命中：want 应与种子值一致")
	}
	got, err := store.GetTask(ctx, "task-summary-cas")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if n := utf8.RuneCountInString(got.ResultSummary); n > maxStoredSummaryRunes {
		t.Fatalf("CAS 摘要未截断：%d 字 > 上限 %d", n, maxStoredSummaryRunes)
	}
}

// 活动 summary 是回复气泡的终态文案。`UpdateStatus` 的 summary 指针是
// `commitCompletion` / 结算路径直接用的，落库层同样要兜底。
func TestActivityUpdateStatusTruncatesOversizeSummary(t *testing.T) {
	store := setupActivityStore(t)
	ctx := context.Background()
	row := TaskActivity{
		ID: "act-summary-trunc", TaskID: "task-act-summary", RootID: "root",
		Type: ActivityTypeAgentReply, Author: "system", Role: "agent",
		Status: ActivityStatusCompleted, Body: "正文",
	}
	if _, err := store.InsertOrIgnore(ctx, &row); err != nil {
		t.Fatalf("InsertOrIgnore: %v", err)
	}

	long := strings.Repeat("这是一段很长的节点总结，活动行也必须服从同一个上限。", 400)
	if err := store.UpdateStatus(ctx, row.ID, ActivityStatusCompleted, &long); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	got, err := store.Get(ctx, row.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if n := utf8.RuneCountInString(got.Summary); n > maxStoredSummaryRunes {
		t.Fatalf("活动 summary 未截断：%d 字 > 上限 %d", n, maxStoredSummaryRunes)
	}
}

// 插入期与 Prompt 收口期是另外两条路：InsertOrIgnore 直接带 Summary，
// ResolvePrompt 在收卡片时写 summary。落库兜底必须覆盖全部四条。
func TestActivityInsertAndResolvePromptTruncateSummary(t *testing.T) {
	store := setupActivityStore(t)
	ctx := context.Background()
	long := strings.Repeat("节点正文作为终态摘要时也必须服从同一个上限。", 400)

	reply := TaskActivity{
		ID: "act-insert-trunc", TaskID: "task-act-insert", RootID: "root",
		Type: ActivityTypeAgentReply, Author: "system", Role: "agent",
		Status: ActivityStatusCompleted, Body: "正文", Summary: long,
	}
	if _, err := store.InsertOrIgnore(ctx, &reply); err != nil {
		t.Fatalf("InsertOrIgnore: %v", err)
	}
	got, err := store.Get(ctx, reply.ID)
	if err != nil {
		t.Fatalf("Get(insert): %v", err)
	}
	if n := utf8.RuneCountInString(got.Summary); n > maxStoredSummaryRunes {
		t.Fatalf("InsertOrIgnore summary 未截断：%d 字", n)
	}

	prompt := TaskActivity{
		ID: "act-resolve-trunc", TaskID: "task-act-resolve", RootID: "root",
		Type: ActivityTypeHumanGate, Author: "system", Role: "agent",
		Status: ActivityStatusWaitingUser, Body: "等待批准",
	}
	if _, err := store.InsertOrIgnore(ctx, &prompt); err != nil {
		t.Fatalf("InsertOrIgnore(prompt): %v", err)
	}
	if _, _, err := store.ResolvePrompt(ctx, prompt.ID, ActivityStatusCompleted, "{}", &long); err != nil {
		t.Fatalf("ResolvePrompt: %v", err)
	}
	got, err = store.Get(ctx, prompt.ID)
	if err != nil {
		t.Fatalf("Get(resolve): %v", err)
	}
	if n := utf8.RuneCountInString(got.Summary); n > maxStoredSummaryRunes {
		t.Fatalf("ResolvePrompt summary 未截断：%d 字", n)
	}
}

// 异步压缩落地的 CAS 写口（任务 #87 第 2 条）是第四条路。
func TestActivityReplaceSummaryIfUnchangedTruncates(t *testing.T) {
	store := setupActivityStore(t)
	ctx := context.Background()
	row := TaskActivity{
		ID: "act-cas-trunc", TaskID: "task-act-cas", RootID: "root",
		Type: ActivityTypeAgentReply, Author: "system", Role: "agent",
		Status: ActivityStatusCompleted, Body: "正文", Summary: "旧摘要",
	}
	if _, err := store.InsertOrIgnore(ctx, &row); err != nil {
		t.Fatalf("InsertOrIgnore: %v", err)
	}
	long := strings.Repeat("压缩器回吐的超长活动总结也必须被同一层兜住。", 400)
	ok, err := store.ReplaceSummaryIfUnchanged(ctx, row.ID, "旧摘要", long)
	if err != nil {
		t.Fatalf("ReplaceSummaryIfUnchanged: %v", err)
	}
	if !ok {
		t.Fatalf("CAS 未命中")
	}
	got, err := store.Get(ctx, row.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if n := utf8.RuneCountInString(got.Summary); n > maxStoredSummaryRunes {
		t.Fatalf("CAS 活动 summary 未截断：%d 字", n)
	}
}
