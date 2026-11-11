package kanban

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Agent-first workflow patches. A patch is a typed, ordered list of structural
// edits — never an arbitrary JSON-path update — so the server can validate every
// operation, enforce frozen history, and produce a deterministic diff. The same
// value is used for draft patches (control plane) and run future-graph patches
// (task-node data plane); applying it is a separate, CAS-checked action.
const (
	WorkflowPatchMaxOperations = 200
	WorkflowPatchMaxNodes      = 500
	WorkflowPatchMaxNodeBytes  = 64 << 10
)

var ErrWorkflowPatchInvalid = errors.New("workflow patch invalid")

type WorkflowPatchOperation struct {
	ID     string        `json:"id"`
	Op     string        `json:"op"`
	Node   *WorkflowNode `json:"node,omitempty"`
	NodeID string        `json:"node_id,omitempty"`
	Edge   *WorkflowEdge `json:"edge,omitempty"`
}

type WorkflowPatch struct {
	BaseRevision string                   `json:"base_revision,omitempty"`
	Author       string                   `json:"author,omitempty"`
	Operations   []WorkflowPatchOperation `json:"operations"`
}

type WorkflowPatchIssue struct {
	OperationID string `json:"operation_id,omitempty"`
	// NodeID labels issues that are scoped to a node rather than a patch
	// operation (dry-run simulation). Patch validation leaves it empty.
	NodeID  string `json:"node_id,omitempty"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

type WorkflowPatchError struct {
	Issues []WorkflowPatchIssue
}

func (e *WorkflowPatchError) Error() string {
	if e == nil || len(e.Issues) == 0 {
		return ErrWorkflowPatchInvalid.Error()
	}
	return fmt.Sprintf("%s: %s", ErrWorkflowPatchInvalid.Error(), e.Issues[0].Message)
}

func (e *WorkflowPatchError) Unwrap() error { return ErrWorkflowPatchInvalid }

type WorkflowDiffOperation struct {
	OperationID string        `json:"operation_id"`
	Op          string        `json:"op"`
	NodeID      string        `json:"node_id,omitempty"`
	Edge        *WorkflowEdge `json:"edge,omitempty"`
	Before      *WorkflowNode `json:"before,omitempty"`
	After       *WorkflowNode `json:"after,omitempty"`
}

type WorkflowDiff struct {
	BaseRevision string                  `json:"base_revision,omitempty"`
	Operations   []WorkflowDiffOperation `json:"operations"`
}

// ValidateWorkflowPatch applies the patch to a copy of base and returns a
// deterministic diff plus all structural issues. It never mutates base, never
// writes to a store, and never evaluates an expression or runs an Agent.
func ValidateWorkflowPatch(base WorkflowDefinition, patch WorkflowPatch) (WorkflowDiff, []WorkflowPatchIssue, error) {
	_, diff, issues := ApplyWorkflowPatchToGraph(base, patch, nil)
	if len(issues) > 0 {
		return diff, issues, &WorkflowPatchError{Issues: issues}
	}
	return diff, nil, nil
}

// ApplyWorkflowPatchToGraph is the pure patch engine. frozen contains node ids
// that may not be changed (run patches); nil means no frozen history (drafts).
func ApplyWorkflowPatchToGraph(base WorkflowDefinition, patch WorkflowPatch, frozen map[string]bool) (WorkflowDefinition, WorkflowDiff, []WorkflowPatchIssue) {
	graph := cloneGraph(base)
	diff := WorkflowDiff{BaseRevision: patch.BaseRevision}
	issues := []WorkflowPatchIssue{}
	if len(patch.Operations) == 0 {
		issues = append(issues, WorkflowPatchIssue{Field: "operations", Message: "at least one operation required"})
		return graph, diff, issues
	}
	if len(patch.Operations) > WorkflowPatchMaxOperations {
		issues = append(issues, WorkflowPatchIssue{Field: "operations", Message: fmt.Sprintf("at most %d operations allowed", WorkflowPatchMaxOperations)})
		return graph, diff, issues
	}
	seenOps := map[string]bool{}
	lastChangedOp := ""
	for _, op := range patch.Operations {
		opID := strings.TrimSpace(op.ID)
		if opID == "" {
			issues = append(issues, WorkflowPatchIssue{Field: "id", Message: "operation id required"})
			continue
		}
		if seenOps[opID] {
			issues = append(issues, WorkflowPatchIssue{OperationID: opID, Field: "id", Message: "duplicate operation id"})
			continue
		}
		seenOps[opID] = true
		changed, opIssues := applyWorkflowPatchOperation(&graph, &diff, opID, op, frozen)
		if len(opIssues) > 0 {
			issues = append(issues, opIssues...)
			continue
		}
		if !changed {
			continue
		}
		lastChangedOp = opID
		if n := workflowDefinitionNodeCount(graph); n > WorkflowPatchMaxNodes {
			issues = append(issues, WorkflowPatchIssue{OperationID: opID, Field: "nodes", Message: fmt.Sprintf("at most %d nodes allowed (got %d)", WorkflowPatchMaxNodes, n)})
			return graph, diff, issues
		}
	}
	if len(issues) == 0 {
		if err := ValidateWorkflowDefinition(graph); err != nil {
			field := "graph"
			if lastChangedOp == "" {
				field = "operations"
			}
			issues = append(issues, WorkflowPatchIssue{OperationID: lastChangedOp, Field: field, Message: err.Error()})
		}
	}
	return graph, diff, issues
}

func applyWorkflowPatchOperation(graph *WorkflowDefinition, diff *WorkflowDiff, opID string, op WorkflowPatchOperation, frozen map[string]bool) (bool, []WorkflowPatchIssue) {
	switch op.Op {
	case "add_node":
		return patchAddNode(graph, diff, opID, op, frozen)
	case "update_future_node":
		return patchUpdateNode(graph, diff, opID, op, frozen)
	case "remove_future_node":
		return patchRemoveNode(graph, diff, opID, op, frozen)
	case "add_edge":
		return patchAddEdge(graph, diff, opID, op, frozen)
	case "remove_future_edge":
		return patchRemoveEdge(graph, diff, opID, op, frozen)
	default:
		return false, []WorkflowPatchIssue{{OperationID: opID, Field: "op", Message: fmt.Sprintf("unknown operation %q", op.Op)}}
	}
}

func patchAddNode(graph *WorkflowDefinition, diff *WorkflowDiff, opID string, op WorkflowPatchOperation, frozen map[string]bool) (bool, []WorkflowPatchIssue) {
	n := op.Node
	if n == nil || strings.TrimSpace(n.ID) == "" {
		return false, []WorkflowPatchIssue{{OperationID: opID, Field: "node.id", Message: "node with id required"}}
	}
	if _, exists := graph.node(n.ID); exists {
		return false, []WorkflowPatchIssue{{OperationID: opID, Field: "node.id", Message: fmt.Sprintf("node %q already exists", n.ID)}}
	}
	if issue := validatePatchNode(n, opID, frozen); issue != nil {
		return false, []WorkflowPatchIssue{*issue}
	}
	graph.Nodes = append(graph.Nodes, cloneWorkflowNode(*n))
	diff.Operations = append(diff.Operations, WorkflowDiffOperation{OperationID: opID, Op: op.Op, NodeID: n.ID, After: cloneWorkflowNodePtr(*n)})
	return true, nil
}

func patchUpdateNode(graph *WorkflowDefinition, diff *WorkflowDiff, opID string, op WorkflowPatchOperation, frozen map[string]bool) (bool, []WorkflowPatchIssue) {
	n := op.Node
	if n == nil || strings.TrimSpace(n.ID) == "" {
		return false, []WorkflowPatchIssue{{OperationID: opID, Field: "node.id", Message: "node with id required"}}
	}
	if frozen[n.ID] {
		return false, []WorkflowPatchIssue{{OperationID: opID, Field: "node", Message: fmt.Sprintf("node %q is frozen", n.ID)}}
	}
	idx := -1
	for i := range graph.Nodes {
		if graph.Nodes[i].ID == n.ID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false, []WorkflowPatchIssue{{OperationID: opID, Field: "node.id", Message: fmt.Sprintf("node %q not found", n.ID)}}
	}
	if issue := validatePatchNode(n, opID, frozen); issue != nil {
		return false, []WorkflowPatchIssue{*issue}
	}
	before := cloneWorkflowNode(graph.Nodes[idx])
	graph.Nodes[idx] = cloneWorkflowNode(*n)
	diff.Operations = append(diff.Operations, WorkflowDiffOperation{OperationID: opID, Op: op.Op, NodeID: n.ID, Before: &before, After: cloneWorkflowNodePtr(*n)})
	return true, nil
}

func patchRemoveNode(graph *WorkflowDefinition, diff *WorkflowDiff, opID string, op WorkflowPatchOperation, frozen map[string]bool) (bool, []WorkflowPatchIssue) {
	id := strings.TrimSpace(op.NodeID)
	if id == "" && op.Node != nil {
		id = strings.TrimSpace(op.Node.ID)
	}
	if id == "" {
		return false, []WorkflowPatchIssue{{OperationID: opID, Field: "node_id", Message: "node_id required"}}
	}
	if frozen[id] {
		return false, []WorkflowPatchIssue{{OperationID: opID, Field: "node", Message: fmt.Sprintf("node %q is frozen", id)}}
	}
	idx := -1
	for i := range graph.Nodes {
		if graph.Nodes[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false, []WorkflowPatchIssue{{OperationID: opID, Field: "node_id", Message: fmt.Sprintf("node %q not found", id)}}
	}
	var removedEdges []WorkflowEdge
	for _, e := range graph.Edges {
		if e.From == id || e.To == id {
			if frozen[e.From] || frozen[e.To] {
				return false, []WorkflowPatchIssue{{OperationID: opID, Field: "edge", Message: fmt.Sprintf("node %q has a frozen incident edge %s->%s", id, e.From, e.To)}}
			}
			removedEdges = append(removedEdges, e)
		}
	}
	before := cloneWorkflowNode(graph.Nodes[idx])
	graph.Nodes = append(graph.Nodes[:idx], graph.Nodes[idx+1:]...)
	diff.Operations = append(diff.Operations, WorkflowDiffOperation{OperationID: opID, Op: op.Op, NodeID: id, Before: &before})
	sort.Slice(removedEdges, func(i, j int) bool {
		a, b := removedEdges[i], removedEdges[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		return a.Route < b.Route
	})
	for _, e := range removedEdges {
		edge := e
		graph.Edges = removeWorkflowEdge(graph.Edges, edge)
		diff.Operations = append(diff.Operations, WorkflowDiffOperation{OperationID: opID, Op: "remove_future_edge", NodeID: id, Edge: &edge})
	}
	return true, nil
}

func patchAddEdge(graph *WorkflowDefinition, diff *WorkflowDiff, opID string, op WorkflowPatchOperation, frozen map[string]bool) (bool, []WorkflowPatchIssue) {
	e := op.Edge
	if e == nil {
		return false, []WorkflowPatchIssue{{OperationID: opID, Field: "edge", Message: "edge required"}}
	}
	if strings.TrimSpace(e.From) == "" {
		return false, []WorkflowPatchIssue{{OperationID: opID, Field: "edge.from", Message: "edge.from required"}}
	}
	if strings.TrimSpace(e.To) == "" {
		return false, []WorkflowPatchIssue{{OperationID: opID, Field: "edge.to", Message: "edge.to required"}}
	}
	from, ok := graph.node(e.From)
	if !ok {
		return false, []WorkflowPatchIssue{{OperationID: opID, Field: "edge.from", Message: fmt.Sprintf("edge.from %q not found", e.From)}}
	}
	if _, ok := graph.node(e.To); !ok {
		return false, []WorkflowPatchIssue{{OperationID: opID, Field: "edge.to", Message: fmt.Sprintf("edge.to %q not found", e.To)}}
	}
	if frozen[e.From] || frozen[e.To] {
		return false, []WorkflowPatchIssue{{OperationID: opID, Field: "edge", Message: fmt.Sprintf("edge %s->%s touches a frozen node", e.From, e.To)}}
	}
	if from.Kind == NodeKindCondition {
		routes, _ := from.Params["routes"].([]any)
		if strings.TrimSpace(e.Route) == "" {
			return false, []WorkflowPatchIssue{{OperationID: opID, Field: "edge.route", Message: fmt.Sprintf("edge from condition %q requires a route", e.From)}}
		}
		found := false
		for _, r := range routes {
			if s, ok := r.(string); ok && s == e.Route {
				found = true
				break
			}
		}
		if !found {
			return false, []WorkflowPatchIssue{{OperationID: opID, Field: "edge.route", Message: fmt.Sprintf("route %q is not declared by condition %q", e.Route, e.From)}}
		}
	}
	for _, existing := range graph.Edges {
		if existing == *e {
			return false, []WorkflowPatchIssue{{OperationID: opID, Field: "edge", Message: fmt.Sprintf("edge %s->%s already exists", e.From, e.To)}}
		}
	}
	edge := *e
	graph.Edges = append(graph.Edges, edge)
	diff.Operations = append(diff.Operations, WorkflowDiffOperation{OperationID: opID, Op: op.Op, NodeID: e.To, Edge: &edge})
	return true, nil
}

func patchRemoveEdge(graph *WorkflowDefinition, diff *WorkflowDiff, opID string, op WorkflowPatchOperation, frozen map[string]bool) (bool, []WorkflowPatchIssue) {
	e := op.Edge
	if e == nil {
		return false, []WorkflowPatchIssue{{OperationID: opID, Field: "edge", Message: "edge required"}}
	}
	if frozen[e.From] || frozen[e.To] {
		return false, []WorkflowPatchIssue{{OperationID: opID, Field: "edge", Message: fmt.Sprintf("edge %s->%s touches a frozen node", e.From, e.To)}}
	}
	found := false
	for _, existing := range graph.Edges {
		if existing == *e {
			found = true
			break
		}
	}
	if !found {
		return false, []WorkflowPatchIssue{{OperationID: opID, Field: "edge", Message: fmt.Sprintf("edge %s->%s not found", e.From, e.To)}}
	}
	edge := *e
	graph.Edges = removeWorkflowEdge(graph.Edges, edge)
	diff.Operations = append(diff.Operations, WorkflowDiffOperation{OperationID: opID, Op: op.Op, NodeID: e.To, Edge: &edge})
	return true, nil
}

func validatePatchNode(n *WorkflowNode, opID string, frozen map[string]bool) *WorkflowPatchIssue {
	if frozen[n.ID] {
		return &WorkflowPatchIssue{OperationID: opID, Field: "node", Message: fmt.Sprintf("node %q is frozen", n.ID)}
	}
	switch n.Kind {
	case NodeKindAgent, NodeKindCondition, NodeKindHumanGate, NodeKindLoop, NodeKindNotify:
	default:
		return &WorkflowPatchIssue{OperationID: opID, Field: "node.kind", Message: fmt.Sprintf("unknown node kind %q", n.Kind)}
	}
	b, err := json.Marshal(n)
	if err != nil {
		return &WorkflowPatchIssue{OperationID: opID, Field: "node", Message: err.Error()}
	}
	if len(b) > WorkflowPatchMaxNodeBytes {
		return &WorkflowPatchIssue{OperationID: opID, Field: "node", Message: fmt.Sprintf("node exceeds %d bytes", WorkflowPatchMaxNodeBytes)}
	}
	return nil
}

func removeWorkflowEdge(edges []WorkflowEdge, target WorkflowEdge) []WorkflowEdge {
	for i, e := range edges {
		if e == target {
			return append(edges[:i], edges[i+1:]...)
		}
	}
	return edges
}

func workflowDefinitionNodeCount(d WorkflowDefinition) int {
	count := len(d.Nodes)
	for _, n := range d.Nodes {
		if n.Kind != NodeKindLoop {
			continue
		}
		inner, err := workflowLoopGraph(n)
		if err != nil {
			continue
		}
		count += len(inner.Nodes)
	}
	return count
}

func cloneWorkflowNode(n WorkflowNode) WorkflowNode {
	b, _ := json.Marshal(n)
	var out WorkflowNode
	_ = json.Unmarshal(b, &out)
	return out
}

func cloneWorkflowNodePtr(n WorkflowNode) *WorkflowNode {
	c := cloneWorkflowNode(n)
	return &c
}
