package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

// 异步复核回合（Plan 2026-11-20 Task 7）。
//
// 证据门是硬证据：契约里写了的项，Go 求值说了算。但没有契约的任务（直连任务、
// 未声明完成证据的工作流）机检只能回答「对话收干净了吗」，回答不了「事情真的
// 做完了吗」。这一层补的是后者 —— 另起一个只读会话（Read/Grep/Glob），让它看
// 工作区给出 pass/fail/uncertain。
//
// 三条约束来自 spec §6.4.2：
//   - 复核不能占 Router 回合：apply 只落 verifying 就返回，判定在后台跑，90s 上限；
//   - 只走 Claude：ToolPolicy 只有 Claude 适配器能强制，别的 harness 上「只读」没有保证；
//   - 复核是模型判定，不是证明：结论落库并如实展示，不被当成机检证据。

const (
	// CompletionStatusVerifying 表示完成声明已受理、复核还没回来。
	CompletionStatusVerifying = "verifying"

	CompletionVerdictPass      = "pass"
	CompletionVerdictFail      = "fail"
	CompletionVerdictUncertain = "uncertain"

	// completionVerifierTimeout 是复核回合的上限。它比 Router 回合（180s）短得多：
	// 复核失败不致命，按未完成处理即可，没必要拖住补做节奏。
	completionVerifierTimeout = 90 * time.Second

	completionVerifierSessionPrefix = "verify-"
	completionVerifierPromptLimit   = 2000
)

// completionVerifierKey 是复核会话与结论复用的身份：同一 (任务, Run, 缺口) 只
// 复核一次。会话 key 直接进会话文件名，所以不用 ':'（Windows 上非法）。
func completionVerifierKey(taskID, runID, reportHash string) string {
	target := strings.TrimSpace(runID)
	if target == "" {
		target = "direct"
	}
	hash := strings.TrimSpace(reportHash)
	if len(hash) > 8 {
		hash = hash[:8]
	}
	if hash == "" {
		hash = "none"
	}
	return completionVerifierSessionPrefix + strings.TrimSpace(taskID) + "-" + target + "-" + hash
}

// parseVerifierVerdict 只认严格 JSON。模型多说一句、包一层解释、或者干脆跑题，
// 一律 uncertain —— 复核读不懂的输出不能当通过，也不能当失败（失败会消耗补做
// 预算并让 Router 去重试根本不存在的缺口）。
func parseVerifierVerdict(raw string) (verdict string, findings []string, err error) {
	body := strings.TrimSpace(raw)
	if start, end := strings.Index(body, "{"), strings.LastIndex(body, "}"); start >= 0 && end > start {
		body = body[start : end+1]
	}
	var payload struct {
		Verdict  string   `json:"verdict"`
		Findings []string `json:"findings"`
		Missing  []string `json:"missing"`
	}
	if unmarshalErr := json.Unmarshal([]byte(body), &payload); unmarshalErr != nil {
		return CompletionVerdictUncertain, nil, fmt.Errorf("复核输出不是 JSON: %w", unmarshalErr)
	}
	findings = append(findings, payload.Findings...)
	findings = append(findings, payload.Missing...)
	switch strings.ToLower(strings.TrimSpace(payload.Verdict)) {
	case CompletionVerdictPass:
		return CompletionVerdictPass, findings, nil
	case CompletionVerdictFail:
		return CompletionVerdictFail, findings, nil
	case CompletionVerdictUncertain:
		return CompletionVerdictUncertain, findings, nil
	default:
		return CompletionVerdictUncertain, findings, fmt.Errorf("复核结论 %q 不在 pass/fail/uncertain 里", payload.Verdict)
	}
}

// completionNeedsVerifier 判断「机检不通过」是不是该交给独立复核。
//
// 有契约且契约项不通过时是硬证据，不再问模型（模型推翻机检只会让证据门失效）。
// 没有契约（直连任务 / 未声明证据的工作流）才退到复核；契约显式写了
// verify_when_unverifiable=false 的，按声明直接驳回。
func completionNeedsVerifier(run *WorkflowRun, report CompletionReport) bool {
	if report.Passed {
		return false
	}
	if report.Contract {
		return false
	}
	if run != nil && run.Graph.Completion != nil {
		if allow := run.Graph.Completion.VerifyWhenUnverifiable; allow != nil && !*allow {
			return false
		}
	}
	return true
}

