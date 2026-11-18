package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Router 工具分两类：
//   - 只读（inspect_run）：立即执行，不写入 actions_json；
//   - 写入（say/run_workflow/...）：只记录动作并返回 {"status":"recorded","seq":N}。
//     P0 永不 apply；P1 在回合结束后按 seq 应用。
//
// 参数在这里做第一道校验（必填、枚举、跨任务 ID），跨动作约束由
// validateRouterActions 在回合结束后统一检查。

// 单条 say 的正文上限（Plan §17：单条 ≤4KB，整轮 ≤MaxBytes）。
const routerActionBodyMaxBytes = 4 * 1024

type routerTurnCollector struct {
	mu      sync.Mutex
	config  RouterConfig
	actions []RouterAction
	err     error
}

func newRouterTurnCollector(config RouterConfig) *routerTurnCollector {
	return &routerTurnCollector{config: config.Normalized()}
}

func (c *routerTurnCollector) record(action RouterAction) (RouterAction, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return RouterAction{}, c.err
	}
	if len(c.actions) >= c.config.MaxActions {
		return RouterAction{}, fmt.Errorf("too many actions: max %d", c.config.MaxActions)
	}
	if action.Kind == RouterActionSay {
		says := 0
		for _, existing := range c.actions {
			if existing.Kind == RouterActionSay {
				says++
			}
		}
		if says >= c.config.MaxSay {
			return RouterAction{}, fmt.Errorf("too many say actions: max %d", c.config.MaxSay)
		}
	}
	action.Seq = len(c.actions)
	c.actions = append(c.actions, action)
	return action, nil
}

func (c *routerTurnCollector) snapshot() []RouterAction {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]RouterAction, len(c.actions))
	copy(out, c.actions)
	return out
}

func (c *routerTurnCollector) fail(err error) {
	c.mu.Lock()
	if c.err == nil && err != nil {
		c.err = err
	}
	c.mu.Unlock()
}

type routerToolEnv struct {
	store     *TaskStore
	brief     *SituationBrief
	collector *routerTurnCollector
}

func (e *routerToolEnv) tools() []RouterToolSpec {
	return []RouterToolSpec{
		e.inspectRunTool(),
		e.sayTool(),
		e.runWorkflowTool(),
		e.messageAgentTool(),
		e.askUserTool(),
		e.silentTool(),
		e.deliverTool(),
	}
}

func (e *routerToolEnv) inspectRunTool() RouterToolSpec {
	return RouterToolSpec{
		Name:        "router_inspect_run",
		Description: "只读：查看最近一次 WorkflowRun 的节点终态与输出摘要。不修改任何状态。",
		Class:       RouterToolRead,
		InputSchema: objectSchema(map[string]any{
			"run_id": map[string]any{"type": "string", "description": "可选；必须是 brief.latest_run.id"},
		}, nil),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				RunID string `json:"run_id"`
			}
			_ = json.Unmarshal(raw, &args)
			return e.inspectRun(ctx, strings.TrimSpace(args.RunID))
		},
	}
}

