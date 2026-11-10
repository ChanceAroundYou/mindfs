package kanban

import (
	"encoding/json"
	"fmt"
	"strings"
)

// workflowLoopConfig is the persisted contract of one Loop node. Parsing is
// strict: a missing bound, exit expression or inner graph is an error, never a
// silent default (the spec makes all three mandatory).
type workflowLoopConfig struct {
	MaxIterations  int
	ExitExpression string
	Inner          WorkflowDefinition
}

func workflowLoopGraph(n WorkflowNode) (WorkflowDefinition, error) {
	raw, ok := n.Params["graph"]
	if !ok || raw == nil {
		return WorkflowDefinition{}, fmt.Errorf("loop node %q: inner graph required", n.ID)
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return WorkflowDefinition{}, err
	}
	var inner WorkflowDefinition
	if err := json.Unmarshal(b, &inner); err != nil {
		return WorkflowDefinition{}, fmt.Errorf("loop node %q: invalid inner graph: %w", n.ID, err)
	}
	if len(inner.Nodes) == 0 {
		return WorkflowDefinition{}, fmt.Errorf("loop node %q: inner graph must have at least one node", n.ID)
	}
	return inner, nil
}

func parseWorkflowLoopConfig(n WorkflowNode) (workflowLoopConfig, error) {
	max, ok := toInt(n.Params["max_iterations"])
	if !ok {
		return workflowLoopConfig{}, fmt.Errorf("loop node %q: max_iterations required (1..20)", n.ID)
	}
	if max < LoopMinMax || max > LoopMaxMax {
		return workflowLoopConfig{}, fmt.Errorf("loop node %q: max_iterations must be %d..%d", n.ID, LoopMinMax, LoopMaxMax)
	}
	exit, _ := n.Params["exit_expression"].(string)
	exit = strings.TrimSpace(exit)
	if exit == "" {
		return workflowLoopConfig{}, fmt.Errorf("loop node %q: exit_expression required", n.ID)
	}
	inner, err := workflowLoopGraph(n)
	if err != nil {
		return workflowLoopConfig{}, err
	}
	return workflowLoopConfig{MaxIterations: max, ExitExpression: exit, Inner: inner}, nil
}

// workflowLoopIterationRun projects one iteration's persisted NodeRuns into the
// engine's sub-run view. The run's own NodeStates/NodeOutputs never carry inner
// nodes: they are per-iteration facts, and older iterations must stay in the
// NodeRun history rather than be overwritten by the next pass.
func workflowLoopIterationRun(inner WorkflowDefinition, runs []NodeRun, loopID string, iteration int) *WorkflowRun {
	sub := &WorkflowRun{
		ID:             loopID,
		Graph:          inner,
		NodeStates:     map[string]string{},
		NodeOutputs:    map[string]map[string]any{},
		SelectedRoutes: map[string]string{},
	}
	best := map[string]NodeRun{}
	for _, nr := range runs {
		if nr.LoopPath != loopID || nr.Iteration != iteration {
			continue
		}
		if cur, seen := best[nr.NodeID]; !seen || nr.Attempt >= cur.Attempt {
			best[nr.NodeID] = nr
		}
	}
	for id, nr := range best {
		state := nr.Status
		if state == NodeRunQueued {
			// A requeued inner row is ready to execute again; the persisted row
			// (not a fresh attempt) is what the executor claims.
			state = NodeRunPending
		}
		sub.NodeStates[id] = state
		if nr.Output != nil {
			sub.NodeOutputs[id] = nr.Output
		}
		// Inner condition routes are per-iteration facts; they live on the
		// condition's NodeRun output so recovery needs no extra run field.
		if route, ok := nr.Output["route"].(string); ok && strings.TrimSpace(route) != "" {
			sub.SelectedRoutes[id] = route
		}
	}
	return sub
}

// workflowLoopIterationTerminal reports whether every inner node reached a
// terminal state in this iteration. Running nodes are not terminal.
func workflowLoopIterationTerminal(inner WorkflowDefinition, sub *WorkflowRun) bool {
	for _, n := range inner.Nodes {
		switch sub.NodeStates[n.ID] {
		case NodeRunSuccess, NodeRunSkipped, NodeRunFail, NodeRunWaitingUser, NodeRunUnknown, NodeRunCancelled:
		default:
			return false
		}
	}
	return true
}

