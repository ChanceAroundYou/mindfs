package kanban

import (
	"encoding/json"
	"fmt"
	"strings"
)

// WorkflowAuthoringProposal is the untrusted candidate an Agent returns for a
// Draft or a Run's future subgraph. It is never applied by the producer: the
// human reviews the typed operations and the existing CAS apply is a separate
// action. Summary/rationale are for the reviewer; operations are the only
// persistence input.
type WorkflowAuthoringProposal struct {
	Summary      string                   `json:"summary"`
	Rationale    string                   `json:"rationale,omitempty"`
	BaseRevision string                   `json:"base_revision"`
	Operations   []WorkflowPatchOperation `json:"operations"`
}

var workflowAuthoringOps = map[string]bool{
	"add_node":           true,
	"update_future_node": true,
	"remove_future_node": true,
	"add_edge":           true,
	"remove_future_edge": true,
}

// ParseWorkflowAuthoringProposal extracts one strict JSON proposal from raw
// Agent text and rejects anything that could become an unreviewable write.
// A nil proposal always comes with at least one issue; callers must fail closed.
func ParseWorkflowAuthoringProposal(raw, expectedBase string) (*WorkflowAuthoringProposal, []WorkflowPatchIssue) {
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return nil, []WorkflowPatchIssue{{Field: "response", Message: "response must contain a JSON object"}}
	}

	var proposal WorkflowAuthoringProposal
	if err := json.Unmarshal([]byte(raw[start:end+1]), &proposal); err != nil {
		return nil, []WorkflowPatchIssue{{Field: "response", Message: "response is not valid JSON: " + err.Error()}}
	}

	var issues []WorkflowPatchIssue
	proposal.Summary = strings.TrimSpace(proposal.Summary)
	if proposal.Summary == "" {
		issues = append(issues, WorkflowPatchIssue{Field: "summary", Message: "summary is required"})
	}
	proposal.BaseRevision = strings.TrimSpace(proposal.BaseRevision)
	if proposal.BaseRevision == "" {
		issues = append(issues, WorkflowPatchIssue{Field: "base_revision", Message: "base_revision is required"})
	} else if proposal.BaseRevision != expectedBase {
		issues = append(issues, WorkflowPatchIssue{
			Field:   "base_revision",
			Message: fmt.Sprintf("proposal base_revision %q does not match current revision %q", proposal.BaseRevision, expectedBase),
		})
	}

	switch {
	case len(proposal.Operations) == 0:
		issues = append(issues, WorkflowPatchIssue{Field: "operations", Message: "operations must contain at least one operation"})
	case len(proposal.Operations) > WorkflowPatchMaxOperations:
		issues = append(issues, WorkflowPatchIssue{
			Field:   "operations",
			Message: fmt.Sprintf("operations exceeds the limit of %d", WorkflowPatchMaxOperations),
		})
	default:
		issues = append(issues, validateAuthoringOperations(proposal.Operations)...)
	}

	if len(issues) > 0 {
		return nil, issues
	}
	return &proposal, nil
}

func validateAuthoringOperations(operations []WorkflowPatchOperation) []WorkflowPatchIssue {
	var issues []WorkflowPatchIssue
	seen := map[string]bool{}
	for i := range operations {
		op := &operations[i]
		op.ID = strings.TrimSpace(op.ID)
		if op.ID == "" {
			issues = append(issues, WorkflowPatchIssue{Field: "operations", Message: "operation id is required"})
			continue
		}
		if seen[op.ID] {
			issues = append(issues, WorkflowPatchIssue{OperationID: op.ID, Field: "id", Message: "duplicate operation id: " + op.ID})
			continue
		}
		if !workflowAuthoringOps[op.Op] {
			issues = append(issues, WorkflowPatchIssue{OperationID: op.ID, Field: "op", Message: "unknown operation: " + op.Op})
			continue
		}
		seen[op.ID] = true
		if msg := missingAuthoringField(*op); msg != "" {
			issues = append(issues, WorkflowPatchIssue{OperationID: op.ID, Field: "operation", Message: msg})
		}
	}
	return issues
}

func missingAuthoringField(op WorkflowPatchOperation) string {
	switch op.Op {
	case "add_node":
		if op.Node == nil {
			return "add_node requires node"
		}
	case "update_future_node":
		if op.NodeID == "" || op.Node == nil {
			return "update_future_node requires node_id and node"
		}
	case "remove_future_node":
		if op.NodeID == "" {
			return "remove_future_node requires node_id"
		}
	case "add_edge", "remove_future_edge":
		if op.Edge == nil {
			return op.Op + " requires edge"
		}
	}
	return ""
}

// NewWorkflowAuthoringBaseGraph is the deterministic empty Draft that a
// create-scope proposal is reviewed against. Apply rebuilds this exact graph,
// so the reviewed operations and the persisted Draft share one base and
// creation stays a single CAS write (revision r1).
func NewWorkflowAuthoringBaseGraph() WorkflowDefinition {
	return WorkflowDefinition{
		Nodes: []WorkflowNode{{
			ID:     "start",
			Name:   "Start",
			Kind:   NodeKindAgent,
			Params: map[string]any{"writes_workspace": true},
		}},
		Edges: []WorkflowEdge{},
	}
}
