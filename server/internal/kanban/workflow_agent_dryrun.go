package kanban

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Dry-run is a pure static simulation. It reuses the production graph engine
// and safe expression evaluator, but never touches a TaskStore, a Runner, a
// session, notifications or the database. Agent nodes are simulated only when
// the caller supplies their output; otherwise the simulation stops and explains
// that the Agent would run.
const (
	WorkflowDryRunDone        = "done"
	WorkflowDryRunWaitingUser = "waiting_user"
)

const (
	WorkflowDryRunSkip          = "skip"
	WorkflowDryRunWouldNotify   = "would_notify"
	WorkflowDryRunWouldRunAgent = "would_run_agent"
	WorkflowDryRunWouldEvaluate = "would_evaluate_condition"
	WorkflowDryRunHumanGate     = "human_gate"
	WorkflowDryRunLoopBudget    = "loop_budget"
	WorkflowDryRunWouldRunLoop  = "would_run_loop"
	WorkflowDryRunBlocked       = "blocked"
	WorkflowDryRunWorkflowDone  = "workflow_done"
	workflowDryRunMaxSteps      = 1000
)

type WorkflowDryRunInput struct {
	Graph     WorkflowDefinition        `json:"graph"`
	Outputs   map[string]map[string]any `json:"outputs,omitempty"`
	LoopState map[string]*LoopState     `json:"loop_state,omitempty"`
	// NodeStates / SelectedRoutes let a caller simulate from the persisted state
	// of a live run. Without them a run's already-triggered nodes would look
	// like they are about to run again.
	NodeStates     map[string]string `json:"node_states,omitempty"`
	SelectedRoutes map[string]string `json:"selected_routes,omitempty"`
}

type WorkflowDryRunStep struct {
	NodeID string `json:"node_id"`
	Action string `json:"action"`
	Detail string `json:"detail,omitempty"`
}

type WorkflowDryRunResult struct {
	Status string               `json:"status"`
	Steps  []WorkflowDryRunStep `json:"steps"`
	Issues []WorkflowPatchIssue `json:"issues,omitempty"`
}

func RunWorkflowDryRun(in WorkflowDryRunInput) (WorkflowDryRunResult, error) {
	graph := cloneGraph(in.Graph)
	if err := ValidateWorkflowDefinition(graph); err != nil {
		return WorkflowDryRunResult{Status: WorkflowDryRunWaitingUser, Issues: []WorkflowPatchIssue{{Field: "graph", Message: err.Error()}}}, err
	}
	if n := workflowDefinitionNodeCount(graph); n > WorkflowPatchMaxNodes {
		err := fmt.Errorf("at most %d nodes allowed (got %d)", WorkflowPatchMaxNodes, n)
		return WorkflowDryRunResult{Status: WorkflowDryRunWaitingUser, Issues: []WorkflowPatchIssue{{Field: "nodes", Message: err.Error()}}}, err
	}
	outputs, err := cloneDryRunOutputs(in.Outputs)
	if err != nil {
		return WorkflowDryRunResult{}, err
	}
	loops, err := cloneDryRunLoopState(in.LoopState)
	if err != nil {
		return WorkflowDryRunResult{}, err
	}
	run := &WorkflowRun{
		Graph:          graph,
		NodeStates:     cloneDryRunStates(in.NodeStates),
		NodeOutputs:    outputs,
		SelectedRoutes: cloneDryRunStates(in.SelectedRoutes),
		LoopState:      loops,
	}
	for id := range outputs {
		if _, ok := graph.node(id); !ok {
			continue
		}
		// A supplied output stands in for an Agent only when the run has no
		// recorded state for that node. A persisted failed/unknown node must not
		// be rewritten as success by a caller-supplied output.
		if st := run.NodeStates[id]; st == "" || st == NodeRunPending {
			run.NodeStates[id] = NodeRunSuccess
		}
	}
	result := WorkflowDryRunResult{Status: WorkflowDryRunDone}
	for step := 0; step < workflowDryRunMaxSteps; step++ {
		ready, skipped, err := EngineReadyNodes(run)
		if err != nil {
			return result, err
		}
		for _, id := range skipped {
			run.NodeStates[id] = NodeRunSkipped
			result.Steps = append(result.Steps, WorkflowDryRunStep{NodeID: id, Action: WorkflowDryRunSkip, Detail: "condition route not selected"})
		}
		if len(ready) == 0 {
			break
		}
		node, ok := graph.node(ready[0])
		if !ok {
			return result, fmt.Errorf("engine returned unknown ready node %q", ready[0])
		}
		stop, wait := simulateDryRunNode(run, node, &result)
		if wait {
			result.Status = WorkflowDryRunWaitingUser
			return result, nil
		}
		if stop {
			return result, nil
		}
	}
	if len(result.Steps) == 0 {
		result.Steps = append(result.Steps, WorkflowDryRunStep{Action: WorkflowDryRunWorkflowDone, Detail: "no runnable node"})
	}
	return result, nil
}

