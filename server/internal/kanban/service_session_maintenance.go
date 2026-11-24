package kanban

import (
	"context"
	"errors"
	"fmt"
)

// SetSessionMaintainer 由装配期注入；零值 nil 时 Router 工具显式报错。
func (s *Service) SetSessionMaintainer(port ActivitySessionMaintainer) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.sessionMaintainer = port
	s.mu.Unlock()
}

// applyRouteSessionMaintenance 只调用 usecase 暴露的无损维护能力。
// 动作在 collect-then-apply 阶段执行，重放决策时由框架按 effect ID 去重。
func (s *ActivityService) applyRouteSessionMaintenance(ctx context.Context, decision *RouterDecision, task Task, action RouterAction) error {
	if s == nil || s.sessionMaintainer == nil {
		return errors.New("session_maintenance: 维护端口未配置")
	}
	rootID := task.RootID
	switch action.MaintenanceAction {
	case "clean_locks":
		_, err := s.sessionMaintainer.SessionMaintenanceCleanLocks(ctx)
		return err
	case "strip_images":
		_, err := s.sessionMaintainer.SessionMaintenanceCleanImages(ctx, rootID, action.SessionKey)
		return err
	case "purge_empty":
		if !action.Confirm {
			return errors.New("purge_empty requires confirm:true")
		}
		_, err := s.sessionMaintainer.SessionMaintenancePurgeEmpty(ctx, rootID, action.SessionKey, true)
		return err
	default:
		return fmt.Errorf("invalid maintenance_action %q", action.MaintenanceAction)
	}
}
