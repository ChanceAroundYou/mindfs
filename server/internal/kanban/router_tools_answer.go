package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// answerTarget 是「用户这句话该回给哪个停住的节点」的解析结果。工具在当轮把它
// 落成 RouterAction.NodeRunID，apply 阶段只认这个 ID —— 回合之间状态可能变化，
// 到时候再挑一次会挑到别的节点，或挑到一个已经不在等答复的节点。
type answerTarget struct {
	NodeRun NodeRun
	Node    WorkflowNode
}

// answerTool 是「答复回填停住的 Agent 节点」的写入口。
//
// 它和 deliver 的分工：deliver 把留言排进**正在执行**节点的下一轮；这个工具是
// 给**已经停下**（waiting_user）的节点补一次带答复的重跑。两者都要求「恰好一个
// 目标」，否则宁可报错让模型问用户，也不扇出。
func (e *routerToolEnv) answerTool() RouterToolSpec {
	return RouterToolSpec{
		Name:        "router_answer_waiting_node",
		Description: "把用户的答复交给停在 waiting_user 的唯一 Agent 节点，带着答复重跑该节点。运行中的节点请改用 deliver_to_active_run。",
		Class:       RouterToolWrite,
		InputSchema: objectSchema(map[string]any{
			"body":    map[string]any{"type": "string", "minLength": 1},
			"node_id": map[string]any{"type": "string", "description": "可选；有多个停住的节点时必须指定"},
			"reason":  map[string]any{"type": "string"},
		}, []string{"body"}),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Body   string `json:"body"`
				NodeID string `json:"node_id"`
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			body := strings.TrimSpace(args.Body)
			if body == "" {
				return "", errors.New("body required")
			}
			// 运行期事件回合不允许执行类动作：那是白名单的明确拒绝（spec），
			// 这里不掺一脚，免得把「这一类动作被禁止」说成「这一轮没有目标」。
			if isRuntimeEventBrief(e.brief) {
				return e.record(RouterAction{
					Kind:   RouterActionAnswerWaiting,
					Body:   body,
					Reason: truncateRouterText(args.Reason, 500),
				})
			}
			target, err := e.resolveAnswerTarget(strings.TrimSpace(args.NodeID))
			if err != nil {
				return "", err
			}
			return e.record(RouterAction{
				Kind:      RouterActionAnswerWaiting,
				Body:      body,
				NodeRunID: target.NodeRun.ID,
				Reason:    truncateRouterText(args.Reason, 500),
			})
		},
	}
}

// resolveAnswerTarget 把「用户这句话该回给谁」判成唯一目标，判不出来就给出
// 可操作的拒绝理由（模型会把它转述给用户）。
func (e *routerToolEnv) resolveAnswerTarget(nodeID string) (answerTarget, error) {
	if e == nil || e.store == nil || e.brief == nil || e.brief.LatestRun == nil {
		return answerTarget{}, fmt.Errorf("无法答复：当前没有进行中的工作流运行")
	}
	ctx := context.Background()
	run, err := e.store.WorkflowRunByID(ctx, e.brief.LatestRun.ID)
	if err != nil || run == nil {
		return answerTarget{}, fmt.Errorf("无法答复：读取当前工作流运行失败")
	}
	runs, err := e.store.ListWorkflowNodeRuns(ctx, run.ID)
	if err != nil {
		return answerTarget{}, fmt.Errorf("无法答复：读取节点状态失败")
	}
	var waiting []NodeRun
	running := 0
	gate := false
	for _, nr := range runs {
		node, ok := workflowNodeByID(run.Graph, nr.NodeID)
		if !ok {
			continue
		}
		switch {
		case node.Kind == NodeKindAgent && nr.Status == NodeRunRunning:
			running++
		case nr.Status != NodeRunWaitingUser:
			continue
		// 历史行会永久停在 waiting_user；只有 Run 投影仍是 waiting_user 的节点
		// 才真的在等人（否则答复过的节点会被反复当成候选）。
		case run.NodeStates[nr.NodeID] != NodeRunWaitingUser:
			continue
		case node.Kind == NodeKindHumanGate:
			gate = true
		case node.Kind == NodeKindAgent && nr.LoopPath == "":
			waiting = append(waiting, nr)
		}
	}
	if running > 0 {
		return answerTarget{}, fmt.Errorf("无法答复：当前还有 %d 个 Agent 节点正在执行；先等这一轮结束，再决定是投递还是答复", running)
	}
	if gate {
		return answerTarget{}, fmt.Errorf("无法答复：当前工作流正在等待你审批，普通留言不能替代批准/驳回")
	}
	if len(waiting) == 0 {
		return answerTarget{}, fmt.Errorf("无法答复：当前没有停住等待你答复的 Agent 节点")
	}
	if nodeID == "" {
		if len(waiting) > 1 {
			return answerTarget{}, fmt.Errorf("无法答复：有 %d 个停住的 Agent 节点（%s），请在 node_id 指明回给哪一个", len(waiting), strings.Join(nodeRunIDs(waiting), "、"))
		}
		return e.answerTargetOf(run, waiting[0]), nil
	}
	for _, nr := range waiting {
		if nr.NodeID == nodeID || nr.ID == nodeID {
			return e.answerTargetOf(run, nr), nil
		}
	}
	return answerTarget{}, fmt.Errorf("无法答复：节点 %s 不在停住的候选里（%s）", nodeID, strings.Join(nodeRunIDs(waiting), "、"))
}

func (e *routerToolEnv) answerTargetOf(run *WorkflowRun, nr NodeRun) answerTarget {
	node, _ := workflowNodeByID(run.Graph, nr.NodeID)
	return answerTarget{NodeRun: nr, Node: node}
}

func nodeRunIDs(runs []NodeRun) []string {
	out := make([]string, 0, len(runs))
	for _, nr := range runs {
		out = append(out, nr.NodeID)
	}
	return out
}
