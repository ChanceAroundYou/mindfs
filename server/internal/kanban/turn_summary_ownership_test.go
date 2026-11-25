package kanban

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// W1-1：purpose=progress 的进度口信没有总结所有权。线上 #100 的卡片摘要就是被
// 这类「已交给执行会话」的口信反复覆盖成不可读的。
func TestProgressSayDoesNotOwnTaskSummary(t *testing.T) {
	ctx := context.Background()
	store, activity, decision, _ := answerDecisionFixture(t, "task-progress-summary", "run-progress-summary", "decision-progress-summary")
	decision.Actions = []RouterAction{{
		Seq: 0, Kind: RouterActionSay, Purpose: "progress",
		Body: "已交给执行会话，完成后我会汇总。",
	}}
	if err := NewRouterStore(store.db).InsertDecision(ctx, decision); err != nil {
		t.Fatalf("InsertDecision: %v", err)
	}
	if err := activity.ApplyRouteActions(ctx, decision); err != nil {
		t.Fatalf("ApplyRouteActions: %v", err)
	}
	task, err := store.GetTask(ctx, "task-progress-summary")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if strings.TrimSpace(task.ResultSummary) != "" {
		t.Fatalf("progress say wrote the task summary: %q", task.ResultSummary)
	}
}

// 没有执行动作的 chat 回复仍然算这一轮的答复：用户问一句、Router 答一句，
// 卡片摘要就该是这句回答。
func TestChatSayOwnsTaskSummaryWithoutExecution(t *testing.T) {
	ctx := context.Background()
	store, activity, decision, _ := answerDecisionFixture(t, "task-chat-summary", "run-chat-summary", "decision-chat-summary")
	decision.Actions = []RouterAction{{
		Seq: 0, Kind: RouterActionSay, Purpose: "chat",
		Body: "这个仓库用 Go 写后端、React 写前端。",
	}}
	if err := NewRouterStore(store.db).InsertDecision(ctx, decision); err != nil {
		t.Fatalf("InsertDecision: %v", err)
	}
	if err := activity.ApplyRouteActions(ctx, decision); err != nil {
		t.Fatalf("ApplyRouteActions: %v", err)
	}
	task, err := store.GetTask(ctx, "task-chat-summary")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.ResultSummary != "这个仓库用 Go 写后端、React 写前端。" {
		t.Fatalf("chat summary = %q", task.ResultSummary)
	}
}

// W1-2：摘要存储硬上限 1000 字（提示词要求 800），且要停在句子边界上；正文一个字都不动。
func TestTruncateSummaryStopsAtSentenceBoundary(t *testing.T) {
	sentence := "先把路由的归属改清楚再补上巡检。"       // 16 字，不整除 600，硬切一定落在句中
	text := strings.Repeat(sentence, 80) // 远超过 600 字
	got := truncateSummary(text)
	if n := len([]rune(got)); n > maxStoredSummaryRunes+1 {
		t.Fatalf("summary keeps %d runes, want <= %d", n, maxStoredSummaryRunes+1)
	}
	body := strings.TrimSuffix(got, "…")
	if !strings.HasSuffix(body, "。") {
		t.Fatalf("summary is cut mid-sentence: %q", tailRunes(body, 12))
	}
	if !strings.HasPrefix(text, body) {
		t.Fatal("summary must be a prefix of the original text")
	}
}

func tailRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}

// W1-3：直连回合的兜底摘要必须是结构化摘要，不能把整段 Agent 逐字叙述塞进卡片。
func TestDirectTurnSummaryIsDigestNotRawNarration(t *testing.T) {
	ctx := context.Background()
	raw := longDirectReply()
	f, svc, act := directSummaryFixture(t, &stubCompressor{err: errors.New("model down")})
	f.service.SetDirectExecutor(directStubExecutor{out: raw})

	task, err := f.tasks.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	f.service.executeDirectActivity(ctx, act, task, "把这三处改掉")
	if err := svc.WaitActivityTurnsIdle(ctx); err != nil {
		t.Fatalf("WaitActivityTurnsIdle: %v", err)
	}
	stored, err := f.tasks.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	summary := strings.TrimSpace(stored.ResultSummary)
	if summary == "" {
		t.Fatal("task summary is empty")
	}
	if summary == truncateSummary(raw) {
		t.Fatal("task summary is still the raw narration")
	}
	if !strings.Contains(summary, "本轮未生成总结") || !strings.Contains(summary, "用户诉求") {
		t.Fatalf("summary is not the deterministic digest: %q", summary)
	}
	if n := len([]rune(summary)); n > maxStoredSummaryRunes {
		t.Fatalf("digest keeps %d runes, want <= %d", n, maxStoredSummaryRunes)
	}
}

