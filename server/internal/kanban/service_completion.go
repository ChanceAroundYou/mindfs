package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// complete_task 的应用路径（Plan 2026-11-20 Task 5）。
//
// Router 的 complete_task 只是意图；这里在应用阶段重新求值完成契约：
//   - 证据全部成立 → 复用 finishWorkflowRunByOperator 正式收尾（不新造收尾路径）；
//   - 证据不成立   → 任务原地不动，把缺口编号写成一条可见系统行。
//
// 求值只读任务库、NodeRun 行与磁盘，不读转录；重放按确定性 ID 幂等。

// CompletionApplier 是 ActivityService → Service 的应用端口。ActivityService 只
// 负责路由动作，收尾要停 driver、冻结 Run、写事件，这些都在 Service 上。
type CompletionApplier interface {
	CompleteTaskFromRouter(ctx context.Context, store *TaskStore, task Task, decision RouterDecision, action RouterAction) error
}

// SetCompletionApplier 装配 complete_task 的应用端口。装配期调用一次。
func (s *ActivityService) SetCompletionApplier(c CompletionApplier) {
	if s != nil {
		s.completion = c
	}
}

// CompleteTaskFromRouter 实现 CompletionApplier。
func (s *Service) CompleteTaskFromRouter(ctx context.Context, store *TaskStore, task Task, decision RouterDecision, action RouterAction) error {
	return s.applyCompleteTask(ctx, store, task, decision, action)
}

// applyCompleteTask 是 complete_task 的入口：先求值，再决定 commit 还是 block。
func (s *Service) applyCompleteTask(ctx context.Context, store *TaskStore, task Task, decision RouterDecision, action RouterAction) error {
	if store == nil {
		return errors.New("complete_task: task store missing")
	}
	summary := strings.TrimSpace(action.Summary)
	if summary == "" {
		return errors.New("complete_task summary required")
	}
	run, hasRun, err := s.workflowRunForTask(ctx, store, task.ID)
	if err != nil {
		return err
	}
	if !hasRun {
		run = nil
	}
	report, err := s.EvaluateCompletion(ctx, store, task, run)
	if err != nil {
		return err
	}
	if !report.Passed {
		return s.blockCompletion(ctx, store, task, run, report, decision)
	}
	return s.commitCompletion(ctx, store, task, run, report, summary, decision.ID)
}

// commitCompletion 正式收尾。工作流任务走 finishWorkflowRunByOperator（停 driver、
// 关 open 节点、写 run_completed、按操作员终态落任务状态）；直连任务只改任务状态。
func (s *Service) commitCompletion(ctx context.Context, store *TaskStore, task Task, run *WorkflowRun, report CompletionReport, summary, decisionID string) error {
	// 同一条决策重放：已经收过尾就什么都不做。系统行本身也按确定 ID 插入。
	repairsUsed := 0
	if existing, found, err := store.GetTaskCompletion(ctx, task.ID); err != nil {
		return err
	} else if found && existing.Status == CompletionStatusCompleted && existing.DecisionID == decisionID {
		return nil
	} else if found {
		repairsUsed = existing.RepairsUsed
	}

	body := fmt.Sprintf("任务收尾：按完成契约核对，%d 项证据全部成立。\n%s", len(report.Items), summary)
	row := &TaskActivity{
		ID:               "completion-evidence:" + decisionID,
		TaskID:           task.ID,
		RootID:           task.RootID,
		Type:             ActivityTypeAttention,
		Author:           "system",
		Role:             "system",
		Status:           ActivityStatusCompleted,
		Body:             body,
		RouterDecisionID: decisionID,
	}
	created, err := NewActivityStore(store.db).InsertOrIgnore(ctx, row)
	if err != nil {
		return err
	}
	s.broadcastWorkflowActivity(ctx, store, row, created, nil)

	reason := summary
	// 先落结果摘要，再改状态：setTaskStatus 会带着完整 detail 广播出去，
	// 反过来的话推送里还是上一轮的旧摘要。
	if err := store.UpdateTaskResultSummary(ctx, task.ID, summary); err != nil {
		return err
	}
	var runID string
	if run != nil {
		runID = run.ID
		if _, err := s.finishWorkflowRunByOperator(ctx, task.RootID, task.ID, StatusSuccess, "completed", WorkflowEventRunCompleted, reason); err != nil {
			return err
		}
	} else {
		if _, err := s.setTaskStatus(ctx, task.RootID, task.ID, StatusSuccess, "completed", reason, true); err != nil {
			return err
		}
	}
	return store.UpsertTaskCompletion(ctx, TaskCompletion{
		TaskID:      task.ID,
		RunID:       runID,
		Status:      CompletionStatusCompleted,
		ReportJSON:  completionReportJSON(report),
		DecisionID:  decisionID,
		RepairsUsed: repairsUsed,
		UpdatedAt:   time.Now().UTC(),
	})
}

