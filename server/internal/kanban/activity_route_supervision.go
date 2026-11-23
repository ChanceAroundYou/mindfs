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

// ActivityRunResumer 是「唤醒停住的 Run」端口（S3）。Service 实现它：唤醒走
// 既有的 single-flight 入口，driver 重算 ready 节点，没有可推进的节点时无事发生。
type ActivityRunResumer interface {
	WakeWorkflowRun(ctx context.Context, rootID, taskID, cause string) error
}

// ActivityRunCanceller 是取消三档端口。取消会停 driver、结算回复行、清工作区
// 归属，都在 Service 上，ActivityService 只表达「该取消什么」。
type ActivityRunCanceller interface {
	CancelWorkflowNodeForRouter(ctx context.Context, rootID, taskID, nodeRunID, reason string) error
	CancelWorkflowRunForRouter(ctx context.Context, rootID, taskID, reason string) error
	CancelTaskForRouter(ctx context.Context, rootID, taskID, reason string) error
}

// SetRunCanceller 装配取消端口。装配期调用一次。
func (s *ActivityService) SetRunCanceller(c ActivityRunCanceller) {
	s.runCanceller = c
}

// SetRunResumer 装配监管唤醒端口。装配期调用一次。
func (s *ActivityService) SetRunResumer(r ActivityRunResumer) {
	if s != nil {
		s.runResumer = r
	}
}

func (s *ActivityService) applyRouteResumeRun(ctx context.Context, decision *RouterDecision, action RouterAction) error {
	if s.runResumer == nil {
		return errors.New("resume_run is not available: run resumer not configured")
	}
	if err := s.runResumer.WakeWorkflowRun(ctx, decision.RootID, decision.TaskID, supervisionResumeReason); err != nil {
		return fmt.Errorf("resume run: %w", err)
	}
	return nil
}

// applyRouteEscalate 把「机器停手」交给用户：一条可见系统说明 + 任务放回
// waiting_user。**不取消 Run** —— 用户裁定（B8-2）由用户接手，而不是替他把
// 正在跑的东西砍掉。effect ID 由决策 + seq 确定，重放只会命中同一行。
func (s *ActivityService) applyRouteEscalate(ctx context.Context, decision *RouterDecision, task Task, action RouterAction) error {
	body := strings.TrimSpace(action.Body)
	if body == "" {
		return errors.New("escalate summary required")
	}
	activity := &TaskActivity{
		ID:     "route-escalate:" + decision.ID + ":" + strconv.Itoa(action.Seq),
		TaskID: decision.TaskID, RootID: task.RootID,
		Type: ActivityTypeAttention, Author: "system", Role: "system",
		Status: ActivityStatusCompleted,
		Body:   body,
	}
	if _, err := s.applyVisibleSystemRow(ctx, activity); err != nil {
		return err
	}
	return s.parkTaskWaitingUser(ctx, decision.TaskID)
}

// applyRouteCancelNode 取消一个节点。取消保留 worktree 与会话，下游按跳过语义
// 收敛；任务不因此进入终态。
func (s *ActivityService) applyRouteCancelNode(ctx context.Context, decision *RouterDecision, task Task, action RouterAction) error {
	if s.runCanceller == nil {
		return errors.New("cancel_node is not available: run canceller not configured")
	}
	nodeRunID := strings.TrimSpace(action.NodeRunID)
	if nodeRunID == "" {
		return errors.New("cancel_node: node_run_id required")
	}
	nodeID := strings.TrimSpace(action.Reason)
	if err := s.runCanceller.CancelWorkflowNodeForRouter(ctx, decision.RootID, decision.TaskID, nodeRunID, cancelReasonOf(action)); err != nil {
		return fmt.Errorf("cancel node %s: %w", nodeRunID, err)
	}
	return s.noteCancel(ctx, decision, task, action, "节点 "+nodeID)
}

