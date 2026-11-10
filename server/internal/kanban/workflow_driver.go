package kanban

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// workflowAdvanceStepLimit bounds one Advance call. Every iteration persists at
// least one transition, so hitting this limit means a graph/engine bug, not a
// large workflow; fail loudly instead of spinning forever.
const workflowAdvanceStepLimit = 1000

// Condition routes are the two booleans of the safe expression language. The
// frontend default is already ["pass","fail"]; the evaluator maps true→pass and
// false→fail and rejects a graph that does not declare both.
const (
	WorkflowRoutePass = "pass"
	WorkflowRouteFail = "fail"
)

// AdvanceWorkflowRun drives one task's workflow until it has no synchronously
// runnable node left. Each step reloads the authoritative run, so a concurrent
// edit/claim is never overwritten by a stale in-memory snapshot.
//
// It intentionally stops at HumanGate and Loop nodes: their approval/iteration
// semantics belong to the later packages and must not be guessed here.
func (s *Service) AdvanceWorkflowRun(ctx context.Context, store *TaskStore, taskID string) error {
	if s == nil || store == nil {
		return errors.New("workflow service and store required")
	}
	for step := 0; step < workflowAdvanceStepLimit; step++ {
		if err := ctx.Err(); err != nil {
			return nil // cancellation is an operator decision, not a failure
		}
		task, err := store.GetTask(ctx, taskID)
		if err != nil {
			return err
		}
		// Pause and terminal states are hard stops: an in-flight node may still
		// finish (its own path persists the result), but this driver must not
		// claim another node or overwrite the operator's status.
		if task.Status == StatusPaused || isTerminalStatus(task.Status) {
			return nil
		}
		run, err := store.WorkflowRunForTask(ctx, taskID)
		if err != nil {
			if errors.Is(err, ErrWorkflowRunNotFound) {
				return nil // legacy task: nothing to drive
			}
			return err
		}
		ready, skipped, err := EngineReadyNodes(run)
		if err != nil {
			return err
		}
		if len(skipped) > 0 {
			node, ok := run.Graph.node(skipped[0])
			if !ok {
				return fmt.Errorf("engine returned unknown skipped node %q", skipped[0])
			}
			if _, err := s.commitSkippedWorkflowNode(ctx, store, run, node); err != nil {
				return err
			}
			continue
		}
		if len(ready) == 0 {
			// A Loop container is not "ready" once it started (it is running),
			// yet its iteration still has to advance. Drive it explicitly before
			// declaring the run quiescent.
			if loop, ok := runningWorkflowLoopNode(run); ok {
				advanced, err := s.advanceWorkflowLoop(ctx, store, run, loop)
				if err != nil {
					return err
				}
				if advanced {
					continue
				}
			}
			return s.projectWorkflowTaskStatus(ctx, store, run)
		}
		node, ok := run.Graph.node(ready[0])
		if !ok {
			return fmt.Errorf("engine returned unknown ready node %q", ready[0])
		}
		advanced, err := s.advanceWorkflowNode(ctx, store, run, node)
		if err != nil {
			return err
		}
		if !advanced {
			return s.projectWorkflowTaskStatus(ctx, store, run)
		}
	}
	return fmt.Errorf("workflow advance exceeded %d steps for task %s", workflowAdvanceStepLimit, taskID)
}

// runningWorkflowLoopNode returns the first Loop container that is currently
// iterating. Graph order keeps the choice deterministic; nested loops are
// rejected by the validator, so at most the top level is relevant.
func runningWorkflowLoopNode(run *WorkflowRun) (WorkflowNode, bool) {
	for _, n := range run.Graph.Nodes {
		if n.Kind == NodeKindLoop && run.NodeStates[n.ID] == NodeRunRunning {
			return n, true
		}
	}
	return WorkflowNode{}, false
}

func (s *Service) advanceWorkflowNode(ctx context.Context, store *TaskStore, run *WorkflowRun, node WorkflowNode) (bool, error) {
	switch node.Kind {
	case NodeKindCondition:
		return s.advanceWorkflowCondition(ctx, store, run, node)
	case NodeKindNotify:
		return s.advanceWorkflowNotify(ctx, store, run, node)
	case NodeKindAgent:
		nr, err := s.workflowAttemptForReadyNode(ctx, store, run, node)
		if err != nil {
			return false, err
		}
		_, err = s.RunWorkflowNodeAttempt(ctx, store, run, node, nr)
		if err != nil {
			return false, err
		}
		// Agent already projected NodeStates/task status; reload on the next step
		// regardless of success/fail/waiting so EngineReadyNodes sees the truth.
		return true, nil
	case NodeKindHumanGate:
		return s.advanceWorkflowHumanGate(ctx, store, run, node)
	case NodeKindLoop:
		return s.advanceWorkflowLoop(ctx, store, run, node)
	default:
		// Unknown kinds stop the driver rather than invent semantics.
		return false, nil
	}
}

