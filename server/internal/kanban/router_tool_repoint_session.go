package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// router_repoint_session 把当前任务的一条会话改到新的工作目录。collect-then-apply：
// 这里只记录动作，真正的关 pool / 搬转录 / 改绑定在应用阶段由 usecase 完成。
// target_path 留空表示项目主 checkout；路径合法性也由 usecase 判定，不在这里重复校验。
func (e *routerToolEnv) repointSessionTool() RouterToolSpec {
	return RouterToolSpec{
		Name:        "router_repoint_session",
		Description: "把当前任务的一条会话搬到新的工作目录：先填 session_key（必填），target_path 可选，留空表示项目主 checkout。会关闭该会话当前 agent pool 并切换工作目录/转录，正在回复时会被拒绝。",
		Class:       RouterToolWrite,
		InputSchema: objectSchema(map[string]any{
			"session_key": map[string]any{"type": "string", "minLength": 1},
			"target_path": map[string]any{"type": "string"},
		}, []string{"session_key"}),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				SessionKey string `json:"session_key"`
				TargetPath string `json:"target_path"`
			}
			if len(raw) > 0 && string(raw) != "null" {
				if err := json.Unmarshal(raw, &args); err != nil {
					return "", fmt.Errorf("invalid arguments: %w", err)
				}
			}
			if e == nil || e.brief == nil || e.sessionRepointer == nil {
				return "", errors.New("repoint_session: 端口未配置")
			}
			sessionKey := strings.TrimSpace(args.SessionKey)
			if sessionKey == "" {
				return "", errors.New("repoint_session requires session_key")
			}
			// root 取自 brief，不作为工具参数暴露给模型；apply 阶段用任务上的
			// RootID（重放时更权威）。这里保证当前任务确实有根。
			if strings.TrimSpace(e.brief.Task.RootID) == "" {
				return "", errors.New("repoint_session: 当前任务 root 为空")
			}
			return e.record(RouterAction{
				Kind:       RouterActionRepointSession,
				SessionKey: sessionKey,
				TargetPath: strings.TrimSpace(args.TargetPath),
			})
		},
	}
}
