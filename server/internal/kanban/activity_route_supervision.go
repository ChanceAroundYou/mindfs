package kanban

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// 监管动作的应用端（S2）。两个动作都必须幂等：运行期事件回合走「先写效果、
// 后推游标」，崩在两者之间会重放同一动作。

// ActivityNodeRetrier 是「重试失败节点」端口。Service 实现它；nil 时动作明确
// 失败而不是假装成功。
type ActivityNodeRetrier interface {
	RetryFailedWorkflowNode(ctx context.Context, rootID, taskID, nodeRunID, reason string) (NodeRun, error)
}

// SetNodeRetrier 装配监管重试端口。装配期调用一次。
func (s *ActivityService) SetNodeRetrier(r ActivityNodeRetrier) {
	if s != nil {
		s.nodeRetrier = r
	}
}

func (s *ActivityService) applyRouteRetryNode(ctx context.Context, decision *RouterDecision, action RouterAction) error {
	if s.nodeRetrier == nil {
		return errors.New("retry_node is not available: node retrier not configured")
	}
	nodeRunID := strings.TrimSpace(action.NodeRunID)
	if nodeRunID == "" {
		return errors.New("retry_node: node_run_id required")
	}
	// 账本 reason 固定为 supervisor_retry：它是「谁发起的重试」这一维度的枚举，
	// 模型的解释留在 decision.Actions 里（actions_json 本身就是审计记录）。
	if _, err := s.nodeRetrier.RetryFailedWorkflowNode(ctx, decision.RootID, decision.TaskID, nodeRunID, supervisionRetryReason); err != nil {
		return fmt.Errorf("retry node %s: %w", nodeRunID, err)
	}
	return nil
}

// applyRouteNotifyUser 落一条可见的系统说明。effect ID 由决策 + seq 确定，
// InsertOrIgnore 保证重放只会命中同一行。
func (s *ActivityService) applyRouteNotifyUser(ctx context.Context, decision *RouterDecision, task Task, action RouterAction) error {
	body := strings.TrimSpace(action.Body)
	if body == "" {
		return errors.New("notify_user body required")
	}
	act := &TaskActivity{
		ID:     "route-notify:" + decision.ID + ":" + strconv.Itoa(action.Seq),
		TaskID: decision.TaskID, RootID: task.RootID,
		Type: ActivityTypeAttention, Author: "system", Role: "system",
		Status: ActivityStatusCompleted,
		Body:   body,
	}
	created, err := s.activities.InsertOrIgnore(ctx, act)
	if err != nil {
		return err
	}
	if created && s.broadcaster != nil {
		s.broadcaster.ActivityCreated(act)
	}
	return nil
}
