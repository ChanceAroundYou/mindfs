package kanban

import (
	"context"
	"errors"
	"strings"
)

// applyRouteRepointSession 只做薄适配：重放时以任务上的 RootID 为准，
// 转录搬移/游标推进/归属清理都在 usecase 里，错误原样上返给 coordinate 链路。
func (s *ActivityService) applyRouteRepointSession(ctx context.Context, task Task, action RouterAction) error {
	if s == nil || s.sessionRepointer == nil {
		return errors.New("repoint_session: 端口未配置")
	}
	rootID := strings.TrimSpace(task.RootID)
	sessionKey := strings.TrimSpace(action.SessionKey)
	if rootID == "" {
		return errors.New("repoint_session: task root required")
	}
	if sessionKey == "" {
		return errors.New("repoint_session requires session_key")
	}
	return s.sessionRepointer.RepointSessionForRouter(ctx, SessionRepointInput{
		RootID:     rootID,
		SessionKey: sessionKey,
		TargetPath: action.TargetPath,
	})
}
