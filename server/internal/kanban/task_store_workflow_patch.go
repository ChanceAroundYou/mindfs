package kanban

import (
	"context"
	"errors"
	"strings"
)

// ApplyWorkflowRunPatch edits only the untriggered part of a run's graph.
// Frozen nodes and their incident edges come from both the run projection and
// the per-attempt NodeRun rows (loop inner nodes only exist in the latter).
func (s *TaskStore) ApplyWorkflowRunPatch(ctx context.Context, taskID, runID string, baseRevision int, patch WorkflowPatch) (*WorkflowRun, WorkflowDiff, error) {
	if strings.TrimSpace(taskID) == "" || strings.TrimSpace(runID) == "" {
		return nil, WorkflowDiff{}, errors.New("task id and workflow run id required")
	}
	run, err := s.WorkflowRunByID(ctx, runID)
	if err != nil {
		return nil, WorkflowDiff{}, err
	}
	if run.TaskID != taskID {
		return nil, WorkflowDiff{}, ErrWorkflowRunNotFound
	}
	if run.Revision != baseRevision {
		return run, WorkflowDiff{}, ErrRunConflict
	}
	frozen, err := s.workflowRunFrozenNodes(ctx, run)
	if err != nil {
		return run, WorkflowDiff{}, err
	}
	graph, diff, issues := ApplyWorkflowPatchToGraph(run.Graph, patch, frozen)
	if len(issues) > 0 {
		return run, diff, &WorkflowPatchError{Issues: issues}
	}
	run.Graph = graph
	if err := s.SaveWorkflowRun(ctx, run); err != nil {
		return run, diff, err
	}
	return run, diff, nil
}

// ValidateWorkflowRunPatch is the read-only twin of ApplyWorkflowRunPatch. It
// performs the same revision and frozen-history checks but persists nothing.
func (s *TaskStore) ValidateWorkflowRunPatch(ctx context.Context, taskID, runID string, baseRevision int, patch WorkflowPatch) (WorkflowDiff, []WorkflowPatchIssue, error) {
	_, diff, issues, err := s.PreviewWorkflowRunPatch(ctx, taskID, runID, baseRevision, patch)
	if err != nil {
		return diff, issues, err
	}
	if len(issues) > 0 {
		return diff, issues, &WorkflowPatchError{Issues: issues}
	}
	return diff, nil, nil
}

// PreviewWorkflowRunPatch returns the candidate future graph in addition to the
// diff so an authoring gateway can statically dry-run it. It applies the same
// CAS and frozen-history checks as the write path and persists nothing.
func (s *TaskStore) PreviewWorkflowRunPatch(ctx context.Context, taskID, runID string, baseRevision int, patch WorkflowPatch) (WorkflowDefinition, WorkflowDiff, []WorkflowPatchIssue, error) {
	if strings.TrimSpace(taskID) == "" || strings.TrimSpace(runID) == "" {
		return WorkflowDefinition{}, WorkflowDiff{}, nil, errors.New("task id and workflow run id required")
	}
	run, err := s.WorkflowRunByID(ctx, runID)
	if err != nil {
		return WorkflowDefinition{}, WorkflowDiff{}, nil, err
	}
	if run.TaskID != taskID {
		return WorkflowDefinition{}, WorkflowDiff{}, nil, ErrWorkflowRunNotFound
	}
	if run.Revision != baseRevision {
		return WorkflowDefinition{}, WorkflowDiff{}, nil, ErrRunConflict
	}
	frozen, err := s.workflowRunFrozenNodes(ctx, run)
	if err != nil {
		return WorkflowDefinition{}, WorkflowDiff{}, nil, err
	}
	graph, diff, issues := ApplyWorkflowPatchToGraph(run.Graph, patch, frozen)
	return graph, diff, issues, nil
}

// workflowRunFrozenNodes is the single definition of triggered history used by
// both full-graph edits and typed patches.
func (s *TaskStore) workflowRunFrozenNodes(ctx context.Context, run *WorkflowRun) (map[string]bool, error) {
	frozen := run.FrozenNodeIDs()
	// Loop inner nodes never enter the run-level NodeStates projection (they are
	// per-iteration facts), so their history lives only in NodeRun rows. Treat a
	// node with any persisted attempt — and the loop container owning it — as
	// triggered, otherwise deleting an inner node would orphan its records.
	nodeRuns, err := s.ListWorkflowNodeRuns(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	for _, nr := range nodeRuns {
		frozen[nr.NodeID] = true
		if nr.LoopPath != "" {
			frozen[nr.LoopPath] = true
		}
	}
	return frozen, nil
}
