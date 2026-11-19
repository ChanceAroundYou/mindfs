package kanban

import "strings"

// WorkflowNodeSessionName 给「任务迸出来的节点会话」拼一个能区分轮次的名字。
//
// 背景：建会话时名字一直只取 TaskSessionName(task.Name, taskNumber)（“任务名 /
// #87”），同一条任务每开一轮就新建一个 worker 会话，于是会话列表里一排同名项，
// 用户分不出哪条是哪一轮。
//
// 后缀由两部分组成，都可缺省：
//   - nodeLabel：节点显示名，多节点工作流靠它区分；
//   - userInput：触发这一轮 Run 的用户留言，同一节点跨轮靠它区分。
//
// 留言只取第一行并截断到 limit 个字符，避免侧栏名字变成一整段话。纯函数，
// 不碰 store，便于单测。
func WorkflowNodeSessionName(base, nodeLabel, userInput string) string {
	base = strings.TrimSpace(base)
	label := strings.TrimSpace(nodeLabel)
	snippet := firstLineSnippet(userInput, 20)

	parts := make([]string, 0, 2)
	// base 在任务没名字时会回落到节点名；这时再拼一遍节点名就是噪音。
	if label != "" && label != base {
		parts = append(parts, label)
	}
	if snippet != "" {
		parts = append(parts, snippet)
	}
	if len(parts) == 0 {
		return base
	}
	suffix := strings.Join(parts, " · ")
	if base == "" {
		return suffix
	}
	return base + " · " + suffix
}

// firstLineSnippet 取第一行、去掉首尾空白，超长时按 rune 截断并加省略号。
// 按 rune 而不是 byte，避免把中文/emoji 切坏。
func firstLineSnippet(text string, limit int) string {
	line := strings.TrimSpace(text)
	if i := strings.IndexAny(line, "\r\n"); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	if line == "" || limit <= 0 {
		return line
	}
	runes := []rune(line)
	if len(runes) <= limit {
		return line
	}
	return strings.TrimSpace(string(runes[:limit])) + "…"
}
