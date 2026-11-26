package usecase

import "strings"

// thoughtAnchor 把一段流式思考的片段攒成一条 aux，并记录它的**真实起点**。
//
// 位置必须在思考开始时就确定，不能等 flush：Codex 的 reasoning summary 直到
// item.completed 才完整，而那时正文已经流完；用 flush 时刻的正文行数当 Line，
// 整块思考会被锚到正文末尾（实测 line=93/93，前端渲染成答案之后的大思考块）。
//
// Start 信号只带时序、不带内容；内容稍后（或逐块）到达时沿用同一个锚点。
// 没有任何 Start 的 agent 在首个片段到达时补锚，语义与旧行为一致。
type thoughtAnchor struct {
	text     strings.Builder
	id       string // 暴露给前端的思考 id
	itemID   string // agent 侧条目 id，用来区分同一轮的多次推理
	line     int    // 正文行号
	anchored bool
}

// note 记录思考起点。同一段思考（同 itemID、或 agent 不给 id）只锚一次；
// 上一段内容还没收口时不改锚，避免把两段思考的起点混在一起。
func (a *thoughtAnchor) note(itemID string, line int) {
	if a.anchored {
		if itemID == "" || itemID == a.itemID {
			return
		}
		if strings.TrimSpace(a.text.String()) != "" {
			return
		}
	}
	a.itemID = itemID
	a.line = line
	a.anchored = true
}

// add 追加一个片段；到达时还没有锚点就按当前正文行号补锚。
func (a *thoughtAnchor) add(itemID, content string, line int) {
	a.note(itemID, line)
	if a.id == "" {
		a.id = "thought-" + randomHex(8)
	}
	a.text.WriteString(content)
}

func (a *thoughtAnchor) thoughtID() string { return a.id }

// take 取走已收口的思考。空内容不消费锚点 —— Start 已到、summary 还没到的
// 状态必须保留到回合收尾或下一段内容到达。
func (a *thoughtAnchor) take() (content string, line int, id string, ok bool) {
	content = a.text.String()
	if strings.TrimSpace(content) == "" {
		return "", 0, "", false
	}
	line, id = a.line, a.id
	a.text.Reset()
	a.id = ""
	a.itemID = ""
	a.line = 0
	a.anchored = false
	return content, line, id, true
}
