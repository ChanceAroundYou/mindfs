package kanban

import (
	"context"
	"encoding/json"
	"strings"
)

// brief 的 completion 段（Plan 2026-11-20 Task 6）。
//
// 它只在 completion_blocked 事件回合挂上：那一轮的 brief 必须说清「哪几项证据
// 没成立、还能补做几次」，否则模型只能看到一句「不能收尾」而无从下手。普通
// 用户留言回合不挂 —— 证据清单是补做的输入，不是每轮都要看的噪音。

// routerBriefCompletionItemLimit 与 brief 的其它列表段同一量级：契约本身很短，
// 超过这个数说明规则写错了，截断并标 truncated 即可。
const routerBriefCompletionItemLimit = 12

type RouterBriefCompletion struct {
	Present           bool                        `json:"present"`
	Items             []RouterBriefCompletionItem `json:"items,omitempty"`
	Truncated         bool                        `json:"truncated,omitempty"`
	RepairsUsed       int                         `json:"repairs_used"`
	RepairsMax        int                         `json:"repairs_max"`
	CanComplete       bool                        `json:"can_complete"`
	VerifierAvailable bool                        `json:"verifier_available"`
}

type RouterBriefCompletionItem struct {
	Index  int    `json:"index"`
	ID     string `json:"id"`
	Label  string `json:"label,omitempty"`
	Kind   string `json:"kind,omitempty"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail,omitempty"`
}

// loadTaskCompletionReport 读回上一次判定。行不存在或报告解不开都按「没有
// 判定」处理：补做决策不能建立在半截数据上。
func loadTaskCompletionReport(ctx context.Context, store *TaskStore, taskID string) (TaskCompletion, CompletionReport, bool) {
	if store == nil {
		return TaskCompletion{}, CompletionReport{}, false
	}
	row, found, err := store.GetTaskCompletion(ctx, taskID)
	if err != nil || !found || strings.TrimSpace(row.ReportJSON) == "" {
		return TaskCompletion{}, CompletionReport{}, false
	}
	var report CompletionReport
	if err := json.Unmarshal([]byte(row.ReportJSON), &report); err != nil {
		return TaskCompletion{}, CompletionReport{}, false
	}
	return row, report, true
}

// routerBriefCompletion 把上一次的判定结果（task_completion 行）翻译成 brief 段。
// 读不到行就返回 nil：没有判定就没有补做可言。
func (s *Service) routerBriefCompletion(ctx context.Context, store *TaskStore, task Task, run *WorkflowRun) *RouterBriefCompletion {
	row, report, ok := loadTaskCompletionReport(ctx, store, task.ID)
	if !ok {
		return nil
	}
	out := &RouterBriefCompletion{
		Present:           true,
		RepairsUsed:       row.RepairsUsed,
		RepairsMax:        completionRepairsMaxFor(run),
		CanComplete:       report.Passed,
		VerifierAvailable: strings.TrimSpace(row.VerifierKey) != "",
	}
	for index, item := range report.Items {
		if len(out.Items) >= routerBriefCompletionItemLimit {
			out.Truncated = true
			break
		}
		out.Items = append(out.Items, RouterBriefCompletionItem{
			Index:  index + 1,
			ID:     item.ID,
			Label:  truncateRouterText(item.Label, 80),
			Kind:   item.Kind,
			Pass:   item.Pass,
			Detail: truncateRouterText(item.Detail, 240),
		})
	}
	return out
}

// completionRepairsMaxFor 取本次 Run 的补做上限。直连任务没有契约快照，按默认值。
func completionRepairsMaxFor(run *WorkflowRun) int {
	if run == nil {
		return defaultCompletionMaxRepairs
	}
	return completionMaxRepairs(run.Graph.Completion)
}

// attachCompletionBrief 给 completion_blocked 回合挂上判定结果，并在有监管块
// 时把「还值得补做」的缺口翻成节点状态 —— 后者是 router_retry_node 的合法
// 目标集合，没有它模型只能猜。
func (rs *RouterService) attachCompletionBrief(ctx context.Context, store *TaskStore, brief *SituationBrief, event RouterEvent) {
	if rs == nil || rs.service == nil || store == nil || brief == nil {
		return
	}
	task, err := store.GetTask(ctx, event.TaskID)
	if err != nil {
		return
	}
	var run *WorkflowRun
	if runID := strings.TrimSpace(event.RunID); runID != "" {
		if loaded, runErr := store.WorkflowRunByID(ctx, runID); runErr == nil {
			run = loaded
		}
	}
	brief.Completion = rs.service.routerBriefCompletion(ctx, store, task, run)
	if brief.Supervision == nil || run == nil {
		return
	}
	if _, report, ok := loadTaskCompletionReport(ctx, store, task.ID); ok {
		brief.Supervision.EvidenceFailedNodes = supervisionEvidenceFailedNodes(ctx, store, run, report)
	}
}
