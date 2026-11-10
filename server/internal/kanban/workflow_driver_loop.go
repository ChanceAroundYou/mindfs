package kanban

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

func (s *Service) advanceWorkflowLoop(ctx context.Context, store *TaskStore, run *WorkflowRun, node WorkflowNode) (bool, error) {
	if run.LoopState == nil {
		run.LoopState = map[string]*LoopState{}
	}
	if run.NodeStates == nil {
		run.NodeStates = map[string]string{}
	}
	if run.NodeOutputs == nil {
		run.NodeOutputs = map[string]map[string]any{}
	}
	cfg, err := parseWorkflowLoopConfig(node)
	if err != nil {
		// An unparseable loop (legacy/hand-edited snapshot) must stop visibly,
		// not silently succeed or spin.
		return false, s.stopWorkflowLoop(ctx, store, run, node, NodeRunWaitingUser,
			map[string]any{"error": err.Error()}, WorkflowEventLoopLimit)
	}
	ls := run.LoopState[node.ID]
	if ls == nil || ls.Iteration <= 0 {
		fresh := &LoopState{Iteration: 1, MaxIterations: cfg.MaxIterations}
		run.LoopState[node.ID] = fresh
		run.NodeStates[node.ID] = NodeRunRunning
		if err := s.saveWorkflowLoopProjection(ctx, store, run, node, WorkflowEventLoopIteration,
			map[string]any{"iteration": 1}); err != nil {
			return false, err
		}
		return true, s.projectWorkflowTaskStatus(ctx, store, run)
	}
	if ls.Iteration > cfg.MaxIterations {
		return false, s.stopWorkflowLoop(ctx, store, run, node, NodeRunWaitingUser,
			map[string]any{"iterations": ls.Iteration, "reason": "loop iteration exceeds max_iterations"},
			WorkflowEventLoopLimit)
	}
	ls.MaxIterations = cfg.MaxIterations

	runs, err := store.ListWorkflowNodeRuns(ctx, run.ID)
	if err != nil {
		return false, err
	}
	sub := workflowLoopIterationRun(cfg.Inner, runs, node.ID, ls.Iteration)
	ready, skipped, err := EngineReadyNodes(sub)
	if err != nil {
		return false, err
	}
	if len(skipped) > 0 {
		inner, ok := cfg.Inner.node(skipped[0])
		if !ok {
			return false, fmt.Errorf("loop %q: engine returned unknown skipped node %q", node.ID, skipped[0])
		}
		if _, _, err := store.CommitWorkflowNodeTransition(ctx, run, WorkflowNodeTransition{
			NodeRun:   workflowLoopNodeRun(run, node, inner, ls.Iteration, NodeRunSkipped),
			EventType: WorkflowEventNodeSkipped,
		}); err != nil {
			return false, err
		}
		return true, nil
	}
	if len(ready) > 0 {
		inner, ok := cfg.Inner.node(ready[0])
		if !ok {
			return false, fmt.Errorf("loop %q: engine returned unknown ready node %q", node.ID, ready[0])
		}
		switch inner.Kind {
		case NodeKindAgent:
			nr := workflowLoopNodeRun(run, node, inner, ls.Iteration, NodeRunQueued)
			finished, runErr := s.RunWorkflowNodeAttempt(ctx, store, run, inner, nr)
			if runErr != nil {
				// The attempt is already persisted as a terminal failure; the next
				// step maps it onto the loop projection. Only a non-persisted
				// error (e.g. DB failure) is returned as fatal.
				if finished.Status == NodeRunFail || finished.Status == NodeRunUnknown {
					return true, nil
				}
				return false, runErr
			}
			return true, nil
		case NodeKindCondition:
			return s.advanceWorkflowLoopCondition(ctx, store, run, node, inner, ls.Iteration, sub)
		case NodeKindNotify:
			return s.advanceWorkflowLoopNotify(ctx, store, run, node, inner, ls.Iteration)
		default:
			return false, s.stopWorkflowLoop(ctx, store, run, node, NodeRunWaitingUser,
				map[string]any{"iterations": ls.Iteration, "reason": "unsupported inner node kind " + inner.Kind},
				WorkflowEventLoopLimit)
		}
	}
	if !workflowLoopIterationTerminal(cfg.Inner, sub) {
		// No ready node yet every inner node is not terminal: the driver is
		// waiting on an inner node that is still running (restart window).
		// Stop here; startup reconciliation decides whether it may resume.
		return false, s.projectWorkflowTaskStatus(ctx, store, run)
	}
	return s.finishWorkflowLoopIteration(ctx, store, run, node, cfg, ls, sub)
}