// beginCompletionVerification 是 apply 里的复核分支：写 verifying 行、留一条可见
// 说明、起后台复核，然后立即返回。apply 的调用方是 Router 回合，不能在这里等。
func (s *Service) beginCompletionVerification(ctx context.Context, store *TaskStore, task Task, run *WorkflowRun, report CompletionReport, decision RouterDecision, summary string) error {
	runID := ""
	if run != nil {
		runID = run.ID
	}
	key := completionVerifierKey(task.ID, runID, completionGapHash(report))
	repairsUsed := 0
	existing, found, err := store.GetTaskCompletion(ctx, task.ID)
	if err != nil {
		return err
	}
	if found {
		repairsUsed = existing.RepairsUsed
		if existing.RunID != runID {
			repairsUsed = 0
		}
		if existing.VerifierKey == key && existing.VerifierVerdict != "" {
			// 同一缺口已经有结论：不再问一次模型，直接按结论办。
			return s.applyVerifierVerdict(ctx, store, task, run, report, decision, summary, existing.VerifierVerdict, nil)
		}
	}
	if err := store.UpsertTaskCompletion(ctx, TaskCompletion{
		TaskID:     task.ID,
		RunID:      runID,
		Status:     CompletionStatusVerifying,
		ReportJSON: completionReportJSON(report),
		VerifierKey: key,
		DecisionID: decision.ID,
		RepairsUsed: repairsUsed,
		UpdatedAt:  time.Now().UTC(),
	}); err != nil {
		return err
	}
	s.noteCompletionSystemRow(ctx, store, task, "completion-verifying:"+decision.ID,
		"完成声明已受理：这一轮没有可机检的完成契约，正在独立复核（只读会话，约一分钟）。复核通过就自动收尾，不通过会告诉你还差什么。",
		decision.ID)
	s.startCompletionVerification(task.ID, runID, decision.ID)
	return nil
}

// startCompletionVerification 起后台复核。用独立 ctx：Router 回合结束后回合 ctx
// 就被取消，复核会跟着死；这里只受 90s 上限约束。
func (s *Service) startCompletionVerification(taskID, runID, decisionID string) {
	if s == nil {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[completion-verifier] panic task=%s decision=%s: %v", taskID, decisionID, r)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), completionVerifierTimeout+30*time.Second)
		defer cancel()
		if err := s.verifyCompletion(ctx, taskID, runID, decisionID); err != nil {
			log.Printf("[completion-verifier] task=%s decision=%s: %v", taskID, decisionID, err)
		}
	}()
}

// verifyCompletion 是复核的后台主体。它自己重读任务、Run、决策与报告：后台
// goroutine 里没有 apply 时刻的内存态，重启后也不需要（重启会把 verifying 收口）。
func (s *Service) verifyCompletion(ctx context.Context, taskID, runID, decisionID string) error {
	store, task, err := s.locateCompletionTask(ctx, taskID)
	if err != nil {
		return err
	}
	decision, err := s.loadCompletionDecision(ctx, store, task, decisionID)
	if err != nil {
		return err
	}
	run, err := s.completionRunByID(ctx, store, runID)
	if err != nil {
		return err
	}
	report, err := s.EvaluateCompletion(ctx, store, task, run)
	if err != nil {
		return err
	}
	summary := completionClaimSummary(decision)
	key := completionVerifierKey(task.ID, runID, completionGapHash(report))

	if reason, err := s.completionVerifierStaleReason(ctx, store, task, run); err != nil {
		return err
	} else if reason != "" {
		// 复核还没开始，这一轮就已经不在了：不必再花一轮模型。
		s.markVerifierVerdictStale(ctx, store, task, decision, reason)
		return nil
	}

	if report.Passed && report.Contract {
		// 契约在复核回来之前就对上了（例如补做刚跑完）：直接收尾，不必问模型。
		// 没有契约 / 直连任务不在此列 —— 那时 Passed 只代表「对话收尾了」，
		// 恰恰是复核要判的那件事。
		return s.commitCompletion(ctx, store, task, run, report, summary, decision.ID)
	}
	raw, turnErr := s.runCompletionVerifierTurn(ctx, store, task, run, report, summary, key)
	if turnErr != nil {
		s.noteCompletionSystemRow(ctx, store, task, "completion-verifier-unavailable:"+decision.ID,
			"独立复核不可用（"+turnErr.Error()+"），按未完成处理：需要你确认这一轮是否真的做完了。",
			decision.ID)
		s.parkForUser(ctx, store, task)
		return s.blockCompletion(ctx, store, task, run, report, decision)
	}
	verdict, findings, parseErr := parseVerifierVerdict(raw)
	if parseErr != nil {
		log.Printf("[completion-verifier] task=%s decision=%s: %v", task.ID, decision.ID, parseErr)
	}
	if err := store.UpsertTaskCompletion(ctx, TaskCompletion{
		TaskID:          task.ID,
		RunID:           runIDOf(run),
		Status:          CompletionStatusVerifying,
		ReportJSON:      completionReportJSON(report),
		VerifierKey:     key,
		VerifierVerdict: verdict,
		DecisionID:      decision.ID,
		UpdatedAt:       time.Now().UTC(),
	}); err != nil {
		return err
	}
	return s.applyVerifierVerdict(ctx, store, task, run, report, decision, summary, verdict, findings)
}

