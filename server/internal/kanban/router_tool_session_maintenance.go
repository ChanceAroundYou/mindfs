package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// router_session_maintenance 把 session-compress 中真正无损的部分做成 Router 能力。
// 不做思考剥离、工具负载截断或 --drop-tools：这些都会改变模型可见的对话正文。
func (e *routerToolEnv) sessionMaintenanceTool() RouterToolSpec {
	return RouterToolSpec{
		Name:        "router_session_maintenance",
		Description: "会话无损维护。action=diagnose 只读；clean_locks 清 PID 已死的锁；strip_images 把图片块换成文本占位（留 .bak）；purge_empty 删空会话，必须 confirm:true。不剥思考、不截工具负载。",
		Class:       RouterToolWrite,
		InputSchema: objectSchema(map[string]any{
			"action":      map[string]any{"type": "string", "enum": []string{"diagnose", "clean_locks", "strip_images", "purge_empty"}},
			"session_key": map[string]any{"type": "string"},
			"confirm":     map[string]any{"type": "boolean"},
		}, nil),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Action     string `json:"action"`
				SessionKey string `json:"session_key"`
				Confirm    bool   `json:"confirm"`
			}
			if len(raw) > 0 && string(raw) != "null" {
				if err := json.Unmarshal(raw, &args); err != nil {
					return "", fmt.Errorf("invalid arguments: %w", err)
				}
			}
			action := strings.TrimSpace(args.Action)
			if action == "" {
				action = "diagnose"
			}
			if e == nil || e.brief == nil || e.sessionMaintainer == nil {
				return "", errors.New("session_maintenance: 维护端口未配置")
			}
			rootID := e.brief.Task.RootID
			switch action {
			case "diagnose":
				report, err := e.sessionMaintainer.SessionMaintenanceDiagnose(ctx, rootID)
				if err != nil {
					return "", err
				}
				summary := map[string]any{
					"scanned":       report.Scanned,
					"with_images":   0,
					"stale_locks":   len(report.StaleLocks),
					"missing_paths": 0,
				}
				for _, item := range report.Sessions {
					if item.ImageBytes > 0 {
						summary["with_images"] = summary["with_images"].(int) + 1
					}
					if item.RuntimeMissing {
						summary["missing_paths"] = summary["missing_paths"].(int) + 1
					}
				}
				out, _ := json.Marshal(summary)
				return string(out), nil
			case "clean_locks":
				return e.record(RouterAction{Kind: RouterActionSessionMaintenance, MaintenanceAction: action})
			case "strip_images", "purge_empty":
				sessionKey := strings.TrimSpace(args.SessionKey)
				if sessionKey == "" {
					return "", fmt.Errorf("%s requires session_key", action)
				}
				if action == "purge_empty" && !args.Confirm {
					return "", errors.New("purge_empty requires confirm:true")
				}
				return e.record(RouterAction{
					Kind:              RouterActionSessionMaintenance,
					MaintenanceAction: action,
					SessionKey:        sessionKey,
					Confirm:           args.Confirm,
				})
			default:
				return "", fmt.Errorf("invalid action %q", action)
			}
		},
	}
}
