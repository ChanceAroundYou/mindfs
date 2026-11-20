package kanban

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
// Run」，于是永远有活动执行。所以两条证据一起看：
//
//   - 任何 queued/running/waiting_user 行 → 未收尾。行的优先级最高：重试时投影
//     还写着上一次的 fail，而 attempt+1 已经排队，这一轮马上就要动。
//   - 投影里有这个节点 → 以投影为准（循环退出、合并收尾都只写投影）。
//   - 投影没有、行有 → 以行准（#86）。
//   - 两边都没有 → 未收尾（刚建出来、driver 还没 claim 的 Run）。
func workflowRunHasOpenNodes(run *WorkflowRun, rows []NodeRun) bool {
	if run == nil || len(run.Graph.Nodes) == 0 {
		return false
	}
	openByRow := map[string]bool{}
	settledByRow := map[string]bool{}
	for _, row := range rows {
		if workflowNodeStateSettled(row.Status) {
			settledByRow[row.NodeID] = true
			continue
		}
		openByRow[row.NodeID] = true
	}
	for _, node := range run.Graph.Nodes {
		if openByRow[node.ID] {
			return true
		}
		if state, ok := run.NodeStates[node.ID]; ok {
			if !workflowNodeStateSettled(state) {
				return true
			}
			continue
		}
		if settledByRow[node.ID] {
			continue
		}
		return true
	}
	return false
}
