package session

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
	rootfs "mindfs/server/internal/fs"
)

// 窗口读必须 O(窗口)：块流是全站最热端点的载荷来源，而单会话实测一轮可达 83 步 /
// 974 条消息。全量读在实测会话上是 MB 级且持 m.mu（每 root 一把），会卡住整个 root。
// 断言计数器而不是计时 —— 计时会随机器抖动。
func TestGetBlocksWindowReadsTailOnly(t *testing.T) {
	root := rootfs.NewRootInfo("blocks-window-tail", "blocks-window-tail", t.TempDir())
	m := NewManager(root)
	created, err := m.Create(context.Background(), CreateInput{Type: TypeChat, Name: "Window"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// 300 个 seq，每个 4 块 —— 足够大到「全量读」与「尾部读」行为可分。
	const seqs = 300
	const perSeq = 4
	blocks := make([]Block, 0, seqs*perSeq)
	for seq := 1; seq <= seqs; seq++ {
		for pos := 0; pos < perSeq; pos++ {
			blocks = append(blocks, Block{
				Seq: seq, Pos: pos, Type: BlockTypeText, Step: 1, TurnID: fmt.Sprintf("turn-%d", seq),
				Text: fmt.Sprintf("seq-%d-pos-%d-%s", seq, pos, filler(600)),
			})
		}
	}
	if err := m.AppendBlocks(context.Background(), created.Key, blocks); err != nil {
		t.Fatalf("AppendBlocks: %v", err)
	}
	if _, err := m.ReadBlocks(context.Background(), created.Key); err != nil {
		t.Fatalf("warm ReadBlocks: %v", err)
	}
	m.fullBlockReads = 0

	window := map[int]bool{seqs - 1: true, seqs: true}
	got, err := m.GetBlocksWindow(context.Background(), created.Key, window)
	if err != nil {
		t.Fatalf("GetBlocksWindow: %v", err)
	}
	if m.fullBlockReads != 0 {
		t.Fatalf("窗口读触发了 %d 次全量读：必须只读尾部", m.fullBlockReads)
	}
	if len(got) != 2*perSeq {
		t.Fatalf("blocks = %d, want %d", len(got), 2*perSeq)
	}
	for i, block := range got {
		if block.Seq != seqs-1 && block.Seq != seqs {
			t.Fatalf("窗口外块泄漏：%#v", block)
		}
		// 必须按 (seq,pos) 升序 —— 前端按 pos 遍历渲染，顺序错就是正文顺序错。
		if i > 0 {
			prev := got[i-1]
			if block.Seq < prev.Seq || (block.Seq == prev.Seq && block.Pos <= prev.Pos) {
				t.Fatalf("块序不是 (seq,pos) 升序：%d/%d 出现在 %d/%d 之后", block.Seq, block.Pos, prev.Seq, prev.Pos)
			}
		}
	}
}

// 反向分块读不得丢掉跨块的长行（实测单行最大 308KB，块流里超长 reasoning 常见）。
func TestGetBlocksWindowHandlesLineLargerThanChunk(t *testing.T) {
	root := rootfs.NewRootInfo("blocks-window-long", "blocks-window-long", t.TempDir())
	m := NewManager(root)
	created, err := m.Create(context.Background(), CreateInput{Type: TypeChat, Name: "Long"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	huge := filler(700 << 10) // 700KB，跨 512KB chunk 边界
	if err := m.AppendBlocks(context.Background(), created.Key, []Block{
		{Seq: 1, Pos: 0, Type: BlockTypeText, Step: 1, Text: "small-1"},
		{Seq: 2, Pos: 0, Type: BlockTypeReasoning, Step: 1, Text: huge},
		{Seq: 3, Pos: 0, Type: BlockTypeText, Step: 1, Text: "small-3"},
	}); err != nil {
		t.Fatalf("AppendBlocks: %v", err)
	}
	got, err := m.GetBlocksWindow(context.Background(), created.Key, map[int]bool{2: true, 3: true})
	if err != nil {
		t.Fatalf("GetBlocksWindow: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("blocks = %d, want 2", len(got))
	}
	if len(got[0].Text) != len(huge) || got[0].Text != huge {
		t.Fatalf("超长块被截断：len=%d want %d", len(got[0].Text), len(huge))
	}
	if got[1].Text != "small-3" {
		t.Fatalf("后一个块 = %q", got[1].Text)
	}
}

// 同 (seq,pos) 多版本取最高 revision（方案 D8）；step<0 的版本是「步序未定」的中间态，
// 读取侧必须忽略（方案 §3.3）。两者都必须在窗口读里生效。
func TestGetBlocksWindowResolvesRevisionAndSkipsUnsealedStep(t *testing.T) {
	root := rootfs.NewRootInfo("blocks-window-rev", "blocks-window-rev", t.TempDir())
	m := NewManager(root)
	created, err := m.Create(context.Background(), CreateInput{Type: TypeChat, Name: "Rev"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := m.AppendBlocks(context.Background(), created.Key, []Block{
		{Seq: 1, Pos: 0, Revision: 0, Type: BlockTypeReasoning, Step: 1, Text: "v0"},
		{Seq: 1, Pos: 0, Revision: 2, Type: BlockTypeReasoning, Step: 1, Text: "v2"},
		{Seq: 1, Pos: 0, Revision: 1, Type: BlockTypeReasoning, Step: 1, Text: "v1"},
		// 只有哨兵版本：读取侧必须忽略它（不能拿 -1 当步序渲染）。
		{Seq: 1, Pos: 1, Revision: 0, Type: BlockTypeText, Step: blockStepSentinel, Text: "unsealed"},
		// 有已收口版本，就取已收口的那个。
		{Seq: 1, Pos: 2, Revision: 0, Type: BlockTypeText, Step: blockStepSentinel, Text: "draft"},
		{Seq: 1, Pos: 2, Revision: 1, Type: BlockTypeText, Step: 3, Text: "final"},
		{Seq: 1, Pos: 3, Revision: 0, Type: BlockTypeToolCall, Step: 1, ToolCall: &agenttypes.ToolCall{CallID: "c1", Title: "t"}},
	}); err != nil {
		t.Fatalf("AppendBlocks: %v", err)
	}
	got, err := m.GetBlocksWindow(context.Background(), created.Key, map[int]bool{1: true})
	if err != nil {
		t.Fatalf("GetBlocksWindow: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("blocks = %#v, want 3（pos1 只有未收口版本，必须被忽略）", got)
	}
	if got[0].Text != "v2" || got[0].Revision != 2 {
		t.Fatalf("revision 未取最高：%#v", got[0])
	}
	if got[1].Text != "final" || got[1].Step != 3 {
		t.Fatalf("已收口版本未胜出：%#v", got[1])
	}
	if got[2].Pos != 3 || got[2].ToolCall == nil {
		t.Fatalf("blocks[2] = %#v", got[2])
	}
}

// 空 seqSet / 缺文件都返回空，不报错 —— 历史会话没有块流是常态。
func TestGetBlocksWindowEmptyInputs(t *testing.T) {
	root := rootfs.NewRootInfo("blocks-window-empty", "blocks-window-empty", t.TempDir())
	m := NewManager(root)
	created, err := m.Create(context.Background(), CreateInput{Type: TypeChat, Name: "Empty"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := m.GetBlocksWindow(context.Background(), created.Key, map[int]bool{1: true})
	if err != nil {
		t.Fatalf("缺文件不该报错：%v", err)
	}
	if len(got) != 0 {
		t.Fatalf("blocks = %#v, want empty", got)
	}
	got, err = m.GetBlocksWindow(context.Background(), created.Key, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("空 seqSet：got=%#v err=%v", got, err)
	}
}

// 窗口只含「有块但不在窗口里」的 seq 时不得泄漏窗口外的块。
func TestGetBlocksWindowExcludesSeqsOutsideWindow(t *testing.T) {
	root := rootfs.NewRootInfo("blocks-window-outside", "blocks-window-outside", t.TempDir())
	m := NewManager(root)
	created, err := m.Create(context.Background(), CreateInput{Type: TypeChat, Name: "Outside"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := m.AppendBlocks(context.Background(), created.Key, []Block{
		{Seq: 1, Pos: 0, Type: BlockTypeText, Step: 1, Text: "one"},
		{Seq: 2, Pos: 0, Type: BlockTypeText, Step: 1, Text: "two"},
		{Seq: 3, Pos: 0, Type: BlockTypeText, Step: 1, Text: "three"},
	}); err != nil {
		t.Fatalf("AppendBlocks: %v", err)
	}
	got, err := m.GetBlocksWindow(context.Background(), created.Key, map[int]bool{2: true})
	if err != nil {
		t.Fatalf("GetBlocksWindow: %v", err)
	}
	if len(got) != 1 || got[0].Text != "two" {
		t.Fatalf("blocks = %#v, want only seq 2", got)
	}
}

// 窗口读必须容忍坏行：块流是 append-only 的，一行写坏不能让整个会话的正文读不出来。
func TestGetBlocksWindowSkipsCorruptLines(t *testing.T) {
	root := rootfs.NewRootInfo("blocks-window-corrupt", "blocks-window-corrupt", t.TempDir())
	m := NewManager(root)
	created, err := m.Create(context.Background(), CreateInput{Type: TypeChat, Name: "Corrupt"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	path, err := m.blocksPath(created.Key)
	if err != nil {
		t.Fatalf("blocksPath: %v", err)
	}
	good, err := json.Marshal(Block{Seq: 1, Pos: 0, Type: BlockTypeText, Step: 1, Text: "good"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	payload := append([]byte("{not json at all}\n"), good...)
	payload = append(payload, '\n')
	payload = append(payload, []byte("{\"seq\":1,\"pos\":1,\"type\":\"text\"")...) // 截断的半行
	payload = append(payload, '\n')
	if err := m.root.WriteMetaFile(path, payload); err != nil {
		t.Fatalf("WriteMetaFile: %v", err)
	}
	got, err := m.GetBlocksWindow(context.Background(), created.Key, map[int]bool{1: true})
	if err != nil {
		t.Fatalf("GetBlocksWindow: %v", err)
	}
	if len(got) != 1 || got[0].Text != "good" {
		t.Fatalf("blocks = %#v, want only the good one", got)
	}
}

func filler(n int) string {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = byte('a' + i%26)
	}
	return string(buf)
}

// 窗口压缩只压工具卡详情，**绝不压正文** —— 块流是正文的唯一载体（D1），
// 在窗口里截断等于把本方案要修的 bug 换个地方复现。
func TestCompactBlockLightKeepsBodyAndDropsToolDetails(t *testing.T) {
	huge := filler(120000) // 实测 os 失控 reasoning 119714 字符
	blocks := []Block{
		{Seq: 1, Pos: 0, Type: BlockTypeReasoning, Step: 1, Text: huge},
		{Seq: 1, Pos: 1, Type: BlockTypeText, Step: 1, Text: "正文"},
		{Seq: 1, Pos: 2, Type: BlockTypeToolCall, Step: 1, ToolCall: &agenttypes.ToolCall{
			CallID: "c1", Kind: agenttypes.ToolKindEdit, Title: "edit", Content: []agenttypes.ToolCallContentItem{{Type: "diff", NewText: "big"}},
		}},
		{Seq: 1, Pos: 3, Type: BlockTypeText, Step: 1, Text: "after"},
	}
	got := CompactBlocksLight(blocks)
	if len(got) != 4 {
		t.Fatalf("blocks = %d, want 4（压缩不得丢块）", len(got))
	}
	if got[0].Text != huge {
		t.Fatalf("reasoning 正文被压缩了：len=%d want %d", len(got[0].Text), len(huge))
	}
	if got[1].Text != "正文" || got[3].Text != "after" {
		t.Fatalf("text 块被动过：%#v", got)
	}
	if got[2].ToolCall == nil || len(got[2].ToolCall.Content) != 0 {
		t.Fatalf("edit 的 content 未清空（窗口最大的一块）：%#v", got[2].ToolCall)
	}
	if got[2].ToolCall.Title != "edit" || got[2].ToolCall.Kind != agenttypes.ToolKindEdit {
		t.Fatalf("折叠卡片所需的最小字段被清了：%#v", got[2].ToolCall)
	}
}

// 未知块类型跳过而不是报错：agent 加新类型时旧服务不能炸。
func TestCompactBlockLightSkipsUnknownType(t *testing.T) {
	got := CompactBlocksLight([]Block{
		{Seq: 1, Pos: 0, Type: BlockTypeText, Step: 1, Text: "keep"},
		{Seq: 1, Pos: 1, Type: BlockType("brand_new_kind"), Step: 1, Text: "skip"},
	})
	if len(got) != 1 || got[0].Text != "keep" {
		t.Fatalf("blocks = %#v", got)
	}
}
