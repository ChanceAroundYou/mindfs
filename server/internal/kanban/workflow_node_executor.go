package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
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
// the turn really used; Output is filled by the Router result normalizer from
// the worker's natural-language reply (summary plus any condition fields).
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

// WorkflowPromptPlaceholders are the substitutions an agent node may use in
// params.prompt. They deliberately reuse the legacy stage template names: the
// built-in workflows ship `{"prompt":"{previous_input}"}`, and one placeholder
// must never mean two things depending on which execution path ran it.
var WorkflowPromptPlaceholders = []string{"previous_input", "task_initial_input", "task_number"}

// resolveWorkflowNodePrompt substitutes the placeholders in one agent node's
// prompt. Without this the built-in workflows sent the literal string
// `{previous_input}` to the agent — the user's message never reached the turn.
//
// A placeholder that cannot be resolved is a hard error: sending the raw
// `{previous_input}` text is worse than failing, because the agent answers
// something plausible about a task it never saw.
func (s *Service) resolveWorkflowNodePrompt(ctx context.Context, store *TaskStore, run *WorkflowRun, task Task, node WorkflowNode, prompt string) (string, error) {
	values := WorkflowPromptValues(ctx, store, run, task)
	// 替换是纯字符串替换，空值会把占位符「悄悄抹掉」，留下一条没有需求的空
	// prompt。所以先按空值判定失败，再替换。
	for _, key := range WorkflowPromptPlaceholders {
		if strings.Contains(prompt, "{"+key+"}") && strings.TrimSpace(values[key]) == "" {
			return "", fmt.Errorf("workflow agent node %q 的 {%s} 无法解析：该任务没有可用的用户输入", node.ID, key)
		}
	}
	resolved := RenderPrompt(prompt, values)
	for _, key := range WorkflowPromptPlaceholders {
		if strings.Contains(resolved, "{"+key+"}") {
			return "", fmt.Errorf("workflow agent node %q 的 {%s} 无法解析：该任务没有可用的用户输入", node.ID, key)
		}
	}
	return resolved, nil
}

// WorkflowPromptValues builds the placeholder values for a workflow run.
//
// previous_input is the user activity that triggered the run (run.ActivityID).
// Runs started without a user message (agent_auto / scheduled) fall back to the
// task's most recent user input, which is what "the input this node works on"
// means for them. task_initial_input is the task's first user input — same
// split as the legacy promptValues.
func WorkflowPromptValues(ctx context.Context, store *TaskStore, run *WorkflowRun, task Task) map[string]string {
	values := map[string]string{
		"previous_input":     "",
		"task_initial_input": "",
		"task_number":        strconv.Itoa(task.TaskNumber),
	}
	if store == nil {
		return values
	}
	activities, err := NewActivityStore(store.db).List(ctx, task.ID)
	if err != nil {
		// 取不到活动流时保持空值：调用方会把未解析的占位符变成显式错误，
		// 而不是静默把字面量发给 agent。
		return values
	}
	triggerID := strings.TrimSpace(run.ActivityID)
	triggerBody := ""
	lastBody := ""
	for _, activity := range activities {
		if activity == nil || !isUserInputActivity(activity) {
			continue
		}
		body := strings.TrimSpace(activity.Body)
		if body == "" {
			continue
		}
		if values["task_initial_input"] == "" {
			values["task_initial_input"] = body
		}
		lastBody = body
		if triggerID != "" && activity.ID == triggerID {
			triggerBody = body
		}
	}
	// 触发它的那条留言优先；run 不是留言触发（agent_auto / scheduled）时用
	// 最近一条用户输入。
	values["previous_input"] = lastBody
	if triggerBody != "" {
		values["previous_input"] = triggerBody
	}
	return values
}

// isUserInputActivity: 只有用户自己写下的话才算「输入」。系统事件与 agent 回复
// 混进来会让 {previous_input} 变成上一条提示音或 agent 自己的答案。
func isUserInputActivity(activity *TaskActivity) bool {
	if activity.Role == RoleUser {
		return true
	}
	return activity.Type == ActivityTypeUserMessage || activity.Type == ActivityTypeUserComment
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
	// 空 prompt 是配置错误，必须在碰 store 之前判掉：调用方（含测试）可能只
	// 想验证这条校验，传进来的 store 未必可用。
	rawPrompt, err := WorkflowNodePrompt(node)
	if err != nil {
		return WorkflowNodeExecution{}, err
	}
	task, err := store.GetTask(ctx, run.TaskID)
	if err != nil {
		return WorkflowNodeExecution{}, err
	}
	prompt, err := s.resolveWorkflowNodePrompt(ctx, store, run, task, node, rawPrompt)
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

// ParseWorkflowNodeEnvelope parses the Router result normalizer's JSON answer
// (outcome/output/reason). It is not sent to workers: an Agent node replies in
// natural language, and the Router layer converts that text before it reaches
// the engine. Anything other than a valid envelope is rejected here; callers
// decide whether to fall back to the raw text.
//
// 宽松只针对「envelope 外面包了什么」：真实回复会带解释文字、markdown 代码块，
// 甚至因为本机 Stop hook 追加一段而出现多个 JSON 对象。这里扫描出所有 JSON
// 对象，取最后一个通过严格校验的 envelope；schema（字段白名单、outcome 枚举、
// blocked/silent 必须带 reason）一点不放宽。
func ParseWorkflowNodeEnvelope(text string) (WorkflowNodeResult, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return WorkflowNodeResult{}, errors.New("workflow node output is empty; JSON envelope required")
	}
	candidates := extractJSONObjects(trimmed)
	if len(candidates) == 0 {
		return WorkflowNodeResult{}, errors.New("workflow node output is not a JSON envelope: no JSON object found")
	}
	var lastErr error
	for i := len(candidates) - 1; i >= 0; i-- {
		result, err := parseWorkflowNodeEnvelopeObject(candidates[i])
		if err == nil {
			return result, nil
		}
		if lastErr == nil {
			lastErr = err
		}
	}
	return WorkflowNodeResult{}, lastErr
}

// parseWorkflowNodeEnvelopeObject is the strict single-object parser.
func parseWorkflowNodeEnvelopeObject(raw string) (WorkflowNodeResult, error) {
	var envelope struct {
		Outcome string         `json:"outcome"`
		Output  map[string]any `json:"output"`
		Reason  string         `json:"reason"`
	}
	dec := json.NewDecoder(strings.NewReader(raw))
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

// extractJSONObjects returns every balanced top-level {...} substring, in text
// order. Braces inside JSON strings (including escaped quotes) do not count, and
// a nested object is not emitted separately from its enclosing one.
func extractJSONObjects(text string) []string {
	var out []string
	for i := 0; i < len(text); i++ {
		if text[i] != '{' {
			continue
		}
		end, ok := scanJSONObjectEnd(text, i)
		if !ok {
			continue
		}
		out = append(out, text[i:end+1])
		i = end
	}
	return out
}

func scanJSONObjectEnd(text string, start int) (int, bool) {
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(text); i++ {
		ch := text[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case ch == '\\':
				escaped = true
			case ch == '"':
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i, true
			}
		}
	}
	return 0, false
}