// applyVerifierVerdict 把结论变成事实：pass 收尾，fail/uncertain 按未完成处理。
//
// 复核是后台回合，可能跑上一分钟：结论回来时任务可能已被用户取消、被重开、
// 或已经起了新一轮执行。那时结论不再描述任何现存的执行，只能留痕、不能改状态。
func (s *Service) applyVerifierVerdict(ctx context.Context, store *TaskStore, task Task, run *WorkflowRun, report CompletionReport, decision RouterDecision, summary, verdict string, findings []string) error {
	if reason, err := s.completionVerifierStaleReason(ctx, store, task, run); err != nil {
		return err
	} else if reason != "" {
		s.markVerifierVerdictStale(ctx, store, task, decision, reason)
		return nil
	}
	switch verdict {
	case CompletionVerdictPass:
		return s.commitCompletion(ctx, store, task, run, report, verifierSummary(summary, findings), decision.ID)
	case CompletionVerdictFail, CompletionVerdictUncertain:
		label := "未完成"
		if verdict == CompletionVerdictUncertain {
			label = "无法判定"
		}
		body := "独立复核判定" + label
		if len(findings) > 0 {
			body += "：" + strings.Join(findings, "；")
		}
		s.noteCompletionSystemRow(ctx, store, task, "completion-verifier:"+decision.ID, body, decision.ID)
		// 复核说没做完：任务不能继续显示「进行中」。停在 waiting_user 让人看得见，
		// Router 仍可以在这个状态上继续补做（waiting_user 不算活动执行）。
		s.parkForUser(ctx, store, task)
		return s.blockCompletion(ctx, store, task, run, report, decision)
	default:
		return fmt.Errorf("复核结论 %q 不认识", verdict)
	}
}

// completionVerifierStaleReason 判断复核结论回来时这一轮执行还在不在。
// 返回空串表示仍然有效。
func (s *Service) completionVerifierStaleReason(ctx context.Context, store *TaskStore, task Task, run *WorkflowRun) (string, error) {
	fresh, err := store.GetTask(ctx, task.ID)
	if err != nil {
		return "", err
	}
	switch fresh.Status {
	case StatusSuccess, StatusFail, StatusCancelled:
		return "任务已经结束（" + fresh.Status + "）", nil
	}
	if run == nil {
		return "", nil
	}
	current, hasCurrent, err := s.workflowRunForTask(ctx, store, task.ID)
	if err != nil {
		return "", err
	}
	if !hasCurrent || current == nil || current.ID != run.ID {
		return "任务已经起了新一轮执行", nil
	}
	return "", nil
}

// markVerifierVerdictStale 留一条可核对的说明，并把停在 verifying 的记录收成
// blocked —— 结论没被采用，不能让记录永远显示「复核中」。
func (s *Service) markVerifierVerdictStale(ctx context.Context, store *TaskStore, task Task, decision RouterDecision, reason string) {
	if existing, found, err := store.GetTaskCompletion(ctx, task.ID); err != nil {
		log.Printf("[completion-verifier] task=%s stale read: %v", task.ID, err)
	} else if found && existing.Status == CompletionStatusVerifying {
		existing.Status = CompletionStatusBlocked
		existing.UpdatedAt = time.Now().UTC()
		if err := store.UpsertTaskCompletion(ctx, existing); err != nil {
			log.Printf("[completion-verifier] task=%s stale settle: %v", task.ID, err)
		}
	}
	s.noteCompletionSystemRow(ctx, store, task, "completion-verifier-stale:"+decision.ID,
		"独立复核结论回来时"+reason+"，结论未采用：这一轮是否做完由当前执行自己判定。",
		decision.ID)
}

// parkForUser 把非终态任务放回 waiting_user；终态（含已收尾）不动。
func (s *Service) parkForUser(ctx context.Context, store *TaskStore, task Task) {
	switch task.Status {
	case StatusSuccess, StatusFail, StatusCancelled:
		return
	}
	if err := store.UpdateTaskStatus(ctx, task.ID, StatusWaitingUser, false); err != nil {
		log.Printf("[completion-verifier] task=%s park status: %v", task.ID, err)
	}
}

