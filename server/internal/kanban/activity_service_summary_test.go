package kanban

// 一轮成功后的回复文本：把节点 output 投影成给人看的一句话。
// 从 activity_service_test.go 拆出（硬线 500，2026-11-22）。

import (
	"context"
	"testing"
	"time"
)

// 一轮成功后的回复要给人看得懂的结果，而不是把节点 output 原样 JSON 出来
// （2026-11-14 隔离实例里用户看到的就是 {"files":[...]} 这种转储）。
func TestWorkflowReplySummaryUsesReadableOutputText(t *testing.T) {
	cases := []struct {
		name   string
		output map[string]any
		want   string
	}{
		{name: "summary wins", output: map[string]any{"summary": "已完成", "result": "次要"}, want: "已完成"},
		{name: "result is human text", output: map[string]any{"result": "登录页已经改好了", "files": []any{"a.ts"}}, want: "登录页已经改好了"},
		{name: "message key", output: map[string]any{"message": "全部通过"}, want: "全部通过"},
		{name: "lone string value", output: map[string]any{"note": "只剩一句"}, want: "只剩一句"},
		// 执行器会往 output 里塞元数据（outcome/reason/worktree_*），它们不是给人
		// 看的结果，参与「唯一字符串」判断会让真实正文永远被判为歧义。
		{name: "envelope outcome is metadata", output: map[string]any{"outcome": "done", "sentence": "本次演示已完成"}, want: "本次演示已完成"},
		{name: "empty reason is metadata", output: map[string]any{"outcome": "done", "reason": "", "note": "只有这句"}, want: "只有这句"},
		{name: "worktree error is metadata", output: map[string]any{"outcome": "done", "worktree_error": "merge failed", "note": "结果"}, want: "结果"},
		// 真实样本（2026-11-16 任务 #66）：模型把结果放在 changes 字符串数组里，
		// 顶层只有 outcome 元数据和 verification 对象。数组不能参与「输出原样
		// JSON」的转储，但每条都是给人看的变更说明，必须成为这一轮的总结。
		{
			name: "changes list becomes the summary",
			output: map[string]any{
				"changes":      []any{"去掉与发布重复的按钮", "新增删除工作流入口"},
				"outcome":      "done",
				"verification": map[string]any{"go_tests": "pass", "web_tests": "pass"},
			},
			want: "• 去掉与发布重复的按钮\n• 新增删除工作流入口",
		},
		{
			name:   "single change item",
			output: map[string]any{"changes": []string{"只改了一处"}, "outcome": "done"},
			want:   "• 只改了一处",
		},
		{name: "never raw json", output: map[string]any{"files": []any{"a.ts"}, "count": float64(2)}, want: ""},
		// 真实样本：节点 output 没有 canonical 键时，把文本字段按 key 列出来；
		// 只报一句、或用「最长」猜一条都会丢掉模型实际汇报的另一个要点。
		{name: "multiple strings render with keys", output: map[string]any{"a": "x", "b": "y"}, want: "a: x；b: y"},
		{name: "keys are sorted for determinism", output: map[string]any{"file": "a.txt", "content": "hello"}, want: "content: hello；file: a.txt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			f := setupActivityService(t)
			run, _ := f.seedRunState(t, NodeRunSuccess, NodeRunSuccess, StatusSuccess)
			if err := f.tasks.UpsertWorkflowNodeRun(ctx, NodeRun{
				ID: run.ID + ":agent-1::1:1", RunID: run.ID, NodeID: "agent-1",
				Attempt: 1, Status: NodeRunSuccess, Output: tc.output, FinishedAt: time.Now().UTC(),
			}); err != nil {
				t.Fatalf("UpsertWorkflowNodeRun: %v", err)
			}
			if got := workflowReplySummary(ctx, f.tasks, run); got != tc.want {
				t.Fatalf("summary = %q, want %q", got, tc.want)
			}
		})
	}
}

