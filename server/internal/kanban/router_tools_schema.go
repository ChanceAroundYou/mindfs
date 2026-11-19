package kanban

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

// isRuntimeEventBrief 表示这是运行期事件回合的 brief（T8 的 Event 三段已挂上）。
// 事件回合不携带 workflow 目录，也不允许开新执行，所以「目录里没有这个 workflow」
// 的检查对它不成立；真正该拒绝它的是运行期动作白名单。
func isRuntimeEventBrief(brief *SituationBrief) bool {
	return brief != nil && brief.Event != nil
}
