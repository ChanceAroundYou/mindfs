package session

import (
	"context"
	"testing"

	rootfs "mindfs/server/internal/fs"
	agenttypes "mindfs/server/internal/agent/types"
)

func userShellToolCall(shell string) *agenttypes.ToolCall {
	return &agenttypes.ToolCall{
		CallID:  "call-" + shell,
		Title:   "run",
		Kind:    agenttypes.ToolKindExecute,
		RawType: "commandExecution",
		Meta: map[string]any{
			"source": "userShell",
			"phase":  "final",
			"shell":  shell,
		},
	}
}

// 列表页给命令会话补 shell 标签必须走 aux 尾部有界读，不能全量读 aux
// （单会话 aux 实测最大 19MB，列表页对每个命令会话各读一次，且持 m.mu）。
func TestGetCommandShellReadsAuxTailOnly(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	m := NewManager(root)
	ctx := context.Background()
	created, err := m.Create(ctx, CreateInput{Type: TypeCommand, Name: "Cmd"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := m.AddExchangeAux(ctx, created.Key, ExchangeAux{Seq: 1, ToolCall: userShellToolCall("zsh")}); err != nil {
		t.Fatalf("add aux: %v", err)
	}
	// 尾部再堆一些无关条目，确认「取最新」语义。
	if err := m.AddExchangeAux(ctx, created.Key, ExchangeAux{
		Seq:      2,
		ToolCall: &agenttypes.ToolCall{CallID: "call-2", Kind: agenttypes.ToolKindSearch, Title: "grep"},
	}); err != nil {
		t.Fatalf("add aux 2: %v", err)
	}

	before := m.fullAuxReads
	shell, err := m.GetCommandShell(ctx, created.Key)
	if err != nil {
		t.Fatalf("GetCommandShell: %v", err)
	}
	if shell != "zsh" {
		t.Fatalf("shell = %q, want zsh", shell)
	}
	if m.fullAuxReads != before {
		t.Fatalf("GetCommandShell triggered %d full aux reads, want 0", m.fullAuxReads-before)
	}
}

// 没有 aux / 没有匹配项时返回空串，且不报错。
func TestGetCommandShellWithoutMatch(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	m := NewManager(root)
	ctx := context.Background()
	created, err := m.Create(ctx, CreateInput{Type: TypeCommand, Name: "CmdEmpty"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if shell, err := m.GetCommandShell(ctx, created.Key); err != nil || shell != "" {
		t.Fatalf("shell = %q err = %v, want empty nil", shell, err)
	}
	if err := m.AddExchangeAux(ctx, created.Key, ExchangeAux{
		Seq:      1,
		ToolCall: &agenttypes.ToolCall{CallID: "call-1", Kind: agenttypes.ToolKindSearch, Title: "grep"},
	}); err != nil {
		t.Fatalf("add aux: %v", err)
	}
	if shell, err := m.GetCommandShell(ctx, created.Key); err != nil || shell != "" {
		t.Fatalf("shell = %q err = %v, want empty nil", shell, err)
	}
}

// 窗口 aux 读同样不得全量解析 aux。
func TestGetExchangeAuxWindowReadsAuxTailOnly(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	m := NewManager(root)
	ctx := context.Background()
	created, err := m.Create(ctx, CreateInput{Type: TypeChat, Name: "AuxWin"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for seq := 1; seq <= 3; seq++ {
		if err := m.AddExchangeAux(ctx, created.Key, ExchangeAux{
			Seq:      seq,
			ToolCall: &agenttypes.ToolCall{CallID: "call", Kind: agenttypes.ToolKindSearch, Title: "grep"},
		}); err != nil {
			t.Fatalf("add aux %d: %v", seq, err)
		}
	}

	before := m.fullAuxReads
	items, err := m.GetExchangeAuxWindow(ctx, created.Key, map[int]bool{3: true})
	if err != nil {
		t.Fatalf("GetExchangeAuxWindow: %v", err)
	}
	if len(items[3]) != 1 {
		t.Fatalf("window aux = %#v, want only seq 3", items)
	}
	if m.fullAuxReads != before {
		t.Fatalf("window aux read triggered %d full aux reads, want 0", m.fullAuxReads-before)
	}
}
