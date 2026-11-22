package kanban

// 路由动作落库后的收尾：占位回复的隐藏 / 落行 / 结算标记，以及版本解析与
// 回合起点的确定性 ID。动作分发与各动作实现在 activity_route_apply.go。
// 从该文件拆出（硬线 500，2026-11-22）。

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

func (s *ActivityService) hideRouteActivity(ctx context.Context, state *routeApplyState, act *TaskActivity, reason string) error {
	if act == nil {
		act = state.take()
	}
	if act == nil {
		return nil
	}
	if err := s.activities.SetHidden(ctx, act.ID, true); err != nil {
		return err
	}
	act.Hidden = true
	s.broadcastUpdated(act, map[string]any{"hidden": true, "hidden_reason": reason})
	return nil
}

// routeEffectActivityID 给出运行期事件回合的确定活动 ID。用户留言保持既有语义：
// 复用同步占位，或每次新起随机 ID 的活动。
func routeEffectActivityID(decision *RouterDecision, action RouterAction) string {
	if decision == nil || decision.TriggerKind == RouterTriggerUserMessage {
		return ""
	}
	return routerEventEffectID(decision.ID, action.Seq)
}

// insertRouteReply 插入一条回复活动。确定 ID 路径用 InsertOrIgnore：已存在同一行
// 时只更新正文/状态（可广播 updated），不插第二行、不广播 created。
func (s *ActivityService) insertRouteReply(ctx context.Context, act *TaskActivity, deterministic bool) error {
	if !deterministic {
		if err := s.activities.Insert(ctx, act); err != nil {
			return err
		}
		s.broadcastCreated(act)
		return nil
	}
	created, err := s.activities.InsertOrIgnore(ctx, act)
	if err != nil {
		return err
	}
	if created {
		s.broadcastCreated(act)
		return nil
	}
	if err := s.activities.UpdateBody(ctx, act.ID, act.Body); err != nil {
		return err
	}
	return s.activities.UpdateStatus(ctx, act.ID, act.Status, nil)
}

// settledRunSucceeded 判定这条 run_settled 决策背后是不是一轮真的成功结算的
// Run。运行期事件里也有 kind=run_settled 但任务根本没有 Run 的合成回合（T8
// 的运行时汇报），那些只是进度说明，不能顶掉任务结果摘要。
func (s *ActivityService) settledRunSucceeded(ctx context.Context, decision *RouterDecision) bool {
	if s.tasks == nil || decision == nil || decision.TriggerKind != RouterTriggerRunSettled {
		return false
	}
	runID := strings.TrimSpace(decision.TriggerRef)
	if runID == "" {
		return false
	}
	run, err := s.tasks.WorkflowRunByID(ctx, runID)
	if err != nil || run == nil {
		return false
	}
	return ProjectStatusFromRun(run) == StatusSuccess
}

// settleSummaryActionSeq 决定结算回合里哪一条 say 承担总结：模型显式写了
// purpose=summary 就用它；只写了 progress/chat（线上补偿回合的实测形态）时
// 取最后一条 say。没有 say 返回 -1。
func settleSummaryActionSeq(decision *RouterDecision) int {
	if decision == nil {
		return -1
	}
	last := -1
	for _, action := range decision.Actions {
		if action.Kind != RouterActionSay {
			continue
		}
		if strings.TrimSpace(action.Purpose) == "summary" {
			return action.Seq
		}
		last = action.Seq
	}
	return last
}

// decisionHasHandoff 报告这一轮是否把活交给了别的执行者。带执行动作时同一轮里
// 的 chat / progress 都只是过程口信：它们既不写任务摘要，progress 也不渲染。
func decisionHasHandoff(decision *RouterDecision) bool {
	if decision == nil {
		return false
	}
	for _, action := range decision.Actions {
		switch action.Kind {
		case RouterActionMessageAgent, RouterActionDeliver, RouterActionRunWorkflow,
			RouterActionAnswerWaiting, RouterActionRetryNode, RouterActionResumeRun,
			RouterActionRetryDirect:
			return true
		}
	}
	return false
}

// summaryTriggerAllowed 报告这个触发的回合是否允许写任务结果摘要。总结只属于
// 「一轮真的结算了」的回合：Run 成功结算，或一段任务绑定会话的回合结算。
func summaryTriggerAllowed(kind RouterTriggerKind) bool {
	switch kind {
	case RouterTriggerRunSettled, RouterTriggerTurnSettled:
		return true
	default:
		return false
	}
}

