package kanban

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// 任务 #87 第 1 条：写进任务详情的总结不得被截断在半句话上。
//
// 历史实现把 nodeRunOutputSummary 的每条返回都过了 1200 字硬截断，线上表现是
// 总结停在一句话中间加「…」。2026-11-22 起上限收到 600 字（摘要就是一屏读完
// 的东西），但截断必须停在句末标点上。这里用一条远超上限、由完整句子拼成的
// 总结走真实结算路径，钉住「活动行和任务行都拿到同一段、且都停在句末」。
func TestSettleWorkflowReplyKeepsLongSummary(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)
	run, act := f.seedRunState(t, NodeRunSuccess, NodeRunSuccess, StatusSuccess)

	long := strings.Repeat("这一轮改了会话页与任务详情面板的事。", 160) // ≈2900 字符
	if len([]rune(long)) <= maxStoredSummaryRunes {
		t.Fatalf("fixture summary too short: %d runes", len([]rune(long)))
	}
	if err := f.tasks.UpsertWorkflowNodeRun(ctx, NodeRun{
		ID: run.ID + ":agent-1::1:1", RunID: run.ID, NodeID: "agent-1",
		Attempt: 1, Status: NodeRunSuccess, Output: map[string]any{"summary": long},
		FinishedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertWorkflowNodeRun: %v", err)
	}

	NewService(testRoots{root: f.root}).settleWorkflowReply(ctx, f.tasks, run)

	stored, err := f.service.activities.Get(ctx, act.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	assertSummaryIsSentenceBounded(t, "activity", stored.Summary, long)

	task, err := f.tasks.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	assertSummaryIsSentenceBounded(t, "task", task.ResultSummary, long)
}

// assertSummaryIsSentenceBounded 校验一段被截断的摘要是原文前缀、不超上限、
// 且停在句末（硬截时以「…」收尾）。
func assertSummaryIsSentenceBounded(t *testing.T, label, got, original string) {
	t.Helper()
	if n := len([]rune(got)); n > maxStoredSummaryRunes+1 {
		t.Fatalf("%s summary keeps %d runes, want <= %d", label, n, maxStoredSummaryRunes+1)
	}
	body := strings.TrimSuffix(got, "…")
	if !strings.HasPrefix(original, body) {
		t.Fatalf("%s summary is not a prefix of the original", label)
	}
	if !strings.HasSuffix(body, "。") {
		t.Fatalf("%s summary is cut mid-sentence: %q", label, tailRunes(body, 12))
	}
}

// truncateSummary 只是「病态输出」的兜底：正常长度的总结原样返回，只有超过
// 存储上限才截，且截断后仍停在句末标点上。
func TestTruncateSummaryOnlyGuardsPathologicalOutput(t *testing.T) {
	normal := strings.Repeat("短总结。", 100) // 400 字符
	if got := truncateSummary(normal); got != normal {
		t.Fatalf("normal summary must pass through, got %q", got)
	}
	huge := strings.Repeat("很长的节点输出，", 5000) // 没有句末标点：只能硬截
	got := truncateSummary(huge)
	if len([]rune(got)) != maxStoredSummaryRunes+1 {
		t.Fatalf("huge summary = %d runes, want %d (+ ellipsis)", len([]rune(got)), maxStoredSummaryRunes)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("huge summary must end with an ellipsis")
	}
	// 有句末标点时不能切在句中：取最后一个完整句子。
	bounded := truncateSummary(strings.Repeat("很长的节点输出。", 5000))
	assertSummaryIsSentenceBounded(t, "bounded", bounded, strings.Repeat("很长的节点输出。", 5000))
}

// stubCompressor 是可控的 SummaryCompressor：记录调用次数与提示词，
// 可以阻塞到测试放行（验证 CAS 与迟到压缩的先后关系）。
type stubCompressor struct {
	mu      sync.Mutex
	calls   []string
	out     string
	err     error
	entered chan struct{}
	release chan struct{}
}

func (c *stubCompressor) Compress(_ context.Context, prompt string) (string, error) {
	c.mu.Lock()
	c.calls = append(c.calls, prompt)
	out, err := c.out, c.err
	entered, release := c.entered, c.release
	c.mu.Unlock()
	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if release != nil {
		<-release
	}
	return out, err
}

func (c *stubCompressor) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

// seedSettledRun 准备一条「引擎原文已经写进回复」的成功 Run，供压缩用例复用。
func seedSettledRun(t *testing.T, f *activityServiceFixture, raw string) (*WorkflowRun, *TaskActivity) {
	t.Helper()
	ctx := context.Background()
	run, act := f.seedRunState(t, NodeRunSuccess, NodeRunSuccess, StatusSuccess)
	if err := f.tasks.UpsertWorkflowNodeRun(ctx, NodeRun{
		ID: run.ID + ":agent-1::1:1", RunID: run.ID, NodeID: "agent-1",
		Attempt: 1, Status: NodeRunSuccess, Output: map[string]any{"summary": raw},
		FinishedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertWorkflowNodeRun: %v", err)
	}
	return run, act
}

func settleWithCompressor(t *testing.T, f *activityServiceFixture, c SummaryCompressor) *Service {
	t.Helper()
	svc := NewService(testRoots{root: f.root})
	if c != nil {
		svc.SetSummaryCompressor(c)
	}
	return svc
}

func waitTurns(t *testing.T, svc *Service) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := svc.WaitActivityTurnsIdle(ctx); err != nil {
		t.Fatalf("WaitActivityTurnsIdle: %v", err)
	}
}

// 任务 #87 第 2 条主路径：结算先写原文，压缩成功后活动行与任务行都换成短总结。
func TestSettleWorkflowReplyCompressesSummary(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)
	raw := "我把会话页三个组件拆开了，顺手把任务详情的运行视图入口换了图标，门禁全绿。"
	run, act := seedSettledRun(t, f, raw)

	compressor := &stubCompressor{out: "会话页组件拆分完成，运行视图入口换成图标，门禁全绿。"}
	svc := settleWithCompressor(t, f, compressor)
	svc.settleWorkflowReply(ctx, f.tasks, run)
	waitTurns(t, svc)

	if compressor.callCount() != 1 {
		t.Fatalf("compressor calls = %d, want 1", compressor.callCount())
	}
	stored, err := f.service.activities.Get(ctx, act.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Summary != compressor.out {
		t.Fatalf("activity summary = %q, want compressed text", stored.Summary)
	}
	task, err := f.tasks.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.ResultSummary != compressor.out {
		t.Fatalf("task result summary = %q, want compressed text", task.ResultSummary)
	}
}

// 压缩是锦上添花：失败、返回空、或明显是抄写（超过接受上限）时必须保持原文。
func TestSettleWorkflowReplyKeepsRawWhenCompressionFails(t *testing.T) {
	raw := "改了 a.txt 与 b.txt，测试通过。"
	cases := []struct {
		name string
		out  string
		err  error
	}{
		{name: "compressor error", err: errors.New("model unavailable")},
		{name: "empty output", out: ""},
		{name: "whitespace only", out: "   \n  "},
		{name: "not a compression (too long)", out: strings.Repeat("原文搬运。", 200)},
		{name: "identical to raw", out: raw},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			f := setupActivityService(t)
			run, act := seedSettledRun(t, f, raw)
			svc := settleWithCompressor(t, f, &stubCompressor{out: tc.out, err: tc.err})
			svc.settleWorkflowReply(ctx, f.tasks, run)
			waitTurns(t, svc)

			stored, err := f.service.activities.Get(ctx, act.ID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if stored.Summary != raw {
				t.Fatalf("summary = %q, want raw text kept", stored.Summary)
			}
			if stored.Status != ActivityStatusCompleted {
				t.Fatalf("status = %q, want completed", stored.Status)
			}
		})
	}
}

