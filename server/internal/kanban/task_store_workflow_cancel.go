package kanban

import (
	"context"
	"errors"
	"strings"
	"time"
)

// CancelWorkflowNodeRunProjected 在同一事务里把行改成 cancelled、落 node_cancelled
// 事件，并推进 run 投影。
//
// 节点级取消与整轮取消不同：这一轮还要继续跑，driver 醒来重算 ready 时必须看见
// 这个节点已经收尾。只用 CancelWorkflowNodeRun 写行的话，投影里它还是「没跑过」，
// driver 会把一个 cancelled 行反复当成待跑节点派发 —— 实测空转到 1000 步上限，
// 下游节点永远不动（W1 T4 的崩溃窗口正是从这条缺陷里发现的）。
//
// 已收敛的行返回 false（幂等）；投影 revision 被别人推进时返回 ErrRunConflict，
// 由调用方重载后重试。
func (s *TaskStore) CancelWorkflowNodeRunProjected(ctx context.Context, run *WorkflowRun, n NodeRun, reason string) (bool, error) {
	if run == nil || n.ID == "" || n.NodeID == "" || n.RunID != run.ID {
		return false, errors.New("cancel node run: run and node run required")
	}
	states := cloneWorkflowStringMap(run.NodeStates)
	if states == nil {
		states = map[string]string{}
	}
	// Loop 内部行按 iteration 落自己的事实，不能按 node_id 覆盖 run 级投影
	// （与 CommitWorkflowNodeTransition 同一规则）。
	if n.LoopPath == "" {
		states[n.NodeID] = NodeRunCancelled
	}
	statesJSON, err := marshalWorkflowJSON(states)
	if err != nil {
		return false, err
	}
	payload := map[string]any{"reason": strings.TrimSpace(reason)}
	outputJSON, err := marshalWorkflowJSON(payload)
	if err != nil {
		return false, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	res, err := tx.ExecContext(ctx,
		`UPDATE workflow_node_runs SET status=?, finished_at=?, output_json=?, updated_at=? WHERE id=? AND status IN (?,?)`,
		NodeRunCancelled, stamp, outputJSON, stamp, n.ID, NodeRunQueued, NodeRunRunning)
	if err != nil {
		return false, err
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if changed != 1 {
		return false, nil
	}
	if _, err := appendWorkflowRunEventTx(ctx, tx, WorkflowRunEvent{
		RunID: n.RunID, Type: WorkflowEventNodeCancelled, NodeID: n.NodeID, NodeRunID: n.ID,
		Attempt: n.Attempt, Payload: payload, CreatedAt: now,
	}); err != nil {
		return false, err
	}
	res, err = tx.ExecContext(ctx,
		`UPDATE workflow_runs SET node_states_json=?, revision=revision+1, updated_at=? WHERE id=? AND revision=?`,
		statesJSON, stamp, run.ID, run.Revision)
	if err != nil {
		return false, err
	}
	moved, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if moved != 1 {
		return false, ErrRunConflict
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	run.NodeStates = states
	run.Revision++
	run.UpdatedAt = now
	return true, nil
}