// runCompletionVerifierTurn 起一个只读会话跑一轮复核，返回模型的原始输出。
func (s *Service) runCompletionVerifierTurn(ctx context.Context, store *TaskStore, task Task, run *WorkflowRun, report CompletionReport, summary, key string) (string, error) {
	config := s.RouterConfig()
	agentName := strings.TrimSpace(config.Agent)
	if agentName == "" {
		agentName = "claude"
	}
	if !strings.EqualFold(agentName, "claude") {
		return "", fmt.Errorf("复核只支持 Claude（当前 %s）", agentName)
	}
	agent := s.routerAgent
	if agent == nil {
		return "", errors.New("没有可用的 Claude 复核回合")
	}
	workspace, err := s.completionVerifierWorkspace(task)
	if err != nil {
		return "", err
	}
	res, err := agent.RunRouterTurn(ctx, RouterAgentRequest{
		RootID:                task.RootID,
		TaskID:                task.ID,
		SessionKey:            key,
		Cwd:                   workspace,
		ReadOnly:              true,
		Agent:                 agentName,
		Model:                 config.Model,
		Effort:                config.Effort,
		Prompt:                completionVerifierPrompt(task, run, report, summary, latestUserMessageBody(ctx, store, task.ID)),
		DeveloperInstructions: completionVerifierInstructions,
		Timeout:               completionVerifierTimeout,
	})
	if err != nil {
		return "", err
	}
	return res.FinalText, nil
}

// completionVerifierWorkspace 给复核选一个能看见产出的目录：优先任务 worktree，
// 树已被拆掉时回落项目根。
func (s *Service) completionVerifierWorkspace(task Task) (string, error) {
	if p := strings.TrimSpace(task.WorktreePath); p != "" && worktreeDirUsable(p) {
		return p, nil
	}
	if s.Roots == nil {
		return "", errors.New("找不到任务工作区（root provider 未配置）")
	}
	root, err := s.Roots.GetRoot(task.RootID)
	if err != nil {
		return "", err
	}
	p := strings.TrimSpace(root.RootPath)
	if p == "" || !worktreeDirUsable(p) {
		return "", fmt.Errorf("项目根目录不可用（%q）", p)
	}
	return p, nil
}

func (s *Service) locateCompletionTask(ctx context.Context, taskID string) (*TaskStore, Task, error) {
	if s == nil || s.Roots == nil {
		return nil, Task{}, errors.New("task roots not configured")
	}
	for _, root := range s.Roots.ListRoots() {
		store, err := s.taskStore(root.ID)
		if err != nil {
			continue
		}
		task, err := store.GetTask(ctx, taskID)
		if err == nil {
			return store, task, nil
		}
	}
	return nil, Task{}, fmt.Errorf("任务不存在: %s", taskID)
}

// loadCompletionDecision 读回触发这次收尾的决策；读不到时合成一个最小决策，
// 让复核仍能把结论落成事实（summary 为空，其余字段够用）。
func (s *Service) loadCompletionDecision(ctx context.Context, store *TaskStore, task Task, decisionID string) (RouterDecision, error) {
	decisionID = strings.TrimSpace(decisionID)
	if decision, err := NewRouterStore(store.db).GetDecision(ctx, decisionID); err == nil && decision != nil {
		return *decision, nil
	}
	return RouterDecision{ID: decisionID, TaskID: task.ID, RootID: task.RootID, TriggerKind: RouterTriggerUserMessage}, nil
}

func (s *Service) completionRunByID(ctx context.Context, store *TaskStore, runID string) (*WorkflowRun, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, nil
	}
	run, err := store.WorkflowRunByID(ctx, runID)
	if err != nil {
		return nil, err
	}
	return run, nil
}

func runIDOf(run *WorkflowRun) string {
	if run == nil {
		return ""
	}
	return run.ID
}

// completionClaimSummary 从决策里取回被驳回的那句完成声明（写进复核提示与收尾
// 摘要）。后台复核没有 action 参数，只能从决策存档里找。
func completionClaimSummary(decision RouterDecision) string {
	for _, action := range decision.Actions {
		if action.Kind == RouterActionComplete && strings.TrimSpace(action.Summary) != "" {
			return strings.TrimSpace(action.Summary)
		}
	}
	return ""
}

