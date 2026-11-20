package kanban

// brief 的体积裁剪与文本截断。单独成文件是为了守住 500 行硬线：裁剪顺序与
// 24 KiB 硬上限属于同一条策略，改动时一起看。

import (
	"encoding/json"
	"fmt"
)

// capSituationBrief 按「先砍 workflows，再砍 recent_activities，再砍 excerpt」
// 的顺序把 brief 压到上限内。24KB 是硬上限，宁可少信息也不撑爆上下文。
func capSituationBrief(brief *SituationBrief) *SituationBrief {
	if brief == nil {
		return nil
	}
	if briefSize(brief) <= routerBriefMaxBytes {
		return brief
	}
	brief.Workflows = nil
	if briefSize(brief) <= routerBriefMaxBytes {
		return brief
	}
	// 事件 brief 的补充顺序：证据节点条数先收缩，再丢最近活动，然后缩节点摘要，
	// 再缩用户原始要求，最后缩未验证项。任何证据裁剪都要置 Evidence.Truncated。
	if trimRouterEventNodes(brief, routerEventBriefKeptNodes) {
		if briefSize(brief) <= routerBriefMaxBytes {
			return brief
		}
	}
	for len(brief.RecentActivities) > 0 && briefSize(brief) > routerBriefMaxBytes {
		brief.RecentActivities = brief.RecentActivities[1:]
	}
	for _, activity := range brief.RecentActivities {
		if briefSize(brief) <= routerBriefMaxBytes {
			break
		}
		activity.BodyExcerpt = truncateRouterText(activity.BodyExcerpt, 200)
	}
	trimRouterCompletion(brief)
	trimRouterEventExcerpts(brief)
	if brief.Trigger.UserMessage != nil && briefSize(brief) > routerBriefMaxBytes {
		brief.Trigger.UserMessage.Body = truncateRouterText(brief.Trigger.UserMessage.Body, routerEventBriefUserMessageLimit)
	}
	trimRouterEventUnverified(brief)
	return brief
}

// trimRouterCompletion 把证据清单压到 8 条：缺口再多也不该挤掉 brief 的其它段，
// 被裁掉的项在系统行里仍然完整可读。
func trimRouterCompletion(brief *SituationBrief) bool {
	if brief == nil || brief.Completion == nil || len(brief.Completion.Items) <= 8 {
		return false
	}
	brief.Completion.Items = brief.Completion.Items[:8]
	brief.Completion.Truncated = true
	return true
}

// trimRouterEventNodes 把证据节点收缩到 keep 条并累计被省略数；返回是否有裁剪。
func trimRouterEventNodes(brief *SituationBrief, keep int) bool {
	if brief == nil || brief.Event == nil || brief.Evidence == nil || len(brief.Evidence.Nodes) <= keep {
		return false
	}
	omitted := len(brief.Evidence.Nodes) - keep
	brief.Evidence.Nodes = brief.Evidence.Nodes[:keep]
	brief.Evidence.OmittedNodes += omitted
	brief.Evidence.Truncated = true
	return true
}

// trimRouterEventExcerpts 把节点摘要压到 200 字节；返回是否有裁剪。
func trimRouterEventExcerpts(brief *SituationBrief) bool {
	if brief == nil || brief.Event == nil || brief.Evidence == nil {
		return false
	}
	trimmed := false
	for i := range brief.Evidence.Nodes {
		node := &brief.Evidence.Nodes[i]
		if len(node.OutputExcerpt) > 200 {
			node.OutputExcerpt = truncateRouterText(node.OutputExcerpt, 200)
			trimmed = true
		}
		if len(node.ErrorExcerpt) > 200 {
			node.ErrorExcerpt = truncateRouterText(node.ErrorExcerpt, 200)
			trimmed = true
		}
	}
	if trimmed {
		brief.Evidence.Truncated = true
	}
	return trimmed
}

// trimRouterEventUnverified 把未验证项压到 5 条；返回是否有裁剪。最后一条固定
// 换成「还有 N 条」的标记：证据不全必须能看出来，不能裁成恰好 5 条就装完整。
func trimRouterEventUnverified(brief *SituationBrief) bool {
	if brief == nil || brief.Event == nil || brief.Evidence == nil ||
		len(brief.Evidence.Unverified) <= routerEventBriefKeptUnverifd {
		return false
	}
	items := brief.Evidence.Unverified
	kept := append([]string(nil), items[:routerEventBriefKeptUnverifd-1]...)
	brief.Evidence.Unverified = append(kept,
		fmt.Sprintf("… and %d more unverified items", len(items)-(routerEventBriefKeptUnverifd-1)))
	brief.Evidence.Truncated = true
	return true
}

func briefSize(brief *SituationBrief) int {
	raw, err := json.Marshal(brief)
	if err != nil {
		return 0
	}
	return len(raw)
}
