package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"mindfs/server/internal/agent"
	"mindfs/server/internal/session"
)

func isACPAgent(pool *agent.Pool, agentName string) bool {
	if pool == nil {
		return false
	}
	def, ok := pool.Config().GetAgent(agentName)
	if !ok {
		return false
	}
	protocol := def.Protocol
	if protocol == "" {
		protocol = agent.DefaultProtocol(agentName)
	}
	return protocol == agent.ProtocolACP
}

func resolveForkTarget(current *session.Session, seq int) (session.Exchange, int, error) {
	if current == nil {
		return session.Exchange{}, 0, errors.New("session required")
	}
	var target session.Exchange
	found := false
	for _, exchange := range current.Exchanges {
		if exchange.Seq == seq {
			target = exchange
			found = true
			break
		}
	}
	if !found {
		return session.Exchange{}, 0, errors.New("message not found")
	}
	if !isAgentExchangeRole(target.Role) {
		return session.Exchange{}, 0, errors.New("fork target must be an agent message")
	}
	agentName := strings.TrimSpace(target.Agent)
	if agentName == "" {
		agentName = strings.TrimSpace(session.InferAgentFromSession(current))
	}
	turnIndex := 0
	for _, exchange := range current.Exchanges {
		if exchange.Seq > target.Seq {
			break
		}
		if !isAgentExchangeRole(exchange.Role) {
			continue
		}
		exchangeAgent := strings.TrimSpace(exchange.Agent)
		if agentName != "" && exchangeAgent != "" && exchangeAgent != agentName {
			continue
		}
		turnIndex++
	}
	if turnIndex <= 0 {
		return session.Exchange{}, 0, errors.New("agent turn not found")
	}
	return target, turnIndex, nil
}

func isAgentExchangeRole(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "agent", "assistant":
		return true
	default:
		return false
	}
}

func resolveForkModel(current *session.Session, target session.Exchange) string {
	if model := strings.TrimSpace(target.Model); model != "" {
		return model
	}
	if current == nil {
		return ""
	}
	return strings.TrimSpace(current.Model)
}

func buildForkSessionName(current *session.Session, seq int) string {
	base := "Fork"
	if current != nil && strings.TrimSpace(current.Name) != "" {
		base = strings.TrimSpace(current.Name)
	}
	if seq > 0 {
		name := fmt.Sprintf("%s#%d", base, seq)
		runes := []rune(name)
		if len(runes) > 80 {
			name = string(runes[:80])
		}
		return name
	}
	name := base
	runes := []rune(name)
	if len(runes) > 80 {
		name = string(runes[:80])
	}
	return name
}

func copyForkHistory(ctx context.Context, manager *session.Manager, from, to *session.Session, maxSeq int, fallbackAgent string) (int, error) {
	if manager == nil || from == nil || to == nil {
		return 0, errors.New("session required")
	}
	seqMap := make(map[int]int)
	copied := 0
	for _, exchange := range from.Exchanges {
		if exchange.Seq <= 0 || exchange.Seq > maxSeq {
			continue
		}
		agentName := strings.TrimSpace(exchange.Agent)
		if agentName == "" {
			agentName = strings.TrimSpace(fallbackAgent)
		}
		exchangeCtx := session.WithExchangeModelDisplayName(ctx, exchange.ModelDisplayName)
		exchangeCtx = session.WithExchangeTokenUsage(exchangeCtx, exchange.TokenUsage)
		exchangeCtx = session.WithExchangeSource(exchangeCtx, exchange.Source)
		// 复制轮次身份：mindfs_id 是 MindFS 权威身份，fork 复制的是**同一批轮次**，
		// 所以原样带过去；turn_seq 要重映射到 fork 的 seq 空间。
		// native_* **不带**：fork 开的是全新 agent 会话（AgentSessionID:"" + ForkPoint），
		// 源会话的 native id 对新转录一律无效（方案 §14 C1）。
		exchangeCtx = session.WithExchangeIdentity(exchangeCtx, session.ExchangeIdentity{
			MindFSID: exchange.MindFSID,
			TurnSeq:  seqMap[exchange.TurnSeq],
		})
		if err := manager.AddExchangeForAgentAt(exchangeCtx, to, exchange.Role, exchange.LegacyContent, agentName, exchange.Mode, exchange.Effort, exchange.FastService, exchange.Timestamp); err != nil {
			return copied, err
		}
		copied++
		seqMap[exchange.Seq] = copied
	}
	auxBySeq, err := manager.GetExchangeAux(ctx, from.Key, 0)
	if err != nil {
		return copied, err
	}
	for oldSeq, items := range auxBySeq {
		newSeq := seqMap[oldSeq]
		if newSeq <= 0 {
			continue
		}
		for _, aux := range items {
			next := aux
			next.Seq = newSeq
			if err := manager.AddExchangeAux(ctx, to.Key, next); err != nil {
				return copied, err
			}
		}
	}
	// 源已有块流的会话：原样复制（revision/step 是盘上真值，不重编号）。
	// 老会话（源无块流）：下面用 LegacyContent + aux 编译。
	// 两者都做，缺一不可 —— 只补编译会丢源块里的 revision 信息，只复制会让老会话
	// fork 出一份「没有正文」的会话。
	if err := copyForkBlocks(ctx, manager, from.Key, to.Key, seqMap); err != nil {
		return copied, err
	}
	// 老会话（源还没有块流）的正文只在主日志里 —— 上面只复制了「源已有的块」，
	// 于是 fork 出来的会话会只剩工具卡、没有正文，而用户看不出为什么。这里把
	// 「复制过去、但目标还没有块」的轮次用 LegacyContent + aux 补编译一次。
	// 与 `backfillTurnsFromWindow` 同一口径：判据是「这个 seq 一个块都没有」。
	if err := fillForkBlocksFromLegacy(ctx, manager, to.Key, from.Exchanges, seqMap, auxBySeq); err != nil {
		return copied, err
	}
	return copied, nil
}

