package kanban

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// ── WP-05/06 Workflow domain types (spec 2026-11-09) ────────────────────────
// WorkflowVersion is immutable and lives on the control plane (main node).
// WorkflowRun/NodeRun are data-plane, stored with the Task's node and carry an
// inlined immutable snapshot so workers never query the control plane.

const (
	NodeKindAgent     = "agent"
	NodeKindCondition = "condition"
	NodeKindHumanGate = "human_gate"
	NodeKindLoop      = "loop"
	NodeKindNotify    = "notify"
)

const (
	NodeRunPending     = "pending"
	NodeRunQueued      = "queued"
	NodeRunRunning     = "running"
	NodeRunSuccess     = "success"
	NodeRunFail        = "fail"
	NodeRunWaitingUser = "waiting_user"
	NodeRunSkipped     = "skipped"
	NodeRunUnknown     = "unknown"
	NodeRunCancelled   = "cancelled"
)

const (
	LoopDefaultMax = 5
	LoopMinMax     = 1
	LoopMaxMax     = 20
)

// WorkflowNode is one node in a workflow graph. Params holds typed config per kind:
//   - agent:    {agent,harness,model,prompt,writes_workspace(bool,default true),session_reuse_policy}
//   - condition:{expression(string),routes([]string)} selects exactly one route
//   - human_gate:{prompt}
//   - loop:     {max_iterations(int 1..20,default 5),exit_expression(string),graph(WorkflowDefinition)}
//   - notify:   {message}
type WorkflowNode struct {
	ID     string         `json:"id"`
	Name   string         `json:"name"`
	Kind   string         `json:"kind"`
	Params map[string]any `json:"params,omitempty"`
}

// WorkflowEdge is a directed dependency. Route is set only on edges out of a
// condition node; empty Route means unconditional.
type WorkflowEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Route string `json:"route,omitempty"`
}

type WorkflowDefinition struct {
	Nodes  []WorkflowNode `json:"nodes"`
	Edges  []WorkflowEdge `json:"edges,omitempty"`
	RootID string         `json:"root_id,omitempty"` // scope: empty=global, else project
}

// Triggered reports whether the node has a persisted NodeRun (queued/started).
func (d WorkflowDefinition) node(id string) (WorkflowNode, bool) {
	for _, n := range d.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return WorkflowNode{}, false
}

type WorkflowVersion struct {
	Version     string             `json:"version"` // immutable id e.g. "wf1:v3"
	WorkflowID  string             `json:"workflow_id"`
	Revision    string             `json:"revision"` // draft revision this was published from
	Graph       WorkflowDefinition `json:"graph"`
	Author      string             `json:"author"`
	PublishedBy string             `json:"published_by"`
	PublishedAt time.Time          `json:"published_at"`
}

type WorkflowDraft struct {
	WorkflowID string             `json:"workflow_id"`
	Revision   string             `json:"revision"` // current revision id, e.g. "rN"
	Graph      WorkflowDefinition `json:"graph"`
	Author     string             `json:"author"`
	UpdatedAt  time.Time          `json:"updated_at"`
}

// ResolvedRevision returns the pinned revision id for CAS / publish confirmation.
func (d WorkflowDraft) ResolvedRevision() string { return d.Revision }

type LoopState struct {
	Iteration     int `json:"iteration"`
	MaxIterations int `json:"max_iterations"`
}

type WorkflowRun struct {
	ID             string                    `json:"id"`
	TaskID         string                    `json:"task_id"`
	Version        string                    `json:"version,omitempty"` // base WorkflowVersion id (may be empty for ad-hoc runs)
	Revision       int                       `json:"revision"`          // run edit revision, CAS
	Graph          WorkflowDefinition        `json:"graph"`             // inlined immutable snapshot + future edits
	NodeStates     map[string]string         `json:"node_states,omitempty"`
	NodeOutputs    map[string]map[string]any `json:"node_outputs,omitempty"`
	SelectedRoutes map[string]string         `json:"selected_routes,omitempty"`
	LoopState      map[string]*LoopState     `json:"loop_state,omitempty"`
	LoopExitExpr   map[string]string         `json:"loop_exit_expr,omitempty"`
	CreatedAt      time.Time                 `json:"created_at"`
	UpdatedAt      time.Time                 `json:"updated_at"`
}

// FrozenNodeIDs returns nodes that already triggered (have a NodeRun).
func (r *WorkflowRun) FrozenNodeIDs() map[string]bool {
	frozen := map[string]bool{}
	for id, st := range r.NodeStates {
		if st != "" && st != NodeRunPending {
			frozen[id] = true
		}
	}
	return frozen
}

type NodeRun struct {
	ID            string         `json:"id"`
	RunID         string         `json:"run_id"`
	NodeID        string         `json:"node_id"`
	LoopPath      string         `json:"loop_path,omitempty"` // e.g. "l/2" iteration path, empty = top-level
	Iteration     int            `json:"iteration,omitempty"`
	Attempt       int            `json:"attempt"`
	Agent         string         `json:"agent,omitempty"`
	Harness       string         `json:"harness,omitempty"`
	Model         string         `json:"model,omitempty"`
	SessionKey    string         `json:"session_key,omitempty"`
	Status        string         `json:"status"`
	Input         map[string]any `json:"input,omitempty"`
	Output        map[string]any `json:"output,omitempty"`
	StartedAt     time.Time      `json:"started_at,omitempty"`
	FinishedAt    time.Time      `json:"finished_at,omitempty"`
	PrevAttemptID string         `json:"prev_attempt_id,omitempty"`
}

// ── WriterGate (A4 WP-04 owns the real implementation; this is the contract) ──
// Default serial gate serializes per-task. A4 will replace with per-worktree
// granularity: key = (task_id, worktree_id), same worktree serial, different
// worktrees parallel. Engine must call Acquire before starting a write node.
type WriterGate interface {
	Acquire(taskID, worktreeID string) error
	Release(taskID, worktreeID string)
}

type serialWriterGate struct {
	mu   sync.Mutex
	held map[string]bool
}

func NewSerialWriterGate() WriterGate {
	return &serialWriterGate{held: map[string]bool{}}
}

func (g *serialWriterGate) Acquire(taskID, worktreeID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.held[taskID] {
		return errors.New("writer gate: task already holds write lock")
	}
	g.held[taskID] = true
	return nil
}

func (g *serialWriterGate) Release(taskID, worktreeID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.held, taskID)
}

// ── Template suggestion (WP-06: suggest, never auto-publish) ────────────────

type TemplateSuggestion struct {
	SourceRunID string             `json:"source_run_id"`
	Graph       WorkflowDefinition `json:"graph"`
	Note        string             `json:"note"`
	Published   bool               `json:"published"`
}

func SuggestTemplateFromRun(run *WorkflowRun, author string) TemplateSuggestion {
	if run == nil {
		return TemplateSuggestion{Note: "no run to suggest from"}
	}
	return TemplateSuggestion{
		SourceRunID: run.ID,
		Graph:       run.Graph,
		Note:        fmt.Sprintf("suggested by %s from run %s; requires explicit user confirm before publish", author, run.ID),
		Published:   false,
	}
}
