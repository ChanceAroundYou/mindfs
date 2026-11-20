package kanban

import "strings"

// 「这个节点还会不会自己动」在本仓库有三套判断，历史上混用过两次，写清楚免得
// 下次再串：
//
//   - workflowNodeStateTerminal（service_user_turn.go）：这一行真的结束了没有。
//     unknown **不算** —— 被新留言取代时必须冻结成 cancelled，否则
//     ProjectStatusFromRun 继续报 waiting_user，绑定守卫会拒绝下一条留言。
//   - EngineReadyNodes / workflowLoopIterationTerminal：还能不能重放。
//     unknown 算结束，绝不重发一个结果不明的回合。
//   - workflowNodeStateSettled（这里）：还会不会在没人操作的情况下变化。
//     unknown 算已收尾 —— 行已经不在跑，留着只会把 Router 永久挡在门外
//     （#86 现场：unknown + 空投影 → ActiveExecution 永不释放）。
//
// unknown 的语义是「副作用不明」，所以它既不能重放（第二套），也不能当成终态
// 写进投影（第一套），但它确实不需要任何人再等它动（第三套）。

func workflowNodeStateSettled(state string) bool {
	switch state {
	case NodeRunSuccess, NodeRunSkipped, NodeRunFail, NodeRunCancelled, NodeRunUnknown:
		return true
	default:
		return false
	}
}

// workflowRunHasOpenNodes 报告 Run 里是否还有「不需要新操作也可能变化」的节点。
//
// 只看投影会漏掉 #86 那种半写状态：NodeStates 只在节点终态时写，进程重启把
// running 行标成 unknown 时投影还是空的 —— 「一个节点都没写」被当成「刚建的
// Run」，于是永远有活动执行。所以按**每个顶层节点最新一次尝试**的行判断，
// 行是比投影更接近现场的证据：
//
//   - 最新行是 queued/running/waiting_user → 未收尾。行优先保证重试安全：重试
//     排队时投影还写着上一次的 fail，而 attempt+1 这一轮马上就要动。
//   - 最新行是 unknown，但留下的是「独立树没合完」的证据 → 仍未收尾（见
//     workflowUnknownBlocksRelease）：分支可能只合了一半，新开一轮会在状态
//     不明的主树上继续写。
//   - 其余终态 → 已收尾，以行为准（投影即使停在 running 也只是没写完的旧值）。
//   - 没有行、投影里有 → 以投影为准（循环退出、合并收尾都只写投影）。
//   - 两边都没有 → 未收尾（刚建出来、driver 还没 claim 的 Run）。
func workflowRunHasOpenNodes(run *WorkflowRun, rows []NodeRun) bool {
	if run == nil || len(run.Graph.Nodes) == 0 {
		return false
	}
	latest := workflowLatestTopLevelNodeRuns(rows)
	for _, node := range run.Graph.Nodes {
		if row, ok := latest[node.ID]; ok {
			if !workflowNodeStateSettled(row.Status) || workflowUnknownBlocksRelease(row) {
				return true
			}
			continue
		}
		if state, ok := run.NodeStates[node.ID]; ok {
			if !workflowNodeStateSettled(state) {
				return true
			}
			continue
		}
		return true
	}
	return false
}

// workflowLatestTopLevelNodeRuns 按节点取最新一次尝试的行。Loop 内部行有自己的
// 迭代进度、收尾写在外层 Loop 的投影上，不参与 Run 级闸门。
func workflowLatestTopLevelNodeRuns(rows []NodeRun) map[string]NodeRun {
	out := make(map[string]NodeRun, len(rows))
	for _, row := range rows {
		if row.LoopPath != "" {
			continue
		}
		if prev, ok := out[row.NodeID]; ok && prev.Attempt >= row.Attempt {
			continue
		}
		out[row.NodeID] = row
	}
	return out
}

// workflowUnknownBlocksRelease 区分两种 unknown（用户裁定 2026-11-20）：
//
//   - 重启产出（`startup_ambiguous_side_effect`）：回合可能还没发出、也可能跑完
//     了，但工作区没有半截的合并状态 —— 算已收尾，放行新一轮（#86）。
//   - 合并产出：独立 worktree 的续合并失败，分支可能只合了一半。新开一轮会让
//     节点在状态不明的主树上继续写，必须继续挡闸门，等人处理。
//
// 判别不用事件账本：MergeWorkflowNodeRun 的续合并路径在转 unknown 前一定写
// `worktree_error`，而重启标 unknown 的 transition 会把 output 整体换成
// `{"reason": ...}`。所以「unknown + output 里有 worktree_error」就是合并产出。
func workflowUnknownBlocksRelease(row NodeRun) bool {
	if row.Status != NodeRunUnknown || strings.TrimSpace(row.WorktreeID) == "" {
		return false
	}
	_, mergeErr := row.Output["worktree_error"].(string)
	return mergeErr
}
