package usecase

import (
	"context"
	"regexp"
	"testing"
	"time"

	"mindfs/server/internal/session"
)

var uuidV5Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// 方案 D4/D5：mindfs_id 由 MindFS 在写入点统一铸，**确定性** UUIDv5。
// 三条服务端发起的轮次路径（定时任务 / 看板任务阶段 / 子会话）都经 SendMessage，
// 所以 mint 点只有 persistUserTurnExchange 一处。
//
// **UUIDv5 而非 v4**：同一条消息在两个写入者（live / import）手里必须算出同一个
// mindfs_id，v4 的随机性做不到这件事。所以这里钉的不再是「64 次互不相同」，
// 而是「同 seed 同 id、异 seed 异 id」。
func TestNewMindFSIDIsDeterministicUUIDv5(t *testing.T) {
	// 同 seed → 同 id（这是判重能只认身份的前提）。
	for i := 0; i < 64; i++ {
		id := newMindFSID("", "sess-a|user|7")
		if !uuidV5Pattern.MatchString(id) {
			t.Fatalf("newMindFSID() = %q, want a UUIDv5", id)
		}
	}
	first := newMindFSID("", "sess-a|user|7")
	second := newMindFSID("", "sess-a|user|7")
	if first != second {
		t.Fatalf("同 seed 得到不同 id: %q vs %q —— 重导不再幂等，判重修不回内容猜", first, second)
	}
	// agent_id 优先于 fallbackSeed：同一 agent 消息在两个写入者手里 seed 相同，
	// 这才是「live 与 import 对齐」的那一步。
	if agentA, agentB := newMindFSID("uuid-1", "fb-a"), newMindFSID("uuid-1", "fb-b"); agentA != agentB {
		t.Fatalf("agent_id 相同时 fallbackSeed 影响了结果: %q vs %q", agentA, agentB)
	}
	// 异 seed → 异 id，否则不同轮次会互相吞掉。
	if newMindFSID("", "sess-a|user|7") == newMindFSID("", "sess-a|user|8") {
		t.Fatal("不同 seed 得到同一个 id")
	}
}