// simulateDryRunNode returns wait=true when the workflow would stop and need a
// human, and stop=true when the simulation cannot continue without guessing.
func simulateDryRunNode(run *WorkflowRun, node WorkflowNode, result *WorkflowDryRunResult) (stop, wait bool) {
	switch node.Kind {
	case NodeKindAgent:
		if _, hasOutput := run.NodeOutputs[node.ID]; !hasOutput {
			result.Steps = append(result.Steps, WorkflowDryRunStep{NodeID: node.ID, Action: WorkflowDryRunWouldRunAgent, Detail: "no simulated output; the Agent would run"})
			return false, true
		}
		run.NodeStates[node.ID] = NodeRunSuccess
		return false, false
	case NodeKindCondition:
		route, err := evaluateWorkflowConditionRoot(workflowExpressionRoot(run.NodeOutputs, nil), node)
		if err != nil {
			result.Issues = append(result.Issues, WorkflowPatchIssue{NodeID: node.ID, Field: "node.expression", Message: err.Error()})
			result.Steps = append(result.Steps, WorkflowDryRunStep{NodeID: node.ID, Action: WorkflowDryRunBlocked, Detail: err.Error()})
			return false, true
		}
		run.SelectedRoutes[node.ID] = route
		run.NodeOutputs[node.ID] = map[string]any{"route": route}
		run.NodeStates[node.ID] = NodeRunSuccess
		result.Steps = append(result.Steps, WorkflowDryRunStep{NodeID: node.ID, Action: WorkflowDryRunWouldEvaluate, Detail: "route=" + route})
		return false, false
	case NodeKindNotify:
		run.NodeStates[node.ID] = NodeRunSuccess
		message, _ := node.Params["message"].(string)
		result.Steps = append(result.Steps, WorkflowDryRunStep{NodeID: node.ID, Action: WorkflowDryRunWouldNotify, Detail: strings.TrimSpace(message)})
		return false, false
	case NodeKindHumanGate:
		result.Steps = append(result.Steps, WorkflowDryRunStep{NodeID: node.ID, Action: WorkflowDryRunHumanGate, Detail: "workflow waits for an explicit approve/reject"})
		return false, true
	case NodeKindLoop:
		ls := run.LoopState[node.ID]
		if ls != nil && ls.MaxIterations > 0 && ls.Iteration >= ls.MaxIterations {
			result.Steps = append(result.Steps, WorkflowDryRunStep{NodeID: node.ID, Action: WorkflowDryRunLoopBudget, Detail: fmt.Sprintf("iteration %d / max %d; workflow pauses for user", ls.Iteration, ls.MaxIterations)})
			return false, true
		}
		result.Steps = append(result.Steps, WorkflowDryRunStep{NodeID: node.ID, Action: WorkflowDryRunWouldRunLoop, Detail: "loop iteration would run; supply loop_state to inspect the budget stop"})
		return false, true
	default:
		result.Issues = append(result.Issues, WorkflowPatchIssue{NodeID: node.ID, Field: "node.kind", Message: fmt.Sprintf("unknown node kind %q", node.Kind)})
		result.Steps = append(result.Steps, WorkflowDryRunStep{NodeID: node.ID, Action: WorkflowDryRunBlocked, Detail: "unknown node kind"})
		return false, true
	}
}

func cloneDryRunStates(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneDryRunOutputs(in map[string]map[string]any) (map[string]map[string]any, error) {
	out := map[string]map[string]any{}
	if len(in) == 0 {
		return out, nil
	}
	b, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func cloneDryRunLoopState(in map[string]*LoopState) (map[string]*LoopState, error) {
	out := map[string]*LoopState{}
	if len(in) == 0 {
		return out, nil
	}
	b, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}
