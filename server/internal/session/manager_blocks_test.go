package session

import (
	"context"
	"strings"
	"testing"

	rootfs "mindfs/server/internal/fs"

	agenttypes "mindfs/server/internal/agent/types"
)

// 块流是会话正文的唯一载体（方案 D1）。这一步先只做存储层：能 append、能按序读回、
// 能按 seq 集合过滤、同 seq 不同块都在。写路径接线在 S2a 后半。
func TestAppendBlocksPersistsAndReadsBackInOrder(t *testing.T) {
	m := newBlockTestManager(t)
	key := newBlockTestSession(t, m)
	ctx := context.Background()

	blocks := []Block{
		{Seq: 2, Pos: 0, Type: BlockTypeReasoning, Text: "thinking", Step: 2},
		{Seq: 2, Pos: 1, Type: BlockTypeText, Text: "hello", Step: 2},
		{Seq: 2, Pos: 2, Type: BlockTypeToolCall, ToolCall: &agenttypes.ToolCall{CallID: "c1", Title: "edit"}, Step: 2},
		{Seq: 3, Pos: 0, Type: BlockTypeText, Text: "user said", Step: 3},
	}
	if err := m.AppendBlocks(ctx, key, blocks); err != nil {
		t.Fatalf("AppendBlocks: %v", err)
	}
	// 空切片是合法的 no-op（用户空消息不该产生块，调用方也因此不用分支）。
	if err := m.AppendBlocks(ctx, key, nil); err != nil {
		t.Fatalf("AppendBlocks(nil): %v", err)
	}

	got, err := m.ReadBlocks(ctx, key)
	if err != nil {
		t.Fatalf("ReadBlocks: %v", err)
	}
	if len(got) != len(blocks) {
		t.Fatalf("blocks = %d rows, want %d (%#v)", len(got), len(blocks), got)
	}
	for i, want := range blocks {
		if got[i].Seq != want.Seq || got[i].Pos != want.Pos || got[i].Type != want.Type {
			t.Fatalf("blocks[%d] = %+v, want seq=%d pos=%d type=%s", i, got[i], want.Seq, want.Pos, want.Type)
		}
	}
	if got[0].Text != "thinking" || got[1].Text != "hello" || got[2].ToolCall.CallID != "c1" {
		t.Fatalf("block payload not round-tripped: %#v", got)
	}
}

