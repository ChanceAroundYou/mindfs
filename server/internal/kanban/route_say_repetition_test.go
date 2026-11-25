package kanban

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// S4：结算回合的总结无条件覆盖卡片，哪怕这一轮什么都没干、写下的还是上一轮
// 那句话。线上 #104 就是这样：用户问题的答案被下一条失败通知顶掉。
//
// 判据全部机械可判：这一轮没跑过工具（tool_calls == 0）、系统发起（没有新的
// 用户留言）、没有作答提问卡，且新总结与卡片上那条的字面高度相似 —— 此时记为
// progress，卡片保持上一条实质结论。
func TestTurnSettledRepeatedSummaryDoesNotOverwriteCard(t *testing.T) {
	ctx := context.Background()
	oldSummary := "结论：三处修复已落地，等用户重启后复验。没做：线上端到端。下一步：重启。"
	newSummary := "结论：三处修复已落地，等用户重启后再复验。没做：线上端到端验证。下一步：重启。"

	cases := []struct {
		name     string
		toolCall bool
		payload  map[string]any
		wantCard string
		wantPur  string
	}{
		{
			// out_of_band=false 才是「系统发起、用户没说话」：只有这种回合才可能没带来
			// 新结论却把同一段话再写一遍。
			name:     "零工具的系统回合重复总结降级成 progress",
			payload:  map[string]any{"out_of_band": false, "answered_question": false},
			wantCard: oldSummary,
			wantPur:  "progress",
		},
		{
			name:     "跑过工具的回合照常写卡片",
			toolCall: true,
			payload:  map[string]any{"out_of_band": false, "answered_question": false},
			wantCard: newSummary,
			wantPur:  "summary",
		},
		{
			// out_of_band=true 是用户在会话视图里自己推了一轮 —— 这是新的用户发言，
			// 这一轮写下的结论必须落到卡片上。
			name:     "用户刚说过话的回合照常写卡片",
			payload:  map[string]any{"out_of_band": true, "answered_question": false},
			wantCard: newSummary,
			wantPur:  "summary",
		},
		{
			name:     "答复了提问卡的回合照常写卡片",
			payload:  map[string]any{"out_of_band": true, "answered_question": true},
			wantCard: newSummary,
			wantPur:  "summary",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, activity, decision, _ := answerDecisionFixture(t,
				"task-s4-"+strings.ReplaceAll(tc.name, " ", "-"), "run-s4", "decision-s4")
			taskID := decision.TaskID
			if err := store.UpdateTaskResultSummary(ctx, taskID, oldSummary); err != nil {
				t.Fatalf("seed result summary: %v", err)
			}
			decision.TriggerKind = RouterTriggerTurnSettled
			decision.TriggerRef = "turn-s4"
			// 运行期事件回合没有同步占位：生产上 ApplyRouteActions 走插入路径，
			// 清掉占位才能测到同一条路径（否则会复用夹具里那条空回复）。
			decision.PlaceholderActivityID = ""
			decision.HadToolCalls = tc.toolCall
			decision.BriefJSON = turnSettledBriefJSON(t, tc.payload)
			decision.Actions = []RouterAction{{Seq: 0, Kind: RouterActionSay, Purpose: "summary", Body: newSummary}}
			if err := activity.ApplyRouteActions(ctx, decision); err != nil {
				t.Fatalf("ApplyRouteActions: %v", err)
			}
			task, err := store.GetTask(ctx, taskID)
			if err != nil {
				t.Fatalf("GetTask: %v", err)
			}
			if task.ResultSummary != tc.wantCard {
				t.Fatalf("result_summary = %q, want %q", task.ResultSummary, tc.wantCard)
			}
			replies := routeRepliesWithBody(t, activity, taskID, newSummary)
			if len(replies) != 1 {
				t.Fatalf("replies with body = %d, want 1", len(replies))
			}
			if replies[0].Purpose != tc.wantPur {
				t.Fatalf("purpose = %q, want %q", replies[0].Purpose, tc.wantPur)
			}
			// 降级成 progress 的那条仍然可见：用户要能在会话里看到这一轮说过话，
			// 只是它不再顶掉卡片（宁可漏降级也不误顶卡片）。
			if replies[0].Hidden {
				t.Fatalf("demoted summary must stay visible")
			}
		})
	}
}

