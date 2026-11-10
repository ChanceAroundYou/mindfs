package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// WorkflowNodeOutcome is the agent's explicit report for one NodeRun. A nil
// transport error alone never implies completion: "silent" and "blocked" must
// stay distinguishable so the driver never advances on a guess.
type WorkflowNodeOutcome string

const (
	WorkflowNodeDone    WorkflowNodeOutcome = "done"
	WorkflowNodeBlocked WorkflowNodeOutcome = "blocked"
	WorkflowNodeSilent  WorkflowNodeOutcome = "silent"
	WorkflowNodeUnknown WorkflowNodeOutcome = "unknown"
)

// WorkflowNodeExecution is everything the app adapter needs to run one workflow
// node. It deliberately carries NodeRun identity instead of a StageRun: workflow
// execution must never dual-write the legacy stage tables.
type WorkflowNodeExecution struct {
	RootID          string
	RuntimeRootPath string
	Task            Task
	Run             *WorkflowRun
	Node            WorkflowNode
	NodeRun         NodeRun
	SessionPolicy   string
	// ReuseSessionKey is the earlier attempt/iteration session for
	// session_reuse_policy=same_stage. Empty means there is none.
	ReuseSessionKey string
	Prompt          string
}

// WorkflowNodeAgentProfile is the per-node agent selection. Workflow nodes pick
// agent/harness/model independently from legacy StageTemplate.
type WorkflowNodeAgentProfile struct {
	Agent       string
	Harness     string
	Model       string
	Mode        string
	Effort      string
	FastService string
	PlanMode    bool
}

// WorkflowNodeAgentProfileOf reads the node params. Missing values stay empty so
// the existing agent defaults still apply; an invalid type fails closed by
// ignoring that param (the adapter validates the final agent name).
func WorkflowNodeAgentProfileOf(node WorkflowNode) WorkflowNodeAgentProfile {
	str := func(key string) string {
		v, _ := node.Params[key].(string)
		return strings.TrimSpace(v)
	}
	p := WorkflowNodeAgentProfile{
		Agent:       str("agent"),
		Harness:     str("harness"),
		Model:       str("model"),
		Mode:        str("mode"),
		Effort:      str("effort"),
		FastService: str("fast_service"),
	}
	if plan, ok := node.Params["plan_mode"].(bool); ok {
		p.PlanMode = plan
	}
	return p
}

// WorkflowNodeResult is the adapter's answer. SessionKey is always the session
// the turn really used; Output is only populated from the strict JSON envelope.
type WorkflowNodeResult struct {
	Outcome    WorkflowNodeOutcome
	SessionKey string
	Output     map[string]any
	Reason     string
	StartedAt  time.Time
	FinishedAt time.Time
}

// WorkflowNodeRunner is the execution port for Agent nodes. The legacy Runner
// stays untouched; app wiring implements this one with the agent pool/session.
type WorkflowNodeRunner interface {
	EnsureWorkflowNodeSession(ctx context.Context, exec WorkflowNodeExecution) (string, error)
	RunWorkflowNode(ctx context.Context, exec WorkflowNodeExecution) (WorkflowNodeResult, error)
}

// WorkflowNodeSessionPolicy reads the node param; D2 default is per-node (new
// session, isolated context). Only explicit task_main reuses the task session.
func WorkflowNodeSessionPolicy(node WorkflowNode) string {
	raw, _ := node.Params["session_reuse_policy"].(string)
	switch p := strings.TrimSpace(raw); p {
	case SessionReuseTaskMain, SessionReuseSameStage, SessionReuseAlwaysNew, SessionReusePerNode:
		return p
	default:
		return SessionReusePerNode
	}
}

