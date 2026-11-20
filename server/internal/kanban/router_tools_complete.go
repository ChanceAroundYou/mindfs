package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// complete_task 工具（Plan 2026-11-20 Task 4）。它是 Router 唯一能主动宣告
// 「这轮做完了」的入口：此前只有 say，收尾只能靠用户自己判断。
//
// 这里只记录意图，不判真伪 —— 收尾是否成立由完成契约的证据门在 apply 阶段
// 核对（EvaluateCompletion）。工具不读库、不改状态，保持 write 工具的
// collect-then-apply 约定。
func (e *routerToolEnv) completeTaskTool() RouterToolSpec {
	return RouterToolSpec{
		Name: "router_complete_task",
		Description: "宣告这一轮任务已经做完。只有在你按完成契约逐条核对、确认证据都成立时才用；" +
			"summary 写给用户看，说明实际完成了什么。",
		Class: RouterToolWrite,
		InputSchema: objectSchema(map[string]any{
			"summary": map[string]any{"type": "string", "minLength": 1},
			"reason":  map[string]any{"type": "string"},
		}, []string{"summary"}),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Summary string `json:"summary"`
				Reason  string `json:"reason"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			summary := strings.TrimSpace(args.Summary)
			if summary == "" {
				return "", errors.New("summary required")
			}
			return e.record(RouterAction{
				Kind:    RouterActionComplete,
				RunID:   completionBriefRunIDOf(e.brief),
				Summary: truncateRouterText(summary, 500),
				Reason:  truncateRouterText(args.Reason, 500),
			})
		},
	}
}
