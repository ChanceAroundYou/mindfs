package kanban

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// W2 T2 的节点级管理工具：改档与跳过。
//
// 两个工具都只记录动作，apply 阶段统一执行（与其它 write 工具一致）。工具本身
// 先按 brief 的节点卡片判一次「这个动作此刻合不合法」：卡片是唯一口径，模型不用
// 猜，也不靠 apply 阶段才发现自己越界 —— 「运行中不切换」在这一层就说清楚。

// nodeAdminCard 在当轮 brief 的节点卡片里找目标节点。没有卡片（运行期事件回合、
// 或这一轮根本没有 Run）时直接拒绝：节点级动作只在看得见节点状态的回合里可用。
func (e *routerToolEnv) nodeAdminCard(nodeID string) (RouterBriefNode, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return RouterBriefNode{}, fmt.Errorf("node_id required")
	}
	brief := e.brief
	if brief == nil || brief.LatestRun == nil || len(brief.LatestRun.Nodes) == 0 {
		return RouterBriefNode{}, fmt.Errorf("这一轮没有节点卡片（没有进行中的 Run 或运行期事件回合）：节点级动作不可用")
	}
	for _, card := range brief.LatestRun.Nodes {
		if card.NodeID == nodeID {
			return card, nil
		}
	}
	return RouterBriefNode{}, fmt.Errorf("节点 %q 不在本轮 Run 里；可用节点：%s", nodeID, briefNodeIDs(brief))
}

// requireNodeAction 把「卡片上有没有这个动作」变成一句人能看懂的话。
func requireNodeAction(card RouterBriefNode, action string) error {
	for _, available := range card.Actions {
		if available == action {
			return nil
		}
	}
	switch card.Status {
	case NodeRunQueued, NodeRunRunning:
		return fmt.Errorf("节点 %s 正在运行（%s），运行中不切换；先 router_cancel_node，或等这一回合结束", card.NodeID, card.Status)
	case NodeRunSuccess, NodeRunSkipped, NodeRunCancelled:
		return fmt.Errorf("节点 %s 已收尾（%s），不再接受 %s", card.NodeID, card.Status, action)
	}
	return fmt.Errorf("节点 %s（%s）当前不支持 %s；可用动作：%v", card.NodeID, card.Status, action, card.Actions)
}

func briefNodeIDs(brief *SituationBrief) string {
	if brief == nil || brief.LatestRun == nil {
		return "none"
	}
	ids := make([]string, 0, len(brief.LatestRun.Nodes))
	for _, card := range brief.LatestRun.Nodes {
		ids = append(ids, card.NodeID)
	}
	if brief.LatestRun.NodeCardsTruncated > 0 {
		ids = append(ids, fmt.Sprintf("…（另有 %d 个节点未展示）", brief.LatestRun.NodeCardsTruncated))
	}
	return strings.Join(ids, ", ")
}

func (e *routerToolEnv) setNodeModelTool() RouterToolSpec {
	return RouterToolSpec{
		Name: "router_set_node_model",
		Description: "改一个节点的模型档（写进本轮 Run 的节点参数），下一次 attempt 生效。" +
			"运行中的节点不能改（运行中不切换）；失败 / 未跑过的节点可以。改完失败节点还要 router_retry_node 才会真的重跑。",
		Class: RouterToolWrite,
		InputSchema: objectSchema(map[string]any{
			"node_id": map[string]any{"type": "string"},
			"model":   map[string]any{"type": "string", "minLength": 1},
			"reason":  map[string]any{"type": "string"},
		}, []string{"node_id", "model"}),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				NodeID string `json:"node_id"`
				Model  string `json:"model"`
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			card, err := e.nodeAdminCard(args.NodeID)
			if err != nil {
				return "", err
			}
			model := strings.TrimSpace(args.Model)
			if model == "" {
				return "", fmt.Errorf("model required")
			}
			if len(model) > 64 {
				return "", fmt.Errorf("model name too long (max 64 bytes)")
			}
			if err := requireNodeAction(card, string(RouterActionSetNodeModel)); err != nil {
				return "", err
			}
			return e.record(RouterAction{
				Kind: RouterActionSetNodeModel, NodeID: card.NodeID, Model: model,
				Reason: strings.TrimSpace(args.Reason),
			})
		},
	}
}

func (e *routerToolEnv) skipNodeTool() RouterToolSpec {
	return RouterToolSpec{
		Name: "router_skip_node",
		Description: "跳过一个还没跑过的节点（按跳过语义收尾，下游继续）。" +
			"运行中、失败过或已收尾的节点不能跳过：运行中的先取消，失败过的用重试或改档，" +
			"失败本身已经让下游停住了，跳过也不会让它动。",
		Class: RouterToolWrite,
		InputSchema: objectSchema(map[string]any{
			"node_id": map[string]any{"type": "string"},
			"reason":  map[string]any{"type": "string"},
		}, []string{"node_id"}),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				NodeID string `json:"node_id"`
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			card, err := e.nodeAdminCard(args.NodeID)
			if err != nil {
				return "", err
			}
			if err := requireNodeAction(card, string(RouterActionSkipNode)); err != nil {
				return "", err
			}
			return e.record(RouterAction{
				Kind: RouterActionSkipNode, NodeID: card.NodeID, Reason: strings.TrimSpace(args.Reason),
			})
		},
	}
}