// markRouteReply 写 Router 归属字段（决策/回复对象/用途/顺序）。运行期事件的
// 汇报默认 purpose=progress；用户留言仍是 reply 或调用方指定值。
func (s *ActivityService) markRouteReply(
	ctx context.Context,
	decision *RouterDecision,
	userAct *TaskActivity,
	act *TaskActivity,
	action RouterAction,
) (map[string]any, error) {
	purpose := strings.TrimSpace(action.Purpose)
	if purpose == "" {
		purpose = "reply"
		if decision.TriggerKind != RouterTriggerUserMessage {
			purpose = "progress"
		}
	}
	// purpose=summary 只对「一轮真的结算了」成立（Run 成功结算 / 会话回合结算）。
	// 模型在别的回合写了 summary 也不该让整轮失败（那会把一条汇报连同回合一起
	// 丢掉）：降级成 progress，正文照常发给用户，只是不写任务结果摘要。
	if purpose == "summary" && !summaryTriggerAllowed(decision.TriggerKind) {
		purpose = "progress"
	}
	if err := s.activities.SetRouteFields(ctx, act.ID, decision.ID, userAct.ID, purpose, action.Seq); err != nil {
		return nil, err
	}
	act.RouterDecisionID = decision.ID
	act.ReplyToID = userAct.ID
	act.Purpose = purpose
	act.Ordinal = action.Seq
	return map[string]any{"purpose": purpose}, nil
}

// resolveRouteWorkflowVersion 解析 Router 给的版本：显式 version_id 优先，
// 否则取该 workflow 的最新发布版本。两者都不存在是决策失败，不是回落。
func (s *ActivityService) resolveRouteWorkflowVersion(ctx context.Context, task Task, action RouterAction) (WorkflowVersion, error) {
	// 临时图（Router 当场起草）不进控制面目录，也不需要控制面可用：合成一个
	// 只属于这一次 Run 的版本，作用域取任务所在的 root —— 项目级、免批。
	if action.Graph != nil {
		graph := *action.Graph
		graph.RootID = task.RootID
		return WorkflowVersion{
			WorkflowID: routerAdhocWorkflowID,
			Version:    adhocVersionID(graph),
			Revision:   "1",
			Graph:      graph,
			Author:     "router",
		}, nil
	}
	if s.workflows == nil {
		return WorkflowVersion{}, errors.New("workflow control-plane store unavailable")
	}
	if id := strings.TrimSpace(action.VersionID); id != "" {
		if version, err := s.workflows.GetVersion(ctx, id); err == nil {
			return version, nil
		}
	}
	workflowID := strings.TrimSpace(action.WorkflowID)
	if workflowID == "" {
		return WorkflowVersion{}, errors.New("run_workflow requires workflow_id or version_id")
	}
	latest, ok := s.latestVersion(ctx, workflowID)
	if !ok {
		return WorkflowVersion{}, fmt.Errorf("workflow %s has no published version", workflowID)
	}
	return s.workflows.GetVersion(ctx, latest)
}

// routeTurnStarted 把「起跑守卫拒绝」翻译成决策失败：占位回复已经显示原因，
// 这里只负责让 router_decisions 不假装 applied。
func routeTurnStarted(act *TaskActivity, kind string) error {
	if act != nil && act.Status == ActivityStatusFailed {
		summary := strings.TrimSpace(act.Summary)
		if summary == "" {
			summary = "turn rejected"
		}
		return fmt.Errorf("%s: %s", kind, summary)
	}
	return nil
}

func cleanRouteOptions(options []string) []string {
	out := make([]string, 0, len(options))
	for _, option := range options {
		if trimmed := strings.TrimSpace(option); trimmed != "" {
			out = append(out, "· "+trimmed)
		}
	}
	return out
}

// routeOptionLabels 是卡片用的选项标签：与正文同源（同样去空、同样顺序），
// 但不带正文里的项目符号 —— 卡片按钮自己就是图形，再叠一个「· 」只是噪声。
func routeOptionLabels(options []string) []string {
	out := make([]string, 0, len(options))
	for _, option := range options {
		if trimmed := strings.TrimSpace(option); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
