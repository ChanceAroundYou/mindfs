package kanban

import (
	"context"
	"fmt"
)

// deliverTargetError 是 deliver_to_active_run 的**前置**闸门：只有唯一活动
// Agent 节点可投递。
//
// 为什么必须在这里、而不是只靠回合结束后的 validateRouterActions：
// 校验拒绝是整个决策 rejected，随后 fallbackRoute 回落旧路径 —— 而旧路径会
// 冻结当前 Run 另起一条。用户明明只是回答了一个停住的节点，看到的却是整轮
// 勘察被作废（2026-11-19 task_2ea72d70e560f8e3：`input` 停在 waiting_user，
// 留言「我希望是A」被判 rejected 后 `run-be0aa…` 全 cancelled）。工具错误会
// 直接回给模型（session_open.go 的 ErrorResult 分支），模型当轮就能改口成
// say/ask_user，仍是一条正常决策。
//
// 0 个活动节点时要分辨两种「停在 waiting_user 的节点」，否则模型只会拿到
// 一句「没有目标」，无从向用户解释该点哪里：
//   - HumanGate：批准/驳回是专用动作（D4），普通留言不能绕过；
//   - Agent 节点以 blocked/silent 停住：模型声称需要用户拍板，重试该节点才会
//     带着答复重跑。
func (e *routerToolEnv) deliverTargetError() error {
	// 运行期事件回合根本不允许执行类动作。那是「类别被禁止」，与「这一轮没
	// 有可投递目标」是两回事：报成后者会让模型以为换个时机就能投递，还会把
	// 白名单的明确拒绝（spec：只允许 say/ask_user/silent）掩盖掉。
	if isRuntimeEventBrief(e.brief) {
		return nil
	}
	nodes := 0
	if e != nil && e.brief != nil && e.brief.ActiveExecution != nil {
		nodes = len(e.brief.ActiveExecution.CurrentNodes)
	}
	if nodes == 1 {
		return nil
	}
	if nodes > 1 {
		return fmt.Errorf("无法转交：当前有 %d 个活动 Agent 节点，留言不能扇出（消息不会被投递）", nodes)
	}
	switch stop := e.waitingStop(); stop {
	case "human_gate":
		return fmt.Errorf("无法转交：当前工作流正在等待你审批，普通留言不能替代批准/驳回（消息不会被投递）")
	case "agent_blocked":
		return fmt.Errorf("无法转交：当前没有正在执行的 Agent 节点，工作流停在一个等你答复的节点上；请改用 router_answer_waiting_node 把答复交给它（留言不会被投递）")
	}
	return fmt.Errorf("无法转交：当前没有正在执行的 Agent 节点，留言没有投递目标（消息不会被投递）")
}

// waitingStop 判断「没有活动节点」时是不是停在某个需要用户处理的节点上。
// gate 优先：HumanGate 的专用动作是审批，哪怕旁边还停着一个 blocked 节点。
func (e *routerToolEnv) waitingStop() string {
	if e == nil || e.store == nil || e.brief == nil || e.brief.LatestRun == nil {
		return ""
	}
	run, err := e.store.WorkflowRunByID(context.Background(), e.brief.LatestRun.ID)
	if err != nil || run == nil {
		return ""
	}
	blockedAgent := false
	for _, node := range run.Graph.Nodes {
		if run.NodeStates[node.ID] != NodeRunWaitingUser {
			continue
		}
		switch node.Kind {
		case NodeKindHumanGate:
			return "human_gate"
		case NodeKindAgent:
			blockedAgent = true
		}
	}
	if blockedAgent {
		return "agent_blocked"
	}
	return ""
}
