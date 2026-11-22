package kanban

import (
	"sort"
	"strings"
)

// 节点输出的可读文本提取（Plan 2026-11-18 集成拆分：原在 service_activities.go，
// 文件超过 500 行硬线后按职责拆出；仅移动代码，语义不变）。
//
// 2026-11-19（任务 #87 第 1 条）：这里曾经对 summary 做 1200 字截断，用户看到
// 一半句子加「…」。截断只该发生在把正文喂给**模型**的地方（brief 有字节上限），
// 落到任务详情给人看的总结必须是全文；展示端的窄卡片本来就另有 3 行 CSS 裁剪。

func nodeRunText(nr NodeRun, keys ...string) string {
	for _, key := range keys {
		value, _ := nr.Output[key].(string)
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// nodeRunOutputMetadataKeys 是执行器写进 output 的簿记字段，不是节点产出内容。
// 它们参与「唯一字符串」判断会让真实正文永远被判为歧义（真实样本：envelope
// 的 outcome=done + 模型的 sentence 两把字符串，结果一条 summary 都发不出去）。
var nodeRunOutputMetadataKeys = map[string]bool{
	"outcome":               true,
	"reason":                true,
	"worktree":              true,
	"worktree_error":        true,
	"resumed_after_restart": true,
	"comment_id":            true,
	// 条件节点的选路结果。它是最常见的「后跑成功节点」，但不是任务产出。
	"route": true,
}

// nodeRunOutputSummary 从节点 output 里挑「给人看的那句」，顺序固定：
//  1. 显式 summary / message / text / result / title / detail；
//  2. 只剩一个文本字段 → 直接用它；
//  3. 多个文本字段 → 按 key 排序后渲染成「key: value；…」。真实模型常返回
//     conclusion/report/key_point 或 content/file 这类自由字段，没有 canonical
//     键；挑「最长」的一条会丢掉另一个要点，渲染键名则既不猜也不漏。
//
// 非文本值（数组/数字/对象）一律不参与：output 是节点间数据契约，转储出来只会
// 让用户看到内部结构。元数据键（outcome/reason/worktree_*）同理。
func nodeRunOutputSummary(nr NodeRun) string {
	if len(nr.Output) == 0 {
		return ""
	}
	for _, key := range []string{"summary", "message", "text", "result", "title", "detail"} {
		value, _ := nr.Output[key].(string)
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	// 真实模型常把结果写成 changes: ["改了什么", ...]（任务 #66 的
	// workflow-blank 就是这样）。它不是节点间数据契约里的一个 key，而是
	// 这一轮给人看的变更清单，必须成为回复正文；否则用户只看到「已完成」。
	if changes := stringList(nr.Output["changes"]); len(changes) > 0 {
		lines := make([]string, len(changes))
		for i, change := range changes {
			lines[i] = "• " + change
		}
		return strings.Join(lines, "\n")
	}
	type textField struct{ key, value string }
	fields := make([]textField, 0, len(nr.Output))
	for key, value := range nr.Output {
		if nodeRunOutputMetadataKeys[key] {
			continue
		}
		text, ok := value.(string)
		if !ok {
			continue
		}
		if trimmed := strings.TrimSpace(text); trimmed != "" {
			fields = append(fields, textField{key: key, value: trimmed})
		}
	}
	if len(fields) == 0 {
		return ""
	}
	if len(fields) == 1 {
		return fields[0].value
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].key < fields[j].key })
	parts := make([]string, len(fields))
	for i, field := range fields {
		parts[i] = field.key + ": " + field.value
	}
	return strings.Join(parts, "；")
}

// stringList 把 JSON 数组里的非空字符串按原顺序取出来；其它元素忽略。
// 兼容 json.Unmarshal 的 []any 与单测里直接写的 []string。
func stringList(value any) []string {
	var raw []any
	switch items := value.(type) {
	case []any:
		raw = items
	case []string:
		raw = make([]any, len(items))
		for i, item := range items {
			raw[i] = item
		}
	default:
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if text, ok := item.(string); ok {
			if trimmed := strings.TrimSpace(text); trimmed != "" {
				out = append(out, trimmed)
			}
		}
	}
	return out
}

// maxStoredSummaryRunes 是写入 task_activities.summary / tasks.result_summary 的
// 硬上限。历史值是 1200，再后来是 20000 —— 两者都只防「病态输出撑爆库」，
// 于是任务卡片上出现了整段 Agent 叙述（#100）。2026-11-22 起改成 600 字：
// 摘要本来就是一屏之内能读完的东西，超过就不是摘要了。
//
// 截断停在句末标点上（见 turn_summary.go 的 cutAtSentence），不把一句话切成
// 半截；找不到句末标点才硬截并补「…」。
const maxStoredSummaryRunes = 600

func truncateSummary(text string) string {
	return cutAtSentence(text, maxStoredSummaryRunes)
}
