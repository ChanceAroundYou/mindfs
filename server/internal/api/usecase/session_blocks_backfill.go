package usecase

import (
	"context"
	"log"

	"mindfs/server/internal/session"
)

// S2d-1：旧数据回填（读时惰性、幂等、零额外读盘）。
//
// 为什么必须有这一步：方案 §2 的 D1 定的是「块流是唯一正文载体，S2d 删
// `Exchange.Content`」，但本机实测 **320 个会话、2166 行、0 个块流文件**
// （2026-10-11，S2a 已提交但从未部署 —— 块流要 `make install` 之后的二进制才写）。
// 直接删字段等于把全部历史正文清空。
//
// 为什么**读时惰性**而不是写迁移脚本：
//
//   · 迁移脚本意味着「必须有人记得跑」。会话库按项目 / 账户分区散在各处（本机就有
//     4 个 `.mindfs/sessions`，WSL 端另有一份），没有任何一处能保证跑全；漏掉一处
//     就是静默丢历史。
//   · 回填的输入（exchanges + aux）**调用方本来就传进来了** —— 窗口响应本来就带着
//     这两份数据。不额外读一次盘才是关键：`GET /api/sessions/{key}` 是全站最重的
//     端点（实测单次 1.14MB，客户端 1.7s 一次轮询，占 70% 流量），在那里做任何
//     额外 IO 都是把「回填」变成新的性能问题。
//
// 它还顺手救回一类**已经永久丢内容**的行：实测 300 行 agent 正文为空（那个 bug 已
// 落盘的形态，`os` 下只产出 reasoning），但它们的 aux 里还留着 thought —— 回填后
// 它们至少渲染成灰显的思考块，而不是空气泡。

// missingBlockSeqs 返回「窗口里一个块都没有的 seq」。
//
// 判据只有这一条：**这个 seq 没有任何块**。不是「块里没有 text」—— 一轮只有工具
// 调用、正文为空是完全合法的状态，它的渲染结果本就该是「只有工具卡」，不该被回填
// 塞进一个空 text 块。
func missingBlockSeqs(blocks []session.Block, seqs map[int]bool) []int {
	has := make(map[int]bool, len(blocks))
	for _, block := range blocks {
		has[block.Seq] = true
	}
	var missing []int
	for seq := range seqs {
		if seq > 0 && !has[seq] {
			missing = append(missing, seq)
		}
	}
	return missing
}

// backfillTurnsFromWindow 把「没有块的轮次」用调用方给的数据编译成块并落盘，
// 返回实际回填的轮次数。**幂等**：已有块的轮次不在 missing 里，不写第二个字节
// （重复块会让同一个 seq 渲染出两段正文）。
//
// 编译逐字取自老主日志 —— 回填是**搬运**，不是重写：任何「顺手整理一下」都会让它
// 偏离用户当时看到的东西，而它正是那份东西的唯一副本。块序与交错由
// `BuildTurnBlocks` 保证（与前端 `buildAssistantTimeline` 逐条一致）。
func backfillTurnsFromWindow(
	ctx context.Context,
	manager *session.Manager,
	key string,
	exchanges []session.Exchange,
	aux map[int][]session.ExchangeAux,
	seqs map[int]bool,
) (int, error) {
	blocks, err := manager.GetBlocksWindow(ctx, key, seqs)
	if err != nil {
		return 0, err
	}
	missing := missingBlockSeqs(blocks, seqs)
	if len(missing) == 0 {
		return 0, nil
	}
	bySeq := make(map[int]session.Exchange, len(exchanges))
	for _, ex := range exchanges {
		bySeq[ex.Seq] = ex
	}
	compiled := make([]session.Block, 0, len(missing)*2)
	filled := 0
	for _, seq := range missing {
		ex, ok := bySeq[seq]
		if !ok {
			continue
		}
		turnBlocks := session.BuildTurnBlocks(seq, turnBlockStep, ex.TurnID, ex.Content, aux[seq])
		if len(turnBlocks) == 0 {
			// 空正文 + 无 aux = 没有任何可编译的东西（纯工具轮、空占位行）。
			// 不为它们建块：造出一堆空块只会让块流凭空变大，渲染结果一个字都不多。
			continue
		}
		compiled = append(compiled, turnBlocks...)
		filled++
	}
	if len(compiled) == 0 {
		// 什么都不补得出来 —— 不建空块流文件。
		return 0, nil
	}
	if err := manager.AppendBlocks(ctx, key, compiled); err != nil {
		return 0, err
	}
	return filled, nil
}

// backfillTurnsIfNeeded 是给读路径用的入口：失败**不**让整次读失败。
//
// 回填失败不是「这次读失败」，只是「这次读拿不到补出来的正文」—— 前端有回退
// （`buildBaseTimeline` 每 seq 回退旧格式）。要记日志，否则它会静默地一直失败。
func backfillTurnsIfNeeded(
	ctx context.Context,
	manager *session.Manager,
	key string,
	exchanges []session.Exchange,
	aux map[int][]session.ExchangeAux,
	seqs map[int]bool,
) {
	if manager == nil || key == "" || len(seqs) == 0 {
		return
	}
	filled, err := backfillTurnsFromWindow(ctx, manager, key, exchanges, aux, seqs)
	if err != nil {
		log.Printf("[session/blocks] backfill.error session=%s err=%v", key, err)
		return
	}
	if filled > 0 {
		log.Printf("[session/blocks] backfill.done session=%s turns=%d", key, filled)
	}
}