// reconcileVerifyingCompletions 把停在 verifying 的行收口成 blocked：复核是后台
// goroutine，进程一死就没人把结论写回来，不收拾的话任务会永远显示「复核中」。
func (s *Service) reconcileVerifyingCompletions(ctx context.Context, store *TaskStore) {
	rows, err := store.ListVerifyingCompletions(ctx)
	if err != nil {
		log.Printf("[completion-verifier] list verifying: %v", err)
		return
	}
	for _, row := range rows {
		task, err := store.GetTask(ctx, row.TaskID)
		if err != nil {
			continue
		}
		if err := store.UpsertTaskCompletion(ctx, TaskCompletion{
			TaskID:          row.TaskID,
			RunID:           row.RunID,
			Status:          CompletionStatusBlocked,
			ReportJSON:      row.ReportJSON,
			VerifierKey:     row.VerifierKey,
			VerifierVerdict: row.VerifierVerdict,
			DecisionID:      row.DecisionID,
			RepairsUsed:     row.RepairsUsed,
			UpdatedAt:       time.Now().UTC(),
		}); err != nil {
			log.Printf("[completion-verifier] reconcile task=%s: %v", row.TaskID, err)
			continue
		}
		s.noteCompletionSystemRow(ctx, store, task, "completion-verifier-interrupted:"+row.TaskID,
			"复核被重启打断：这一轮还没有复核结论，按未完成处理。需要你确认是否真的做完了。",
			row.DecisionID)
	}
}

// noteCompletionSystemRow 写一条确定性系统行：同一条决策重放不会多出一条。
func (s *Service) noteCompletionSystemRow(ctx context.Context, store *TaskStore, task Task, id, body, decisionID string) {
	row := &TaskActivity{
		ID:               id,
		TaskID:           task.ID,
		Type:             ActivityTypeAttention,
		Author:           "system",
		Role:             "system",
		Status:           ActivityStatusCompleted,
		Body:             body,
		RouterDecisionID: decisionID,
	}
	created, err := NewActivityStore(store.db).InsertOrIgnore(ctx, row)
	if err != nil || !created {
		return
	}
	s.broadcastWorkflowActivity(ctx, store, row, created, nil)
}

func verifierSummary(summary string, findings []string) string {
	summary = strings.TrimSpace(summary)
	note := "完成：独立复核回合核对工作区后判定已完成"
	if len(findings) > 0 {
		note += "（" + strings.Join(findings, "；") + "）"
	}
	if summary == "" {
		return note
	}
	return summary + "\n" + note
}

const completionVerifierInstructions = `你是一个独立的完成度复核者。你只能读，不能改：可用工具只有 Read、Grep、Glob。
你看不到对话，只能看到下面这份任务说明、被驳回的完成声明与缺口清单，以及当前工作区里的文件。
请以工作区里能核对到的实际内容为准，不要相信声明里自称完成的部分。`

// latestUserMessageBody 取该任务最后一条用户留言：复核判断「做没做到」只能靠
// 任务名与这句要求，不是靠任务名。
func latestUserMessageBody(ctx context.Context, store *TaskStore, taskID string) string {
	items, err := NewActivityStore(store.db).ListIncludingHidden(ctx, taskID)
	if err != nil {
		return ""
	}
	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		if item == nil || item.Role != RoleUser || strings.TrimSpace(item.Body) == "" {
			continue
		}
		return item.Body
	}
	return ""
}

// completionVerifierPrompt 把任务、声明与缺口摊给复核者。
func completionVerifierPrompt(task Task, run *WorkflowRun, report CompletionReport, summary, goal string) string {
	var b strings.Builder
	b.WriteString("请判断这个任务是否真的做完了。\n\n")
	fmt.Fprintf(&b, "任务：%s\n", strings.TrimSpace(task.Name))
	if goal != "" {
		fmt.Fprintf(&b, "用户要求：%s\n", truncateRouterText(goal, completionVerifierPromptLimit))
	}
	if summary != "" {
		fmt.Fprintf(&b, "被驳回的完成声明：%s\n", truncateRouterText(summary, completionVerifierPromptLimit))
	}
	if run != nil && len(report.Items) > 0 {
		b.WriteString("\n机检没能确认的项：\n")
		for _, line := range completionGapLines(report) {
			fmt.Fprintf(&b, "- %s\n", line)
		}
	}
	b.WriteString("\n请在工作区里核实这些产出是否真的存在、是否与任务要求一致，然后只输出一个 JSON 对象：\n")
	b.WriteString(`{"verdict":"pass|fail|uncertain","findings":["核对到的具体证据或缺失"],"missing":["还缺什么"]}` + "\n")
	b.WriteString("pass 只在你能指出具体证据时使用；看不到证据、或证据与要求对不上时用 fail；无法判断时用 uncertain。")
	return b.String()
}
