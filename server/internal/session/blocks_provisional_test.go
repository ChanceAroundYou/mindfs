package session

import (
	"context"
	"strings"
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
	rootfs "mindfs/server/internal/fs"
)

func TestProvisionalTurnBlocksStayHiddenUntilSealed(t *testing.T) {
	ctx := context.Background()
	m := NewManager(rootfs.NewRootInfo("provisional-blocks", "provisional-blocks", t.TempDir()))
	created, err := m.Create(ctx, CreateInput{Type: TypeChat, Name: "Provisional"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	const seq = 4
	const turnID = "11111111-2222-3333-4444-555555555555"

	if err := m.AppendProvisionalText(ctx, created.Key, seq, turnID, 0, "我已收到，先查一下。"); err != nil {
		t.Fatalf("AppendProvisionalText: %v", err)
	}
	if err := m.AppendProvisionalToolCall(ctx, created.Key, seq, turnID, 1, agenttypes.ToolCall{
		CallID: "call-1", Title: "Bash", Status: "running", Kind: agenttypes.ToolKindExecute,
	}); err != nil {
		t.Fatalf("AppendProvisionalToolCall: %v", err)
	}
	if err := m.AppendProvisionalText(ctx, created.Key, seq, turnID, 2, "工具还没返回。"); err != nil {
		t.Fatalf("AppendProvisionalText 2: %v", err)
	}

	provisional, err := m.ReadProvisionalTurnBlocks(ctx, created.Key, seq, turnID)
	if err != nil {
		t.Fatalf("ReadProvisionalTurnBlocks: %v", err)
	}
	if len(provisional) != 3 {
		t.Fatalf("provisional blocks = %d, want 3", len(provisional))
	}

	window, err := m.GetBlocksWindow(ctx, created.Key, map[int]bool{seq: true})
	if err != nil {
		t.Fatalf("GetBlocksWindow before seal: %v", err)
	}
	if len(window) != 0 {
		t.Fatalf("unsealed blocks leaked into window: %#v", window)
	}

	if err := m.SealProvisionalTurnBlocks(ctx, created.Key, seq, turnID); err != nil {
		t.Fatalf("SealProvisionalTurnBlocks: %v", err)
	}
	window, err = m.GetBlocksWindow(ctx, created.Key, map[int]bool{seq: true})
	if err != nil {
		t.Fatalf("GetBlocksWindow after seal: %v", err)
	}
	if len(window) != 3 {
		t.Fatalf("sealed window blocks = %d, want 3", len(window))
	}
	if window[0].Text != "我已收到，先查一下。" || window[2].Text != "工具还没返回。" {
		t.Fatalf("sealed text order/content wrong: %#v", window)
	}
	if window[1].ToolCall == nil || window[1].ToolCall.Status != "interrupted" {
		t.Fatalf("tool call not marked interrupted: %#v", window[1].ToolCall)
	}
	if !strings.Contains(window[1].ToolCall.Title, "服务端重启，工具未返回") {
		t.Fatalf("interrupted tool call lacks visible reason: %q", window[1].ToolCall.Title)
	}

	// 重复封口按 revision 取最高版本，界面仍只看到一份。
	if err := m.SealProvisionalTurnBlocks(ctx, created.Key, seq, turnID); err != nil {
		t.Fatalf("second SealProvisionalTurnBlocks: %v", err)
	}
	window, err = m.GetBlocksWindow(ctx, created.Key, map[int]bool{seq: true})
	if err != nil {
		t.Fatalf("GetBlocksWindow after second seal: %v", err)
	}
	if len(window) != 3 {
		t.Fatalf("window blocks after second seal = %d, want 3", len(window))
	}
}

func TestSealProvisionalTurnBlocksMergesAdjacentTextChunks(t *testing.T) {
	ctx := context.Background()
	m := NewManager(rootfs.NewRootInfo("provisional-merge", "provisional-merge", t.TempDir()))
	created, err := m.Create(ctx, CreateInput{Type: TypeChat, Name: "Provisional merge"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	const seq = 2
	const turnID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

	// 一次回复在流里通常被切成多个 chunk；封口后应拼回一条正文，
	// 而不是在界面上变成多个紧挨着的独立气泡。
	if err := m.AppendProvisionalText(ctx, created.Key, seq, turnID, 0, "ANCHOR_"); err != nil {
		t.Fatalf("AppendProvisionalText 1: %v", err)
	}
	if err := m.AppendProvisionalText(ctx, created.Key, seq, turnID, 1, "2026"); err != nil {
		t.Fatalf("AppendProvisionalText 2: %v", err)
	}
	if err := m.AppendProvisionalToolCall(ctx, created.Key, seq, turnID, 2, agenttypes.ToolCall{
		CallID: "call-2", Title: "Bash", Status: "running", Kind: agenttypes.ToolKindExecute,
	}); err != nil {
		t.Fatalf("AppendProvisionalToolCall: %v", err)
	}
	if err := m.AppendProvisionalText(ctx, created.Key, seq, turnID, 3, "工具之后的正文"); err != nil {
		t.Fatalf("AppendProvisionalText 3: %v", err)
	}

	if err := m.SealProvisionalTurnBlocks(ctx, created.Key, seq, turnID); err != nil {
		t.Fatalf("SealProvisionalTurnBlocks: %v", err)
	}
	window, err := m.GetBlocksWindow(ctx, created.Key, map[int]bool{seq: true})
	if err != nil {
		t.Fatalf("GetBlocksWindow: %v", err)
	}
	if len(window) != 3 {
		t.Fatalf("sealed window blocks = %d, want 3: %#v", len(window), window)
	}
	if window[0].Text != "ANCHOR_2026" || window[0].Pos != 0 {
		t.Fatalf("adjacent text chunks not merged at first pos: %#v", window[0])
	}
	if window[1].ToolCall == nil || window[1].ToolCall.Status != "interrupted" {
		t.Fatalf("tool call not preserved between text blocks: %#v", window[1])
	}
	if window[2].Text != "工具之后的正文" {
		t.Fatalf("text after tool call changed: %#v", window[2])
	}
}
