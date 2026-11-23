package kanban

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// emitStartupReconcileRouterEvent 记录「这个 Run 重启后被怎么处理」。
//
// 正常扫描对显式发起的 Run（triggered_by=user、没有 router_decision_id）是刻意
// 跳过的：那不属于 Router 的生命周期。启动对账不一样 —— 把在跑节点标成 unknown、
// 把回复结算成 cancelled 是用户必须知道的事实，不能被同一个 ownership 闸门吞掉。
// 这里只放宽 startup_reconcile 这一种事件，其余语义不变；没有用户留言可挂的事件
// 仍由事件回合的既有闸门跳过。
func (s *Service) emitStartupReconcileRouterEvent(ctx context.Context, store *TaskStore, runID string, extra map[string]any) error {
	if s == nil || store == nil {
		return errors.New("kanban service and task store required")
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return errors.New("router event run id required")
	}
	run, err := store.WorkflowRunByID(ctx, runID)
	if err != nil {
		return err
	}
	if run == nil {
		return fmt.Errorf("workflow run %s not found", runID)
	}
	task, err := store.GetTask(ctx, run.TaskID)
	if err != nil {
		return err
	}
	origin, err := s.routerEventOriginForRun(ctx, store, run)
	if err != nil {
		return err
	}
	if !origin.owned {
		userActivityID, err := startupReconcileUserActivity(ctx, store, run)
		if err != nil {
			return err
		}
		origin = routerEventOrigin{
			owned:          true,
			userActivityID: userActivityID,
			reason:         "startup reconcile on explicit run",
		}
	}
	now := time.Now().UTC()
	instanceKey, err := RouterEventInstanceKey(RouterEventStartupReconcile, run.ID, "", 0, now)
	if err != nil {
		return err
	}
	_, _, err = s.insertRouterEventForRun(ctx, store, task, run, origin, RouterEventStartupReconcile,
		instanceKey, 0, extra, "", now)
	return err
}

// startupReconcileUserActivity 找这条 Run 的「来源留言」：显式 Run 记为
// activity_id 的留言（必须是本任务的用户消息）优先，取不到再回溯到 Run 创建前
// 最近一条用户留言。
func startupReconcileUserActivity(ctx context.Context, store *TaskStore, run *WorkflowRun) (string, error) {
	activities := NewActivityStore(store.db)
	if id := strings.TrimSpace(run.ActivityID); id != "" {
		act, err := activities.Get(ctx, id)
		switch {
		case err == nil && act != nil:
			if act.TaskID == run.TaskID && act.Role == RoleUser {
				return act.ID, nil
			}
		case errors.Is(err, ErrActivityNotFound):
		default:
			return "", err
		}
	}
	act, err := activities.PrecedingUserMessage(ctx, run.TaskID, run.CreatedAt, "")
	if err != nil || act == nil {
		return "", err
	}
	return act.ID, nil
}