func TestReadBlocksMissingFileIsEmpty(t *testing.T) {
	m := newBlockTestManager(t)
	key := newBlockTestSession(t, m)
	got, err := m.ReadBlocks(context.Background(), key)
	if err != nil {
		t.Fatalf("ReadBlocks: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("blocks = %#v, want empty", got)
	}
}

func TestGetBlocksWindowFiltersBySeq(t *testing.T) {
	m := newBlockTestManager(t)
	key := newBlockTestSession(t, m)
	ctx := context.Background()
	blocks := []Block{
		{Seq: 1, Pos: 0, Type: BlockTypeText, Text: "one", Step: 1},
		{Seq: 2, Pos: 0, Type: BlockTypeText, Text: "two", Step: 2},
		{Seq: 3, Pos: 0, Type: BlockTypeText, Text: "three", Step: 3},
	}
	if err := m.AppendBlocks(ctx, key, blocks); err != nil {
		t.Fatalf("AppendBlocks: %v", err)
	}
	got, err := m.GetBlocksWindow(ctx, key, map[int]bool{2: true})
	if err != nil {
		t.Fatalf("GetBlocksWindow: %v", err)
	}
	if len(got) != 1 || got[0].Text != "two" {
		t.Fatalf("window blocks = %#v, want only seq 2", got)
	}
	// 空集合 = 窗口为空，不报错。
	got, err = m.GetBlocksWindow(ctx, key, nil)
	if err != nil {
		t.Fatalf("GetBlocksWindow(nil): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("window blocks = %#v, want empty", got)
	}
}

// 旧格式兼容（方案 §3.2）：写入时没有 pos 的行要能按 pos 缺省读回，
// 且读侧必须忽略 pos 缺失（退回 line）的旧行 —— 这里先钉住「缺省 pos 不当成 0 参与排序」。
func TestReadBlocksToleratesLegacyRowsWithoutPos(t *testing.T) {
	m := newBlockTestManager(t)
	key := newBlockTestSession(t, m)
	path, err := m.blocksPath(key)
	if err != nil {
		t.Fatalf("blocksPath: %v", err)
	}
	legacy := "{\"seq\":7,\"type\":\"text\",\"text\":\"legacy\"}\n"
	if err := m.root.WriteMetaFile(path, []byte(legacy)); err != nil {
		t.Fatalf("WriteMetaFile: %v", err)
	}
	got, err := m.ReadBlocks(context.Background(), key)
	if err != nil {
		t.Fatalf("ReadBlocks: %v", err)
	}
	if len(got) != 1 || got[0].Seq != 7 || got[0].Text != "legacy" {
		t.Fatalf("legacy blocks = %#v, want one seq=7 text row", got)
	}
	if got[0].Pos != 0 {
		t.Fatalf("legacy pos = %d, want 0 (缺省即 0，读侧不猜)", got[0].Pos)
	}
}

func TestBlocksLiveBesideExchangeLog(t *testing.T) {
	m := newBlockTestManager(t)
	key := newBlockTestSession(t, m)
	path, err := m.blocksPath(key)
	if err != nil {
		t.Fatalf("blocksPath: %v", err)
	}
	if !strings.HasSuffix(path, ".blocks.jsonl") {
		t.Fatalf("blocks path = %q, want <key>.blocks.jsonl", path)
	}
}

func newBlockTestManager(t *testing.T) *Manager {
	t.Helper()
	return newColdManager(t, rootfs.NewRootInfo("blocks", "blocks", t.TempDir()))
}

func newBlockTestSession(t *testing.T, m *Manager) string {
	t.Helper()
	created, err := m.Create(context.Background(), CreateInput{Type: TypeChat, Name: "Blocks"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return created.Key
}

// 删会话必须连块流一起删：块是正文载体（CUSTOM(G-BH)），留在盘上既是隐私残留，
// 也会在会话 key 复用时被当成「这个会话本来就有正文」读出来。
func TestDeleteSessionRemovesBlockStream(t *testing.T) {
	root := rootfs.NewRootInfo("blocks-delete", "blocks-delete", t.TempDir())
	m := NewManager(root)
	created, err := m.Create(context.Background(), CreateInput{Type: TypeChat, Name: "Doomed"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := m.AppendBlocks(context.Background(), created.Key, []Block{
		{Seq: 1, Pos: 0, Type: BlockTypeText, Text: "body", Step: 1, TurnID: "t"},
	}); err != nil {
		t.Fatalf("AppendBlocks: %v", err)
	}
	blocksPath, err := m.blocksPath(created.Key)
	if err != nil {
		t.Fatalf("blocksPath: %v", err)
	}
	if _, err := m.root.StatMetaFile(blocksPath); err != nil {
		t.Fatalf("块流应当已存在：%v", err)
	}

	if err := m.Delete(context.Background(), created.Key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := m.root.StatMetaFile(blocksPath); err == nil {
		t.Fatalf("删会话后块流仍在：%s", blocksPath)
	}
	// 会话 key 复用后不得读到上一轮的正文。
	reused, err := m.Create(context.Background(), CreateInput{Type: TypeChat, Key: created.Key, Name: "Reused"})
	if err != nil {
		t.Fatalf("re-Create: %v", err)
	}
	blocks, err := m.ReadBlocks(context.Background(), reused.Key)
	if err != nil {
		t.Fatalf("ReadBlocks: %v", err)
	}
	if len(blocks) != 0 {
		t.Fatalf("复用 key 后读到了上一轮的块：%#v", blocks)
	}
}
