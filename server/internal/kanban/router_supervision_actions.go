package kanban

import (
	"errors"
	"fmt"
	"strings"
)

// 主动监管的写入动作（S1 影子 / S2 有限执行）。它们只在带 supervision 块的
// 运行期事件回合可用：观测（S0）给出「哪些节点失败、失败属于哪一类、预算还剩
// 多少」，这里把观测变成动作，并守住预算与证据边界。

const supervisionRetryReason = "supervisor_retry"

// supervisionBudgetError 把「预算用尽」与其他校验失败分开：预算用尽不是模型
// 犯错，而是这台机器该停手了 —— 回合要停在 waiting_user 并把情况写给用户，
// 而不是把事件标成失败后悄悄收场。
type supervisionBudgetError struct{ reason string }

func (e *supervisionBudgetError) Error() string { return e.reason }

func isSupervisionBudgetError(err error) bool {
	var target *supervisionBudgetError
	return errors.As(err, &target)
}

// validateSupervisionActions 校验监管写入动作。影子模式同样要过这一关：影子
// 记录的是「本来会发生什么」，因此可重试性、预算与参数都必须与 on 一致。
func validateSupervisionActions(brief *SituationBrief, actions []RouterAction, config RouterConfig) error {
	cfg := config.Supervision.Normalized()
	var obs *SupervisionObservation
	if brief != nil {
		obs = brief.Supervision
	}
	for _, action := range actions {
		switch action.Kind {
		case RouterActionRetryNode:
			if obs == nil {
				return errors.New("retry_node requires a supervision block; this turn has none")
			}
			target, ok := supervisionFailedNode(obs, action)
			if !ok {
				return fmt.Errorf("retry_node targets an unknown failed node (node_run_id=%q): %s",
					action.NodeRunID, supervisionFailedNodeList(obs))
			}
			switch target.FailureKind {
			case SupervisionFailureRetryable:
			case SupervisionFailureUncertain:
				return fmt.Errorf("retry of node %s is not allowed: failure evidence is uncertain; use router_notify_user instead", target.NodeID)
			default:
				return fmt.Errorf("retry of node %s is not allowed: failure is %s; use router_notify_user instead", target.NodeID, target.FailureKind)
			}
			if target.RetriesUsed >= cfg.MaxRetriesPerNode {
				return &supervisionBudgetError{reason: fmt.Sprintf(
					"supervision retry budget exhausted for node %s (%d/%d)", target.NodeID, target.RetriesUsed, cfg.MaxRetriesPerNode)}
			}
			if obs.ActionsLastHour >= cfg.MaxActionsPerHour {
				return &supervisionBudgetError{reason: fmt.Sprintf(
					"supervision action budget exhausted (%d/%d actions in the last hour)", obs.ActionsLastHour, cfg.MaxActionsPerHour)}
			}
		case RouterActionNotifyUser:
			if strings.TrimSpace(action.Body) == "" {
				return errors.New("notify_user body required")
			}
			if len(action.Body) > routerActionBodyMaxBytes {
				return fmt.Errorf("notify_user body exceeds %d bytes", routerActionBodyMaxBytes)
			}
		}
	}
	return nil
}

// supervisionFailedNode 按工具当轮解析出的 NodeRunID 定位失败节点；缺 ID 时
// 退回 NodeID（Reason 字段由工具填成节点名），保证手工构造的动作也能被校验。
func supervisionFailedNode(obs *SupervisionObservation, action RouterAction) (SupervisionNodeState, bool) {
	if obs == nil {
		return SupervisionNodeState{}, false
	}
	nodeRunID := strings.TrimSpace(action.NodeRunID)
	nodeID := strings.TrimSpace(action.Reason)
	for _, node := range obs.FailedNodes {
		if nodeRunID != "" && node.NodeRunID == nodeRunID {
			return node, true
		}
		if nodeRunID == "" && nodeID != "" && node.NodeID == nodeID {
			return node, true
		}
	}
	return SupervisionNodeState{}, false
}

func supervisionFailedNodeList(obs *SupervisionObservation) string {
	if obs == nil || len(obs.FailedNodes) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(obs.FailedNodes))
	for _, node := range obs.FailedNodes {
		parts = append(parts, fmt.Sprintf("%s(%s)", node.NodeID, node.FailureKind))
	}
	return strings.Join(parts, ", ")
}

// supervisionActionSplit 按模式切分动作：
//   - on：监管动作照常执行；
//   - shadow：监管动作只留在决策里（remembered），不产生任何副作用；
//   - 汇报类动作两种模式都执行。
func supervisionActionSplit(mode RouterSupervisionMode, actions []RouterAction) (applicable, shadowed []RouterAction) {
	for _, action := range actions {
		switch action.Kind {
		case RouterActionRetryNode, RouterActionNotifyUser:
			if mode == RouterSupervisionOn {
				applicable = append(applicable, action)
				continue
			}
			shadowed = append(shadowed, action)
		default:
			applicable = append(applicable, action)
		}
	}
	return applicable, shadowed
}