func (e *routerToolEnv) inspectRun(ctx context.Context, runID string) (string, error) {
	if e.brief == nil || e.brief.LatestRun == nil {
		return "", errors.New("no workflow run for this task")
	}
	if runID != "" && runID != e.brief.LatestRun.ID {
		return "", fmt.Errorf("run %s is not the latest run of this task", runID)
	}
	payload := map[string]any{"run": e.brief.LatestRun}
	if e.store != nil {
		if nodes, err := e.store.ListWorkflowNodeRuns(ctx, e.brief.LatestRun.ID); err == nil {
			items := make([]map[string]any, 0, len(nodes))
			for _, node := range nodes {
				items = append(items, map[string]any{
					"node_id":     node.NodeID,
					"loop_path":   node.LoopPath,
					"iteration":   node.Iteration,
					"attempt":     node.Attempt,
					"status":      node.Status,
					"summary":     truncateRouterText(nodeRunText(node, "summary", "reason", "error"), 500),
					"session_key": node.SessionKey,
				})
			}
			payload["nodes"] = items
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (e *routerToolEnv) sayTool() RouterToolSpec {
	return RouterToolSpec{
		Name:        "router_say",
		Description: "向用户回复一句话（不执行任务）。purpose=chat 是正常对话，progress 是进度说明。",
		Class:       RouterToolWrite,
		InputSchema: objectSchema(map[string]any{
			"body":    map[string]any{"type": "string", "minLength": 1},
			"purpose": map[string]any{"type": "string", "enum": []string{"chat", "progress"}},
		}, []string{"body"}),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Body    string `json:"body"`
				Purpose string `json:"purpose"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			body := strings.TrimSpace(args.Body)
			if body == "" {
				return "", errors.New("body required")
			}
			purpose := strings.TrimSpace(args.Purpose)
			if purpose == "" {
				purpose = "chat"
			}
			if purpose != "chat" && purpose != "progress" {
				return "", fmt.Errorf("invalid purpose %q", purpose)
			}
			return e.record(RouterAction{Kind: RouterActionSay, Body: body, Purpose: purpose})
		},
	}
}

func (e *routerToolEnv) runWorkflowTool() RouterToolSpec {
	return RouterToolSpec{
		Name:        "router_run_workflow",
		Description: "为任务启动一次 workflow 执行。workflow_id 必须来自 brief.workflows；有活动执行时禁止调用。",
		Class:       RouterToolWrite,
		InputSchema: objectSchema(map[string]any{
			"workflow_id": map[string]any{"type": "string", "minLength": 1},
			"version_id":  map[string]any{"type": "string"},
			"reason":      map[string]any{"type": "string"},
		}, []string{"workflow_id"}),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				WorkflowID string `json:"workflow_id"`
				VersionID  string `json:"version_id"`
				Reason     string `json:"reason"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			workflowID := strings.TrimSpace(args.WorkflowID)
			if workflowID == "" {
				return "", errors.New("workflow_id required")
			}
			if !briefHasWorkflow(e.brief, workflowID) {
				return "", fmt.Errorf("workflow %q is not available for this task", workflowID)
			}
			return e.record(RouterAction{
				Kind:       RouterActionRunWorkflow,
				WorkflowID: workflowID,
				VersionID:  strings.TrimSpace(args.VersionID),
				Reason:     truncateRouterText(args.Reason, 500),
			})
		},
	}
}

func (e *routerToolEnv) messageAgentTool() RouterToolSpec {
	return RouterToolSpec{
		Name:        "router_message_agent",
		Description: "把任务交给任务主会话的普通 agent 回合（可读文件、改代码）。有活动执行时禁止调用。",
		Class:       RouterToolWrite,
		InputSchema: objectSchema(map[string]any{
			"body":   map[string]any{"type": "string", "minLength": 1},
			"reason": map[string]any{"type": "string"},
		}, []string{"body"}),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Body   string `json:"body"`
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			body := strings.TrimSpace(args.Body)
			if body == "" {
				return "", errors.New("body required")
			}
			return e.record(RouterAction{
				Kind:   RouterActionMessageAgent,
				Body:   body,
				Reason: truncateRouterText(args.Reason, 500),
			})
		},
	}
}

func (e *routerToolEnv) askUserTool() RouterToolSpec {
	return RouterToolSpec{
		Name:        "router_ask_user",
		Description: "信息不足或有风险时向用户提问。必须单独使用；不能与其它写入工具同轮。",
		Class:       RouterToolWrite,
		InputSchema: objectSchema(map[string]any{
			"question": map[string]any{"type": "string", "minLength": 1},
			"options":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		}, []string{"question"}),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Question string   `json:"question"`
				Options  []string `json:"options"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			question := strings.TrimSpace(args.Question)
			if question == "" {
				return "", errors.New("question required")
			}
			options := make([]string, 0, len(args.Options))
			for _, option := range args.Options {
				if option = strings.TrimSpace(option); option != "" {
					options = append(options, option)
				}
			}
			return e.record(RouterAction{Kind: RouterActionAskUser, Question: question, Options: options})
		},
	}
}

func (e *routerToolEnv) silentTool() RouterToolSpec {
	return RouterToolSpec{
		Name:        "router_silent",
		Description: "确实无需回复时使用。必须单独使用；reason 会落库供审计。",
		Class:       RouterToolWrite,
		InputSchema: objectSchema(map[string]any{
			"reason": map[string]any{"type": "string"},
		}, nil),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Reason string `json:"reason"`
			}
			_ = json.Unmarshal(raw, &args)
			reason := strings.TrimSpace(args.Reason)
			if reason == "" {
				reason = "no action"
			}
			return e.record(RouterAction{Kind: RouterActionSilent, Reason: reason})
		},
	}
}

func (e *routerToolEnv) deliverTool() RouterToolSpec {
	return RouterToolSpec{
		Name:        "router_deliver_to_active_run",
		Description: "把用户留言转交给唯一的活动 Agent 节点；由运行层在该节点回合结束后按 FIFO 投递。",
		Class:       RouterToolWrite,
		InputSchema: objectSchema(map[string]any{
			"body":   map[string]any{"type": "string", "minLength": 1},
			"reason": map[string]any{"type": "string"},
		}, []string{"body"}),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Body   string `json:"body"`
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			body := strings.TrimSpace(args.Body)
			if body == "" {
				return "", errors.New("body required")
			}
			return e.record(RouterAction{
				Kind:   RouterActionDeliver,
				Body:   body,
				Reason: truncateRouterText(args.Reason, 500),
			})
		},
	}
}

func (e *routerToolEnv) record(action RouterAction) (string, error) {
	recorded, err := e.collector.record(action)
	if err != nil {
		return "", err
	}
	raw, _ := json.Marshal(map[string]any{"status": "recorded", "seq": recorded.Seq, "kind": recorded.Kind})
	return string(raw), nil
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func briefHasWorkflow(brief *SituationBrief, workflowID string) bool {
	if brief == nil {
		return false
	}
	for _, workflow := range brief.Workflows {
		if workflow.ID == workflowID {
			return true
		}
	}
	return false
}

// validateRouterActions 做跨动作校验。返回的错误会让整条决策标 rejected，
// 而不是只丢坏动作：半套动作比没有动作更危险。
func validateRouterActions(brief *SituationBrief, actions []RouterAction, config RouterConfig) error {
	if len(actions) == 0 {
		return errors.New("router produced no actions")
	}
	if len(actions) > config.MaxActions {
		return fmt.Errorf("too many actions: %d > %d", len(actions), config.MaxActions)
	}
	says := 0
	executions := 0
	totalBytes := 0
	for index, action := range actions {
		if action.Seq != index {
			return fmt.Errorf("action seq %d is not contiguous at index %d", action.Seq, index)
		}
		totalBytes += len(action.Body) + len(action.Question)
		switch action.Kind {
		case RouterActionSay:
			says++
			if strings.TrimSpace(action.Body) == "" {
				return errors.New("say body required")
			}
			if len(action.Body) > routerActionBodyMaxBytes {
				return fmt.Errorf("say body exceeds %d bytes", routerActionBodyMaxBytes)
			}
		case RouterActionAskUser, RouterActionSilent:
			if len(actions) != 1 {
				return fmt.Errorf("%s must be the only action", action.Kind)
			}
		case RouterActionRunWorkflow:
			executions++
			if briefHasWorkflow(brief, action.WorkflowID) == false {
				return fmt.Errorf("workflow %q is not available", action.WorkflowID)
			}
		case RouterActionMessageAgent, RouterActionDeliver:
			executions++
			if strings.TrimSpace(action.Body) == "" {
				return fmt.Errorf("%s body required", action.Kind)
			}
		case RouterActionSuggestFinish:
			// P2 only; accepted here so the enum remains forward-compatible.
		default:
			return fmt.Errorf("invalid action kind %q", action.Kind)
		}
	}
	if says > config.MaxSay {
		return fmt.Errorf("too many say actions: %d > %d", says, config.MaxSay)
	}
	if totalBytes > config.MaxBytes {
		return fmt.Errorf("actions exceed %d bytes: %d", config.MaxBytes, totalBytes)
	}
	if executions > 1 {
		return errors.New("at most one execution action per turn")
	}
	if brief != nil && brief.ActiveExecution != nil {
		for _, action := range actions {
			if action.Kind == RouterActionRunWorkflow || action.Kind == RouterActionMessageAgent {
				return fmt.Errorf("%s is not allowed while an execution is active", action.Kind)
			}
			if action.Kind == RouterActionDeliver && len(brief.ActiveExecution.CurrentNodes) != 1 {
				return fmt.Errorf("deliver requires exactly one active node, got %d", len(brief.ActiveExecution.CurrentNodes))
			}
		}
	}
	return nil
}

// errRouterEmptyDecision marks a model turn that produced no decision at all:
// zero write-tool calls and no visible final text. Treating it as a deliberate
// silence would hide the user's message behind a placeholder, so callers must
// fall back to the legacy path the same way they do for model failures.
var errRouterEmptyDecision = errors.New("router produced no decision: empty final text and no tool calls")

// synthesizeActions 处理「模型只说了话、没调写入工具」的情况：非空最终文本
// 合成为 say(chat)。空文本说明这一轮没有任何决策——既没有工具调用，也没有可
// 转述的话，返回错误由调用方回落旧路径；silent 只能来自显式的 router_silent 工具。
func synthesizeActions(actions []RouterAction, finalText string) ([]RouterAction, bool, error) {
	if len(actions) > 0 {
		return actions, false, nil
	}
	text := strings.TrimSpace(finalText)
	if text == "" {
		return nil, false, errRouterEmptyDecision
	}
	return []RouterAction{{Kind: RouterActionSay, Body: text, Purpose: "chat", Seq: 0}}, true, nil
}