func (s *Service) advanceWorkflowLoopCondition(ctx context.Context, store *TaskStore, run *WorkflowRun, loop, inner WorkflowNode, iteration int, sub *WorkflowRun) (bool, error) {
	root := workflowExpressionRoot(run.NodeOutputs, sub.NodeOutputs)
	route, err := evaluateWorkflowConditionRoot(root, inner)
	if err != nil {
		// An unresolvable inner condition is a human problem: persist the
		// waiting NodeRun, then pause the loop for a decision. Continuing with a
		// guessed route would silently run the wrong branch.
		nr := workflowLoopNodeRun(run, loop, inner, iteration, NodeRunWaitingUser)
		nr.Output = map[string]any{"error": err.Error()}
		if _, _, cerr := store.CommitWorkflowNodeTransition(ctx, run, WorkflowNodeTransition{
			NodeRun: nr, EventType: WorkflowEventNodeUnknown,
			EventPayload: map[string]any{"error": err.Error()},
		}); cerr != nil {
			return false, cerr
		}
		return false, s.stopWorkflowLoop(ctx, store, run, loop, NodeRunWaitingUser,
			workflowLoopOutput(iteration, sub.NodeOutputs, err.Error()), WorkflowEventNodeUnknown)
	}
	nr := workflowLoopNodeRun(run, loop, inner, iteration, NodeRunSuccess)
	// The selected route lives on the NodeRun output so recovery can rebuild the
	// iteration's skip/join state without a run-level field.
	nr.Output = map[string]any{"route": route}
	if _, _, err := store.CommitWorkflowNodeTransition(ctx, run, WorkflowNodeTransition{
		NodeRun: nr, EventType: WorkflowEventNodeSucceeded, Route: route,
	}); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Service) advanceWorkflowLoopNotify(ctx context.Context, store *TaskStore, run *WorkflowRun, loop, inner WorkflowNode, iteration int) (bool, error) {
	message, _ := inner.Params["message"].(string)
	message = strings.TrimSpace(message)
	if message == "" {
		message = inner.Name
	}
	if message == "" {
		message = inner.ID
	}
	nr := workflowLoopNodeRun(run, loop, inner, iteration, NodeRunSuccess)
	nr.Output = map[string]any{"message": message}
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

// finishWorkflowLoopIteration evaluates the exit expression only after every
// inner node is terminal. Missing/invalid fields and a bound hit both stop as
// waiting_user with the prior outputs kept; only an explicit exit succeeds.
func (s *Service) finishWorkflowLoopIteration(ctx context.Context, store *TaskStore, run *WorkflowRun, node WorkflowNode, cfg workflowLoopConfig, ls *LoopState, sub *WorkflowRun) (bool, error) {
	for _, inner := range cfg.Inner.Nodes {
		switch sub.NodeStates[inner.ID] {
		case NodeRunFail, NodeRunCancelled:
			return false, s.stopWorkflowLoop(ctx, store, run, node, NodeRunFail,
				workflowLoopOutput(ls.Iteration, sub.NodeOutputs, "inner node "+inner.ID+" failed"),
				WorkflowEventNodeFailed)
		case NodeRunWaitingUser, NodeRunUnknown:
			return false, s.stopWorkflowLoop(ctx, store, run, node, NodeRunWaitingUser,
				workflowLoopOutput(ls.Iteration, sub.NodeOutputs, "inner node "+inner.ID+" needs attention"),
				WorkflowEventNodeBlocked)
		}
	}
	root := workflowExpressionRoot(run.NodeOutputs, sub.NodeOutputs)
	exit, err := EvalSafeExpression(cfg.ExitExpression, root)
	if err != nil {
		return false, s.stopWorkflowLoop(ctx, store, run, node, NodeRunWaitingUser,
			workflowLoopOutput(ls.Iteration, sub.NodeOutputs, err.Error()), WorkflowEventNodeUnknown)
	}
	if exit {
		run.LoopState[node.ID] = ls
		run.NodeStates[node.ID] = NodeRunSuccess
		run.NodeOutputs[node.ID] = workflowLoopOutput(ls.Iteration, sub.NodeOutputs, "")
		if err := s.saveWorkflowLoopProjection(ctx, store, run, node, WorkflowEventLoopExited,
			map[string]any{"iteration": ls.Iteration}); err != nil {
			return false, err
		}
		// Downstream nodes may be ready now; keep the driver running.
		return true, nil
	}
	if ls.Iteration >= cfg.MaxIterations {
		run.LoopState[node.ID] = ls
		run.NodeStates[node.ID] = NodeRunWaitingUser
		run.NodeOutputs[node.ID] = workflowLoopOutput(ls.Iteration, sub.NodeOutputs, "loop reached max_iterations")
		if err := s.saveWorkflowLoopProjection(ctx, store, run, node, WorkflowEventLoopLimit,
			map[string]any{"iteration": ls.Iteration}); err != nil {
			return false, err
		}
		return false, s.projectWorkflowTaskStatus(ctx, store, run)
	}
	ls.Iteration++
	run.LoopState[node.ID] = ls
	run.NodeStates[node.ID] = NodeRunRunning
	if err := s.saveWorkflowLoopProjection(ctx, store, run, node, WorkflowEventLoopIteration,
		map[string]any{"iteration": ls.Iteration}); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Service) stopWorkflowLoop(ctx context.Context, store *TaskStore, run *WorkflowRun, node WorkflowNode, status string, output map[string]any, eventType string) error {
	run.NodeStates[node.ID] = status
	if run.NodeOutputs == nil {
		run.NodeOutputs = map[string]map[string]any{}
	}
	run.NodeOutputs[node.ID] = output
	if err := s.saveWorkflowLoopProjection(ctx, store, run, node, eventType,
		map[string]any{"status": status}); err != nil {
		return err
	}
	return s.projectWorkflowTaskStatus(ctx, store, run)
}

// saveWorkflowLoopProjection persists LoopState/NodeStates plus its typed event.
// A concurrent writer winning the revision race is not a failure: this driver
// reloads the authoritative run on its next step, so the transition is simply
// recomputed from the persisted iteration number.
func (s *Service) saveWorkflowLoopProjection(ctx context.Context, store *TaskStore, run *WorkflowRun, node WorkflowNode, eventType string, payload map[string]any) error {
	err := store.SaveWorkflowRunWithEvent(ctx, run, WorkflowRunEvent{
		Type: eventType, NodeID: node.ID, Payload: payload,
	})
	if errors.Is(err, ErrRunConflict) {
		return nil
	}
	return err
}

func workflowLoopNodeRun(run *WorkflowRun, loop, inner WorkflowNode, iteration int, status string) NodeRun {
	return NodeRun{
		ID:        workflowNodeRunID(run.ID, inner.ID, loop.ID, iteration, 1),
		RunID:     run.ID,
		NodeID:    inner.ID,
		LoopPath:  loop.ID,
		Iteration: iteration,
		Attempt:   1,
		Status:    status,
	}
}

func workflowLoopOutput(iteration int, outputs map[string]map[string]any, reason string) map[string]any {
	inner := make(map[string]any, len(outputs))
	for id, output := range outputs {
		inner[id] = output
	}
	out := map[string]any{"iterations": iteration, "outputs": inner}
	if reason != "" {
		out["reason"] = reason
	}
	return out
}

func (s *Service) projectWorkflowTaskStatus(ctx context.Context, store *TaskStore, run *WorkflowRun) error {
	// A paused/cancelled task is the operator's state, not a derived one. Node
	// completion may land after pause; projecting the run over it would make the
	// pause look ignored and could restart claims on the next wake.
	if task, err := store.GetTask(ctx, run.TaskID); err == nil {
		if task.Status == StatusPaused || isTerminalStatus(task.Status) {
			return nil
		}
	}
	status := ProjectStatusFromRun(run)
	if err := store.UpdateTaskStatus(ctx, run.TaskID, status, isTerminalStatus(status)); err != nil {
		return err
	}
	if detail, err := store.GetDetail(ctx, run.TaskID); err == nil && s != nil && s.Runner != nil {
		s.Runner.TaskUpdated(detail.Task.RootID, detail)
	}
	return nil
}

// workflowAttemptNodeRun builds the top-level attempt identity. Loop packages
// will supply loop_path/iteration/attempt through their own keys.
func workflowAttemptNodeRun(run *WorkflowRun, node WorkflowNode) NodeRun {
	return NodeRun{
		ID:      workflowNodeRunID(run.ID, node.ID, "", 0, 1),
		RunID:   run.ID,
		NodeID:  node.ID,
		Attempt: 1,
		Status:  NodeRunQueued,
	}
}

func workflowNodeRunID(runID, nodeID, loopPath string, iteration, attempt int) string {
	return fmt.Sprintf("%s:%s:%s:%d:%d", runID, nodeID, loopPath, iteration, attempt)
}

// EvaluateWorkflowCondition implements the D6-confirmed expression root:
// every executed node contributes `{<node_id>: {output: <persisted output>}}`,
// so expressions read fully qualified paths such as `build.output.files > 0`.
// A missing node/field is an error (never silently false).
func EvaluateWorkflowCondition(run *WorkflowRun, node WorkflowNode) (string, error) {
	return evaluateWorkflowConditionRoot(workflowExpressionRoot(run.NodeOutputs, nil), node)
}

// evaluateWorkflowConditionRoot is the shared D6 evaluator: the caller decides
// which outputs are in scope (top-level run, or run + current loop iteration).
func evaluateWorkflowConditionRoot(root map[string]any, node WorkflowNode) (string, error) {
	expr, _ := node.Params["expression"].(string)
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return "", fmt.Errorf("condition node %q: expression required", node.ID)
	}
	routes, err := workflowConditionRoutes(node)
	if err != nil {
		return "", err
	}
	ok, err := EvalSafeExpression(expr, root)
	if err != nil {
		return "", err
	}
	route := WorkflowRouteFail
	if ok {
		route = WorkflowRoutePass
	}
	for _, r := range routes {
		if r == route {
			return route, nil
		}
	}
	return "", fmt.Errorf("condition node %q: route %q not declared", node.ID, route)
}

func workflowConditionRoutes(node WorkflowNode) ([]string, error) {
	raw, _ := node.Params["routes"].([]any)
	var routes []string
	for _, r := range raw {
		if s, ok := r.(string); ok && strings.TrimSpace(s) != "" {
			routes = append(routes, s)
		}
	}
	// JSON round-trips may also produce []string (e.g. Go-built fixtures).
	if len(routes) == 0 {
		if ss, ok := node.Params["routes"].([]string); ok {
			routes = append(routes, ss...)
		}
	}
	if len(routes) == 0 {
		return nil, fmt.Errorf("condition node %q: routes required", node.ID)
	}
	return routes, nil
}
