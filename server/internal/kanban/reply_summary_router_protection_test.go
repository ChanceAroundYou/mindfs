package kanban

import (
	"context"
	"strings"
	"testing"
)

// #68 的教训：Router 在 turn_settled 回合里写进 result_summary 的结论（尤其是
// 「待用户决策/审批」那几条），不能被随后落地的异步压缩器覆盖掉。
//
// 保证来自 CAS 的**捕获时机**：压缩目标在回复结算时读取，Router 的回合至少晚一个
// 调度周期才写；等压缩结果回来时任务行的值已经变了，CAS 必须拒绝。这条用例钉住
// 的正是「值变了就不写」——一旦有人把 CAS 改成无条件覆盖，它会立刻红。
func TestCompressedReplySummaryCannotOverwriteRouterSummary(t *testing.T) {
	ctx := context.Background()
	store, svc, root := newDirectReplyRecoveryFixture(t, "task-router-summary-cas", "sess-router-summary", StatusRunning)

	raw := "节点原文：三处改动都已完成。待拍板：1) 是否上线 2) 换哪个模型 3) 摘要口径"
	act := &TaskActivity{
		ID: "act-router-summary-cas", TaskID: "task-router-summary-cas", RootID: root.ID,
		Type: ActivityTypeAgentReply, Author: "system", Role: RoleAgent,
		Status: ActivityStatusCompleted, Body: raw,
	}
	if _, err := NewActivityStore(store.db).InsertOrIgnore(ctx, act); err != nil {
		t.Fatalf("InsertOrIgnore: %v", err)
	}
	// 捕获时的任务摘要还是空的：压缩器准备把它换成压缩短句。
	target := replySummaryTarget{
		rootID: root.ID, activityID: act.ID, taskID: "task-router-summary-cas",
		raw: raw, wantSummary: "", wantTaskResult: "",
	}

	routerSummary := "本轮把三处都改完了，等你拍板：1) 是否上线 2) 换哪个模型 3) 摘要口径"
	if err := store.UpdateTaskResultSummary(ctx, "task-router-summary-cas", routerSummary); err != nil {
		t.Fatalf("UpdateTaskResultSummary: %v", err)
	}

	svc.applyCompressedReplySummary(ctx, store, target, "三处改完，待拍板。")

	task, err := store.GetTask(ctx, "task-router-summary-cas")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.ResultSummary != routerSummary {
		t.Fatalf("result_summary = %q, want Router 写的那份 %q", task.ResultSummary, routerSummary)
	}
	// 活动行的 CAS 仍然生效：气泡照常看到压缩后的短句。
	stored, err := NewActivityStore(store.db).Get(ctx, act.ID)
	if err != nil {
		t.Fatalf("Get activity: %v", err)
	}
	if strings.TrimSpace(stored.Summary) != "三处改完，待拍板。" {
		t.Fatalf("activity summary = %q, want 压缩结果", stored.Summary)
	}
}

// 反向保护：CAS 值没变时压缩器照常落地。避免「防止覆盖」被写成「永不写」。
func TestCompressedReplySummaryLandsWhenTaskSummaryUnchanged(t *testing.T) {
	ctx := context.Background()
	store, svc, root := newDirectReplyRecoveryFixture(t, "task-router-summary-ok", "sess-router-summary-ok", StatusRunning)

	raw := "节点原文：把排序改成按更新时间。"
	act := &TaskActivity{
		ID: "act-router-summary-ok", TaskID: "task-router-summary-ok", RootID: root.ID,
		Type: ActivityTypeAgentReply, Author: "system", Role: RoleAgent,
		Status: ActivityStatusCompleted, Body: raw,
	}
	if _, err := NewActivityStore(store.db).InsertOrIgnore(ctx, act); err != nil {
		t.Fatalf("InsertOrIgnore: %v", err)
	}
	if err := store.UpdateTaskResultSummary(ctx, "task-router-summary-ok", "旧摘要"); err != nil {
		t.Fatalf("seed summary: %v", err)
	}

	target := replySummaryTarget{
		rootID: root.ID, activityID: act.ID, taskID: "task-router-summary-ok",
		raw: raw, wantSummary: "", wantTaskResult: "旧摘要",
	}
	svc.applyCompressedReplySummary(ctx, store, target, "排序改成按更新时间。")

	task, err := store.GetTask(ctx, "task-router-summary-ok")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.ResultSummary != "排序改成按更新时间。" {
		t.Fatalf("result_summary = %q, want 压缩结果", task.ResultSummary)
	}
}
