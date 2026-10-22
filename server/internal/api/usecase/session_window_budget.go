package usecase

import "mindfs/server/internal/session"

// 窗口块数预算：把「一页会话」的载荷从**无上界**变成有上界。
//
// 起因（2026-10-22，pc 节点「重新训练」会话实测）：窗口 API 的 latest / limit 数的是
// **轮**（exchanges），而一轮的块数是 agent 行为决定的 —— 该会话单轮最多 3,224 块。
// 于是 `latest=8` 回 1,282 块 / 2.49 MB，`before_seq=93&limit=8` 回 4,526 块 / 9.36 MB，
// 前端一帧挂 4,500 个时间线条目 → 主线程 1.4–1.9 s 长任务。
//
// 这里给窗口加一个**块数**上界：从尾部往前累加每轮的块数，超出预算就丢掉更老的轮次。
// 只按**整轮**裁剪，不在轮内切块 —— 轮内切块要引入新的游标语义（pos 游标）并波及
// aux.line 对账、工具分组、fork 与 targetSeq 定位，收益不抵风险。
//
// 「单轮自己就超预算」是必须保留的例外：宁可这一页只给一轮（并在 window_meta 标
// truncated），也不能返回空窗口 —— 空窗口会让客户端把画面清空，比慢更糟。
const DefaultWindowBlockBudget = 400

// WindowBudget 是裁剪结果。BlockCount 是**裁剪后**窗口内的块数。
type WindowBudget struct {
	Exchanges  []session.Exchange
	BlockCount int
	Truncated  bool
}

// ApplyWindowBlockBudget 从尾部往前按块数预算裁剪窗口。
//
// 语义保证：
//   - budget <= 0 或窗口为空 ⇒ 原样返回、Truncated=false（关闭预算时零行为变化）；
//   - 永远保留至少最后一条 exchange（单轮超预算时 Truncated=true）；
//   - 返回的 Exchanges 是入参的**后缀**，顺序与内容不变（客户端按 seq 升序消费）。
func ApplyWindowBlockBudget(
	exchanges []session.Exchange,
	blocks map[int][]session.Block,
	budget int,
) WindowBudget {
	if budget <= 0 || len(exchanges) == 0 {
		return WindowBudget{Exchanges: exchanges, BlockCount: countWindowBlocks(exchanges, blocks)}
	}
	total := countWindowBlocks(exchanges, blocks)
	used := 0
	keptFrom := len(exchanges)
	for i := len(exchanges) - 1; i >= 0; i-- {
		seq := exchanges[i].Seq
		weight := 0
		if seq > 0 {
			weight = len(blocks[seq])
		}
		// 至少留一条：i == len-1 时无条件收下（哪怕它自己就超预算）。
		if i != len(exchanges)-1 && used+weight > budget {
			break
		}
		used += weight
		keptFrom = i
	}
	// 两种情况都算「被预算裁过」：更老的轮次被丢掉，或留下的这一轮**自己**就超预算
	// （单轮 3,224 块那一轮正是后者）。客户端据此决定「还有更早的可翻」与是否提示。
	trimmed := keptFrom > 0 || used > budget
	if !trimmed {
		return WindowBudget{Exchanges: exchanges, BlockCount: total, Truncated: false}
	}
	return WindowBudget{
		Exchanges:  exchanges[keptFrom:],
		BlockCount: used,
		Truncated:  true,
	}
}

func countWindowBlocks(exchanges []session.Exchange, blocks map[int][]session.Block) int {
	if len(blocks) == 0 {
		return 0
	}
	total := 0
	for _, ex := range exchanges {
		if ex.Seq > 0 {
			total += len(blocks[ex.Seq])
		}
	}
	return total
}
