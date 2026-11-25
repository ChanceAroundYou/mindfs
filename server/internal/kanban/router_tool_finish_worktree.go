package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// router_finish_worktree 让 Router 在判断任务已经干完时把遗留的 worktree 收掉。
// status 只读，begin/teardown 走 collect-then-apply，不在工具回调里直接改文件 ——
// 工具回调可能被重放，副作用必须在 apply 阶段由幂等状态机承担。
func (e *routerToolEnv) finishWorktreeTool() RouterToolSpec {
	return RouterToolSpec{
		Name:        "router_finish_worktree",
		Description: "查询或收尾当前任务的 worktree。mode=status 只读；mode=begin 起一轮收尾回合（由 agent 自己提交合并，没有单独的提交按钮）；mode=teardown 只做机械清场。任务仍在运行时拒绝。",
		Class:       RouterToolWrite,
		InputSchema: objectSchema(map[string]any{
			"mode": map[string]any{"type": "string", "enum": []string{"status", "begin", "teardown"}},
		}, nil),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Mode string `json:"mode"`
			}
			if len(raw) > 0 && string(raw) != "null" {
				if err := json.Unmarshal(raw, &args); err != nil {
					return "", fmt.Errorf("invalid arguments: %w", err)
				}
			}
			mode := strings.TrimSpace(args.Mode)
			if mode == "" {
				mode = "status"
			}
			switch mode {
			case "status", "begin", "teardown":
			default:
				return "", fmt.Errorf("invalid mode %q", mode)
			}
			if e == nil || e.brief == nil {
				return "", errors.New("finish_worktree: no current task")
			}
			briefTask := e.brief.Task
			if !briefTask.CreateWorktree {
				return "该任务没有 worktree", nil
			}
			if briefTask.Status == StatusRunning || briefTask.Status == StatusPaused {
				return "", errors.New("finish_worktree: 任务仍在运行，不能收尾")
			}
			if mode == "status" {
				state := "可用"
				if !briefTask.WorktreePresent {
					state = "目录已不存在"
				}
				return fmt.Sprintf("worktree %s；收尾请用 mode=begin（如需只清场用 teardown）", state), nil
			}
			if e.worktreeFinisher == nil {
				return "", errors.New("finish_worktree: 收尾端口未配置")
			}
			return e.record(RouterAction{Kind: RouterActionFinishWorktree, FinishMode: mode})
		},
	}
}
