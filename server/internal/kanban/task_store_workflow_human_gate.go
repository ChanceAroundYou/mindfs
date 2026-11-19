package kanban

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrWorkflowHumanGateNotWaiting：节点当前不是 waiting_user，不能当审批处理。
	ErrWorkflowHumanGateNotWaiting = errors.New("workflow human gate is not waiting for a decision")
	// ErrWorkflowHumanGateAlreadyDecided：同一个人工 gate 只允许一个决定。
	ErrWorkflowHumanGateAlreadyDecided = errors.New("workflow human gate already decided")
)

// DecideWorkflowHumanGate commits the explicit approve/reject decision (D4).
//
// Only a waiting_user row can transition, once. A duplicate of the same decision
// is idempotent (returns the row with changed=false); the opposite decision (or
// a decision racing another one) returns ErrWorkflowHumanGateAlreadyDecided.
// Normal comments never call this path.
func (s *TaskStore) DecideWorkflowHumanGate(ctx context.Context, run *WorkflowRun, nodeRun NodeRun, decision, commentID string) (NodeRun, bool, error) {
	if run == nil {
		return NodeRun{}, false, errors.New("workflow run required")
	}
	var status, eventType, activityType, body string
	switch strings.TrimSpace(decision) {
	case "approve":
		status, eventType = NodeRunSuccess, WorkflowEventHumanApproved
		activityType, body = ActivityTypeSystemEvent, "已批准人工确认节点"
	case "reject":
		status, eventType = NodeRunCancelled, WorkflowEventHumanRejected
		activityType, body = ActivityTypeAttention, "已拒绝人工确认节点"
	default:
		return NodeRun{}, false, fmt.Errorf("invalid human gate decision %q (want approve|reject)", decision)
	}
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	output := map[string]any{"decision": strings.TrimSpace(decision)}
	if c := strings.TrimSpace(commentID); c != "" {
		output["comment_id"] = c
	}
	outputJSON, err := marshalWorkflowJSON(output)
	if err != nil {
		return NodeRun{}, false, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return NodeRun{}, false, err
	}
	defer tx.Rollback()

	var current string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM workflow_node_runs WHERE id=?`, nodeRun.ID).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NodeRun{}, false, ErrWorkflowNodeRunNotFound
		}
		return NodeRun{}, false, err
	}
	if current == status {
		existing, err := scanWorkflowNodeRun(tx.QueryRowContext(ctx,
			`SELECT `+workflowNodeRunColumns+` FROM workflow_node_runs WHERE run_id=? AND node_id=? AND loop_path=? AND iteration=? AND attempt=?`,
			run.ID, nodeRun.NodeID, nodeRun.LoopPath, nodeRun.Iteration, nodeRun.Attempt))
		if err != nil {
			return NodeRun{}, false, err
		}
		return *existing, false, nil
	}
	if current != NodeRunWaitingUser {
		return NodeRun{}, false, ErrWorkflowHumanGateAlreadyDecided
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE workflow_node_runs SET status=?, output_json=?, finished_at=?, updated_at=? WHERE id=? AND status=?`,
		status, outputJSON, stamp, stamp, nodeRun.ID, NodeRunWaitingUser)
	if err != nil {
		return NodeRun{}, false, err
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return NodeRun{}, false, err
	}
	if changed != 1 {
		return NodeRun{}, false, ErrWorkflowHumanGateAlreadyDecided
	}

	states := cloneWorkflowStringMap(run.NodeStates)
	states[nodeRun.NodeID] = status
	outputs := cloneWorkflowOutputMap(run.NodeOutputs)
	outputs[nodeRun.NodeID] = output
	statesJSON, err := marshalWorkflowJSON(states)
	if err != nil {
		return NodeRun{}, false, err
	}
	outputsJSON, err := marshalWorkflowJSON(outputs)
	if err != nil {
		return NodeRun{}, false, err
	}
	routesJSON, err := marshalWorkflowJSON(run.SelectedRoutes)
	if err != nil {
		return NodeRun{}, false, err
	}
	updated, err := tx.ExecContext(ctx,
		`UPDATE workflow_runs SET revision=revision+1,node_states_json=?,node_outputs_json=?,selected_routes_json=?,updated_at=? WHERE id=? AND revision=?`,
		statesJSON, outputsJSON, routesJSON, stamp, run.ID, run.Revision)
	if err != nil {
		return NodeRun{}, false, err
	}
	rows, err := updated.RowsAffected()
	if err != nil {
		return NodeRun{}, false, err
	}
	if rows != 1 {
		return NodeRun{}, false, ErrRunConflict
	}
	if _, err := appendWorkflowRunEventTx(ctx, tx, WorkflowRunEvent{
		RunID: run.ID, Type: eventType, NodeID: nodeRun.NodeID, NodeRunID: nodeRun.ID,
		Attempt: nodeRun.Attempt, Payload: output, CreatedAt: now,
	}); err != nil {
		return NodeRun{}, false, err
	}
	activityBody := body
	if c := strings.TrimSpace(commentID); c != "" {
		activityBody += "（决定留言 " + c + "）"
	}
	gateID := "wf:" + nodeRun.ID + ":human_waiting"
	var gatePayload string
	if err := tx.QueryRowContext(ctx,
		`SELECT payload_json FROM task_activities WHERE task_id=? AND id=?`, run.TaskID, gateID).Scan(&gatePayload); err == nil {
		merged := map[string]any{}
		if strings.TrimSpace(gatePayload) != "" {
			_ = json.Unmarshal([]byte(gatePayload), &merged)
		}
		merged["decision"] = strings.TrimSpace(decision)
		merged["status"] = status
		encoded, err := marshalWorkflowJSON(merged)
		if err != nil {
			return NodeRun{}, false, err
		}
		gateStatus := ActivityStatusCompleted
		if status == NodeRunCancelled {
			gateStatus = ActivityStatusCancelled
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE task_activities SET status=?, payload_json=?, updated_at=? WHERE task_id=? AND id=?`,
			gateStatus, encoded, stamp, run.TaskID, gateID); err != nil {
			return NodeRun{}, false, err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return NodeRun{}, false, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO task_activities (id, task_id, root_id, type, author, role, body, workflow_choice, workflow_version_id, run_id, node_run_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(task_id,id) DO NOTHING`,
		"wf:"+nodeRun.ID+":human:"+strings.TrimSpace(decision), run.TaskID, s.root.ID, activityType, "user", RoleUser,
		activityBody, CommentWorkflowChoiceEmpty, "", run.ID, nodeRun.ID, stamp); err != nil {
		return NodeRun{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return NodeRun{}, false, err
	}

	nodeRun.Status = status
	nodeRun.Output = output
	nodeRun.FinishedAt = now
	run.NodeStates = states
	run.NodeOutputs = outputs
	run.Revision++
	run.UpdatedAt = now
	return nodeRun, true, nil
}

// AutoApproveTerminalHumanGate closes a legacy terminal gate that is parked in
// waiting_user. A gate with no outgoing edge has nothing downstream to approve,
// so it must not block the run (2026-11-19 用户裁定).
//
// It deliberately writes no extra user-role activity (unlike an explicit
// approve): nobody approved anything, and a "已批准人工确认节点" bubble would be
// a false record. The gate activity itself is settled with auto_approved.
func (s *TaskStore) AutoApproveTerminalHumanGate(ctx context.Context, run *WorkflowRun, nodeRun NodeRun) (NodeRun, bool, error) {
	if run == nil {
		return NodeRun{}, false, errors.New("workflow run required")
	}
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	output := map[string]any{"auto_approved": true, "status": NodeRunSuccess}
	for k, v := range nodeRun.Output {
		output[k] = v
	}
	outputJSON, err := marshalWorkflowJSON(output)
	if err != nil {
		return NodeRun{}, false, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return NodeRun{}, false, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`UPDATE workflow_node_runs SET status=?, output_json=?, finished_at=?, updated_at=? WHERE id=? AND status=?`,
		NodeRunSuccess, outputJSON, stamp, stamp, nodeRun.ID, NodeRunWaitingUser)
	if err != nil {
		return NodeRun{}, false, err
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return NodeRun{}, false, err
	}
	if changed != 1 {
		existing, err := s.WorkflowNodeRunByKey(ctx, run.ID, nodeRun.NodeID, nodeRun.LoopPath, nodeRun.Iteration, nodeRun.Attempt)
		if err != nil {
			return NodeRun{}, false, err
		}
		return existing, false, nil
	}

	states := cloneWorkflowStringMap(run.NodeStates)
	states[nodeRun.NodeID] = NodeRunSuccess
	outputs := cloneWorkflowOutputMap(run.NodeOutputs)
	outputs[nodeRun.NodeID] = output
	statesJSON, err := marshalWorkflowJSON(states)
	if err != nil {
		return NodeRun{}, false, err
	}
	outputsJSON, err := marshalWorkflowJSON(outputs)
	if err != nil {
		return NodeRun{}, false, err
	}
	routesJSON, err := marshalWorkflowJSON(run.SelectedRoutes)
	if err != nil {
		return NodeRun{}, false, err
	}
	updated, err := tx.ExecContext(ctx,
		`UPDATE workflow_runs SET revision=revision+1,node_states_json=?,node_outputs_json=?,selected_routes_json=?,updated_at=? WHERE id=? AND revision=?`,
		statesJSON, outputsJSON, routesJSON, stamp, run.ID, run.Revision)
	if err != nil {
		return NodeRun{}, false, err
	}
	rows, err := updated.RowsAffected()
	if err != nil {
		return NodeRun{}, false, err
	}
	if rows != 1 {
		return NodeRun{}, false, ErrRunConflict
	}
	if _, err := appendWorkflowRunEventTx(ctx, tx, WorkflowRunEvent{
		RunID: run.ID, Type: WorkflowEventNodeSucceeded, NodeID: nodeRun.NodeID, NodeRunID: nodeRun.ID,
		Attempt: nodeRun.Attempt, Payload: map[string]any{"auto_approved": true, "status": NodeRunSuccess},
		CreatedAt: now,
	}); err != nil {
		return NodeRun{}, false, err
	}
	gateID := "wf:" + nodeRun.ID + ":human_waiting"
	var gatePayload string
	if err := tx.QueryRowContext(ctx,
		`SELECT payload_json FROM task_activities WHERE task_id=? AND id=?`, run.TaskID, gateID).Scan(&gatePayload); err == nil {
		merged := map[string]any{}
		if strings.TrimSpace(gatePayload) != "" {
			_ = json.Unmarshal([]byte(gatePayload), &merged)
		}
		merged["auto_approved"] = true
		merged["status"] = NodeRunSuccess
		encoded, err := marshalWorkflowJSON(merged)
		if err != nil {
			return NodeRun{}, false, err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE task_activities SET status=?, payload_json=?, updated_at=? WHERE task_id=? AND id=?`,
			ActivityStatusCompleted, encoded, stamp, run.TaskID, gateID); err != nil {
			return NodeRun{}, false, err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return NodeRun{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return NodeRun{}, false, err
	}

	nodeRun.Status = NodeRunSuccess
	nodeRun.Output = output
	nodeRun.FinishedAt = now
	run.NodeStates = states
	run.NodeOutputs = outputs
	run.Revision++
	run.UpdatedAt = now
	return nodeRun, true, nil
}
