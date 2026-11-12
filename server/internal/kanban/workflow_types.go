package kanban

import (
	"fmt"
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

// WorkflowRun origin (triggered_by). Conversation tasks record who asked for a
// run so failures and history can be attributed without joining activities.
const (
	WorkflowTriggeredByUser         = "user"
	WorkflowTriggeredByAgentAuto    = "agent_auto"
	WorkflowTriggeredByScheduled    = "scheduled"
	// WorkflowTriggeredByLegacyImport 标记一次性迁移生成的历史 Run。
	// 启动对账必须跳过它：这些 NodeRun 是审计证据，重放会真的再跑一遍 Agent。
	// 它也不占「每任务至多一个非终态 Run」的名额，用户留言可以替换它。
	WorkflowTriggeredByLegacyImport = "legacy_import"
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
	ID       string             `json:"id"`
	TaskID   string             `json:"task_id"`
	Version  string             `json:"version,omitempty"` // base WorkflowVersion id (may be empty for ad-hoc runs)
	Revision int                `json:"revision"`          // run edit revision, CAS
	Graph    WorkflowDefinition `json:"graph"`             // inlined immutable snapshot + future edits
	// IntegrationBranch 是创建 Run 时从项目当前 checkout 解析出的合并目标（D11）。
	// 运行时的 merge 只许读这个快照，不允许再隐式回落到 "main"。
	IntegrationBranch string `json:"integration_branch,omitempty"`
	// TriggeredBy / ActivityID 记录 Run 的来源（对话式任务 Phase 2）。TriggeredBy
	// 取值 user / agent_auto / scheduled；ActivityID 指向触发它的用户留言活动。
	// 两者在创建时写入、之后不再改，属于审计信息而不是运行状态。
	TriggeredBy    string                    `json:"triggered_by,omitempty"`
	ActivityID     string                    `json:"activity_id,omitempty"`
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
	ID         string `json:"id"`
	RunID      string `json:"run_id"`
	NodeID     string `json:"node_id"`
	LoopPath   string `json:"loop_path,omitempty"` // e.g. "l/2" iteration path, empty = top-level
	Iteration  int    `json:"iteration,omitempty"`
	Attempt    int    `json:"attempt"`
	Agent      string `json:"agent,omitempty"`
	Harness    string `json:"harness,omitempty"`
	Model      string `json:"model,omitempty"`
	SessionKey string `json:"session_key,omitempty"`
	// WorktreeID/Path/Branch 是该 NodeRun 的 workspace 归属（D16）。三者都空 =
	// 节点使用 Task 的共享主工作树/项目根，没有独立 worktree。
	WorktreeID     string         `json:"worktree_id,omitempty"`
	WorktreePath   string         `json:"worktree_path,omitempty"`
	WorktreeBranch string         `json:"worktree_branch,omitempty"`
	Status         string         `json:"status"`
	Input          map[string]any `json:"input,omitempty"`
	Output         map[string]any `json:"output,omitempty"`
	StartedAt      time.Time      `json:"started_at,omitempty"`
	FinishedAt     time.Time      `json:"finished_at,omitempty"`
	PrevAttemptID  string         `json:"prev_attempt_id,omitempty"`
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