func (s *Service) advanceWorkflowCondition(ctx context.Context, store *TaskStore, run *WorkflowRun, node WorkflowNode) (bool, error) {
	route, err := EvaluateWorkflowCondition(run, node)
	if err != nil {
		nr := workflowAttemptNodeRun(run, node)
		nr.Output = map[string]any{"error": err.Error()}
		nr.Status = NodeRunWaitingUser
		act := &TaskActivity{
			ID:   "wf:" + nr.ID + ":" + WorkflowEventNodeUnknown,
			Type: ActivityTypeAttention, Author: "system", Role: "system",
			Body: "条件无法求值：" + err.Error(),
		}
		if _, _, cerr := store.CommitWorkflowNodeTransition(ctx, run, WorkflowNodeTransition{
			NodeRun: nr, EventType: WorkflowEventNodeUnknown,
			EventPayload: map[string]any{"error": err.Error()}, Activity: act,
		}); cerr != nil {
			return false, cerr
		}
		return false, s.projectWorkflowTaskStatus(ctx, store, run)
	}
	nr := workflowAttemptNodeRun(run, node)
	nr.Output = map[string]any{"route": route}
	nr.Status = NodeRunSuccess
	if _, _, err := store.CommitWorkflowNodeTransition(ctx, run, WorkflowNodeTransition{
		NodeRun: nr, EventType: WorkflowEventNodeSucceeded, Route: route,
	}); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Service) advanceWorkflowNotify(ctx context.Context, store *TaskStore, run *WorkflowRun, node WorkflowNode) (bool, error) {
	message, _ := node.Params["message"].(string)
	message = strings.TrimSpace(message)
	if message == "" {
		message = node.Name
	}
	if message == "" {
		message = node.ID
	}
	nr := workflowAttemptNodeRun(run, node)
	nr.Output = map[string]any{"message": message}
	nr.Status = NodeRunSuccess
	act := &TaskActivity{
		ID:   "wf:" + nr.ID + ":" + WorkflowEventNodeNotified,
		Type: ActivityTypeSystemEvent, Author: "system", Role: "system", Body: message,
	}
	if _, _, err := store.CommitWorkflowNodeTransition(ctx, run, WorkflowNodeTransition{
		NodeRun: nr, EventType: WorkflowEventNodeNotified, Activity: act,
	}); err != nil {
		return false, err
	}
	return true, nil
}

// advanceWorkflowHumanGate materializes the waiting NodeRun once. The driver
// then stops: approval is a dedicated decision (D4), never inferred from a
// normal comment or from the fact that the node became ready.
func (s *Service) advanceWorkflowHumanGate(ctx context.Context, store *TaskStore, run *WorkflowRun, node WorkflowNode) (bool, error) {
	prompt, _ := node.Params["prompt"].(string)
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		prompt = strings.TrimSpace(node.Name)
	}
	if prompt == "" {
		prompt = node.ID
	}
	nr := workflowAttemptNodeRun(run, node)
	nr.Status = NodeRunWaitingUser
	nr.Output = map[string]any{"prompt": prompt}
	act := &TaskActivity{
		ID:   "wf:" + nr.ID + ":" + WorkflowEventHumanWaiting,
		Type: ActivityTypeAttention, Author: "system", Role: "system",
		Body: "等待你确认：" + prompt,
	}
	if _, _, err := store.CommitWorkflowNodeTransition(ctx, run, WorkflowNodeTransition{
		NodeRun: nr, EventType: WorkflowEventHumanWaiting, Activity: act,
	}); err != nil {
		return false, err
	}
	return false, s.projectWorkflowTaskStatus(ctx, store, run)
}

func (s *Service) commitSkippedWorkflowNode(ctx context.Context, store *TaskStore, run *WorkflowRun, node WorkflowNode) (NodeRun, error) {
	nr := workflowAttemptNodeRun(run, node)
	nr.Status = NodeRunSkipped
	nr.Output = map[string]any{"skipped": true}
	committed, _, err := store.CommitWorkflowNodeTransition(ctx, run, WorkflowNodeTransition{
		NodeRun: nr, EventType: WorkflowEventNodeSkipped,
	})
	return committed, err
}

// ── Bounded Loop ────────────────────────────────────────────────────────────

// advanceWorkflowLoop drives one synchronous step of a bounded Loop. Each call
// persists at most one transition (iteration start, one inner node, iteration
// increment, exit or bound-reached), so a crash between any two steps is
// recoverable from LoopState plus the iteration's NodeRuns:
//   - iteration finished but not yet incremented → increment is recomputed once
//     from the persisted iteration number;
//   - already incremented → the next iteration simply starts, never re-running
//     the previous one.