// blockCompletion 记录证据缺口并原地保留任务状态：任务没做完，但也不是失败，
// 下一回合（自动补做或用户介入）还要接着干。
//
// 每拒绝一次算用掉一轮自动补做（repairs_used+1）。还有余额就落一条去重的
// completion_blocked 事件让 Router 补做；余额用尽就写一条可见系统行、把任务
// 停在 waiting_user，不再产生同缺口的事件 —— 补做不是无限循环。
func (s *Service) blockCompletion(ctx context.Context, store *TaskStore, task Task, run *WorkflowRun, report CompletionReport, decision RouterDecision) error {
	gaps := completionGapLines(report)
	runID := ""
	if run != nil {
		runID = run.ID
	}
	repairsMax := completionRepairsMaxFor(run)
	repairsUsed := 0
	if existing, found, err := store.GetTaskCompletion(ctx, task.ID); err != nil {
		return err
	} else if found && existing.RunID == runID {
		// 换 Run 归零：新一轮执行有自己的补做预算。
		repairsUsed = existing.RepairsUsed
	}
	repairsUsed++
	exhausted := repairsUsed > repairsMax

	body := fmt.Sprintf("这一轮还不能收尾：按完成契约核对，还有 %d 项证据不成立。", len(gaps))
	if exhausted {
		body = fmt.Sprintf("自动补做 %d 次仍未满足证据，需要你介入。还有 %d 项证据不成立。", repairsMax, len(gaps))
	}
	if len(gaps) > 0 {
		body += "\n" + strings.Join(gaps, "\n")
	}
	rowID := "completion-blocked:" + decision.ID
	if exhausted {
		rowID = "completion-exhausted:" + decision.ID
	}
	row := &TaskActivity{
		ID:               rowID,
		TaskID:           task.ID,
		RootID:           task.RootID,
		Type:             ActivityTypeAttention,
		Author:           "system",
		Role:             "system",
		Status:           ActivityStatusCompleted,
		Body:             body,
		RouterDecisionID: decision.ID,
	}
	created, err := NewActivityStore(store.db).InsertOrIgnore(ctx, row)
	if err != nil {
		return err
	}
	s.broadcastWorkflowActivity(ctx, store, row, created, nil)

	if exhausted {
		// 任务停回待审核：补做不再自动发生，等用户回话。
		if !isTerminalStatus(task.Status) && task.Status != StatusWaitingUser {
			if err := store.UpdateTaskStatus(ctx, task.ID, StatusWaitingUser, false); err != nil {
				return err
			}
		}
	}
	if err := store.UpsertTaskCompletion(ctx, TaskCompletion{
		TaskID:      task.ID,
		RunID:       runID,
		Status:      CompletionStatusBlocked,
		ReportJSON:  completionReportJSON(report),
		DecisionID:  decision.ID,
		RepairsUsed: repairsUsed,
		UpdatedAt:   time.Now().UTC(),
	}); err != nil {
		return err
	}
	if exhausted {
		return nil
	}
	return s.emitCompletionBlockedEvent(ctx, store, task, run, report, decision, repairsUsed, repairsMax)
}

// completionGapLines 把未通过的要求排成「1. 标签 — 原因」，让用户能照着补。
func completionGapLines(report CompletionReport) []string {
	var lines []string
	for _, item := range report.Items {
		if item.Pass {
			continue
		}
		label := strings.TrimSpace(item.Label)
		if label == "" {
			label = item.ID
		}
		detail := strings.TrimSpace(item.Detail)
		if detail == "" {
			lines = append(lines, fmt.Sprintf("%d. %s", len(lines)+1, label))
			continue
		}
		lines = append(lines, fmt.Sprintf("%d. %s — %s", len(lines)+1, label, detail))
	}
	return lines
}

func completionReportJSON(report CompletionReport) string {
	raw, err := json.Marshal(report)
	if err != nil {
		return ""
	}
	return string(raw)
}
