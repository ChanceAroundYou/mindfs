package api

import (
	"context"

	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/session"
)

// applyWindowBlockBudget 把「按轮切」的窗口再按**块数**收一次口。
//
// 为什么在 handler 这一层做：块数只有拿到 blocks 之后才知道，而 blocks 是窗口 seq
// 集合的函数 —— 先在 GetSession 里裁轮次就得为「还不确定要不要」的轮次多读一遍块流。
// 顺序改成「先按轮取（现状）→ 读到块 → 按块数收口」，读盘量与今天完全相同，
// 省下的是传输与前端渲染，而那两项才是实测的 1.4–1.9 s 长任务来源。
//
// 收口之后 minSeq/maxSeq 必须按**剩余**轮次重算：客户端的 loadMore 用 minSeq 作为
// 下一页的 before_seq，报旧值会让它跳过被裁掉的那几轮（静默丢历史）。
func applyWindowBlockBudget(
	out *session.Session,
	windowMeta *session.SessionWindowMeta,
	blocks map[int][]session.Block,
	budget int,
) (*session.Session, *session.SessionWindowMeta) {
	if out == nil || windowMeta == nil || budget <= 0 {
		return out, windowMeta
	}
	trimmed := usecase.ApplyWindowBlockBudget(out.Exchanges, blocks, budget)
	windowMeta.BlockCount = trimmed.BlockCount
	windowMeta.Truncated = trimmed.Truncated
	if !trimmed.Truncated {
		return out, windowMeta
	}
	out.Exchanges = trimmed.Exchanges
	if len(out.Exchanges) > 0 {
		windowMeta.MinSeq = out.Exchanges[0].Seq
		windowMeta.MaxSeq = out.Exchanges[len(out.Exchanges)-1].Seq
	}
	// 被裁掉的是更老的轮次 ⇒ 一定还有更早的历史可翻。
	windowMeta.HasMore = true
	return out, windowMeta
}

// windowBlockBudget 是窗口载荷的块数上界（0 = 关闭预算）。
//
// 直接返回常量而不是接配置：这是**载荷守恒**性质的修正（窗口本就不该无上界），
// 不是可调的业务参数。要回滚就回滚这个提交，别留一个「关掉照样冻」的开关。
func (h *HTTPHandler) windowBlockBudget() int {
	if h != nil && h.WindowBlockBudget > 0 {
		return h.WindowBlockBudget
	}
	return usecase.DefaultWindowBlockBudget
}

// restrictBlocksToExchanges / restrictAuxToExchanges 把块与 aux 收窄到回包里的 seq。
//
// 预算裁剪之后 exchanges 只是原窗口的后缀，而块/aux 是按**原**窗口 seq 集合读出来的。
// 不裁的话回包里会带着不在 exchanges 里的 seq 的块；客户端按 seq 覆盖式合并
// （`blocks: {...cache, ...win}`），那些孤立块会一直留在缓存里 —— 表现为
// 「时间线上多出几轮已经翻不到上部的内容」。
func restrictBlocksToExchanges(blocks map[int][]session.Block, exchanges []session.Exchange) map[int][]session.Block {
	if len(blocks) == 0 {
		return blocks
	}
	keep := seqSetOfExchanges(exchanges)
	out := make(map[int][]session.Block, len(keep))
	for seq, items := range blocks {
		if keep[seq] {
			out[seq] = items
		}
	}
	return out
}

func restrictAuxToExchanges(aux map[int][]session.ExchangeAux, exchanges []session.Exchange) map[int][]session.ExchangeAux {
	if len(aux) == 0 {
		return aux
	}
	keep := seqSetOfExchanges(exchanges)
	out := make(map[int][]session.ExchangeAux, len(keep))
	for seq, items := range aux {
		if keep[seq] {
			out[seq] = items
		}
	}
	return out
}

func seqSetOfExchanges(exchanges []session.Exchange) map[int]bool {
	set := make(map[int]bool, len(exchanges))
	for _, ex := range exchanges {
		if ex.Seq > 0 {
			set[ex.Seq] = true
		}
	}
	return set
}

// sessionDetailPayload 组装单会话详情的回包（handleSessionGet 与 handleSessionSync 共用）。
//
// 两个 handler 的差别只在「前面做哪种外部同步」，回包构造必须逐字节一致 ——
// 原先这段是两份复制粘贴，改窗口口径时漏改一处就会出现「点同步与直接打开看到的
// 不一样」（2026-09 已因同类分叉踩过一次）。抽成一处后，窗口预算、aux 去重、
// 块/aux 收窄三种处理天然对两条路径同时生效。
func (h *HTTPHandler) sessionDetailPayload(
	ctx context.Context,
	uc *usecase.Service,
	rootID string,
	key string,
	afterSeq int,
	beforeSeq int,
	latest int,
	limit int,
) (map[string]any, error) {
	out, windowMeta, err := uc.GetSession(ctx, usecase.GetSessionInput{
		RootID:    rootID,
		Key:       key,
		Seq:       afterSeq,
		BeforeSeq: beforeSeq,
		Limit:     limit,
		Latest:    latest,
	})
	if err != nil {
		return nil, err
	}
	contextWindow, _ := uc.GetSessionContextWindow(ctx, usecase.GetSessionContextWindowInput{
		RootID: rootID,
		Key:    key,
	})
	// 窗口请求只需窗口内 seq 的 aux：原先传 Seq=0 会让 manager 全量读整个 aux 文件
	// （实测某会话 11.8MB/约 300ms 且持 m.mu），再在下面按 windowSeqs 过滤掉绝大部分。
	var exchangeAux map[int][]session.ExchangeAux
	if windowMeta != nil {
		exchangeAux, _ = uc.GetSessionExchangeAuxWindow(ctx, usecase.GetSessionExchangeAuxWindowInput{
			RootID: rootID,
			Key:    key,
			Seqs:   windowSeqsForExchanges(out.Exchanges),
		})
	} else {
		exchangeAux, _ = uc.GetSessionExchangeAux(ctx, usecase.GetSessionExchangeAuxInput{
			RootID: rootID,
			Key:    key,
			Seq:    afterSeq,
		})
	}
	// 正文块：seq 集合与上面 aux 的窗口口径一致 —— 用**回包里的** exchange seq，
	// 而不是请求参数推算的窗口边界，这样窗口/增量/全量三条路共用同一段逻辑，
	// 也不会出现「块挂到了不在回包里的 seq 上」。
	blocks, _ := uc.GetSessionBlocksWindow(ctx, usecase.GetSessionBlocksWindowInput{
		RootID: rootID,
		Key:    key,
		Seqs:   windowSeqsForExchanges(out.Exchanges),
		// 回填输入（S2d-1）：这两份数据响应里本来就要带着，回填不该为此再读一遍盘。
		Exchanges: out.Exchanges,
		Aux:       exchangeAux,
	})
	// 窗口按**块数**收口（CUSTOM(G-JS)）：同样是「8 轮」，大会话里可以是 1,282 块
	// （首屏）或 4,526 块（上翻）。必须在 blocks 读完之后裁 —— 见 applyWindowBlockBudget。
	out, windowMeta = applyWindowBlockBudget(out, windowMeta, blocks, h.windowBlockBudget())
	if windowMeta != nil && windowMeta.Truncated {
		blocks = restrictBlocksToExchanges(blocks, out.Exchanges)
		exchangeAux = restrictAuxToExchanges(exchangeAux, out.Exchanges)
	}
	return h.sessionResponse(out, contextWindow, exchangeAux, blocks, windowMeta), nil
}