// WorkflowNodePrompt returns the configured node prompt. Workflow agent nodes
// have no legacy stage template, so an empty prompt is a configuration error,
// not an empty turn.
func WorkflowNodePrompt(node WorkflowNode) (string, error) {
	raw, _ := node.Params["prompt"].(string)
	p := strings.TrimSpace(raw)
	if p == "" {
		return "", fmt.Errorf("workflow agent node %q requires params.prompt", node.ID)
	}
	return p, nil
}

// BuildWorkflowNodeOutputContract is the text appended to every workflow agent
// prompt. The parser and this instruction must stay in one place: if the
// contract says one thing and ParseWorkflowNodeEnvelope accepts another, nodes
// silently park as waiting_user.
func BuildWorkflowNodeOutputContract(nodeID string) string {
	return fmt.Sprintf(
		"\n\n<mindfs-workflow-node-output node=%q>\n"+
			"完成本节点后，只输出一个 JSON 对象，不要 markdown 代码块，不要附加其它文字：\n"+
			`{"outcome":"done|blocked|silent","output":{...},"reason":""}`+"\n"+
			"outcome 只能是 done、blocked、silent；blocked/silent 必须填写 reason。\n"+
			"output 用来把结果交给后续节点；没有结果时输出空对象。\n"+
			"</mindfs-workflow-node-output>",
		nodeID,
	)
}

// PrepareWorkflowNode validates the node, resolves its session and returns the
// execution context. The workflow attempt executor calls this before the
// external turn so the NodeRun's session binding can be committed first.
func (s *Service) PrepareWorkflowNode(ctx context.Context, store *TaskStore, run *WorkflowRun, node WorkflowNode, nodeRun NodeRun) (WorkflowNodeExecution, error) {
	return s.prepareWorkflowNodeAt(ctx, store, run, node, nodeRun, "")
}

// prepareWorkflowNodeAt is PrepareWorkflowNode with an explicit physical root.
// RunWorkflowNodeAttempt passes the workspace resolved by
// PrepareWorkflowNodeWorktree (independent tree or shared Task tree); one-shot
// callers leave runtimeRoot empty and keep the historical Task.WorktreePath.
func (s *Service) prepareWorkflowNodeAt(ctx context.Context, store *TaskStore, run *WorkflowRun, node WorkflowNode, nodeRun NodeRun, runtimeRoot string) (WorkflowNodeExecution, error) {
	if s == nil || store == nil || run == nil {
		return WorkflowNodeExecution{}, errors.New("workflow service, store and run required")
	}
	if node.Kind != NodeKindAgent {
		return WorkflowNodeExecution{}, fmt.Errorf("node %q is %s, not agent", node.ID, node.Kind)
	}
	runner := s.workflowNodeRunner()
	if runner == nil {
		return WorkflowNodeExecution{}, errors.New("workflow node runner not configured")
	}
	if nodeRun.RunID != run.ID || nodeRun.NodeID != node.ID {
		return WorkflowNodeExecution{}, fmt.Errorf("node run %s does not belong to run/node %s/%s", nodeRun.ID, run.ID, node.ID)
	}
	prompt, err := WorkflowNodePrompt(node)
	if err != nil {
		return WorkflowNodeExecution{}, err
	}
	task, err := store.GetTask(ctx, run.TaskID)
	if err != nil {
		return WorkflowNodeExecution{}, err
	}
	rootPath := strings.TrimSpace(runtimeRoot)
	if rootPath == "" {
		rootPath = strings.TrimSpace(task.WorktreePath)
	}
	exec := WorkflowNodeExecution{
		RootID:          task.RootID,
		RuntimeRootPath: rootPath,
		Task:            task,
		Run:             run,
		Node:            node,
		NodeRun:         nodeRun,
		SessionPolicy:   WorkflowNodeSessionPolicy(node),
		Prompt:          prompt,
	}
	// D2 same_stage：同一 node + loop_path 的历次 iteration/attempt 复用同一个
	// session。NodeRun 自己是新行，所以往前找最近一个已绑定 session 的行。
	if exec.SessionPolicy == SessionReuseSameStage && strings.TrimSpace(nodeRun.SessionKey) == "" {
		key, err := store.LatestWorkflowNodeSessionKey(ctx, run.ID, node.ID, nodeRun.LoopPath)
		if err != nil {
			return WorkflowNodeExecution{}, err
		}
		exec.ReuseSessionKey = key
	}
	sessionKey, err := runner.EnsureWorkflowNodeSession(ctx, exec)
	if err != nil {
		return WorkflowNodeExecution{}, err
	}
	exec.NodeRun.SessionKey = strings.TrimSpace(sessionKey)
	return exec, nil
}

