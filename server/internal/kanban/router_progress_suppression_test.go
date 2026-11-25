package kanban

import (
	"context"
	"testing"
)

// M1：结算回合里已经写了总结，后面再补的进度汇报只是把总结再念一遍 —— 用户看到
// 的就是「回复次数太多、进度和汇报老是重复」。正文不能并进总结（存储上限只有 1000 字
// 不允许），也不能整条丢掉（审计与回看要原文），所以行照常落库、只是不进默认
// 活动流。
//
// 顺序有意写成「总结在前、进度在后」：总结**之前**的进度汇报是过程记录，仍是
// 可见的 progress（既有契约 TestExplicitSettleSummaryWinsOverLastSay 钉住）。
func TestTurnSettledProgressIsHiddenWhenSummaryExists(t *testing.T) {
	ctx := context.Background()
	store, activity, decision, _ := answerDecisionFixture(t,
		"task-progress-dup", "run-progress-dup", "decision-progress-dup")
	decision.TriggerKind = RouterTriggerTurnSettled
	decision.TriggerRef = "turn-progress-dup"
	progressBody := "再补一句：三处改动都在 router 侧。"
	summaryBody := "结论：本轮改了三处，测试全绿。"
	decision.Actions = []RouterAction{
		{Seq: 0, Kind: RouterActionSay, Purpose: "summary", Body: summaryBody},
		{Seq: 1, Kind: RouterActionSay, Purpose: "progress", Body: progressBody},
	}
	if err := NewRouterStore(store.db).InsertDecision(ctx, decision); err != nil {
		t.Fatalf("InsertDecision: %v", err)
	}
	if err := activity.ApplyRouteActions(ctx, decision); err != nil {
		t.Fatalf("ApplyRouteActions: %v", err)
	}

	visible, err := activity.activities.List(ctx, "task-progress-dup")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var summary *TaskActivity
	for _, act := range visible {
		if act.Body == progressBody {
			t.Fatalf("重复的进度汇报仍然出现在可见流里：%#v", act)
		}
		if act.Body == summaryBody {
			summary = act
		}
	}
	if summary == nil || summary.Purpose != "summary" {
		t.Fatalf("总结回复 = %#v, want purpose=summary", summary)
	}
	task, err := store.GetTask(ctx, "task-progress-dup")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.ResultSummary != summaryBody {
		t.Fatalf("result_summary = %q, want %q", task.ResultSummary, summaryBody)
	}

	// 正文必须原样保留（hidden 行只是不进默认流）。
	all, err := activity.activities.ListIncludingHidden(ctx, "task-progress-dup")
	if err != nil {
		t.Fatalf("ListIncludingHidden: %v", err)
	}
	var hidden *TaskActivity
	for _, act := range all {
		if act.Body == progressBody {
			hidden = act
		}
	}
	if hidden == nil {
		t.Fatal("进度汇报整条丢了，正文没有落库")
	}
	if !hidden.Hidden {
		t.Fatal("进度汇报没有标成 hidden")
	}
	if hidden.Purpose != "progress" {
		t.Fatalf("进度汇报 purpose = %q, want progress", hidden.Purpose)
	}
}

// 没有总结的结算回合不能被误伤：普通进度汇报照旧可见、照旧是 progress。
func TestTurnSettledProgressStaysVisibleWithoutSummary(t *testing.T) {
	ctx := context.Background()
	store, activity, decision, _ := answerDecisionFixture(t,
		"task-progress-keep", "run-progress-keep", "decision-progress-keep")
	decision.TriggerKind = RouterTriggerTurnSettled
	decision.TriggerRef = "turn-progress-keep"
	body := "正在跑测试，跑完再给结论。"
	decision.Actions = []RouterAction{{Seq: 0, Kind: RouterActionSay, Purpose: "progress", Body: body}}
	if err := NewRouterStore(store.db).InsertDecision(ctx, decision); err != nil {
		t.Fatalf("InsertDecision: %v", err)
	}
	if err := activity.ApplyRouteActions(ctx, decision); err != nil {
		t.Fatalf("ApplyRouteActions: %v", err)
	}

	visible, err := activity.activities.List(ctx, "task-progress-keep")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var got *TaskActivity
	for _, act := range visible {
		if act.Body == body {
			got = act
		}
	}
	if got == nil {
		t.Fatal("没有总结的回合里进度汇报被误隐藏了")
	}
	if got.Purpose != "progress" {
		t.Fatalf("purpose = %q, want progress（会话每轮结算，不能拿进度顶掉卡片结论）", got.Purpose)
	}
	task, err := store.GetTask(ctx, "task-progress-keep")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.ResultSummary != "" {
		t.Fatalf("没有总结的回合写了 result_summary: %q", task.ResultSummary)
	}
}
