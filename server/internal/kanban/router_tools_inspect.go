package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// inspectNodeExcerpt 是单节点详情的产出上限：比卡片摘要宽，仍不接近转录。
const inspectNodeExcerpt = 4000

// inspectNodeGraphStatus 在还没有运行行时回答「这个节点在图里吗、现在是什么状态」。
func inspectNodeGraphStatus(ctx context.Context, store *TaskStore, target *RouterBriefRun, runID, nodeID string) (string, bool) {
	if target != nil {
		for _, card := range target.Nodes {
			if card.NodeID == nodeID {
				return card.Status, true
			}
		}
		return "", false
	}
	if store == nil {
		return "", false
	}
	run, err := store.WorkflowRunByID(ctx, runID)
	if err != nil {
		return "", false
	}
	if _, ok := run.Graph.node(nodeID); !ok {
		return "", false
	}
	if status := run.NodeStates[nodeID]; status != "" {
		return status, true
	}
	return NodeRunPending, true
}

// latestNodeRunOf 取某节点最近一次（attempt 最大）的顶层行。
func latestNodeRunOf(nodes []NodeRun, nodeID string) (NodeRun, bool) {
	var latest NodeRun
	found := false
	for _, node := range nodes {
		if node.NodeID != nodeID || node.LoopPath != "" {
			continue
		}
		if !found || node.Attempt >= latest.Attempt {
			latest, found = node, true
		}
	}
	return latest, found
}

// router_inspect_run 的只读实现。独立成文件是为了守住 500 行硬线：工具定义与
// 守卫校验在 router_tools.go，这里只放「读一个 Run 的节点证据」。

func (e *routerToolEnv) inspectRunTool() RouterToolSpec {
	return RouterToolSpec{
		Name: "router_inspect_run",
		Description: "只读：查看最近一次 WorkflowRun 的节点终态与输出摘要。不修改任何状态。" +
			"带 node_id 时只返回该节点的详情（最近一次 attempt 的产出），不重复整份节点清单，也不读转录。",
		Class: RouterToolRead,
		InputSchema: objectSchema(map[string]any{
			"run_id":  map[string]any{"type": "string", "description": "可选；必须是 brief.latest_run.id"},
			"node_id": map[string]any{"type": "string", "description": "可选；只看这一个节点"},
		}, nil),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				RunID  string `json:"run_id"`
				NodeID string `json:"node_id"`
			}
			_ = json.Unmarshal(raw, &args)
			return e.inspectRun(ctx, strings.TrimSpace(args.RunID), strings.TrimSpace(args.NodeID))
		},
	}
}

// inspectRun 是唯一的只读工具：给模型看一个 Run 的节点终态与输出摘要。
//
// 目标 Run 由 brief 的形态决定，不能靠「任务最新 Run」兜底：
//   - 用户留言回合：brief.LatestRun 就是这一轮要看的 Run；
//   - 运行期事件回合：brief.LatestRun 被刻意置 nil（事件 brief 不许让模型把任务
//     最新 Run 当成事件的 Run），目标 Run 是 brief.Event.RunID。
//
// 事件回合以前在这里直接报 "no workflow run"，而 spec 明确允许 inspect_run ——
// 于是模型只能凭摘要猜事件那个 Run 里到底哪个节点失败了。
func (e *routerToolEnv) inspectRun(ctx context.Context, runID, nodeID string) (string, error) {
	targetRunID, target := "", (*RouterBriefRun)(nil)
	switch {
	case e.brief == nil:
		return "", errors.New("no situation brief for this turn")
	case e.brief.Event != nil:
		targetRunID = strings.TrimSpace(e.brief.Event.RunID)
		if targetRunID == "" {
			return "", errors.New("runtime event has no run id")
		}
	case e.brief.LatestRun != nil:
		targetRunID, target = e.brief.LatestRun.ID, e.brief.LatestRun
	default:
		return "", errors.New("no workflow run for this task")
	}
	if runID != "" && runID != targetRunID {
		return "", fmt.Errorf("run %s is not the run of this turn (%s)", runID, targetRunID)
	}
	payload := map[string]any{}
	if target != nil {
		payload["run"] = target
	} else if e.store != nil {
		// 事件 brief 有意不展开 Run 摘要（它描述节点证据，不重写 Run 快照）：
		// 这里按 ID 取，取不到就如实报错，不拿别的 Run 顶替。
		run, err := e.store.WorkflowRunByID(ctx, targetRunID)
		if err != nil {
			return "", fmt.Errorf("load run %s: %w", targetRunID, err)
		}
		task, taskErr := e.store.GetTask(ctx, run.TaskID)
		if taskErr != nil {
			return "", fmt.Errorf("load task %s: %w", run.TaskID, taskErr)
		}
		payload["run"] = briefRun(ctx, e.store, run, task, nil)
	}
	if e.store != nil {
		if nodes, err := e.store.ListWorkflowNodeRuns(ctx, targetRunID); err == nil && nodeID != "" {
			// 单节点详情：最近一次 attempt 的产出，够回答「它上次到底说了什么」，
			// 又不把整份节点清单再发一遍。
			latest, ok := latestNodeRunOf(nodes, nodeID)
			if !ok {
				// 还没跑过的节点也允许看：报它在图里的状态，而不是「不存在」。
				status, inGraph := inspectNodeGraphStatus(ctx, e.store, target, targetRunID, nodeID)
				if !inGraph {
					return "", fmt.Errorf("节点 %q 不在本轮 Run 里", nodeID)
				}
				payload["node"] = map[string]any{"node_id": nodeID, "status": status, "attempt": 0}
			} else {
				payload["node"] = map[string]any{
					"node_id":     latest.NodeID,
					"attempt":     latest.Attempt,
					"status":      latest.Status,
					"summary":     truncateRouterText(nodeRunText(latest, "summary", "reason", "error"), inspectNodeExcerpt),
					"output":      latest.Output,
					"session_key": latest.SessionKey,
				}
			}
		} else if nodes, err := e.store.ListWorkflowNodeRuns(ctx, targetRunID); err == nil {
			items := make([]map[string]any, 0, len(nodes))
			for _, node := range nodes {
				items = append(items, map[string]any{
					"node_id":     node.NodeID,
					"loop_path":   node.LoopPath,
					"iteration":   node.Iteration,
					"attempt":     node.Attempt,
					"status":      node.Status,
					"summary":     truncateRouterText(nodeRunText(node, "summary", "reason", "error"), 500),
					"session_key": node.SessionKey,
				})
			}
			payload["nodes"] = items
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