// W1-4：0 字节输出不能再记成 completed —— 用户看到的是一条「完成了」的空回复。
func TestEmptyDirectTurnIsRecordedAsFailure(t *testing.T) {
	ctx := context.Background()
	f, svc, act := directSummaryFixture(t, &stubCompressor{err: errors.New("model down")})
	f.service.SetDirectExecutor(directStubExecutor{out: "   \n\t "})

	task, err := f.tasks.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	f.service.executeDirectActivity(ctx, act, task, "把这三处改掉")
	if err := svc.WaitActivityTurnsIdle(ctx); err != nil {
		t.Fatalf("WaitActivityTurnsIdle: %v", err)
	}
	stored, err := f.service.activities.Get(ctx, act.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Status != ActivityStatusFailed {
		t.Fatalf("status = %q, want failed", stored.Status)
	}
	if !strings.Contains(stored.Body, "没有产生任何可见输出") {
		t.Fatalf("body does not explain the empty turn: %q", stored.Body)
	}
	updated, err := f.tasks.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	// 失败可见性归活动行（status=failed + 正文说明），不是卡片摘要：
	// 卡片摘要写兜底文案会把系统提示当成任务结论（M3，2026-11-23 裁定）。
	if strings.TrimSpace(updated.ResultSummary) != "" {
		t.Fatalf("失败回合不该写 result_summary: %q", updated.ResultSummary)
	}
	if updated.Status != StatusWaitingUser {
		t.Fatalf("失败回合应停在待审核，实际 %q", updated.Status)
	}
}

// W1-4：harness 的错误行（Prompt is too long）不能原样当回复正文。
func TestHarnessErrorTextIsScrubbed(t *testing.T) {
	ctx := context.Background()
	f, svc, act := directSummaryFixture(t, &stubCompressor{err: errors.New("model down")})
	f.service.SetDirectExecutor(directStubExecutor{out: "Prompt is too long"})

	task, err := f.tasks.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	f.service.executeDirectActivity(ctx, act, task, "把这三处改掉")
	if err := svc.WaitActivityTurnsIdle(ctx); err != nil {
		t.Fatalf("WaitActivityTurnsIdle: %v", err)
	}
	stored, err := f.service.activities.Get(ctx, act.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Status != ActivityStatusFailed {
		t.Fatalf("status = %q, want failed", stored.Status)
	}
	if strings.Contains(stored.Body, "Prompt is too long") {
		t.Fatalf("raw harness error leaked into the reply body: %q", stored.Body)
	}
	if !strings.Contains(stored.Body, "上下文") && !strings.Contains(stored.Body, "上下文过长") {
		t.Fatalf("body does not explain the harness failure: %q", stored.Body)
	}
}

// W1-2 / W1-5 的内容守卫：提示词是 fork 定制，合上游时会被整份替换。这三句
// 少了任何一句，模型就会滑回「摘要是几千字原文」和「交出去还要复述一遍」。
// H2 补一条：失败回合的结算事件带 failed=true，Router 必须按失败说明写结论。
func TestRouterPromptStatesSummaryContract(t *testing.T) {
	prompt := RouterDeveloperInstructions()
	for _, want := range []string{
		"turn_settled", // 会话回合结算也是收尾回合
		"硬上限 800 字",    // 提示词要求 800，存储上限 1000 留出余量
		"没做或未验证：",      // 固定四个标签行（S7）
		"系统会直接丢弃它",     // 交出执行动作后不再复述 progress
		"failed=true",  // 失败回合的结论是失败说明
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("提示词缺少「%s」", want)
		}
	}
}