// 同一条 Run 会被重复结算（HumanGate 审批继续、启动对账）。第二次结算按 Run
// 重算出的还是原文 —— 绝不能把已经压缩好的总结盖回原文，也不能再压一次。
func TestSettleWorkflowReplyReusesCompressedSummary(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)
	raw := "原文：修复了移动端滚动，补了用例。"
	run, act := seedSettledRun(t, f, raw)

	compressor := &stubCompressor{out: "修好移动端滚动并补充用例。"}
	svc := settleWithCompressor(t, f, compressor)
	svc.settleWorkflowReply(ctx, f.tasks, run)
	waitTurns(t, svc)

	svc.settleWorkflowReply(ctx, f.tasks, run)
	waitTurns(t, svc)

	if compressor.callCount() != 1 {
		t.Fatalf("compressor calls = %d, want 1 (no re-compression)", compressor.callCount())
	}
	stored, err := f.service.activities.Get(ctx, act.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Summary != compressor.out {
		t.Fatalf("summary = %q, want the compressed text kept across re-settles", stored.Summary)
	}
	task, err := f.tasks.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.ResultSummary != compressor.out {
		t.Fatalf("task result summary = %q, want compressed text", task.ResultSummary)
	}
}

// 压缩在后台跑：跑完之前这一行可能已经被改（用户继续留言、操作员改判）。
// CAS 只认「还是它读到的原文」的行，别人的写入一律赢。
func TestCompressedSummaryDefersToConcurrentChange(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)
	raw := "原文：跑完了第一轮。"
	run, act := seedSettledRun(t, f, raw)

	compressor := &stubCompressor{
		out:     "第一轮完成。",
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	svc := settleWithCompressor(t, f, compressor)
	svc.settleWorkflowReply(ctx, f.tasks, run)

	select {
	case <-compressor.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("compressor never started")
	}
	manual := "操作员改写的总结"
	if err := f.service.activities.UpdateStatus(ctx, act.ID, ActivityStatusCompleted, &manual); err != nil {
		t.Fatalf("concurrent UpdateStatus: %v", err)
	}
	close(compressor.release)
	waitTurns(t, svc)

	stored, err := f.service.activities.Get(ctx, act.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Summary != manual {
		t.Fatalf("summary = %q, want the concurrent write to win", stored.Summary)
	}
}

// 提示词必须带上任务、用户这一轮的要求与引擎原文，否则模型无从判断「压缩什么」。
func TestBuildReplySummaryCompressPromptCarriesEvidence(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)
	raw := "原始结果正文"
	run, act := seedSettledRun(t, f, raw)
	task, err := f.tasks.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	task.Name = "任务详情面板修改"
	if err := f.tasks.UpdateTask(ctx, task); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	loaded, err := f.tasks.WorkflowRunByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("WorkflowRunByID: %v", err)
	}
	// 回复活动的 ReplyToID 在 legacy 路径上是空的；用户留言必须靠 ordinal 回溯
	// 拿到，而不是读回复自己的 body（结算后那是空的）。
	svc := NewService(testRoots{root: f.root})
	userMessage := svc.resolveCompressUserMessage(ctx, f.tasks, act)
	prompt, ok := buildSummaryCompressPrompt(ctx, f.tasks, loaded.TaskID, loaded, userMessage, raw)
	if !ok {
		t.Fatal("prompt build rejected a healthy run")
	}
	for _, want := range []string{"任务详情面板修改", "跑一下", raw, "agent-1"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestNormalizeCompressedSummary(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain sentence", in: "修好了登录页。", want: "修好了登录页。"},
		{name: "code fence", in: "```\n修好了登录页。\n```", want: "修好了登录页。"},
		{name: "quotes", in: "“修好了登录页。”", want: "修好了登录页。"},
		{name: "summary prefix", in: "总结：修好了登录页。", want: "修好了登录页。"},
		{name: "bullet lines", in: "- 修好了登录页\n- 补了用例", want: "修好了登录页 补了用例"},
		{name: "numbered lines", in: "1. 修好了登录页\n2) 补了用例", want: "修好了登录页 补了用例"},
		{name: "blank", in: "  \n\t", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeCompressedSummary(tc.in); got != tc.want {
				t.Fatalf("normalize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
