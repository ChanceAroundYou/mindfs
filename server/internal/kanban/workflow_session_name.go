package kanban

import (
	"fmt"
	"strings"
)

// WorkflowNodeSessionName 给「任务迸出来的节点会话」拼一个能区分轮次的名字。
//
// 背景：建会话时名字一直只取 TaskSessionName(task.Name, taskNumber)（“任务名 /
// #87”），同一条任务每开一轮就新建一个 worker 会话，于是会话列表里一排同名项，
// 用户分不出哪条是哪一轮。
//
// 后缀由三部分组成，都可缺省：
//   - nodeLabel：节点显示名，多节点工作流靠它区分；
//   - runMarker：重试/循环轮次的短标记（见 WorkflowNodeRunMarker），同一条留言
//     把同一个节点又跑一遍时靠它区分；
//   - userInput：触发这一轮 Run 的用户留言，同一节点跨轮靠它区分。
//
// 留言只取第一行并截断到 limit 个字符，避免侧栏名字变成一整段话。纯函数，
// 不碰 store，便于单测。
func WorkflowNodeSessionName(base, nodeLabel, userInput string, nodeRun NodeRun) string {
	base = strings.TrimSpace(base)
	label := strings.TrimSpace(nodeLabel)
	marker := WorkflowNodeRunMarker(nodeRun)
	snippet := firstLineSnippet(userInput, 20)

	parts := make([]string, 0, 3)
	// base 在任务没名字时会回落到节点名；这时再拼一遍节点名就是噪音。
	if label != "" && label != base {
		parts = append(parts, label)
	}
	if marker != "" {
		parts = append(parts, marker)
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

// WorkflowNodeRunMarker 给「同一条留言把同一个节点又跑一遍」一个短标记：
// 重试会建 attempt+1 的 NodeRun（session_key 为空，per_node 策略于是又开一个
// 新会话），循环每次迭代同理 —— 这两条路径的节点名与触发留言都和首次执行
// 逐字相同，只有这个标记能把它们分开。
//
// 首次尝试（attempt<=1）与首轮迭代（iteration<=1）返回空串：普通执行的会话名
// 保持干净，不因为「第一次」多出噪音。
func WorkflowNodeRunMarker(nodeRun NodeRun) string {
	parts := make([]string, 0, 2)
	if nodeRun.Iteration > 1 {
		parts = append(parts, fmt.Sprintf("第%d轮", nodeRun.Iteration))
	}
	if nodeRun.Attempt > 1 {
		parts = append(parts, fmt.Sprintf("第%d次", nodeRun.Attempt))
	}
	return strings.Join(parts, " ")
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