// workflowExpressionRoot merges the run-level outputs with the current loop
// iteration's inner outputs. D6: expressions always read `node_id.output.*`;
// inner ids are validated to be unique across the whole workflow, so the two
// sets cannot shadow each other.
func workflowExpressionRoot(outer map[string]map[string]any, inner map[string]map[string]any) map[string]any {
	root := make(map[string]any, len(outer)+len(inner))
	for id, output := range outer {
		root[id] = map[string]any{"output": output}
	}
	for id, output := range inner {
		root[id] = map[string]any{"output": output}
	}
	return root
}

// workflowNodeByID resolves a node through loop inner graphs. Inner ids are
// validated unique across the whole definition, so the first match is the only
// match; callers that only need node kind/params (comment routing, activity)
// must use this rather than the top-level-only Graph.node.
func workflowNodeByID(d WorkflowDefinition, id string) (WorkflowNode, bool) {
	for _, n := range d.Nodes {
		if n.ID == id {
			return n, true
		}
		if n.Kind != NodeKindLoop {
			continue
		}
		inner, err := workflowLoopGraph(n)
		if err != nil {
			continue
		}
		if found, ok := workflowNodeByID(inner, id); ok {
			return found, true
		}
	}
	return WorkflowNode{}, false
}

// workflowAgentNodeIDs returns every agent node id, including agents nested in
// loop inner graphs. Used to classify active NodeRuns for comment binding.
func workflowAgentNodeIDs(d WorkflowDefinition) map[string]bool {
	ids := map[string]bool{}
	for _, n := range d.Nodes {
		switch n.Kind {
		case NodeKindAgent:
			ids[n.ID] = true
		case NodeKindLoop:
			if inner, err := workflowLoopGraph(n); err == nil {
				for id := range workflowAgentNodeIDs(inner) {
					ids[id] = true
				}
			}
		}
	}
	return ids
}

// EngineReadyNodes returns nodes ready to run and nodes to mark skipped.
// A node is ready when all its predecessors are success OR skipped via an
// unselected condition route. A join node only waits for actually-selected
// predecessors; unselected branches are reported as skipped (not blocking).
func EngineReadyNodes(run *WorkflowRun) (ready []string, skipped []string, err error) {
	if run == nil {
		return nil, nil, fmt.Errorf("nil run")
	}
	// Build predecessor list with route info
	type predInfo struct {
		from  string
		route string // empty for unconditional edges
	}
	preds := map[string][]predInfo{}
	// Also collect which nodes are targets of unselected routes => candidates for skip
	unselectedTargets := map[string]bool{}
	for _, e := range run.Graph.Edges {
		preds[e.To] = append(preds[e.To], predInfo{from: e.From, route: e.Route})
		from, _ := run.Graph.node(e.From)
		if from.Kind == NodeKindCondition && run.SelectedRoutes[e.From] != "" {
			if e.Route != run.SelectedRoutes[e.From] {
				unselectedTargets[e.To] = true
			}
		}
	}
	// Fixpoint skip propagation: a pending node whose every incoming edge
	// comes from an unselected condition route (or from an already-skipped
	// node) is skipped. This must run to a fixpoint so joins downstream of
	// a skipped branch are not blocked even though the caller has not yet
	// persisted the intermediate skipped states. Compute skippedSet once,
	// then the per-node loop below only classifies.
	skippedSet := map[string]bool{}
	changed := true
	for changed {
		changed = false
		for _, n := range run.Graph.Nodes {
			if skippedSet[n.ID] || run.NodeStates[n.ID] == NodeRunSuccess || run.NodeStates[n.ID] == NodeRunSkipped {
				continue
			}
			ps := preds[n.ID]
			if len(ps) == 0 || !unselectedTargets[n.ID] {
				// A node with no condition-unselected incoming edge cannot be
				// skipped unless every pred is already in skippedSet and at
				// least one pred is a skippedSet member through a chain.
				// For join nodes, each pred may individually be unselected via
				// its own ancestors; test per-pred below.
				if len(ps) == 0 {
					continue
				}
				// fall through to per-pred check
			}
			allUnselected := true
			for _, pr := range ps {
				from, _ := run.Graph.node(pr.from)
				sel := run.SelectedRoutes[pr.from]
				if from.Kind == NodeKindCondition && sel != "" && sel != pr.route {
					continue
				}
				if skippedSet[pr.from] {
					continue
				}
				allUnselected = false
				break
			}
			if allUnselected {
				// Must have at least one unselected condition edge in its
				// ancestry; if all preds are merely skippedSet, chain is
				// sufficient (transitive skip).
				skippedSet[n.ID] = true
				changed = true
			}
		}
	}
	// Seed skipped slice in node order from the fixpoint set, excluding
	// already-terminal nodes (engine contract: create one NodeRun per skip).
	for _, n := range run.Graph.Nodes {
		if skippedSet[n.ID] && (run.NodeStates[n.ID] == "" || run.NodeStates[n.ID] == NodeRunPending) {
			skipped = append(skipped, n.ID)
		}
	}
	for _, n := range run.Graph.Nodes {
		st := run.NodeStates[n.ID]
		// cancelled/unknown are terminal decisions (D8: never auto-replay an
		// ambiguous side effect); treating them as ready would re-enter the same
		// persisted row and spin the driver to its step limit.
		if st == NodeRunSuccess || st == NodeRunRunning || st == NodeRunQueued || st == NodeRunWaitingUser || st == NodeRunFail || st == NodeRunSkipped || st == NodeRunCancelled || st == NodeRunUnknown {
			continue
		}
		if skippedSet[n.ID] {
			continue
		}
		ps := preds[n.ID]
		if len(ps) == 0 {
			ready = append(ready, n.ID)
			continue
		}
		// routeSelected reports whether edge p (as an incoming edge to the
		// current node) is on the branch actually chosen at its condition source.
		// Non-condition preds and undecided conditions are not unselected.
		isUnselectedEdge := func(p predInfo) bool {
			from, _ := run.Graph.node(p.from)
			if from.Kind != NodeKindCondition {
				return false
			}
			sel := run.SelectedRoutes[p.from]
			return sel != "" && sel != p.route
		}
		// Ready iff every actually-selected predecessor is success/skipped.
		// Unselected-route predecessors and their (transitively) skipped
		// subtrees never block a join.
		allDone := true
		for _, p := range ps {
			if isUnselectedEdge(p) {
				continue
			}
			// A predecessor whose own state is skipped (unselected branch
			// already materialized) counts as satisfied for joins.
			if run.NodeStates[p.from] == NodeRunSkipped || skippedSet[p.from] {
				continue
			}
			if run.NodeStates[p.from] != NodeRunSuccess {
				allDone = false
				break
			}
		}
		if allDone {
			ready = append(ready, n.ID)
		}
	}
	return ready, skipped, nil
}

