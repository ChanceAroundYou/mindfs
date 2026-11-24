package kanban

import (
	"context"
	"errors"
	"fmt"
)

// applyRouteFinishWorktree 把 Router 的收尾决定接到 Service 已有的收尾入口。
//
// 不在这里提交、不在这里拆目录：begin 只起收尾回合（commit/merge 是回合内
// agent 按显式路径做的），teardown 只做机械清场。错误留在事件终态，
// 由 Router 下一轮 notify_user，不静默成功。
func (s *ActivityService) applyRouteFinishWorktree(ctx context.Context, decision *RouterDecision, task Task, action RouterAction) error {
	if s == nil || s.worktreeFinisher == nil {
		return errors.New("finish_worktree: 收尾端口未配置")
	}
	if !task.CreateWorktree {
		return errors.New("finish_worktree: 任务没有 worktree")
	}
	switch action.FinishMode {
	case "begin":
		_, err := s.worktreeFinisher.BeginFinishWorktree(ctx, BeginFinishInput{RootID: task.RootID, TaskID: task.ID})
		return err
	case "teardown":
		_, err := s.worktreeFinisher.FinishTaskWorktree(ctx, FinishWorktreeInput{RootID: task.RootID, TaskID: task.ID})
		return err
	default:
		return fmt.Errorf("invalid finish_mode %q", action.FinishMode)
	}
}
