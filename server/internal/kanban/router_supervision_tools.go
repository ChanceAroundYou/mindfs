package kanban

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// 监管写入工具的暴露条件：实例开着监管（mode != off）时，五个工具名在**每一轮**
// 都暴露，不管这一轮有没有观测块。
//
// 原因是会话级的：Router 会话只在 OpenSession 时把工具注册给 CLI，之后的回合只能
// 按名字换 handler（api/router_runtime.go 的 routerToolBindings）。事件回合再追加
// 的工具模型根本看不到 —— 隔离实例实测 2026-11-20：用户留言回合开出来的会话，到
// run_failed 回合里模型直接回「当前会话里没有可用的重试工具」。所以名字固定，
// 形状与可执行性由 handler 在调用时按本轮观测判断。

func (e *routerToolEnv) supervisionTools() []RouterToolSpec {
	if e == nil || (e.supervisionMode != RouterSupervisionOn && e.supervisionMode != RouterSupervisionShadow) {
		return nil
	}
	obs := e.supervisionObservation()
	return []RouterToolSpec{
		e.retryNodeTool(obs),
		e.cancelNodeTool(obs),
		e.cancelRunTool(obs),
		e.cancelTaskTool(obs),
		e.retryDirectTool(obs),
		e.resumeRunTool(obs),
		e.notifyUserTool(obs),
		e.escalateTool(obs),
	}
}

// supervisionObservation 返回本轮的观测块；没有观测（普通留言回合、mode=off）
// 时返回 nil，各 handler 据此拒绝调用。
func (e *routerToolEnv) supervisionObservation() *SupervisionObservation {
	if e == nil || e.brief == nil || e.brief.Supervision == nil {
		return nil
	}
	if e.brief.Supervision.Mode == "" || e.brief.Supervision.Mode == RouterSupervisionOff {
		return nil
	}
	return e.brief.Supervision
}

// errNoSupervisionTurn 是「这一轮没有监管观测」的统一拒绝文案。
func errNoSupervisionTurn(name string) error {
	return fmt.Errorf("%s is only usable on a failed/stalled event turn; this turn has no supervision observation", name)
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
			if obs == nil {
				return "", errNoSupervisionTurn("router_retry_node")
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

func (e *routerToolEnv) cancelNodeTool(obs *SupervisionObservation) RouterToolSpec {
	return RouterToolSpec{
		Name: "router_cancel_node",
		Description: "取消本轮 Run 里一个卡住或不该继续的节点。最小够用原则：能停一个节点就别停整轮。" +
			"等待审批的 human_gate 节点不能取消（审批只能由人批准或驳回）；取消会保留 worktree 与会话，下游按跳过语义收敛。",
		Class: RouterToolWrite,
		InputSchema: objectSchema(map[string]any{
			"node_id": map[string]any{"type": "string", "minLength": 1, "description": "supervision.open_nodes[].node_id"},
			"reason":  map[string]any{"type": "string", "description": "可选；写进取消账本的原因"},
		}, []string{"node_id"}),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				NodeID string `json:"node_id"`
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			if obs == nil {
				return "", errNoSupervisionTurn("router_cancel_node")
			}
			nodeID := strings.TrimSpace(args.NodeID)
			if nodeID == "" {
				return "", fmt.Errorf("node_id required; open nodes: %s", supervisionOpenNodeList(obs))
			}
			var target *SupervisionNodeState
			for i := range obs.OpenNodes {
				if obs.OpenNodes[i].NodeID == nodeID {
					target = &obs.OpenNodes[i]
					break
				}
			}
			if target == nil {
				return "", fmt.Errorf("node %s is not an open node of this run; open nodes: %s", nodeID, supervisionOpenNodeList(obs))
			}
			return e.record(RouterAction{
				Kind:      RouterActionCancelNode,
				NodeRunID: target.NodeRunID,
				Reason:    target.NodeID,
				Body:      truncateRouterText(strings.TrimSpace(args.Reason), supervisionSummaryLimit),
			})
		},
	}
}

func (e *routerToolEnv) cancelRunTool(obs *SupervisionObservation) RouterToolSpec {
	return RouterToolSpec{
		Name: "router_cancel_run",
		Description: "停掉这一轮 Run：开着的节点全部取消、运行器停止，任务回到 waiting_user 交给用户决定。" +
			"任务本身不会被取消（那要用 router_cancel_task）。只在节点级取消解决不了问题时使用。",
		Class: RouterToolWrite,
		InputSchema: objectSchema(map[string]any{
			"reason": map[string]any{"type": "string", "description": "可选；写进取消账本的原因"},
		}, nil),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			if obs == nil {
				return "", errNoSupervisionTurn("router_cancel_run")
			}
			return e.record(RouterAction{
				Kind: RouterActionCancelRun,
				Body: truncateRouterText(strings.TrimSpace(args.Reason), supervisionSummaryLimit),
			})
		},
	}
}

