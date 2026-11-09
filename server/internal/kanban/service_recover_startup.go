package kanban

import (
	"context"
	"log"
	"strings"
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
	return plan, nil
}
