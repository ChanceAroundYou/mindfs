package kanban

import (
	"context"
	"encoding/json"
	"fmt"
)

// record_acceptance 工具（Plan 2026-11-21 W1 T3）：登记「这一轮做完的样子」，
// 每条自带证据等级。工具只记录意图，真伪由 complete_task 的证据门在 apply 阶段
// 逐条机检（self_report 永远不过、artifact 查真实产物、independent_verification
// 等独立复核结论）。
func (e *routerToolEnv) recordAcceptanceTool() RouterToolSpec {
	return RouterToolSpec{
		Name: "router_record_acceptance",
		Description: "登记这一轮任务的验收项：每条写清做了什么（text）与证据等级（evidence_class）。" +
			"self_report 只是自述、过不了证据门；artifact 必须给出工作区里的产物路径；" +
			"independent_verification 会交给独立复核判定。收尾前登记，complete_task 时逐条核对。",
		Class: RouterToolWrite,
		InputSchema: objectSchema(map[string]any{
			"items": map[string]any{
				"type":     "array",
				"minItems": 1,
				"maxItems": acceptanceMaxItems,
				"items": objectSchema(map[string]any{
					"text":           map[string]any{"type": "string", "minLength": 1},
					"evidence_class": map[string]any{"type": "string", "enum": []string{"self_report", "artifact", "independent_verification"}},
					"evidence_ref":   map[string]any{"type": "string"},
					"node_id":        map[string]any{"type": "string"},
				}, []string{"text", "evidence_class"}),
			},
		}, []string{"items"}),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Items []AcceptanceItemInput `json:"items"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			items, err := normalizeAcceptanceItems(args.Items)
			if err != nil {
				return "", err
			}
			return e.record(RouterAction{
				Kind:  RouterActionRecordAcceptance,
				RunID: completionBriefRunIDOf(e.brief),
				Items: items,
			})
		},
	}
}
