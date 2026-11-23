package kanban

import (
	"context"
	"log"
	"strings"
	"time"
)

// RecoverOnStartupForRoot 是 WP-03 启动恢复的 Service 层入口：同步执行、
// 不起 goroutine（无泄漏），由 workspace_build.go 在 Kanban 装配后、
// KickPending 之前调用。
//
// probe 按 root 传入而不是挂在 Service 上：每个 root 有自己的 session manager，
// 挂在 Service 上会被下一个 root 覆盖，导致 A root 的恢复拿 B root 的会话表判定。
//
// 语义（A3 RecoverOnStartup 复用）：
//   - 可确定（探针 found && resumable）：只返回 ResumeSession，不自动重发 prompt、
//     不建 attempt——真实续跑由 KickPending/后续用户动作驱动，本函数只做判定与落库。
//   - 不可确定（含无探针时）：request 已在 RecoverOnStartup 内标 unknown，
//     这里额外把对应任务置 waiting_user（needs-user），永不自动重试。
//   - ctx 取消：RecoverOnStartup 的查询全部走 ctx，取消时返回 ctx.Err()，
//     已标的 unknown 是幂等 UPDATE，不产生半写状态。
//   - 任务不存在/store 打不开：跳过并 log，绝不阻塞服务启动。
func (s *Service) RecoverOnStartupForRoot(ctx context.Context, rootID string, probe SessionStateProbe) (RecoveryPlan, error) {
	plan := RecoveryPlan{}
	if s == nil {
		return plan, nil
	}
	rootID = strings.TrimSpace(rootID)
	if rootID == "" {
		return plan, nil
	}
	store, err := s.taskStore(rootID)
	if err != nil {
		log.Printf("[kanban/recover] root=%s store error=%v", rootID, err)
		return plan, nil
	}
	plan, err = RecoverOnStartup(ctx, store, probe)
	if err != nil {
		return plan, err
	}
	// D8/D9：WorkflowRun 的 NodeRun 与 legacy continue_requests 是两套持久行。
	// 两者都按同一份探针证据对账，但各自只改自己的状态；合并结果只为了把
	// 「哪些行已安全重排 / 哪些需要用户」统一报给调用方。
	wfPlan, wfErr := s.recoverWorkflowRunsOnStartup(ctx, rootID, store, probe)
	if wfErr != nil {
		return plan, wfErr
	}
	plan.ResumeNodeRunIDs = append(plan.ResumeNodeRunIDs, wfPlan.ResumeNodeRunIDs...)
	plan.UnknownIDs = append(plan.UnknownIDs, wfPlan.UnknownIDs...)
	plan.NeedsUser = plan.NeedsUser || wfPlan.NeedsUser
	// 升级前已结算的回复可能只有「已完成 <workflow>」：用已落库的 NodeRun 输出
	// 重算一次可读 summary。幂等，只补空 summary 的 completed 行。
	s.backfillWorkflowReplySummaries(ctx, store)
	// 直连回合（run_id 为空）的收尾没有 Run 可挂：进程重启带走 goroutine 后，
	// 活动与任务都永远停在 running。与 workflow 侧的结算同一条原则，只是另一条路。
	s.settleOrphanDirectReplies(ctx, store)
	// 第三类死角落：落在大批历史任务上的迁移审计 Run（legacy_import）。它既不在
	// workflow 对账里，回复又不属于直连孤儿，迁移时在跑的节点会永远转圈。
	s.settleLegacyImportLeftovers(ctx, store)
	// 复核回合是后台 goroutine：进程一死它就没了，verifying 行不能永远挂着。
	s.reconcileVerifyingCompletions(ctx, store)
	// 升级前遗留的孤儿等待卡：同一会话已被新 tool_use_id 重问，旧卡没人能答，
	// 却一直挡着任务对话的输入框（现场 #104）。
	s.closeSupersededQuestionsOnStartup(ctx, store)
	if plan.NeedsUser {
		for _, id := range plan.UnknownIDs {
			// 可确定性地拿到 task_id 就置 waiting_user；拿不到就只保留 unknown 标记，
			// 用户侧点开任务可见（不丢证据）。
			req, gerr := (&OutboxService{Store: store}).GetRequest(ctx, id)
			if gerr != nil {
				continue
			}
			_ = store.UpdateTaskStatus(ctx, req.TaskID, StatusWaitingUser, false)
		}
	}
	// 第四类：out-of-band 会话回合。它的状态反查不到任何回复行（会话回合不
	// 落 task_activities），标记又只在进程内 —— 重启后任务会永远停在 running。
	// 超过 30 分钟（C2）仍没结束的回合恢复成 waiting_user 并记 turn_interrupted。
	if n, oobErr := s.restoreStaleOutOfBandTasks(ctx, store, time.Now().UTC()); oobErr != nil {
		log.Printf("[kanban/recover] out-of-band recovery: %v", oobErr)
	} else if n > 0 {
		log.Printf("[kanban/recover] out-of-band recovered=%d root=%s", n, rootID)
	}
	return plan, nil
}
