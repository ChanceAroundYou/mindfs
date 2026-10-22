package session

import "strings"

// 窗口载荷去重：把 aux 里**已被同 seq 的 reasoning 块逐字覆盖**的 thought 丢掉。
//
// 实测（pc 节点 / 「重新训练」，2026-10-22）：该会话单页（`before_seq=93&limit=8`）
// 未压缩 9.36 MB，其中 **2.84 MB 是重复的 thought**（2,270 条）—— 块侧同 seq 有
// 2,276 条 reasoning，aux 侧 thought 的「块里没有」集合为**空**。前端拿到两份同样的
// 文本，一份渲染成思考块、另一份只作为旧渲染器的回退数据存在，纯属白传。
//
// 为什么只去重 thought、不动 toolcall：
//   - reasoning 块的语义是干净的 —— 块流是正文的唯一载体（方案 D1），aux.Thought 只是
//     它的旧载体，两者逐字相同即可判定安全；
//   - toolcall 的 aux 还挂着窗口轻压缩契约（`CompactExchangeAuxLight` 清 content）、
//     commandShell 推断与卡片元数据回退，且 `TestHandleSessionSyncWindowUsesWindowAux`
//     明确钉住了「窗口路径下发的是轻压缩后的 aux 卡片」。那 1.4 MB 的重复要动，得连
//     这些契约一起改并补测试，属于**另一批**工作，不在本批里顺手做。
//
// **判据必须是「文本逐字相同」，不能是「这个 seq 有 reasoning 块」**：块与 aux 是两条
// 独立写入路径，某轮块只落了一部分（进程被杀、旧版本只写 aux）时，按「有块就丢」会把
// 剩下的内容永久删掉 —— 那正是 `CompactExchangeAux` 注释反复警告过的「回复完就消失」。
//
// 只用于**窗口读路径**。全量 sync / 重锚定不走这里：那条路要保持「内容不闪」。

// DropAuxThoughtsCoveredByBlocks 丢掉 aux 里同 seq 已有逐字相同 reasoning 块的 thought 条目。
//
// 不修改入参；返回新 map（被完全覆盖的 seq 不会留下空切片）。
func DropAuxThoughtsCoveredByBlocks(aux map[int][]ExchangeAux, blocks map[int][]Block) map[int][]ExchangeAux {
	if len(aux) == 0 || len(blocks) == 0 {
		return aux
	}
	covered := make(map[int]map[string]bool, len(blocks))
	for seq, items := range blocks {
		if seq <= 0 || len(items) == 0 {
			continue
		}
		var texts map[string]bool
		for _, block := range items {
			if block.Type != BlockTypeReasoning || block.Text == "" {
				continue
			}
			if texts == nil {
				texts = make(map[string]bool, len(items))
			}
			texts[strings.TrimSpace(block.Text)] = true
		}
		if texts != nil {
			covered[seq] = texts
		}
	}
	if len(covered) == 0 {
		return aux
	}

	out := make(map[int][]ExchangeAux, len(aux))
	for seq, items := range aux {
		texts := covered[seq]
		if texts == nil {
			out[seq] = items
			continue
		}
		kept := make([]ExchangeAux, 0, len(items))
		for _, item := range items {
			// 只处理「纯 thought 条目」：带 ToolCall / Todo / Plan / Compact 的条目
			// 仍承载渲染所需的身份与详情，即使它同时带 thought 也原样保留
			// （BuildTurnBlocks 的判据顺序是 thought 优先，这里的保留方向与之相反 ——
			// 宁可多传一条，也不让卡片失去自己的数据）。
			isPureThought := item.Thought != "" && item.ToolCall == nil &&
				item.Todo == nil && item.Plan == nil && item.Compact == nil
			if isPureThought && texts[strings.TrimSpace(item.Thought)] {
				continue
			}
			kept = append(kept, item)
		}
		if len(kept) > 0 {
			out[seq] = kept
		}
	}
	return out
}