// RunPreparedWorkflowNode runs one prepared turn. It does not persist state;
// the attempt executor owns the lifecycle around it.
func (s *Service) RunPreparedWorkflowNode(ctx context.Context, exec WorkflowNodeExecution) (WorkflowNodeResult, error) {
	runner := s.workflowNodeRunner()
	if runner == nil {
		return WorkflowNodeResult{}, errors.New("workflow node runner not configured")
	}
	result, err := runner.RunWorkflowNode(ctx, exec)
	if err != nil {
		return WorkflowNodeResult{}, err
	}
	// 会话是 Ensure 阶段定下的；adapter 没回报时用这里的值补上，NodeRun 落库
	// 不能因为适配层漏填就丢掉 session 归属。
	if strings.TrimSpace(result.SessionKey) == "" {
		result.SessionKey = exec.NodeRun.SessionKey
	}
	return result, nil
}

// StartWorkflowNode is prepare+run for callers that only need a one-shot turn.
// Lifecycle-owning callers (RunWorkflowNodeAttempt) use the halves so session
// binding is persisted before the external turn starts.
func (s *Service) StartWorkflowNode(ctx context.Context, store *TaskStore, run *WorkflowRun, node WorkflowNode, nodeRun NodeRun) (WorkflowNodeResult, error) {
	exec, err := s.PrepareWorkflowNode(ctx, store, run, node, nodeRun)
	if err != nil {
		return WorkflowNodeResult{}, err
	}
	return s.RunPreparedWorkflowNode(ctx, exec)
}

func (s *Service) workflowNodeRunner() WorkflowNodeRunner {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.WorkflowNodeRunnerPort
}

// ParseWorkflowNodeEnvelope implements D5: the agent must answer with one JSON
// object carrying outcome/output/reason. Anything else is an error and must be
// surfaced as waiting_user by the caller — no guessing, no plain-text fallback.
func ParseWorkflowNodeEnvelope(text string) (WorkflowNodeResult, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return WorkflowNodeResult{}, errors.New("workflow node output is empty; JSON envelope required")
	}
	var envelope struct {
		Outcome string         `json:"outcome"`
		Output  map[string]any `json:"output"`
		Reason  string         `json:"reason"`
	}
	dec := json.NewDecoder(strings.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&envelope); err != nil {
		return WorkflowNodeResult{}, fmt.Errorf("workflow node output is not a JSON envelope: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return WorkflowNodeResult{}, errors.New("workflow node output has trailing data after JSON envelope")
	}
	outcome := WorkflowNodeOutcome(strings.TrimSpace(envelope.Outcome))
	switch outcome {
	case WorkflowNodeDone:
	case WorkflowNodeBlocked, WorkflowNodeSilent:
		if strings.TrimSpace(envelope.Reason) == "" {
			return WorkflowNodeResult{}, fmt.Errorf("workflow node outcome %q requires a reason", outcome)
		}
	default:
		return WorkflowNodeResult{}, fmt.Errorf("workflow node outcome %q is invalid", envelope.Outcome)
	}
	if envelope.Output == nil {
		envelope.Output = map[string]any{}
	}
	return WorkflowNodeResult{Outcome: outcome, Output: envelope.Output, Reason: strings.TrimSpace(envelope.Reason)}, nil
}
