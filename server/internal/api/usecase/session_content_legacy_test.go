package usecase

import (
	"context"
	"strings"
	"testing"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/session"
)

// S2d-2：主日志不再承载正文。
//
// D1 的最后一步。S2d-1 补齐了存量（回填成块），现在可以把 `Exchange.Content` 从
// 「真相来源」降级为「回填输入」。本文件钉住降级后的四条契约，每一条对应一个会
// 真实丢内容的失败模式。

// 一条 live 轮次写完后，主日志里那一行的正文必须是空的 —— 正文只在块流里。
func TestLiveTurnLeavesMainLogBodyEmpty(t *testing.T) {
	ctx := context.Background()
	manager, key := newEarlyUserPersistManager(t)
	current, err := manager.Get(ctx, key, 0)
	if err != nil {
		t.Fatal(err)
	}
	ts := time.Now().UTC()
	userSeq, turnID, err := persistUserTurnExchange(ctx, manager, current, "本轮提问", "claude", "", "", "", false, ts)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.AddExchangeForAgentAt(ctx, current, "agent", "", "claude", "", "", "", ts.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	after0, err := manager.Get(ctx, key, 0)
	if err != nil {
		t.Fatal(err)
	}
	agentSeq := session.MaxExchangeSeq(after0.Exchanges)
	if err := persistTurnBlocks(ctx, manager, key, agentSeq, turnID, "模型回复正文", nil); err != nil {
		t.Fatal(err)
	}
	after, err := manager.Get(ctx, key, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, ex := range after.Exchanges {
		if strings.TrimSpace(ex.LegacyContent) != "" {
			t.Fatalf("seq=%d 主日志仍带正文 %q：块流已是唯一载体，双写就是两份真相", ex.Seq, ex.LegacyContent)
		}
	}
	// 「主日志空了」本身不是成果，「正文找得到」才是。
	blocks, err := manager.ReadBlocks(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	got := map[int]string{}
	for _, b := range blocks {
		if b.Type == session.BlockTypeText {
			got[b.Seq] = b.Text
		}
	}
	if got[userSeq] != "本轮提问" || got[agentSeq] != "模型回复正文" {
		t.Fatalf("块流正文 = %#v，want user=%q agent=%q", got, "本轮提问", "模型回复正文")
	}
}

// 导入路径同样要写块。S2d-1 之前导入只写主日志 + aux，全仓 0 个块流文件就是这么来的。
func TestImportedExchangeWritesBlocksInsteadOfMainLogBody(t *testing.T) {
	ctx := context.Background()
	_, manager, _, current, key := newLiveOwnedSyncSession(t, ctx)

	ok, err := appendImportedExchange(ctx, manager, current, "dsh", agenttypes.ImportedExchange{
		Role:      "agent",
		Content:   "导入的正文",
		Timestamp: time.Now().UTC(),
		NativeID:  "11111111-2222-4333-8444-555555555555",
		Aux: []agenttypes.ImportedExchangeAux{
			{Line: 1, Thought: "导入的思考"},
		},
	})
	if err != nil || !ok {
		t.Fatalf("appendImportedExchange ok=%v err=%v", ok, err)
	}
	after, err := manager.Get(ctx, key, 0)
	if err != nil {
		t.Fatal(err)
	}
	seq := session.MaxExchangeSeq(after.Exchanges)
	// 只查**导入新写的这一行**。同文件的 newLiveOwnedSyncSession 前置里那两行是直调
	// 底层 AddExchangeForAgentAt 写下的存量数据形状（那是 fixture，不是生产路径），
	// 它们带正文是合法的 —— 老会话的主日志本来就带正文（S2d-1 的回填输入）。
	for _, ex := range after.Exchanges {
		if ex.Seq < seq {
			continue
		}
		if strings.TrimSpace(ex.LegacyContent) != "" {
			t.Fatalf("导入行 seq=%d 主日志仍带正文 %q", ex.Seq, ex.LegacyContent)
		}
	}
	blocks, err := manager.ReadBlocks(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	var sawText, sawReasoning bool
	for _, b := range blocks {
		if b.Seq != seq {
			continue
		}
		if b.Type == session.BlockTypeText && b.Text == "导入的正文" {
			sawText = true
		}
		if b.Type == session.BlockTypeReasoning && b.Text == "导入的思考" {
			sawReasoning = true
		}
	}
	if !sawText || !sawReasoning {
		t.Fatalf("导入没写成块（text=%v reasoning=%v）: %#v", sawText, sawReasoning, blocks)
	}
}

// 判重仍然要比正文 —— 但正文现在住在块流里。
//
// claude 的 user 行拿不到 native uuid（`Stream.Send(ctx, string)` 没有这个入口），
// 所以 user 行的判重**只能**靠正文比对。主日志不再有正文之后，这条判重必须读块流，
// 否则同一个 prompt 会被反复当成新行写下去 —— 那正是「ask 下面又渲染一轮出现过的文字」。
func TestUserRowDedupComparesBlockTextNotMainLogBody(t *testing.T) {
	ctx := context.Background()
	manager, key := newEarlyUserPersistManager(t)
	current, err := manager.Get(ctx, key, 0)
	if err != nil {
		t.Fatal(err)
	}
	const prompt = "只该出现一次的提问"
	for i := 0; i < 2; i++ {
		// 相邻两次：真人不会在同一瞬间发两次同样的话，判重必须判成重复。
		if _, _, err := persistUserTurnExchange(ctx, manager, current, prompt, "claude", "", "", "", false,
			time.Now().UTC().Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	after, err := manager.Get(ctx, key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := countUserRows(after); got != 1 {
		t.Fatalf("user 行 = %d, want 1（判重没读块流，同一句话被写了两遍）", got)
	}
	_ = key
}

// 真正的重复提问必须**照旧**写两行 —— 判重不能因为换了数据源就变得过于激进。
func TestUserRowDedupStillKeepsGenuineRepeatedPrompts(t *testing.T) {
	ctx := context.Background()
	manager, key := newEarlyUserPersistManager(t)
	current, err := manager.Get(ctx, key, 0)
	if err != nil {
		t.Fatal(err)
	}
	const prompt = "继续"
	for i := 0; i < 3; i++ {
		// 间隔 10 分钟：真人连打三次「继续」不会是同一瞬间。
		if _, _, err := persistUserTurnExchange(ctx, manager, current, prompt, "claude", "", "", "", false,
			time.Now().UTC().Add(time.Duration(i)*10*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	after, err := manager.Get(ctx, key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := countUserRows(after); got != 3 {
		t.Fatalf("user 行 = %d, want 3（判重把真人重复提问误吞了）", got)
	}
}

// fork 一个**老会话**（源还没有块流）必须仍然把正文带过去。
//
// 这条最容易漏：copyForkBlocks 只复制「源已有的块」，而老会话的正文在主日志里。
// 不补这一刀，fork 出来的会话就只剩工具卡、没有正文 —— 而用户看不出为什么。
func TestForkOfLegacySessionCarriesBodyIntoBlocks(t *testing.T) {
	ctx := context.Background()
	manager, key := newEarlyUserPersistManager(t)
	ts := time.Now().UTC()
	// 老形态：正文只在主日志，块流为空。
	if err := manager.AddExchangeForAgentAt(ctx, &session.Session{Key: key}, "user", "老问题", "claude", "", "", "", ts); err != nil {
		t.Fatal(err)
	}
	if err := manager.AddExchangeForAgentAt(ctx, &session.Session{Key: key}, "agent", "老正文", "claude", "", "", "", ts); err != nil {
		t.Fatal(err)
	}
	if blocks, err := manager.ReadBlocks(ctx, key); err != nil || len(blocks) != 0 {
		t.Fatalf("前置条件：源应无块，blocks=%#v err=%v", blocks, err)
	}

	source, err := manager.Get(ctx, key, 0)
	if err != nil {
		t.Fatal(err)
	}
	fork, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Agent: "claude", Name: "Forked"})
	if err != nil {
		t.Fatal(err)
	}
	// 走真正的生产入口。**不能**只调 copyForkBlocks：它只复制「源已有的块」，
	// 老会话一个块都没有，于是它什么都不做 —— 补编译那一刀在 copyForkHistory 里
	// （fillForkBlocksFromLegacy），绕开它就测不到要测的东西。
	if _, err := copyForkHistory(ctx, manager, source, fork, session.MaxExchangeSeq(source.Exchanges), "claude"); err != nil {
		t.Fatal(err)
	}
	blocks, err := manager.ReadBlocks(ctx, fork.Key)
	if err != nil {
		t.Fatal(err)
	}
	var sawBody bool
	for _, b := range blocks {
		if b.Text == "老正文" {
			sawBody = true
		}
	}
	if !sawBody {
		t.Fatalf("fork 丢了老会话的正文: %#v", blocks)
	}
}

func countUserRows(s *session.Session) int {
	n := 0
	for _, ex := range s.Exchanges {
		if ex.Role == "user" {
			n++
		}
	}
	return n
}
