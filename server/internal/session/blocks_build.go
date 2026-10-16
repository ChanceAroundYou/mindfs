package session

import (
	"sort"
	"strings"
)

// BuildTurnBlocks 把「一轮的正文 + aux」编译成块序列。
//
// 顺序必须与前端 buildAssistantTimeline 的既有渲染顺序逐条一致：aux 按
// (line, 到达序) 稳定排序后插进正文段之间，空正文段跳过。S2b 起块序就是渲染序，
// 若这里与渲染器不一致，切换读路径的瞬间正文顺序会变（用户可见的回归）。
//
// step 是步序。S2a 双写期块在轮末一次落完，步已收口，直接写真实步序；
// 流式期（块先落盘、步后收口）写 blockStepSentinel，收口时用 (seq,pos)+revision+1 补正。
func BuildTurnBlocks(seq, step int, turnID, content string, aux []ExchangeAux) []Block {
	blocks := make([]Block, 0, len(aux)+1)
	if len(aux) == 0 {
		if strings.TrimSpace(content) == "" {
			return blocks
		}
		blocks = append(blocks, newBlock(seq, 0, BlockTypeText, step, turnID, content))
		return blocks
	}

	var lines []string
	totalLines := 0
	if content != "" {
		lines = strings.Split(content, "\n")
		totalLines = len(lines)
	}

	type indexed struct {
		aux  ExchangeAux
		line int // 钳到 [0, totalLines]，与渲染器一致
		idx  int
	}
	normalized := make([]indexed, 0, len(aux))
	for i, item := range aux {
		line := item.Line
		if line < 0 {
			line = 0
		}
		if line > totalLines {
			line = totalLines
		}
		normalized = append(normalized, indexed{aux: item, line: line, idx: i})
	}
	sort.SliceStable(normalized, func(i, j int) bool {
		if normalized[i].line != normalized[j].line {
			return normalized[i].line < normalized[j].line
		}
		return normalized[i].idx < normalized[j].idx
	})

	pos := 0
	emitted := 0
	emitText := func(text string) {
		if strings.TrimSpace(text) == "" {
			return
		}
		blocks = append(blocks, newBlock(seq, pos, BlockTypeText, step, turnID, text))
		pos++
	}
	for _, item := range normalized {
		if item.line > emitted {
			emitText(strings.Join(lines[emitted:item.line], "\n"))
			emitted = item.line
		}
		if block, ok := auxBlock(seq, step, turnID, item.aux); ok {
			block.Pos = pos
			blocks = append(blocks, block)
			pos++
		}
	}
	if emitted < totalLines {
		emitText(strings.Join(lines[emitted:], "\n"))
	}
	return blocks
}

// auxBlock 把一条 aux 编译成块。判据顺序与渲染器一致：thought → plan → todo → compact → toolcall。
func auxBlock(seq, step int, turnID string, aux ExchangeAux) (Block, bool) {
	block := Block{Seq: seq, Step: step, MindFSID: turnID}
	switch {
	case aux.Thought != "":
		block.Type = BlockTypeReasoning
		block.Text = aux.Thought
	case aux.Plan != nil:
		block.Type = BlockTypePlan
		block.Plan = aux.Plan
	case aux.Todo != nil:
		block.Type = BlockTypeTodo
		block.Todo = aux.Todo
	case aux.Compact != nil:
		block.Type = BlockTypeCompact
		block.Compact = aux.Compact
	case aux.ToolCall != nil:
		block.Type = BlockTypeToolCall
		block.ToolCall = aux.ToolCall
	default:
		return Block{}, false
	}
	return block, true
}

// GroupBlocksBySeq 把块按 seq 分组，seq 内保持 (pos) 升序。
//
// 回包形状与 exchange_aux 一致（`{"<seq>": [...]}`），前端查表即可，
// 也让「块与 aux 同 seq 对齐」这件事在结构上显而易见。
func GroupBlocksBySeq(blocks []Block) map[int][]Block {
	out := make(map[int][]Block, len(blocks))
	for _, block := range blocks {
		if block.Seq <= 0 {
			continue
		}
		out[block.Seq] = append(out[block.Seq], block)
	}
	for seq, items := range out {
		sort.SliceStable(items, func(i, j int) bool { return items[i].Pos < items[j].Pos })
		out[seq] = items
	}
	return out
}