func (e *routerToolEnv) cancelTaskTool(obs *SupervisionObservation) RouterToolSpec {
	return RouterToolSpec{
		Name: "router_cancel_task",
		Description: "取消整个任务（终态）。只在用户明确不要这个任务、或任务已无法继续时使用；" +
			"普通失败/卡住请用节点级或 Run 级取消，把现场留给用户。",
		Class: RouterToolWrite,
		InputSchema: objectSchema(map[string]any{
			"reason": map[string]any{"type": "string", "minLength": 1, "description": "必填；会写进任务时间线"},
		}, []string{"reason"}),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			if obs == nil {
				return "", errNoSupervisionTurn("router_cancel_task")
			}
			reason := strings.TrimSpace(args.Reason)
			if reason == "" {
				return "", fmt.Errorf("reason required: cancelling a task needs a user-visible explanation")
			}
			return e.record(RouterAction{
				Kind: RouterActionCancelTask,
				Body: truncateRouterText(reason, supervisionSummaryLimit),
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
			if obs == nil {
				return "", errNoSupervisionTurn("router_retry_direct")
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
			if obs == nil {
				return "", errNoSupervisionTurn("router_resume_run")
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

func (e *routerToolEnv) escalateTool(obs *SupervisionObservation) RouterToolSpec {
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
			if obs == nil {
				return "", errNoSupervisionTurn("router_escalate")
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

func (e *routerToolEnv) notifyUserTool(obs *SupervisionObservation) RouterToolSpec {
	return RouterToolSpec{
		Name:        "router_notify_user",
		Description: "在任务里留一条可见的系统说明（用户会看到）。用于证据不确定、失败不可重试、或已经替用户重试过需要告知的场景。",
		Class:       RouterToolWrite,
		InputSchema: objectSchema(map[string]any{
			"body": map[string]any{"type": "string", "minLength": 1, "description": "给用户看的说明"},
			"severity": map[string]any{
				"type": "string", "enum": []string{ActivitySeverityInfo, ActivitySeverityWarning, ActivitySeverityError},
				"description": "这条提醒的紧急程度：info=普通提醒（默认），warning=需要留意/降级，error=这一轮没做成或需要立刻处理",
			},
		}, []string{"body"}),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Body     string `json:"body"`
				Severity string `json:"severity"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			if obs == nil {
				return "", errNoSupervisionTurn("router_notify_user")
			}
			body := strings.TrimSpace(args.Body)
			if body == "" {
				return "", fmt.Errorf("body required")
			}
			if len(body) > routerActionBodyMaxBytes {
				return "", fmt.Errorf("body exceeds %d bytes", routerActionBodyMaxBytes)
			}
			severity := strings.TrimSpace(args.Severity)
			switch severity {
			case "":
				severity = ActivitySeverityInfo
			case ActivitySeverityInfo, ActivitySeverityWarning, ActivitySeverityError:
			default:
				return "", fmt.Errorf("invalid severity %q (want info|warning|error)", severity)
			}
			return e.record(RouterAction{Kind: RouterActionNotifyUser, Body: body, Severity: severity})
		},
	}
}
