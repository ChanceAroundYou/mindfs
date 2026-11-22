package kanban

import (
	"errors"
	"fmt"
	"strings"
)

// 主动监管的写入动作（S1 影子 / S2 有限执行）。它们只在带 supervision 块的
// 运行期事件回合可用：观测（S0）给出「哪些节点失败、失败属于哪一类、预算还剩
// 多少」，这里把观测变成动作，并守住预算与证据边界。

const (
	supervisionRetryReason  = "supervisor_retry"
	supervisionResumeReason = "supervisor_resume"
)

// supervisionIsWriteAction 报这个动作是不是「监管写入」：影子模式只记录、on 才执行。
func supervisionIsWriteAction(kind RouterActionKind) bool {
	switch kind {
	case RouterActionRetryNode, RouterActionNotifyUser, RouterActionResumeRun, RouterActionEscalate,
		RouterActionRetryDirect, RouterActionCancelNode, RouterActionCancelRun, RouterActionCancelTask:
		return true
	default:
		return false
	}
}

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
			case SupervisionFailureRetryable, SupervisionFailureQuota:
			case SupervisionFailureUncertain:
				return fmt.Errorf("retry of node %s is not allowed: failure evidence is uncertain; use router_notify_user instead", target.NodeID)
			case SupervisionFailureEvidenceFailed:
				// 补做预算由契约的 MaxRepairs 把关（超出预算时压根不会有本事件），
				// 不能再叠一层每节点重试预算 —— 那两个数字量的不是同一件事。
			default:
				return fmt.Errorf("retry of node %s is not allowed: failure is %s; use router_notify_user instead", target.NodeID, target.FailureKind)
			}
			if target.FailureKind != SupervisionFailureEvidenceFailed && target.RetriesUsed >= cfg.MaxRetriesPerNode {
				return &supervisionBudgetError{reason: fmt.Sprintf(
					"supervision retry budget exhausted for node %s (%d/%d)", target.NodeID, target.RetriesUsed, cfg.MaxRetriesPerNode)}
			}
		case RouterActionCancelNode, RouterActionCancelRun, RouterActionCancelTask:
			if obs == nil {
				return fmt.Errorf("%s requires a supervision block; this turn has none", action.Kind)
			}
			if err := validateCancelAction(obs, action); err != nil {
				return err
			}
		case RouterActionResumeRun:
			if obs == nil {
				return errors.New("resume_run requires a supervision block; this turn has none")
			}
			// 证据不明时唤醒等于替用户赌一把：先让 notify/escalate 说清情况。
			for _, node := range obs.FailedNodes {
				if node.FailureKind == SupervisionFailureUncertain {
					return fmt.Errorf("resume_run is not allowed: failure evidence for node %s is uncertain; use router_notify_user instead", node.NodeID)
				}
			}
			if len(obs.OpenNodes) == 0 {
				return errors.New("resume_run is not allowed: this run has no open nodes left")
			}
			idle := 0
			for _, node := range obs.OpenNodes {
				if !node.Replying {
					idle++
				}
			}
			if idle == 0 {
				return errors.New("resume_run is not allowed: every open node is still replying")
			}
		case RouterActionRetryDirect:
			if obs == nil || obs.Direct == nil {
				return errors.New("retry_direct requires a direct-turn supervision block; this turn has none")
			}
			activityID := strings.TrimSpace(action.NodeRunID)
			if activityID == "" || activityID != obs.Direct.ActivityID {
				return fmt.Errorf("retry_direct targets an unknown stalled reply (activity=%q, expected %q)",
					action.NodeRunID, obs.Direct.ActivityID)
			}
			if obs.Direct.Interrupted {
				// 重启带走的回合：旧回复已由启动对账结算成 cancelled，不会再有人
				// 推进它。所以「仍停在 pending/running」与「会话还在回复」这两个
				// 前提都不成立，只看重开次数（用户裁定至多 3 次）。
			} else {
				if obs.Direct.Status != ActivityStatusPending && obs.Direct.Status != ActivityStatusRunning {
					return fmt.Errorf("retry_direct is not allowed: reply %s is %s, not pending/running", activityID, obs.Direct.Status)
				}
				// 水位停滞时探针可能仍报 replying —— 那正是这一档存在的理由。
				if obs.Direct.Replying && !obs.Direct.StalledByWatermark {
					return errors.New("retry_direct is not allowed: the task session is still replying")
				}
			}
			limit := obs.Direct.MaxRetries
			if limit <= 0 {
				limit = supervisionDirectMaxRetries
			}
			if !obs.Direct.RetryAllowed || obs.Direct.RetriesUsed >= limit {
				return &supervisionBudgetError{reason: fmt.Sprintf(
					"supervision direct retry budget exhausted for reply %s (%d/%d)",
					activityID, obs.Direct.RetriesUsed, limit)}
			}
		case RouterActionEscalate:
			if obs == nil {
				return errors.New("escalate requires a supervision block; this turn has none")
			}
			if strings.TrimSpace(action.Body) == "" {
				return errors.New("escalate summary required")
			}
			if len(action.Body) > routerActionBodyMaxBytes {
				return fmt.Errorf("escalate summary exceeds %d bytes", routerActionBodyMaxBytes)
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
	// 取消是收口动作：可以与 say / notify_user 同轮（先说明再收），但不能和
	// 重试 / 唤醒 / 答复 / 开工混在一起 —— 那等于把没解释清楚的现场留给下一条执行。
	cancels := 0
	for _, action := range actions {
		switch action.Kind {
		case RouterActionCancelNode, RouterActionCancelRun, RouterActionCancelTask:
			cancels++
		case RouterActionSay, RouterActionNotifyUser:
		default:
			if cancels > 0 {
				return fmt.Errorf("%s cannot be combined with a cancel action", action.Kind)
			}
		}
	}
	if cancels > 1 {
		return errors.New("at most one cancel action per turn")
	}
	// 预算按「这一轮实际要花几个副作用动作」判，两个窗口独立：本 Run 12 个、同任务
	// 滚动一小时 20 个（用户裁定 W1 T2）。用尽时返回 typed error，回合停在
	// waiting_user 并留可见说明 —— 绝不自动取消（预算不是收口的理由）。
	if spend := supervisionSideEffectActionCount(actions); spend > 0 && obs != nil {
		if obs.ActionsThisRun+spend > cfg.MaxActionsPerRun {
			return &supervisionBudgetError{reason: fmt.Sprintf(
				"supervision action budget exhausted for this run (%d/%d actions; this turn asks for %d)",
				obs.ActionsThisRun, cfg.MaxActionsPerRun, spend)}
		}
		if obs.ActionsLastHour+spend > cfg.MaxActionsPerHour {
			return &supervisionBudgetError{reason: fmt.Sprintf(
				"supervision action budget exhausted (%d/%d actions in the last hour; this turn asks for %d)",
				obs.ActionsLastHour, cfg.MaxActionsPerHour, spend)}
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
	for _, nodes := range [][]SupervisionNodeState{obs.FailedNodes, obs.EvidenceFailedNodes} {
		for _, node := range nodes {
			if nodeRunID != "" && node.NodeRunID == nodeRunID {
				return node, true
			}
			if nodeRunID == "" && nodeID != "" && node.NodeID == nodeID {
				return node, true
			}
		}
	}
	return SupervisionNodeState{}, false
}

// validateCancelAction 校验取消三档的参数与保护边界。最小够用原则在参数层
// 也成立：cancel_node 必须指向本轮还开着的节点，cancel_task 必须给出原因。
func validateCancelAction(obs *SupervisionObservation, action RouterAction) error {
	switch action.Kind {
	case RouterActionCancelNode:
		nodeRunID := strings.TrimSpace(action.NodeRunID)
		if nodeRunID == "" {
			return fmt.Errorf("cancel_node requires node_run_id; open nodes: %s", supervisionOpenNodeList(obs))
		}
		var target *SupervisionNodeState
		for i := range obs.OpenNodes {
			if obs.OpenNodes[i].NodeRunID == nodeRunID {
				target = &obs.OpenNodes[i]
				break
			}
		}
		if target == nil {
			return fmt.Errorf("cancel_node targets a node that is not open in this run (node_run_id=%q): %s",
				nodeRunID, supervisionOpenNodeList(obs))
		}
		// 用户裁定：等待审批的节点不自动取消 —— 审批只能由人批准或驳回。
		if target.NodeKind == NodeKindHumanGate || target.Status == NodeRunWaitingUser {
			return fmt.Errorf("cancel_node is not allowed on %s (%s): a node waiting for approval can only be approved or rejected by the user",
				target.NodeID, target.NodeKind)
		}
	case RouterActionCancelRun:
		if len(obs.OpenNodes) == 0 {
			return errors.New("cancel_run is not allowed: this run has no open nodes left")
		}
	case RouterActionCancelTask:
		if strings.TrimSpace(action.Body) == "" {
			return errors.New("cancel_task reason required")
		}
	}
	return nil
}

func supervisionOpenNodeList(obs *SupervisionObservation) string {
	if obs == nil || len(obs.OpenNodes) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(obs.OpenNodes))
	for _, node := range obs.OpenNodes {
		parts = append(parts, fmt.Sprintf("%s(%s)", node.NodeID, node.Status))
	}
	return strings.Join(parts, ", ")
}

func supervisionFailedNodeList(obs *SupervisionObservation) string {
	if obs == nil {
		return "none"
	}
	nodes := append(append([]SupervisionNodeState{}, obs.FailedNodes...), obs.EvidenceFailedNodes...)
	if len(nodes) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(nodes))
	for _, node := range nodes {
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
		if supervisionIsWriteAction(action.Kind) {
			if mode == RouterSupervisionOn {
				applicable = append(applicable, action)
				continue
			}
			shadowed = append(shadowed, action)
			continue
		}
		applicable = append(applicable, action)
	}
	return applicable, shadowed
}