// fillForkBlocksFromLegacy 给 fork 目标里「还没有块」的轮次补编译块（正文取自
// 主日志的 LegacyContent）。只在源会话没有块流时才会真正写东西。
func fillForkBlocksFromLegacy(
	ctx context.Context,
	manager *session.Manager,
	toKey string,
	exchanges []session.Exchange,
	seqMap map[int]int,
	auxBySeq map[int][]session.ExchangeAux,
) error {
	if len(exchanges) == 0 || len(seqMap) == 0 {
		return nil
	}
	seqSet := make(map[int]bool, len(exchanges))
	for _, ex := range exchanges {
		if ex.Seq > 0 {
			seqSet[ex.Seq] = true
		}
	}
	existing, err := manager.GetBlocksWindow(ctx, toKey, seqSet)
	if err != nil {
		return err
	}
	has := make(map[int]bool, len(existing))
	for _, block := range existing {
		has[block.Seq] = true
	}
	compiled := make([]session.Block, 0, len(exchanges))
	for _, ex := range exchanges {
		newSeq := seqMap[ex.Seq]
		if newSeq <= 0 || has[newSeq] {
			continue
		}
		blocks := session.BuildTurnBlocks(newSeq, turnBlockStep, ex.MindFSID, ex.LegacyContent, auxBySeq[ex.Seq])
		if len(blocks) == 0 {
			continue
		}
		compiled = append(compiled, blocks...)
	}
	if len(compiled) == 0 {
		return nil
	}
	return manager.AppendBlocks(ctx, toKey, compiled)
}

// copyForkBlocks 把源会话已有的块复制到 fork：seq 按 seqMap 重映射、native_id 清空。
//
// 与 fillForkBlocksFromLegacy 是**分工**不是重复：
//
//	· 本函数 = 源有块流的正常路径，原样复制（含 revision / step，它们是盘上真值）；
//	· fillForkBlocksFromLegacy = 源没有块流的老会话，用 LegacyContent + aux 编译。
//
// 两者都做，先复制再补缺，才不会「有块流的会话 fork 完丢正文」。
func copyForkBlocks(ctx context.Context, manager *session.Manager, fromKey, toKey string, seqMap map[int]int) error {
	if len(seqMap) == 0 {
		return nil
	}
	blocks, err := manager.ReadBlocks(ctx, fromKey)
	if err != nil {
		return err
	}
	copied := make([]session.Block, 0, len(blocks))
	for _, block := range blocks {
		newSeq := seqMap[block.Seq]
		if newSeq <= 0 {
			continue
		}
		next := block
		next.Seq = newSeq
		next.AgentID = ""
		copied = append(copied, next)
	}
	if len(copied) == 0 {
		return nil
	}
	return manager.AppendBlocks(ctx, toKey, copied)
}

type GetSessionInput struct {
	RootID    string
	Key       string
	Seq       int
	BeforeSeq int
	Limit     int
	Latest    int
}

// GetSession returns the session. When BeforeSeq>0 or Latest>0 it serves a
// windowed slice via manager.GetWindow and the returned SessionWindowMeta is
// non-nil; otherwise it serves a seq-incremental (Seq>0) or full (Seq<=0) view.
