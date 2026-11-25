package kanban

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

// workflowAdvanceStepLimit bounds one Advance call. Every iteration persists at
// least one transition, so hitting this limit means a graph/engine bug, not a
// large workflow; fail loudly instead of spinning forever.
const workflowAdvanceStepLimit = 1000

// Condition routes are the two booleans of the safe expression language. The
// frontend default is already ["pass","fail"]; the evaluator maps true→pass and
// false→fail and rejects a graph that does not declare both.
const (
	WorkflowRoutePass = "pass"
	WorkflowRouteFail = "fail"
)

// AdvanceWorkflowRun drives one task's workflow until it has no synchronously
// runnable node left. Each step reloads the authoritative run, so a concurrent
// edit/claim is never overwritten by a stale in-memory snapshot.
//
// It intentionally stops at HumanGate and Loop nodes: their approval/iteration
// semantics belong to the later packages and must not be guessed here.
func (s *Service) AdvanceWorkflowRun(ctx context.Context, store *TaskStore, taskID string) error {
	if s == nil || store == nil {
		return errors.New("workflow service and store required")
	}
	for step := 0; step < workflowAdvanceStepLimit; step++ {
		if err := ctx.Err(); err != nil {
			return nil // cancellation is an operator decision, not a failure
		}
		task, err := store.GetTask(ctx, taskID)
		if err != nil {
			return err
		}
		// Pause and terminal states are hard stops: an in-flight node may still
		// finish (its own path persists the result), but this driver must not
		// claim another node or overwrite the operator's status.
		if task.Status == StatusPaused || isTerminalStatus(task.Status) {
			return nil
		}
		run, err := store.WorkflowRunForTask(ctx, taskID)
		if err != nil {
			if errors.Is(err, ErrWorkflowRunNotFound) {
				return nil // legacy task: nothing to drive
			}
			return err
		}
		// 升级前留下的末端 gate 停在 waiting_user，EngineReadyNodes 看不见它。
		// 先兼容收尾，再照常推进。
		if healed, err := s.healParkedTerminalGates(ctx, store, run); err != nil {
			return err
		} else if healed {
			continue
		}
		ready, skipped, err := EngineReadyNodes(run)
		if err != nil {
			return err
		}
		if len(skipped) > 0 {
			node, ok := run.Graph.node(skipped[0])
			if !ok {
				return fmt.Errorf("engine returned unknown skipped node %q", skipped[0])
			}
			if _, err := s.commitSkippedWorkflowNode(ctx, store, run, node, ""); err != nil {
				return err
			}
			continue
		}
		if len(ready) == 0 {
			// A Loop container is not "ready" once it started (it is running),
			// yet its iteration still has to advance. Drive it explicitly before
			// declaring the run quiescent.
			if loop, ok := runningWorkflowLoopNode(run); ok {
				advanced, err := s.advanceWorkflowLoop(ctx, store, run, loop)
				if err != nil {
					return err
				}
				if advanced {
					continue
				}
			}
			return s.projectWorkflowTaskStatus(ctx, store, run)
		}
		node, ok := run.Graph.node(ready[0])
		if !ok {
			return fmt.Errorf("engine returned unknown ready node %q", ready[0])
		}
		advanced, err := s.advanceWorkflowNode(ctx, store, run, node)
		if err != nil {
			return err
		}
		if !advanced {
			return s.projectWorkflowTaskStatus(ctx, store, run)
		}
	}
	return fmt.Errorf("workflow advance exceeded %d steps for task %s", workflowAdvanceStepLimit, taskID)
}

// runningWorkflowLoopNode returns the first Loop container that is currently
// iterating. Graph order keeps the choice deterministic; nested loops are
// rejected by the validator, so at most the top level is relevant.
func runningWorkflowLoopNode(run *WorkflowRun) (WorkflowNode, bool) {
	for _, n := range run.Graph.Nodes {
		if n.Kind == NodeKindLoop && run.NodeStates[n.ID] == NodeRunRunning {
			return n, true
		}
	}
	return WorkflowNode{}, false
}

