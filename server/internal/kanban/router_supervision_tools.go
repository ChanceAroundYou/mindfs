package kanban

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// 监管写入工具的暴露条件：本轮 brief 挂着一份 mode != off 的 supervision 观测。
// 普通用户留言回合、shadow 之外没有观测的事件回合都看不到这两个工具。

func (e *routerToolEnv) supervisionTools() []RouterToolSpec {
	obs := e.supervisionObservation()
	if obs == nil {
		return nil
	}
	return []RouterToolSpec{e.retryNodeTool(obs), e.notifyUserTool()}
}

func (e *routerToolEnv) supervisionObservation() *SupervisionObservation {
	if e == nil || e.brief == nil || e.brief.Supervision == nil {
		return nil
	}
	if e.brief.Supervision.Mode == "" || e.brief.Supervision.Mode == RouterSupervisionOff {
		return nil
	}
	return e.brief.Supervision
}

func (e *routerToolEnv) retryNodeTool(obs *SupervisionObservation) RouterToolSpec {
	return RouterToolSpec{
		Name: "router_retry_node",
		Description: "重试本轮 Run 里一个失败节点（attempt+1，沿用原会话）。只能用于 failure_kind=retryable 的节点；" +
			"uncertain/non_retryable 一律改用 router_notify_user 说明情况。受每节点重试次数与每小时动作预算限制。",
		Class: RouterToolWrite,
		InputSchema: objectSchema(map[string]any{
			"node_id": map[string]any{"type": "string", "minLength": 1, "description": "supervision.failed_nodes[].node_id"},
			"reason":  map[string]any{"type": "string", "description": "可选；写进重试账本的原因"},
		}, []string{"node_id"}),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				NodeID string `json:"node_id"`
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			nodeID := strings.TrimSpace(args.NodeID)
			if nodeID == "" {
				return "", fmt.Errorf("node_id required; failed nodes: %s", supervisionFailedNodeList(obs))
			}
			var target *SupervisionNodeState
			for i := range obs.FailedNodes {
				if obs.FailedNodes[i].NodeID == nodeID {
					target = &obs.FailedNodes[i]
					break
				}
			}
			if target == nil {
				return "", fmt.Errorf("node %s is not a failed node of this run; failed nodes: %s", nodeID, supervisionFailedNodeList(obs))
			}
			return e.record(RouterAction{
				Kind:      RouterActionRetryNode,
				NodeRunID: target.NodeRunID,
				Reason:    target.NodeID,
				Body:      truncateRouterText(strings.TrimSpace(args.Reason), supervisionSummaryLimit),
			})
		},
	}
}

func (e *routerToolEnv) notifyUserTool() RouterToolSpec {
	return RouterToolSpec{
		Name:        "router_notify_user",
		Description: "在任务里留一条可见的系统说明（用户会看到）。用于证据不确定、失败不可重试、或已经替用户重试过需要告知的场景。",
		Class:       RouterToolWrite,
		InputSchema: objectSchema(map[string]any{
			"body": map[string]any{"type": "string", "minLength": 1, "description": "给用户看的说明"},
		}, []string{"body"}),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Body string `json:"body"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			body := strings.TrimSpace(args.Body)
			if body == "" {
				return "", fmt.Errorf("body required")
			}
			if len(body) > routerActionBodyMaxBytes {
				return "", fmt.Errorf("body exceeds %d bytes", routerActionBodyMaxBytes)
			}
			return e.record(RouterAction{Kind: RouterActionNotifyUser, Body: body})
		},
	}
}
