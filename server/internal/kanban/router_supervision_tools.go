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
	// 直连回合没有 Run：retry_node / resume_run 指向的东西不存在，只给直连
	// 专用的重开工具，外加两条「说清楚」的工具。
	if obs.Direct != nil {
		return []RouterToolSpec{e.retryDirectTool(obs), e.notifyUserTool(), e.escalateTool()}
	}
	return []RouterToolSpec{e.retryNodeTool(obs), e.notifyUserTool(), e.resumeRunTool(obs), e.escalateTool()}
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

func (e *routerToolEnv) retryDirectTool(obs *SupervisionObservation) RouterToolSpec {
	return RouterToolSpec{
		Name: "router_retry_direct",
		Description: "重开卡住的直连回合：把停在 running 的回复结算成中断，再用原始用户留言重跑一轮。" +
			"只用于 supervision.direct（直连回合）；同一轮最多自动重开一次，预算用尽时系统会自己把任务交还用户。",
		Class: RouterToolWrite,
		InputSchema: objectSchema(map[string]any{
			"reason": map[string]any{"type": "string", "description": "可选；给用户看的一句话说明"},
		}, nil),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			if obs.Direct == nil || strings.TrimSpace(obs.Direct.ActivityID) == "" {
				return "", fmt.Errorf("retry_direct needs a direct-turn supervision block")
			}
			return e.record(RouterAction{
				Kind:      RouterActionRetryDirect,
				NodeRunID: obs.Direct.ActivityID,
				Body:      truncateRouterText(strings.TrimSpace(args.Reason), supervisionSummaryLimit),
			})
		},
	}
}

func (e *routerToolEnv) resumeRunTool(obs *SupervisionObservation) RouterToolSpec {
	return RouterToolSpec{
		Name: "router_resume_run",
		Description: "唤醒这一轮 Run：让驱动重新检查并推进 ready 节点。只用于「还有节点没结束、但没有任何回合在回复」的停摆；" +
			"有节点正在回复时不要调用，证据不确定（uncertain）时改用 router_notify_user。",
		Class: RouterToolWrite,
		InputSchema: objectSchema(map[string]any{
			"reason": map[string]any{"type": "string", "description": "可选；写进唤醒原因"},
		}, nil),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			idle := 0
			for _, node := range obs.OpenNodes {
				if !node.Replying {
					idle++
				}
			}
			if len(obs.OpenNodes) == 0 || idle == 0 {
				return "", fmt.Errorf("resume_run needs at least one open node that is not replying; open nodes: %d, idle: %d", len(obs.OpenNodes), idle)
			}
			return e.record(RouterAction{
				Kind: RouterActionResumeRun,
				Body: truncateRouterText(strings.TrimSpace(args.Reason), supervisionSummaryLimit),
			})
		},
	}
}

func (e *routerToolEnv) escalateTool() RouterToolSpec {
	return RouterToolSpec{
		Name: "router_escalate",
		Description: "把决定权交还给用户：留一条可见说明并把任务放回待审核。用于重试已用尽或需要用户拍板的场景；" +
			"不会取消这一轮 Run。",
		Class: RouterToolWrite,
		InputSchema: objectSchema(map[string]any{
			"summary": map[string]any{"type": "string", "minLength": 1, "description": "试过什么、卡在哪、建议用户怎么做"},
		}, []string{"summary"}),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Summary string `json:"summary"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			summary := strings.TrimSpace(args.Summary)
			if summary == "" {
				return "", fmt.Errorf("summary required")
			}
			if len(summary) > routerActionBodyMaxBytes {
				return "", fmt.Errorf("summary exceeds %d bytes", routerActionBodyMaxBytes)
			}
			return e.record(RouterAction{Kind: RouterActionEscalate, Body: summary})
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
