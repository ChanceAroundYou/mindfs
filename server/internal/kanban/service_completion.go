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
	run, err := s.completionDecisionRun(ctx, store, task, decision, action)
	if err != nil {
		return err
	}
	report, err := s.EvaluateCompletion(ctx, store, task, run)
	if err != nil {
		return err
	}
	// 没有契约时，机检答不了「事情真的做完了吗」：工作流交给独立复核回合看过
	// 工作区再判（Task 7），直连任务没有工作区可看，但同样不能拿「对话收尾了」
	// 当完成证据 —— 它也总是走复核（spec §6.4.2）。有契约且不成立是硬证据，
	// 复核不能推翻，直接驳回。
	if completionNeedsVerifier(run, report) || (run == nil && !report.Contract) {
		return s.beginCompletionVerification(ctx, store, task, run, report, decision, summary)
	}
	if !report.Passed {
		return s.blockCompletion(ctx, store, task, run, report, decision)
	}
	return s.commitCompletion(ctx, store, task, run, report, summary, decision.ID)
}

// completionDecisionRun 认「决策发生时的那一轮 Run」，绝不回落到「最新 Run」：
// 决策可能重放，也可能在补做之后才应用，按当前 Run 求值会让新一轮的证据替旧
// 决策背书（Review Focus 5）。action 显式绑定优先；老决策读 brief —— 事件回合
// 只认 brief.Event.RunID，用户回合认当轮 LatestRun。
func (s *Service) completionDecisionRun(ctx context.Context, store *TaskStore, task Task, decision RouterDecision, action RouterAction) (*WorkflowRun, error) {
	current, hasCurrent, err := s.workflowRunForTask(ctx, store, task.ID)
	if err != nil {
		return nil, err
	}
	if !hasCurrent {
		current = nil
	}
	want := strings.TrimSpace(action.RunID)
	if want == "" {
		want = completionBriefRunID(decision.BriefJSON)
	}
	if want == "" {
		return current, nil
	}
	if current == nil {
		return nil, fmt.Errorf("完成声明绑定的 Run %s 已不是任务的当前执行，不能按旧决策收尾", want)
	}
	if current.ID != want {
		return nil, fmt.Errorf("完成声明绑定的 Run %s 已被新一轮 %s 取代，不能按旧决策收尾", want, current.ID)
	}
	return current, nil
}

// completionBriefRunID 从决策快照里取出这一轮该求值的 Run。
func completionBriefRunID(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	var brief SituationBrief
	if err := json.Unmarshal([]byte(raw), &brief); err != nil {
		return ""
	}
	return completionBriefRunIDOf(&brief)
}

// completionBriefRunIDOf 是同一口径的直读版本（工具在记录动作时用）。
func completionBriefRunIDOf(brief *SituationBrief) string {
	if brief == nil {
		return ""
	}
	if brief.Event != nil {
		return strings.TrimSpace(brief.Event.RunID)
	}
	if brief.LatestRun != nil {
		return strings.TrimSpace(brief.LatestRun.ID)
	}
	return ""
}

// commitCompletion 正式收尾。工作流任务走 finishWorkflowRunByOperator（停 driver、
// 关 open 节点、写 run_completed、按操作员终态落任务状态）；直连任务只改任务状态。
func (s *Service) commitCompletion(ctx context.Context, store *TaskStore, task Task, run *WorkflowRun, report CompletionReport, summary, decisionID string) error {
	// 同一条决策重放：已经收过尾就什么都不做。系统行本身也按确定 ID 插入。
	repairsUsed := 0
	verifierKey, verifierVerdict := "", ""
	if existing, found, err := store.GetTaskCompletion(ctx, task.ID); err != nil {
		return err
	} else if found && existing.Status == CompletionStatusCompleted && existing.DecisionID == decisionID {
		return nil
	} else if found {
		repairsUsed = existing.RepairsUsed
		// 复核结论要跟着留下来：它是这一轮收尾的唯一理由，丢了就只剩一句 summary。
		verifierKey, verifierVerdict = existing.VerifierKey, existing.VerifierVerdict
	}

	body := fmt.Sprintf("任务收尾：按完成契约核对，%d 项证据全部成立。\n%s", len(report.Items), summary)
	if !report.Contract {
		body = "任务收尾：" + summary
	}
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
		TaskID:          task.ID,
		RunID:           runID,
		Status:          CompletionStatusCompleted,
		ReportJSON:      completionReportJSON(report),
		VerifierKey:     verifierKey,
		VerifierVerdict: verifierVerdict,
		DecisionID:      decisionID,
		RepairsUsed:     repairsUsed,
		UpdatedAt:       time.Now().UTC(),
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
	verifierKey, verifierVerdict := "", ""
	if existing, found, err := store.GetTaskCompletion(ctx, task.ID); err != nil {
		return err
	} else if found {
		// 同一条决策重放：已经记过这一轮，不能再扣一轮补做预算、也不能再落行。
		if existing.DecisionID == decision.ID && existing.Status == CompletionStatusBlocked {
			return nil
		}
		if existing.RunID == runID {
			// 换 Run 归零：新一轮执行有自己的补做预算。
			repairsUsed = existing.RepairsUsed
		}
		// 复核结论跟着驳回行一起留下：它是「为什么没过」的唯一独立证据。
		verifierKey, verifierVerdict = existing.VerifierKey, existing.VerifierVerdict
	}
	repairsUsed++
	exhausted := repairsUsed > repairsMax

	body := fmt.Sprintf("这一轮还不能收尾：按完成契约核对，还有 %d 项证据不成立。", len(gaps))
	if !report.Contract {
		body = fmt.Sprintf("这一轮还不能收尾：还有 %d 项完成条件不成立。", len(gaps))
	}
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

	// 缺口没有任何可重试的节点（缺文件、审批没批、直连回合）：Router 补不了，
	// 不能让它空转烧预算。确定性地停下等人，并写明原因。
	if len(supervisionEvidenceFailedNodes(ctx, store, run, report)) == 0 {
		s.noteCompletionSystemRow(ctx, store, task, "completion-needs-user:"+decision.ID,
			"这些缺口没有可以自动补做的节点：需要你介入确认或补齐条件后，再让 Router 继续。",
			decision.ID)
		s.parkForUser(ctx, store, task)
	}

	if exhausted {
		// 任务停回待审核：补做不再自动发生，等用户回话。
		if !isTerminalStatus(task.Status) && task.Status != StatusWaitingUser {
			if err := store.UpdateTaskStatus(ctx, task.ID, StatusWaitingUser, false); err != nil {
				return err
			}
		}
	}
	if err := store.UpsertTaskCompletion(ctx, TaskCompletion{
		TaskID:          task.ID,
		RunID:           runID,
		Status:          CompletionStatusBlocked,
		ReportJSON:      completionReportJSON(report),
		VerifierKey:     verifierKey,
		VerifierVerdict: verifierVerdict,
		DecisionID:      decision.ID,
		RepairsUsed:     repairsUsed,
		UpdatedAt:       time.Now().UTC(),
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
