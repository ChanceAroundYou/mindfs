package usecase

import (
	"context"
	"testing"
	"time"

	"mindfs/server/internal/session"
)

// fork 会开一个**全新的 agent 会话**（ForkSession 里 AgentSessionID:"" + ForkPoint），
// 所以源会话的 native id 对 fork 一律无效 —— 复制历史时不得带过去（方案 §14 C1）。
// 同时块流必须一起复制并重映射 seq，否则 S2b 切读路径后 fork 会话是空的。
func TestCopyForkHistoryCopiesBlocksAndClearsNativeIDs(t *testing.T) {
	manager, fromKey := newEarlyUserPersistManager(t)
	ctx := context.Background()
	from, err := manager.Get(ctx, fromKey, 0)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// 一轮 user + 一轮「只有 reasoning」的 agent —— 正是本 bug 的形态。
	userSeq, turnID, err := persistUserTurnExchange(ctx, manager, from, "问题", "claude", "", "", "", time.Now().UTC())
	if err != nil {
		t.Fatalf("persistUserTurnExchange: %v", err)
	}
	if err := manager.AddExchangeForAgent(
		session.WithExchangeIdentity(ctx, session.ExchangeIdentity{TurnID: turnID, TurnSeq: userSeq, NativeTurn: "old-turn", NativePrompt: "old-prompt"}),
		from, "agent", "", "claude", "", "", ""); err != nil {
		t.Fatalf("AddExchangeForAgent: %v", err)
	}
	agentSeq := session.MaxExchangeSeq(from.Exchanges)
	aux := session.ExchangeAux{Seq: agentSeq, Line: 0, Thought: "只想了一下"}
	if err := manager.AddExchangeAux(ctx, fromKey, aux); err != nil {
		t.Fatalf("AddExchangeAux: %v", err)
	}
	if err := persistTurnBlocks(ctx, manager, fromKey, agentSeq, turnID, "", []session.ExchangeAux{aux}); err != nil {
		t.Fatalf("persistTurnBlocks: %v", err)
	}
	// 给源会话的块补上 native id，验证 fork 不会带过去。
	if err := manager.AppendBlocks(ctx, fromKey, []session.Block{{
		Seq: agentSeq, Pos: 0, Revision: 1, Type: session.BlockTypeReasoning, Text: "只想了一下",
		Step: turnBlockStep, TurnID: turnID, NativeID: "old-native",
	}}); err != nil {
		t.Fatalf("AppendBlocks: %v", err)
	}
	from, err = manager.Get(ctx, fromKey, 0)
	if err != nil {
		t.Fatalf("re-Get: %v", err)
	}

	to, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "Fork"})
	if err != nil {
		t.Fatalf("Create fork: %v", err)
	}
	copied, err := copyForkHistory(ctx, manager, from, to, agentSeq, "claude")
	if err != nil {
		t.Fatalf("copyForkHistory: %v", err)
	}
	if copied != agentSeq {
		t.Fatalf("copied = %d, want %d", copied, agentSeq)
	}

	forked, err := manager.Get(ctx, to.Key, 0)
	if err != nil {
		t.Fatalf("fork Get: %v", err)
	}
	for _, ex := range forked.Exchanges {
		if ex.NativeTurn != "" || ex.NativePrompt != "" {
			t.Fatalf("fork 行带了源会话的 native id（fork 是全新 agent 会话）：%#v", ex)
		}
	}
	blocks, err := manager.ReadBlocks(ctx, to.Key)
	if err != nil {
		t.Fatalf("ReadBlocks: %v", err)
	}
	if len(blocks) == 0 {
		t.Fatalf("fork 没有块流：S2b 之后 fork 会话会渲染成空")
	}
	var sawReasoning bool
	for _, block := range blocks {
		if block.NativeID != "" {
			t.Fatalf("fork 块带了源会话的 native_id：%#v", block)
		}
		if block.TurnID != turnID {
			t.Fatalf("fork 块 turn_id = %q, want %q（权威身份要跟着复制）", block.TurnID, turnID)
		}
		if block.Seq < 1 || block.Seq > agentSeq {
			t.Fatalf("fork 块 seq = %d 越界（必须重映射到 fork 的 seq 空间）", block.Seq)
		}
		if block.Type == session.BlockTypeReasoning && block.Text == "只想了一下" {
			sawReasoning = true
		}
	}
	if !sawReasoning {
		t.Fatalf("fork 块流缺 reasoning 块：%#v", blocks)
	}
	// 块必须挂到 fork 的助手行上。
	for _, block := range blocks {
		if block.Seq == agentSeq && block.Type == session.BlockTypeReasoning {
			// agentSeq 是源会话的助手行 seq；fork 的 seq 空间与它同构（1:1 复制），
			// 所以这里相同是合法的，只要该 seq 在 fork 里确实存在。
			found := false
			for _, ex := range forked.Exchanges {
				if ex.Seq == block.Seq {
					found = true
				}
			}
			if !found {
				t.Fatalf("块 seq=%d 在 fork 里没有对应的行", block.Seq)
			}
		}
	}
}