// 成功节点里混着条件/通知节点的簿记输出（{"route":"pass"}）时，结果摘要
// 必须挑 Agent 节点的那句。以前按「最后一个成功节点」取，条件路由后跑就会
// 把 "pass" 当结果发给用户。
func TestWorkflowReplySummaryPrefersAgentNodeOverLaterCondition(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)
	run, _ := f.seedRunState(t, NodeRunSuccess, NodeRunSuccess, StatusSuccess)
	run.Graph = WorkflowDefinition{Nodes: []WorkflowNode{
		{ID: "agent-1", Kind: NodeKindAgent},
		{ID: "route-1", Kind: NodeKindCondition},
	}}
	if err := f.tasks.SaveWorkflowRun(ctx, run); err != nil {
		t.Fatalf("SaveWorkflowRun: %v", err)
	}
	for _, nr := range []NodeRun{
		{
			ID: run.ID + ":agent-1::1:1", RunID: run.ID, NodeID: "agent-1",
			Attempt: 1, Status: NodeRunSuccess, Output: map[string]any{"summary": "登录页改好了"},
			FinishedAt: time.Now().UTC(),
		},
		{
			ID: run.ID + ":route-1::1:1", RunID: run.ID, NodeID: "route-1",
			Attempt: 1, Status: NodeRunSuccess, Output: map[string]any{"route": "pass"},
			FinishedAt: time.Now().UTC().Add(time.Second),
		},
	} {
		if err := f.tasks.UpsertWorkflowNodeRun(ctx, nr); err != nil {
			t.Fatalf("UpsertWorkflowNodeRun(%s): %v", nr.NodeID, err)
		}
	}
	if got := workflowReplySummary(ctx, f.tasks, run); got != "登录页改好了" {
		t.Fatalf("summary = %q, want the Agent node's sentence, not the condition route", got)
	}
}

// 旧版本已经结算过的回复（summary 为空）在升级后打开仍应看到结果，而不是
// 永远停在「已完成 <workflow>」。启动恢复只补这一类行，且幂等。
func TestBackfillWorkflowReplySummariesForCompletedRuns(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)
	run, act := f.seedRunState(t, NodeRunSuccess, NodeRunSuccess, StatusSuccess)
	if err := f.tasks.UpsertWorkflowNodeRun(ctx, NodeRun{
		ID: run.ID + ":agent-1::1:1", RunID: run.ID, NodeID: "agent-1",
		Attempt: 1, Status: NodeRunSuccess, FinishedAt: time.Now().UTC(),
		Output: map[string]any{"changes": []any{"修复了 A", "补了 B"}, "outcome": "done"},
	}); err != nil {
		t.Fatalf("UpsertWorkflowNodeRun: %v", err)
	}
	empty := ""
	if err := f.service.activities.UpdateStatus(ctx, act.ID, ActivityStatusCompleted, &empty); err != nil {
		t.Fatalf("seed empty summary: %v", err)
	}

	svc := NewService(testRoots{root: f.root})
	svc.backfillWorkflowReplySummaries(ctx, f.tasks)

	stored, err := f.service.activities.Get(ctx, act.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Summary != "• 修复了 A\n• 补了 B" {
		t.Fatalf("summary = %q, want readable changes", stored.Summary)
	}

	// 二次执行不再改行（幂等）。
	svc.backfillWorkflowReplySummaries(ctx, f.tasks)
	again, err := f.service.activities.Get(ctx, act.ID)
	if err != nil {
		t.Fatalf("Get again: %v", err)
	}
	if again.Summary != stored.Summary || !again.UpdatedAt.Equal(stored.UpdatedAt) {
		t.Fatalf("backfill must be idempotent: before=%#v after=%#v", stored, again)
	}
}

// 运行期的「步骤 i/n」正文在结算时必须清掉：终态文案由 summary 提供，
// 留着旧进度会被前端当成结果展示。
func TestSettleWorkflowReplyClearsStaleProgressBody(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)
	run, act := f.seedRunState(t, NodeRunSuccess, NodeRunSuccess, StatusSuccess)
	if err := f.service.activities.UpdateBody(ctx, act.ID, "步骤 2/3: 实现"); err != nil {
		t.Fatalf("UpdateBody: %v", err)
	}
	if err := f.tasks.UpsertWorkflowNodeRun(ctx, NodeRun{
		ID: run.ID + ":agent-1::1:1", RunID: run.ID, NodeID: "agent-1",
		Attempt: 1, Status: NodeRunSuccess, Output: map[string]any{"result": "已经改好了"}, FinishedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertWorkflowNodeRun: %v", err)
	}

	// 生产里由 driver 的 projectWorkflowTaskStatus 调到这里；单测直接走同一入口。
	NewService(testRoots{root: f.root}).settleWorkflowReply(ctx, f.tasks, run)

	stored, err := f.service.activities.Get(ctx, act.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Status != ActivityStatusCompleted {
		t.Fatalf("status = %q, want completed", stored.Status)
	}
	if stored.Summary != "已经改好了" {
		t.Fatalf("summary = %q, want the readable node result", stored.Summary)
	}
	if stored.Body != "" {
		t.Fatalf("body = %q, want stale progress cleared", stored.Body)
	}
}