// 与卡片上那条毫不相干的总结照常写：判据是「实质变化」，不是「零工具就降级」。
func TestTurnSettledDifferentSummaryStillWritesCard(t *testing.T) {
	ctx := context.Background()
	store, activity, decision, _ := answerDecisionFixture(t, "task-s4-diff", "run-s4-diff", "decision-s4-diff")
	if err := store.UpdateTaskResultSummary(ctx, decision.TaskID, "结论：接口改完了。下一步：联调。"); err != nil {
		t.Fatalf("seed result summary: %v", err)
	}
	body := "结论：单元测试 12 条全绿，tsc 干净。证据：go test ./server/... 全绿。没做：真机验收。"
	decision.TriggerKind = RouterTriggerTurnSettled
	decision.TriggerRef = "turn-s4-diff"
	decision.PlaceholderActivityID = ""
	decision.BriefJSON = turnSettledBriefJSON(t, map[string]any{"out_of_band": false, "answered_question": false})
	decision.Actions = []RouterAction{{Seq: 0, Kind: RouterActionSay, Purpose: "summary", Body: body}}
	if err := activity.ApplyRouteActions(ctx, decision); err != nil {
		t.Fatalf("ApplyRouteActions: %v", err)
	}
	task, err := store.GetTask(ctx, decision.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.ResultSummary != body {
		t.Fatalf("result_summary = %q, want the new summary", task.ResultSummary)
	}
}

// S5：巡检回合零变化仍然发一整条 progress。线上 #112 同一句「无变化」连刷
// 16 次。同一任务、同一触发类、同一任务状态、同一段正文，30 分钟内只留第一条
// 可见；其余落库但标 hidden，任务摘要不变。
func TestInspectionDuplicateSayIsHidden(t *testing.T) {
	ctx := context.Background()
	store, activity, decision, _ := answerDecisionFixture(t, "task-s5", "run-s5", "decision-s5-a")
	body := "巡检：任务仍在运行，没有新的节点产出，暂无需要你处理的项。"
	decision.TriggerKind = RouterTriggerTaskOrphaned
	decision.TriggerRef = "turn-s5-a"
	decision.PlaceholderActivityID = ""
	decision.Actions = []RouterAction{{Seq: 0, Kind: RouterActionSay, Body: body}}
	if err := activity.ApplyRouteActions(ctx, decision); err != nil {
		t.Fatalf("first ApplyRouteActions: %v", err)
	}

	second := *decision
	second.ID = "decision-s5-b"
	second.Actions = []RouterAction{{Seq: 0, Kind: RouterActionSay, Body: body}}
	if err := activity.ApplyRouteActions(ctx, &second); err != nil {
		t.Fatalf("second ApplyRouteActions: %v", err)
	}

	replies := routeRepliesWithBody(t, activity, decision.TaskID, body)
	if len(replies) != 2 {
		t.Fatalf("replies = %d, want 2（第二条也要落库，只是不进可见流）", len(replies))
	}
	if replies[0].Hidden {
		t.Fatalf("first inspection report must stay visible")
	}
	if !replies[1].Hidden {
		t.Fatalf("duplicate inspection report must be hidden")
	}
	task, err := store.GetTask(ctx, decision.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if strings.TrimSpace(task.ResultSummary) != "" {
		t.Fatalf("result_summary = %q, want empty（巡检回合不写卡片结论）", task.ResultSummary)
	}
}

// 去重口径：同一任务 + 同一触发类 + 同一任务状态，正文归一化后相同才算重复；
// 30 分钟窗口过后重新可见（用户至少每半小时能收到一次「还在跑」）。
func TestInspectionDedupeFingerprintAndWindow(t *testing.T) {
	now := time.Date(2026, 11, 25, 3, 0, 0, 0, time.UTC)
	d := newInspectionDedupe()
	d.now = func() time.Time { return now }
	body := "巡检：任务仍在运行。 没有新的产出。"

	if got := d.Repeated("task-1", "task_orphaned", StatusRunning, body); got {
		t.Fatalf("first report must not be treated as duplicate")
	}
	if got := d.Repeated("task-1", "task_orphaned", StatusRunning, "巡检：任务仍在运行。没有新的产出。"); !got {
		t.Fatalf("whitespace-only difference must be deduped")
	}
	if got := d.Repeated("task-1", "task_orphaned", StatusRunning, "巡检，任务仍在运行；没有新的产出！"); !got {
		t.Fatalf("punctuation-only difference must be deduped")
	}
	if got := d.Repeated("task-1", "task_orphaned", StatusWaitingUser, body); got {
		t.Fatalf("status change must not be deduped")
	}
	if got := d.Repeated("task-1", "turn_stalled", StatusRunning, body); got {
		t.Fatalf("trigger change must not be deduped")
	}
	if got := d.Repeated("task-1", "task_orphaned", StatusRunning, "巡检：任务已进入等待，节点在两分钟前停住。"); got {
		t.Fatalf("different body must not be deduped")
	}
	now = now.Add(inspectionDedupeWindow + time.Minute)
	if got := d.Repeated("task-1", "task_orphaned", StatusRunning, body); got {
		t.Fatalf("report must become visible again after the window")
	}
}

// S5 的另一半：巡检零变化时模型该沉默。应用层原先会拒掉巡检回合的
// `router_silent`（要求来源留言锚点），沉默被换成一条「动作被拒」的可见说明 ——
// 比说出那句「没有变化」更吵。沉默没有副作用，必须放行。
func TestInspectionSilentTurnIsAccepted(t *testing.T) {
	ctx := context.Background()
	_, activity, decision, _ := answerDecisionFixture(t, "task-s5-silent", "run-s5-silent", "decision-s5-silent")
	decision.TriggerKind = RouterTriggerTaskOrphaned
	decision.TriggerRef = "turn-s5-silent"
	decision.PlaceholderActivityID = ""
	decision.UserActivityID = ""
	decision.Actions = []RouterAction{{Seq: 0, Kind: RouterActionSilent, Reason: "任务仍在运行，与上一次巡检没有变化"}}
	if err := activity.ApplyRouteActions(ctx, decision); err != nil {
		t.Fatalf("silent inspection turn must not be rejected: %v", err)
	}
	if replies := routeRepliesWithBody(t, activity, decision.TaskID, "任务仍在运行，与上一次巡检没有变化"); len(replies) != 0 {
		t.Fatalf("silent turn must not write a visible reply, got %d", len(replies))
	}
}

// turnSettledBriefJSON 拼一份最小 brief：S4 的判据从 event.payload 里读
// out_of_band / answered_question。
func turnSettledBriefJSON(t *testing.T, payload map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"version": 1,
		"trigger": map[string]any{"kind": string(RouterTriggerTurnSettled)},
		"event":   map[string]any{"kind": string(RouterEventTurnSettled), "payload": payload},
	})
	if err != nil {
		t.Fatalf("marshal brief: %v", err)
	}
	return string(raw)
}

// routeRepliesWithBody 找出正文相同的全部回复行（含 hidden）。
func routeRepliesWithBody(t *testing.T, activity *ActivityService, taskID, body string) []*TaskActivity {
	t.Helper()
	all, err := activity.activities.ListIncludingHidden(context.Background(), taskID)
	if err != nil {
		t.Fatalf("ListIncludingHidden: %v", err)
	}
	var out []*TaskActivity
	for _, act := range all {
		if act.Body == body {
			out = append(out, act)
		}
	}
	return out
}
