package kanban

import (
	"context"
	"fmt"
	"strings"
)

// attachRouterEventEvidence 把事件对应的可核对证据挂到 brief 上。
//
// 三类事件没有 Run（直连停摆、无 Run 的会话结算、巡检/恢复），它们的证据来自
// 载荷本身；其余事件按 RunID 读节点证据。
func attachRouterEventEvidence(ctx context.Context, store *TaskStore, evidence *RouterBriefEvidence, event RouterEvent) {
	switch {
	case event.Kind == RouterEventDirectStalled:
		// 直连事件没有 Run：可核对的证据只有那条卡住的回复行本身，节点段留空，
		// 也不写「run 不存在」这种会误导模型的未验证项。
		attachRouterEventDirectReply(ctx, store, evidence, event)
	case event.Kind == RouterEventTurnSettled && strings.TrimSpace(event.RunID) == "":
		// 没有绑定 Run 的会话回合。证据是事件载荷里的会话/时长/产出摘要，节点段
		// 留空，同样不写「run 不存在」——那句话会让 Router 以为执行失败了。
	case event.Kind == RouterEventTurnSettled:
		attachRouterEventTurnRun(ctx, store, evidence, event)
	case routerEventHealthKind(event.Kind):
		// 巡检/恢复事件同样没有 Run：证据是载荷里的水位与会话状态。
		evidence.Unverified = append(evidence.Unverified, routerEventHealthUnverified(event)...)
	default:
		run, err := store.WorkflowRunByID(ctx, event.RunID)
		if err != nil || run == nil {
			evidence.RunStatus = "missing"
			evidence.Unverified = append(evidence.Unverified, fmt.Sprintf("run %s: not found in task store", event.RunID))
			return
		}
		attachRouterEventNodes(ctx, store, evidence, run, event)
	}
}

// attachRouterEventTurnRun 给「会话回合绑定着一轮 Run」的事件补节点证据。
//
// 关键判据不能反：会话回合结算说明的是「这条会话这一轮干完了」，不是
// 「Workflow 成功」。Run 里只要有节点没成功（unknown / cancelled / running /
// queued / waiting_user / fail）或根本没有节点行，就必须留一条确定性的未验证项，
// 否则 Router 只看得到 tool_calls 与完成正文，会把两者混为一谈（#112）。
func attachRouterEventTurnRun(ctx context.Context, store *TaskStore, evidence *RouterBriefEvidence, event RouterEvent) {
	run, err := store.WorkflowRunByID(ctx, event.RunID)
	if err != nil || run == nil {
		evidence.RunStatus = "missing"
		evidence.Unverified = append(evidence.Unverified,
			fmt.Sprintf("settled session turn: bound run %s not found in task store; this turn does not prove the workflow run succeeded", event.RunID))
		return
	}
	attachRouterEventNodes(ctx, store, evidence, run, event)
	if note := turnSettledRunUnverified(evidence.Nodes); note != "" {
		evidence.Unverified = append(evidence.Unverified, note)
	}
}

func turnSettledRunUnverified(nodes []RouterBriefEvidenceNode) string {
	if len(nodes) == 0 {
		return "settled session turn: the bound workflow run has no node runs recorded; this turn does not prove the workflow run succeeded"
	}
	var stuck []string
	for _, node := range nodes {
		switch node.Status {
		case NodeRunSuccess, NodeRunSkipped:
			continue
		}
		stuck = append(stuck, node.NodeID+"="+node.Status)
	}
	if len(stuck) == 0 {
		return ""
	}
	return fmt.Sprintf("settled session turn: the bound workflow run is not settled (%s); this turn does not prove the workflow run succeeded",
		strings.Join(stuck, ", "))
}