// applyRouteCancelRun 停掉这一轮：端口负责取消开着的行、停 driver、把任务放回
// waiting_user；这里只补一条可见说明。
func (s *ActivityService) applyRouteCancelRun(ctx context.Context, decision *RouterDecision, task Task, action RouterAction) error {
	if s.runCanceller == nil {
		return errors.New("cancel_run is not available: run canceller not configured")
	}
	if err := s.runCanceller.CancelWorkflowRunForRouter(ctx, decision.RootID, decision.TaskID, cancelReasonOf(action)); err != nil {
		return fmt.Errorf("cancel run: %w", err)
	}
	return s.noteCancel(ctx, decision, task, action, "本轮执行")
}

// applyRouteCancelTask 把任务收成 cancelled（终态）。
func (s *ActivityService) applyRouteCancelTask(ctx context.Context, decision *RouterDecision, task Task, action RouterAction) error {
	if s.runCanceller == nil {
		return errors.New("cancel_task is not available: run canceller not configured")
	}
	if err := s.runCanceller.CancelTaskForRouter(ctx, decision.RootID, decision.TaskID, cancelReasonOf(action)); err != nil {
		return fmt.Errorf("cancel task: %w", err)
	}
	return s.noteCancel(ctx, decision, task, action, "任务")
}

// noteCancel 把取消原因落成一条用户可见的系统行。effect ID 由决策 + seq 确定，
// 重放只会命中同一行。
func (s *ActivityService) noteCancel(ctx context.Context, decision *RouterDecision, task Task, action RouterAction, target string) error {
	body := "已取消" + target
	if reason := cancelReasonOf(action); reason != "" {
		body += "：" + reason
	}
	activity := &TaskActivity{
		ID:     "route-cancel:" + decision.ID + ":" + strconv.Itoa(action.Seq),
		TaskID: decision.TaskID, RootID: task.RootID,
		Type: ActivityTypeAttention, Author: "system", Role: "system",
		Status: ActivityStatusCompleted,
		Body:   body,
	}
	_, err := s.applyVisibleSystemRow(ctx, activity)
	return err
}

// cancelReasonOf 取模型给的取消原因（工具把它放在 body，节点 id 放在 reason）。
func cancelReasonOf(action RouterAction) string {
	return truncateRouterText(strings.TrimSpace(action.Body), supervisionSummaryLimit)
}

// applyVisibleSystemRow 落一条用户可见的系统行；返回是否真的新建。运行期动作
// 的 effect ID 是确定的，InsertOrIgnore 让重放变成同一行的空操作。
func (s *ActivityService) applyVisibleSystemRow(ctx context.Context, activity *TaskActivity) (bool, error) {
	if activity == nil {
		return false, errors.New("activity required")
	}
	created, err := s.activities.InsertOrIgnore(ctx, activity)
	if err != nil {
		return false, err
	}
	if created {
		s.broadcastCreated(activity)
	}
	return created, nil
}

