package usecase

import (
	"context"
	"strings"
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

// 回合收尾之后的续跑追加（2026-11-25 用户报「工具调用堆在正文后面」）。
//
// 行为判据：首轮落盘的块位置不动，迟到的事件按到达顺序接在后面；已经落过盘的
// 工具卡状态变化在原 pos 上补正，不新开卡。

func newTailTestSession(t *testing.T) (*session.Manager, string) {
	t.Helper()
	root := fs.NewRootInfo("root", "Root", t.TempDir())
	manager := session.NewManager(root)
	created, err := manager.Create(context.Background(), session.CreateInput{
		Type: session.TypeChat, Agent: "hermes", Name: "Tail",
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager, created.Key
}

func tailTestTool(callID, status string) *agenttypes.ToolCall {
	return &agenttypes.ToolCall{CallID: callID, Kind: "execute", Status: status, Title: callID}
}

// 首轮落盘 [正文, 工具 c1]，之后到达「一段续跑正文 + 工具 c2」。
func TestTurnTailAppendsContinuationAfterInitialBlocks(t *testing.T) {
	manager, key := newTailTestSession(t)
	state := &turnStreamState{}
	state.mu.Lock()
	state.content = "先想后做"
	state.lastUpdateType = string(agenttypes.EventTypeToolCall)
	state.mu.Unlock()
	state.addAux(session.ExchangeAux{Line: state.assistantLine(), ToolCall: tailTestTool("c1", "running")})

	content, rawAux := state.snapshot()
	initial, err := persistTurnBlocksReturning(context.Background(), manager, key, 2, "turn-1", content, dedupeExchangeAuxBuffer(rawAux))
	if err != nil {
		t.Fatal(err)
	}
	if len(initial) != 2 {
		t.Fatalf("首轮块数 = %d, want 2", len(initial))
	}
	w := newTurnTailWriter(manager, key, 2, "turn-1", state, len(content), len(rawAux), initial)

	// 续跑：先一句话，再一个工具（与真实顺序一致：文本块先到，工具卡在它之后）。
	state.appendChunk("续跑正文")
	state.addAux(session.ExchangeAux{Line: state.assistantLine(), ToolCall: tailTestTool("c2", "running")})
	w.flush(true)

	resolved, err := manager.ReadResolvedBlocks(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 4 {
		t.Fatalf("块数 = %d, want 4: %+v", len(resolved), resolved)
	}
	for i, block := range resolved {
		if block.Pos != i {
			t.Fatalf("pos[%d] = %d，续跑没有顺延在首轮之后: %+v", i, block.Pos, resolved)
		}
	}
	if resolved[0].Type != session.BlockTypeText || resolved[0].Text != "先想后做" {
		t.Fatalf("首轮正文被改写: %+v", resolved[0])
	}
	if resolved[1].ToolCall == nil || resolved[1].ToolCall.CallID != "c1" {
		t.Fatalf("首轮工具卡被挪动: %+v", resolved[1])
	}
	if resolved[2].Type != session.BlockTypeText || strings.TrimSpace(resolved[2].Text) != "续跑正文" {
		t.Fatalf("迟到的正文没有接在首轮之后: %+v", resolved[2])
	}
	if resolved[3].ToolCall == nil || resolved[3].ToolCall.CallID != "c2" {
		t.Fatalf("迟到的工具卡没有接在正文之后: %+v", resolved[3])
	}
}

// 已落盘工具卡的完成状态：在原 pos 上 revision+1，不新开卡。
func TestTurnTailRevisesPersistedToolStatusInPlace(t *testing.T) {
	manager, key := newTailTestSession(t)
	state := &turnStreamState{}
	state.mu.Lock()
	state.content = "正文"
	state.lastUpdateType = string(agenttypes.EventTypeToolCall)
	state.mu.Unlock()
	state.addAux(session.ExchangeAux{Line: state.assistantLine(), ToolCall: tailTestTool("c1", "running")})

	content, rawAux := state.snapshot()
	initial, err := persistTurnBlocksReturning(context.Background(), manager, key, 2, "turn-1", content, dedupeExchangeAuxBuffer(rawAux))
	if err != nil {
		t.Fatal(err)
	}
	w := newTurnTailWriter(manager, key, 2, "turn-1", state, len(content), len(rawAux), initial)

	state.addAux(session.ExchangeAux{Line: state.assistantLine(), ToolCall: tailTestTool("c1", "completed")})
	w.flush(true)

	resolved, err := manager.ReadResolvedBlocks(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	toolBlocks := 0
	var tool session.Block
	for _, block := range resolved {
		if block.Type == session.BlockTypeToolCall {
			toolBlocks++
			tool = block
		}
	}
	if toolBlocks != 1 {
		t.Fatalf("同一 callId 落出 %d 张卡，want 1: %+v", toolBlocks, resolved)
	}
	if tool.ToolCall == nil || tool.ToolCall.Status != "completed" {
		t.Fatalf("工具状态没有补正: %+v", tool.ToolCall)
	}
	if tool.Revision != 1 {
		t.Fatalf("revision = %d, want 1（补正必须走 revision+1）", tool.Revision)
	}
	if tool.Pos != 1 {
		t.Fatalf("pos = %d, want 1（不得换位置）", tool.Pos)
	}
}

// 行号平移：已落盘前缀的最后一行写没写完，决定续跑里 aux 的相对行号。
func TestTurnTailPrefixLines(t *testing.T) {
	cases := []struct {
		prefix string
		want   int
	}{
		{"", 0},
		{"aaa", 0},   // 最后一行还没写完，剩余部分属于增量
		{"aaa\n", 1}, // 行已收口
		{"aaa\n\n", 1},
		{"a\nb", 1},
		{"a\nb\n", 2},
	}
	for _, tc := range cases {
		if got := turnTailPrefixLines(tc.prefix); got != tc.want {
			t.Fatalf("turnTailPrefixLines(%q) = %d, want %d", tc.prefix, got, tc.want)
		}
	}
}

// 只有空白增量时不落空块，游标仍然推进（否则同一段空白会被反复编译）。
func TestTurnTailSkipsBlankDelta(t *testing.T) {
	manager, key := newTailTestSession(t)
	state := &turnStreamState{}
	state.mu.Lock()
	state.content = "正文"
	state.mu.Unlock()
	content, rawAux := state.snapshot()
	initial, err := persistTurnBlocksReturning(context.Background(), manager, key, 2, "turn-1", content, dedupeExchangeAuxBuffer(rawAux))
	if err != nil {
		t.Fatal(err)
	}
	w := newTurnTailWriter(manager, key, 2, "turn-1", state, len(content), len(rawAux), initial)

	state.appendChunk("\n\n")
	w.flush(true)

	resolved, err := manager.ReadResolvedBlocks(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 1 {
		t.Fatalf("空白增量落出了 %d 个块: %+v", len(resolved), resolved)
	}
	if w.contentAt != len("正文\n\n") {
		t.Fatalf("contentAt = %d, want %d：游标必须推进", w.contentAt, len("正文\n\n"))
	}
}
