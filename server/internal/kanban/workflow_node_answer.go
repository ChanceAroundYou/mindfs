package kanban

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrWorkflowNodeAnswerNotActionable 表示目标节点当前不接受「用户答复」：
// 它不在 waiting_user、不是 Agent 节点，或者这一轮根本不该由答复来推进。
var ErrWorkflowNodeAnswerNotActionable = errors.New("workflow node is not waiting for an answer")

// AnswerWaitingWorkflowNode 把一个停在 waiting_user 的 Agent 节点重开一次尝试，
// 并把用户答复一起带进去。
//
// 为什么不是 deliver：投递只在节点回合**结束**时被消费，停住的节点没有在跑的
// 回合，留言永远等不到投递（2026-11-19 task_2ea72d70e560f8e3）。
// 为什么不是普通 retry：retry 不带消息，用户答了也没人看。
func (s *Service) AnswerWaitingWorkflowNode(ctx context.Context, rootID, taskID, nodeRunID, body, activityID string) (NodeRun, error) {
	if s == nil {
		return NodeRun{}, errors.New("kanban service not configured")
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return NodeRun{}, errors.New("user answer required")
	}
	store, task, err := s.loadForMove(ctx, rootID, taskID)
	if err != nil {
		return NodeRun{}, err
	}
	if task.Status == StatusPaused {
		return NodeRun{}, ErrTaskPaused
	}
	run, found, err := s.workflowRunForTask(ctx, store, task.ID)
	if err != nil {
		return NodeRun{}, err
	}
	if !found {
		return NodeRun{}, fmt.Errorf("%w: task has no workflow run", ErrWorkflowNodeAnswerNotActionable)
	}
	runs, err := store.ListWorkflowNodeRuns(ctx, run.ID)
	if err != nil {
		return NodeRun{}, err
	}
	// 幂等优先：apply 成功但游标没推进的重放必须拿回同一条尝试，而不是因为
	// 「原来的节点已经不在 waiting_user」报错，更不能建第 3 次尝试。因此这一步
	// 必须早于任何「目标是否还能答复」的判定。
	if id := strings.TrimSpace(nodeRunID); id != "" {
		for _, nr := range runs {
			if nr.PrevAttemptID == id {
				return nr, nil
			}
		}
	}
	original, err := s.answerTargetNodeRun(ctx, store, run, nodeRunID, runs)
	if err != nil {
		return NodeRun{}, err
	}
	running := 0
	for _, nr := range runs {
		if nr.Status == NodeRunRunning {
			running++
		}
	}
	if running > 0 {
		return NodeRun{}, fmt.Errorf("%w: %d node run(s) are still running", ErrWorkflowNodeAnswerNotActionable, running)
	}

	answered, err := store.CreateAnsweredWorkflowNodeRun(ctx, run, original, body, activityID)
	if err != nil {
		return NodeRun{}, err
	}
	if _, err := store.AppendWorkflowRunEvent(ctx, WorkflowRunEvent{
		RunID: run.ID, Type: WorkflowEventNodeRetried, NodeID: original.NodeID, NodeRunID: answered.ID,
		Attempt: answered.Attempt, Payload: map[string]any{
			"prev_attempt_id":    original.ID,
			"reason":             "user_answer",
			"answer_activity_id": strings.TrimSpace(activityID),
			"answer_excerpt":     truncateRouterText(body, 200),
		},
	}); err != nil {
		return NodeRun{}, err
	}
	if err := s.resetWorkflowNodeProjection(ctx, store, run.ID, original.NodeID); err != nil {
		return NodeRun{}, err
	}
	if err := store.UpdateTaskStatus(ctx, task.ID, StatusRunning, false); err != nil {
		return NodeRun{}, err
	}
	_ = store.AddEvent(ctx, TaskEvent{
		ID: newID("event"), TaskID: task.ID, Type: "workflow_node_answered",
		Payload: eventPayload(map[string]any{
			"node_id": original.NodeID, "node_run_id": answered.ID,
			"prev_attempt_id": original.ID, "answer_activity_id": strings.TrimSpace(activityID),
		}),
		CreatedAt: time.Now().UTC(),
	})
	if err := s.WakeWorkflowRun(ctx, rootID, task.ID, "answer"); err != nil {
		return NodeRun{}, err
	}
	return answered, nil
}

// answerTargetNodeRun 解析「这段话该回给哪个节点」：给了 ID 就只认它，没给就
// 要求恰好一个候选。多候选绝不猜 —— 猜错会把答复写进另一个节点的会话。
func (s *Service) answerTargetNodeRun(ctx context.Context, store *TaskStore, run *WorkflowRun, nodeRunID string, runs []NodeRun) (NodeRun, error) {
	nodeRunID = strings.TrimSpace(nodeRunID)
	if nodeRunID != "" {
		original, err := store.WorkflowNodeRunByID(ctx, nodeRunID)
		if err != nil {
			return NodeRun{}, err
		}
		if err := answerableNodeRun(run, original); err != nil {
			return NodeRun{}, err
		}
		return original, nil
	}
	var waiting []NodeRun
	for _, nr := range runs {
		if answerableNodeRun(run, nr) == nil {
			waiting = append(waiting, nr)
		}
	}
	switch len(waiting) {
	case 0:
		return NodeRun{}, fmt.Errorf("%w: no agent node is waiting for an answer", ErrWorkflowNodeAnswerNotActionable)
	case 1:
		return waiting[0], nil
	default:
		return NodeRun{}, fmt.Errorf("%w: %d agent nodes are waiting (%s); name one explicitly", ErrWorkflowNodeAnswerNotActionable, len(waiting), strings.Join(nodeRunIDs(waiting), "、"))
	}
}

func answerableNodeRun(run *WorkflowRun, nr NodeRun) error {
	if run == nil || nr.RunID != run.ID {
		return fmt.Errorf("%w: node run does not belong to this run", ErrWorkflowNodeAnswerNotActionable)
	}
	if nr.LoopPath != "" {
		return fmt.Errorf("%w: loop-internal nodes cannot be answered directly", ErrWorkflowNodeAnswerNotActionable)
	}
	if nr.Status != NodeRunWaitingUser {
		return fmt.Errorf("%w: node run %s is %s", ErrWorkflowNodeAnswerNotActionable, nr.ID, nr.Status)
	}
	// 历史行会永久停在 waiting_user（重试不改写原行）；Run 投影才是「现在停没停」
	// 的真相。只认投影，否则答复过的节点会被反复当成还在等人回答。
	if state := run.NodeStates[nr.NodeID]; state != NodeRunWaitingUser {
		return fmt.Errorf("%w: node %s is %s in the run projection", ErrWorkflowNodeAnswerNotActionable, nr.NodeID, state)
	}
	node, ok := workflowNodeByID(run.Graph, nr.NodeID)
	if !ok {
		return fmt.Errorf("%w: node %s is not in this run graph", ErrWorkflowNodeAnswerNotActionable, nr.NodeID)
	}
	if node.Kind == NodeKindHumanGate {
		return fmt.Errorf("%w: HumanGate waits for approve/reject, not a free-text answer", ErrWorkflowNodeAnswerNotActionable)
	}
	if node.Kind != NodeKindAgent {
		return fmt.Errorf("%w: node %s is %s, not agent", ErrWorkflowNodeAnswerNotActionable, nr.NodeID, node.Kind)
	}
	return nil
}