// parkTaskWaitingUser 把仍停在执行/失败态的任务放回待审核；已是待审核、终态
// 或暂停的任务不改（暂停是操作员状态）。
func (s *ActivityService) parkTaskWaitingUser(ctx context.Context, taskID string) error {
	task, err := s.tasks.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	switch task.Status {
	case StatusWaitingUser, StatusCancelled, StatusSuccess, StatusPaused:
		return nil
	}
	return s.tasks.UpdateTaskStatus(ctx, taskID, StatusWaitingUser, false)
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

// supervisionDirectRetrySummary 是旧回复被结算时写给用户的说明。它解释「这一轮
// 为什么没有结果」，但不把责任推给用户。
const supervisionDirectRetrySummary = "这一轮直连回复卡住了（会话已空闲但回合没收尾），已自动重开一轮。"

// recordRejectedInspectionAction 把巡检回合里被闸门拒掉的动作折成一条可见系统
// 说明。巡检没有来源留言，被拒之后整轮只会 failed 在日志里 —— 用户侧什么也
// 看不到，也就无从判断「系统发现了问题、但没权限动」。
//
// 口径：只说明「想做什么 + 为什么没做成」，不假装做过、不写进任务结论。
// effect ID 由决策 + seq 确定，重放只命中同一行。
func (s *ActivityService) recordRejectedInspectionAction(
	ctx context.Context,
	decision *RouterDecision,
	task Task,
	action RouterAction,
) error {
	if s == nil || s.activities == nil || decision == nil {
		return errors.New("activity service not configured")
	}
	body := fmt.Sprintf("巡检回合未能执行 `%s`：这类动作需要一条用户留言作为来源（回复归属、证据与投递目标都绑在它上面），本轮是系统自己发现的现场，没有来源留言。需要推进请先在任务里说一句，或让 Router 用提问卡征得你的意见。", action.Kind)
	act := &TaskActivity{
		ID:     "route-rejected:" + decision.ID + ":" + strconv.Itoa(action.Seq),
		TaskID: decision.TaskID, RootID: task.RootID,
		Type: ActivityTypeAttention, Author: "system", Role: "system",
		Status: ActivityStatusCompleted,
		Body:   body,
	}
	created, err := s.activities.InsertOrIgnore(ctx, act)
	if err != nil {
		return err
	}
	if created {
		s.broadcastCreated(act)
	}
	return nil
}

// applyRouteRetryDirect 重开卡住的直连回合（S3-3）：
//   - 先结算停在 running 的旧回复（条件更新，重放/别人已结算都是空操作）；
//   - 再用确定性 ID `direct-retry:<decision>` 插一条新的待回复行；
//   - 最后用原始用户留言跑一轮直连。
//
// 幂等：重放先看新行在不在。pending 说明上次崩在「插入后、开跑前」，补跑；
// running/completed/failed 说明回合已经（或正在）跑，直接返回。
func (s *ActivityService) applyRouteRetryDirect(ctx context.Context, decision *RouterDecision, task Task, userAct *TaskActivity, action RouterAction) error {
	activityID := strings.TrimSpace(action.NodeRunID)
	if activityID == "" {
		return errors.New("retry_direct: stalled reply id required")
	}
	if s.directExecutor == nil {
		return errors.New("retry_direct is not available: direct executor not configured")
	}
	if userAct == nil || strings.TrimSpace(userAct.Body) == "" {
		return errors.New("retry_direct: originating user message is empty")
	}
	retryID := "direct-retry:" + decision.ID
	existing, err := s.activities.Get(ctx, retryID)
	if err == nil && existing != nil && existing.Status != ActivityStatusPending {
		return nil
	}
	if err != nil && !errors.Is(err, ErrActivityNotFound) {
		return err
	}
	if updated, changed, settleErr := s.activities.SettleOrphanReply(ctx, activityID, supervisionDirectRetrySummary); settleErr != nil {
		return fmt.Errorf("settle stalled reply %s: %w", activityID, settleErr)
	} else if changed {
		s.broadcastUpdatedByID(ctx, updated.ID, map[string]any{
			"status":  ActivityStatusCancelled,
			"summary": supervisionDirectRetrySummary,
			"body":    "",
		})
		// 运行中的进度正文（"步骤 i/n"）不属于终态，留着会被当成结果文案。
		_ = s.activities.ClearBody(ctx, updated.ID)
	}
	if existing != nil && existing.Status == ActivityStatusPending {
		// 上次插完新行就崩了：补跑这一轮，行本身已经在。
		s.runTurn(ctx, func(turnCtx context.Context) {
			s.executeDirectActivity(turnCtx, existing, task, userAct.Body)
		})
		return nil
	}
	retry := &TaskActivity{
		ID:               retryID,
		TaskID:           decision.TaskID,
		RootID:           task.RootID,
		Type:             ActivityTypeAgentReply,
		Role:             RoleAgent,
		Status:           ActivityStatusPending,
		ReplyToID:        userAct.ID,
		RouterDecisionID: decision.ID,
	}
	created, err := s.activities.InsertOrIgnore(ctx, retry)
	if err != nil {
		return fmt.Errorf("insert retry reply: %w", err)
	}
	if created {
		s.broadcastCreated(retry)
	}
	s.runTurn(ctx, func(turnCtx context.Context) {
		s.executeDirectActivity(turnCtx, retry, task, userAct.Body)
	})
	return nil
}