// user 行与它的块必须带同一个 mindfs_id，turn_seq 必须等于该 user 行自己的 seq。
// 少了这一条，S2b 切读路径时用户自己的话会挂不回轮次。
func TestPersistUserTurnExchangeMintsTurnIdentityAndWritesUserBlock(t *testing.T) {
	manager, key := newEarlyUserPersistManager(t)
	ctx := context.Background()
	current, err := manager.Get(ctx, key, 0)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	ts := time.Now().UTC()
	seq, turnID, err := persistUserTurnExchange(ctx, manager, current, "本轮提问", "claude", "", "", "", false, ts)
	if err != nil {
		t.Fatalf("persistUserTurnExchange: %v", err)
	}
	if !uuidV5Pattern.MatchString(turnID) {
		t.Fatalf("mindfs_id = %q, want a UUIDv4", turnID)
	}

	var row *session.Exchange
	for i := range current.Exchanges {
		if current.Exchanges[i].Seq == seq {
			row = &current.Exchanges[i]
		}
	}
	if row == nil {
		t.Fatalf("seq %d not in exchanges: %#v", seq, current.Exchanges)
	}
	if row.MindFSID != turnID {
		t.Fatalf("row mindfs_id = %q, want %q", row.MindFSID, turnID)
	}
	if row.TurnSeq != seq {
		t.Fatalf("row turn_seq = %d, want %d（= 打开该轮的 user 行 seq）", row.TurnSeq, seq)
	}

	blocks, err := manager.ReadBlocks(ctx, key)
	if err != nil {
		t.Fatalf("ReadBlocks: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("blocks = %#v, want exactly one user text block", blocks)
	}
	if blocks[0].Type != session.BlockTypeText || blocks[0].Text != "本轮提问" {
		t.Fatalf("block = %#v, want text 本轮提问", blocks[0])
	}
	if blocks[0].Seq != seq || blocks[0].MindFSID != turnID {
		t.Fatalf("block identity = %#v, want seq=%d mindfs_id=%s", blocks[0], seq, turnID)
	}
	if blocks[0].Step < 0 {
		t.Fatalf("block step = %d：S2a 双写期写哨兵会让读取侧作废这些块", blocks[0].Step)
	}
}

// 判重命中（导入器抢先写过）时不得再写一行，也不得再写一个用户块 —— 否则同一个 seq
// 会有两条 text 块，S2b 渲染出重复正文。
func TestPersistUserTurnExchangeDedupeWritesNoBlock(t *testing.T) {
	manager, key := newEarlyUserPersistManager(t)
	ctx := context.Background()
	current, err := manager.Get(ctx, key, 0)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	ts := time.Now().UTC()
	if err := manager.AddExchangeForAgentAt(session.WithExchangeSource(ctx, session.ExchangeSourceImport), current, "user", "本轮提问", "claude", "", "", "", ts); err != nil {
		t.Fatalf("importer write: %v", err)
	}
	current, err = manager.Get(ctx, key, 0)
	if err != nil {
		t.Fatalf("re-Get: %v", err)
	}

	seq, turnID, err := persistUserTurnExchange(ctx, manager, current, "本轮提问", "claude", "", "", "", false, ts)
	if err != nil {
		t.Fatalf("persistUserTurnExchange: %v", err)
	}
	if seq == 0 {
		t.Fatalf("命中判重时必须回传已有那条的 seq")
	}
	// 导入行没有身份，所以判重路径回传空 mindfs_id 是合法的（历史行「可空、可后补」）。
	if turnID != "" {
		t.Fatalf("mindfs_id = %q, want empty：导入行本来就没有 mindfs_id", turnID)
	}
	blocks, err := manager.ReadBlocks(ctx, key)
	if err != nil {
		t.Fatalf("ReadBlocks: %v", err)
	}
	if len(blocks) != 0 {
		t.Fatalf("blocks = %#v, want none：判重命中不得重复写正文", blocks)
	}
}

// **本 bug 的核心回归**：一轮只产出 reasoning、没有任何 text。
// 旧路径把这一轮落库成 content:""，正文在收尾时消失；块流必须留下 reasoning 块。
func TestPersistTurnBlocksKeepsReasoningOnlyTurn(t *testing.T) {
	manager, key := newEarlyUserPersistManager(t)
	ctx := context.Background()
	ts := time.Now().UTC()

	// 复刻 SendMessage 的落盘顺序：user 行 → 助手行 → aux → 块。
	current, err := manager.Get(ctx, key, 0)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	userSeq, turnID, err := persistUserTurnExchange(ctx, manager, current, "问题", "dsh", "", "", "", false, ts)
	if err != nil {
		t.Fatalf("persistUserTurnExchange: %v", err)
	}
	agentCtx := session.WithExchangeIdentity(
		session.WithExchangeSource(ctx, session.ExchangeSourceLive),
		session.ExchangeIdentity{MindFSID: turnID, TurnSeq: userSeq},
	)
	// 助手正文是空的 —— 这正是 os 模型 ~15% 轮次的形态。
	if err := manager.AddExchangeForAgent(agentCtx, current, "agent", "", "dsh", "", "", ""); err != nil {
		t.Fatalf("AddExchangeForAgent: %v", err)
	}
	aux := session.ExchangeAux{Seq: session.MaxExchangeSeq(current.Exchanges), Line: 0, Thought: "先想一下"}
	if err := manager.AddExchangeAux(ctx, key, aux); err != nil {
		t.Fatalf("AddExchangeAux: %v", err)
	}
	agentSeq := session.MaxExchangeSeq(current.Exchanges)
	if err := persistTurnBlocks(ctx, manager, key, agentSeq, turnID, "", []session.ExchangeAux{aux}); err != nil {
		t.Fatalf("persistTurnBlocks: %v", err)
	}

	blocks, err := manager.ReadBlocks(ctx, key)
	if err != nil {
		t.Fatalf("ReadBlocks: %v", err)
	}
	var reasoning []session.Block
	for _, block := range blocks {
		if block.Seq == agentSeq && block.Type == session.BlockTypeReasoning {
			reasoning = append(reasoning, block)
		}
	}
	if len(reasoning) != 1 || reasoning[0].Text != "先想一下" {
		t.Fatalf("reasoning blocks = %#v：正文为空的轮次必须留下 reasoning 块（本 bug 的根因）", reasoning)
	}
	if reasoning[0].MindFSID != turnID {
		t.Fatalf("block mindfs_id = %q, want %q", reasoning[0].MindFSID, turnID)
	}

	// 助手行本身仍然带着同一个 mindfs_id（两行同身份）。
	reloaded, err := manager.Get(ctx, key, 0)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	for _, ex := range reloaded.Exchanges {
		if ex.Seq == agentSeq && ex.MindFSID != turnID {
			t.Fatalf("助手行 mindfs_id = %q, want %q", ex.MindFSID, turnID)
		}
	}
}

// 块序必须与前端渲染序一致：正文段与 aux 按 line 交错。
// 用「内容 + aux 在磁盘上的落盘形态」反查块序，锁死 S2a 与 S2b 的渲染等价。
func TestPersistTurnBlocksMatchesRendererInterleave(t *testing.T) {
	manager, key := newEarlyUserPersistManager(t)
	ctx := context.Background()
	aux := []session.ExchangeAux{
		{Seq: 4, Line: 2, Thought: "third"},
		{Seq: 4, Line: 0, Thought: "first"},
	}
	if err := persistTurnBlocks(ctx, manager, key, 4, "turn-x", "l0\nl1\nl2\nl3", aux); err != nil {
		t.Fatalf("persistTurnBlocks: %v", err)
	}
	blocks, err := manager.ReadBlocks(ctx, key)
	if err != nil {
		t.Fatalf("ReadBlocks: %v", err)
	}
	if len(blocks) != 4 {
		t.Fatalf("blocks = %#v, want 4", blocks)
	}
	wantTypes := []session.BlockType{
		session.BlockTypeReasoning, session.BlockTypeText,
		session.BlockTypeReasoning, session.BlockTypeText,
	}
	for i, wantType := range wantTypes {
		if blocks[i].Type != wantType {
			t.Fatalf("blocks[%d].Type = %s, want %s", i, blocks[i].Type, wantType)
		}
		if blocks[i].Pos != i {
			t.Fatalf("blocks[%d].Pos = %d, want %d", i, blocks[i].Pos, i)
		}
	}
	if blocks[1].Text != "l0\nl1" || blocks[3].Text != "l2\nl3" {
		t.Fatalf("text segments = %q / %q", blocks[1].Text, blocks[3].Text)
	}
	if blocks[0].Text != "first" || blocks[2].Text != "third" {
		t.Fatalf("aux order wrong: %q / %q", blocks[0].Text, blocks[2].Text)
	}
}
