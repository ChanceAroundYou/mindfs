package session

import (
	"context"
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