func (s *Service) advanceWorkflowNode(ctx context.Context, store *TaskStore, run *WorkflowRun, node WorkflowNode) (bool, error) {
	switch node.Kind {
	case NodeKindCondition:
		return s.advanceWorkflowCondition(ctx, store, run, node)
	case NodeKindNotify:
		return s.advanceWorkflowNotify(ctx, store, run, node)
	case NodeKindAgent:
		nr, err := s.workflowAttemptForReadyNode(ctx, store, run, node)
		if err != nil {
			return false, err
		}
		// 配额挂起中的行还不能跑：不改状态、不绑会话，下一拍再看。到期由
		// releaseDueNodeHolds 清标记后重新唤醒本 Run。
		if _, held := nodeRunHoldUntil(nr); held {
			return false, nil
		}
		_, err = s.RunWorkflowNodeAttempt(ctx, store, run, node, nr)
		if err != nil {
			return false, err
		}
		// Agent already projected NodeStates/task status; reload on the next step
		// regardless of success/fail/waiting so EngineReadyNodes sees the truth.
		return true, nil
	case NodeKindHumanGate:
		return s.advanceWorkflowHumanGate(ctx, store, run, node)
	case NodeKindLoop:
		return s.advanceWorkflowLoop(ctx, store, run, node)
	default:
		// Unknown kinds stop the driver rather than invent semantics.
		return false, nil
	}
}

func (s *Service) advanceWorkflowCondition(ctx context.Context, store *TaskStore, run *WorkflowRun, node WorkflowNode) (bool, error) {
	route, err := EvaluateWorkflowCondition(run, node)
	if err != nil {
		nr := workflowAttemptNodeRun(run, node)
		nr.Output = map[string]any{"error": err.Error()}
		nr.Status = NodeRunWaitingUser
		act := &TaskActivity{
			ID:   "wf:" + nr.ID + ":" + WorkflowEventNodeUnknown,
			Type: ActivityTypeAttention, Author: "system", Role: "system",
			// 条件永远求不出值 = 工作流定义有问题，不是临时抖动：标 error。
			Severity: ActivitySeverityError,
			Body:     "条件无法求值：" + err.Error(),
		}
		if _, _, cerr := store.CommitWorkflowNodeTransition(ctx, run, WorkflowNodeTransition{
			NodeRun: nr, EventType: WorkflowEventNodeUnknown,
			EventPayload: map[string]any{"error": err.Error()}, Activity: act,
		}); cerr != nil {
			return false, cerr
		}
		return false, s.projectWorkflowTaskStatus(ctx, store, run)
	}
	nr := workflowAttemptNodeRun(run, node)
	nr.Output = map[string]any{"route": route}
	nr.Status = NodeRunSuccess
	if _, _, err := store.CommitWorkflowNodeTransition(ctx, run, WorkflowNodeTransition{
		NodeRun: nr, EventType: WorkflowEventNodeSucceeded, Route: route,
	}); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Service) advanceWorkflowNotify(ctx context.Context, store *TaskStore, run *WorkflowRun, node WorkflowNode) (bool, error) {
	message, _ := node.Params["message"].(string)
	message = strings.TrimSpace(message)
	if message == "" {
		message = node.Name
	}
	if message == "" {
		message = node.ID
	}
	nr := workflowAttemptNodeRun(run, node)
	nr.Output = map[string]any{"message": message}
	nr.Status = NodeRunSuccess
	act := &TaskActivity{
		ID:   "wf:" + nr.ID + ":" + WorkflowEventNodeNotified,
		Type: ActivityTypeSystemEvent, Author: "system", Role: "system", Body: message,
	}
	if _, _, err := store.CommitWorkflowNodeTransition(ctx, run, WorkflowNodeTransition{
		NodeRun: nr, EventType: WorkflowEventNodeNotified, Activity: act,
	}); err != nil {
		return false, err
	}
	return true, nil
}

