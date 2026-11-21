package kanban

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// record_acceptance 的应用路径（Plan 2026-11-21 W1 T3）。它只写验收项，不改
// 任务状态；重复应用同一决策是幂等的（按 (task, run) 整体替换）。
func (s *ActivityService) applyRouteRecordAcceptance(ctx context.Context, decision *RouterDecision, task Task, action RouterAction) error {
	if s == nil || s.tasks == nil {
		return errors.New("activity service task store required")
	}
	runID := strings.TrimSpace(action.RunID)
	if runID == "" {
		// 事件回合的动作可能没带 run_id：绑到任务当前这一轮，否则证据门按
		// run 取验收项永远取不到，登记了等于没登记。直连任务本来就没有 Run，
		// 保持空 run_id（空 = 直连命名空间）。
		run, err := s.tasks.WorkflowRunForTask(ctx, task.ID)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return err
		case run != nil:
			runID = run.ID
		}
	}
	_, err := s.tasks.ReplaceAcceptanceItems(ctx, task.ID, runID, action.Items)
	return err
}