// EngineEvaluateLoop evaluates the loop exit expression after an iteration.
// Returns NodeRunSuccess if exit=true, NodeRunRunning to continue another
// iteration, or NodeRunWaitingUser when max iterations reached (needs-user).
func EngineEvaluateLoop(run *WorkflowRun, loopID string, outputs map[string]any) (string, error) {
	ls, ok := run.LoopState[loopID]
	if !ok || ls == nil {
		return "", fmt.Errorf("loop %q: no loop state", loopID)
	}
	expr := run.LoopExitExpr[loopID]
	// fall back to node param
	if expr == "" {
		if n, ok := run.Graph.node(loopID); ok {
			expr, _ = n.Params["exit_expression"].(string)
		}
	}
	if expr == "" {
		return NodeRunRunning, nil
	}
	exit, err := EvalSafeExpression(expr, outputs)
	if err != nil {
		return "", err
	}
	if exit {
		return NodeRunSuccess, nil
	}
	if ls.Iteration >= ls.MaxIterations {
		return NodeRunWaitingUser, nil
	}
	return NodeRunRunning, nil
}

// ProjectStatusFromRun projects WorkflowRun/NodeRun states onto the legacy
// Task.Status column (spec §新旧执行模型互斥). Nil run => "" (legacy engine
// keeps driving Status; migrated tasks only are projected).
func ProjectStatusFromRun(run *WorkflowRun) string {
	if run == nil {
		return ""
	}
	hasWaiting := false
	hasRunning := false
	hasPending := false
	allSuccess := len(run.Graph.Nodes) > 0
	for _, n := range run.Graph.Nodes {
		st := run.NodeStates[n.ID]
		switch st {
		case NodeRunWaitingUser, NodeRunUnknown:
			hasWaiting = true
			allSuccess = false
		case NodeRunRunning, NodeRunQueued:
			hasRunning = true
			allSuccess = false
		case NodeRunSuccess, NodeRunSkipped:
		case NodeRunFail:
			return StatusFail
		case NodeRunCancelled:
			return StatusCancelled
		default:
			hasPending = true
			allSuccess = false
		}
	}
	if hasWaiting {
		return StatusWaitingUser
	}
	if allSuccess {
		return StatusSuccess
	}
	if hasRunning {
		return StatusRunning
	}
	if hasPending {
		return StatusPending
	}
	return StatusPending
}