// advanceWorkflowHumanGate materializes the waiting NodeRun once. The driver
// then stops: approval is a dedicated decision (D4), never inferred from a
// normal comment or from the fact that the node became ready.
func (s *Service) advanceWorkflowHumanGate(ctx context.Context, store *TaskStore, run *WorkflowRun, node WorkflowNode) (bool, error) {
	// 末端 gate 没有可批的下游：旧版本按自动通过收尾，绝不物化等待卡。
	if workflowNodeIsTerminal(run.Graph, node.ID) {
		return s.autoPassTerminalGate(ctx, store, run, node)
	}
	prompt, _ := node.Params["prompt"].(string)
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		prompt = strings.TrimSpace(node.Name)
	}
	if prompt == "" {
		prompt = node.ID
	}
	nr := workflowAttemptNodeRun(run, node)
	nr.Status = NodeRunWaitingUser
	nr.Output = map[string]any{"prompt": prompt}
	// 卡片内容由 payload 决定：节点名 + 审批提示 + 上游产出摘要。没有上游时
	// upstream 键直接省略（旧图/首节点），前端据此不渲染空块。
	payload, err := gateActivityPayload(run, node, nr)
	if err != nil {
		return false, err
	}
	act := &TaskActivity{
		ID:          "wf:" + nr.ID + ":" + WorkflowEventHumanWaiting,
		Type:        ActivityTypeHumanGate,
		Author:      "system",
		Role:        "system",
		Body:        "等待你确认：" + prompt,
		Status:      ActivityStatusWaitingUser,
		PayloadJSON: payload,
	}
	_, changed, err := store.CommitWorkflowNodeTransition(ctx, run, WorkflowNodeTransition{
		NodeRun: nr, EventType: WorkflowEventHumanWaiting, Activity: act,
	})
	if err != nil {
		return false, err
	}
	if changed {
		s.broadcastWorkflowActivity(ctx, store, act, true, nil)
		// 等待行刚刚物化：这是 gate_waiting 的权威状态落库点。attempt 已冻结在
		// NodeRun 上，重复物化会命中同一 InstanceKey 被账本去重；事件写失败只记
		// 日志，扫描会从 waiting 行补齐。
		instanceKey, keyErr := RouterEventInstanceKey(RouterEventGateWaiting, run.ID, nr.ID, nr.Attempt, time.Now().UTC())
		if keyErr != nil {
			log.Printf("[kanban/router-events] gate instance key run=%s node_run=%s err=%v", run.ID, nr.ID, keyErr)
		} else if err := s.emitRouterEvent(ctx, store, run.ID, RouterEventGateWaiting, instanceKey, "", "", "", nr.Attempt,
			map[string]any{"node_id": node.ID, "node_run_id": nr.ID, "attempt": nr.Attempt}); err != nil {
			log.Printf("[kanban/router-events] gate hook run=%s node_run=%s err=%v", run.ID, nr.ID, err)
		}
		s.notifyGateCard(ctx, store, run, node, nr, prompt)
	}
	return false, s.projectWorkflowTaskStatus(ctx, store, run)
}

// reason 非空时写进 output：条件未选中是 ""，Router 手动跳过带一句来由。
func (s *Service) commitSkippedWorkflowNode(ctx context.Context, store *TaskStore, run *WorkflowRun, node WorkflowNode, reason string) (NodeRun, error) {
	nr := workflowAttemptNodeRun(run, node)
	nr.Status = NodeRunSkipped
	nr.Output = map[string]any{"skipped": true}
	if strings.TrimSpace(reason) != "" {
		nr.Output["reason"] = strings.TrimSpace(reason)
	}
	committed, _, err := store.CommitWorkflowNodeTransition(ctx, run, WorkflowNodeTransition{
		NodeRun: nr, EventType: WorkflowEventNodeSkipped,
	})
	return committed, err
}

// ── Bounded Loop ────────────────────────────────────────────────────────────

// advanceWorkflowLoop drives one synchronous step of a bounded Loop. Each call
// persists at most one transition (iteration start, one inner node, iteration
// increment, exit or bound-reached), so a crash between any two steps is
// recoverable from LoopState plus the iteration's NodeRuns:
//   - iteration finished but not yet incremented → increment is recomputed once
//     from the persisted iteration number;
//   - already incremented → the next iteration simply starts, never re-running
//     the previous one.
