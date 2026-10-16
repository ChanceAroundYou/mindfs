package session

import (
	"context"
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
	rootfs "mindfs/server/internal/fs"
)

func TestManagerIncrementalExchangeLoad(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)

	created, err := manager.Create(context.Background(), CreateInput{Type: TypeChat, Agent: "claude", Name: "Incremental"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	ctx := context.Background()
	add := func(role, content string) {
		if err := manager.AddExchangeForAgent(ctx, created, role, content, "claude", "", "", ""); err != nil {
			t.Fatalf("add exchange: %v", err)
		}
	}

	// 首次加载建立缓存 + 游标
	add("user", "q1")
	loaded, err := manager.Get(ctx, created.Key, 0)
	if err != nil || len(loaded.Exchanges) != 1 {
		t.Fatalf("first get exchanges = %d err=%v, want 1", len(loaded.Exchanges), err)
	}

	// 追加后增量读取：Get(seq=1) 应只返回新条目，且不重复缓存已有条目
	add("agent", "a1")
	delta, err := manager.Get(ctx, created.Key, 1)
	if err != nil {
		t.Fatalf("incremental get: %v", err)
	}
	if len(delta.Exchanges) != 1 || delta.Exchanges[0].LegacyContent != "a1" {
		t.Fatalf("incremental exchanges = %#v, want [a1]", delta.Exchanges)
	}

	// 文件未变时增量：Get(seq=2) 返回空
	empty, err := manager.Get(ctx, created.Key, 2)
	if err != nil {
		t.Fatalf("empty incremental get: %v", err)
	}
	if len(empty.Exchanges) != 0 {
		t.Fatalf("empty incremental exchanges = %#v, want []", empty.Exchanges)
	}

	// 缓存保持完整
	full, err := manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatalf("full get: %v", err)
	}
	if len(full.Exchanges) != 2 {
		t.Fatalf("full exchanges = %d, want 2", len(full.Exchanges))
	}
}

func TestManagerIncrementalAuxLoad(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)

	created, err := manager.Create(context.Background(), CreateInput{Type: TypeChat, Agent: "claude", Name: "Aux"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	ctx := context.Background()
	addAux := func(seq int, callID string) {
		if err := manager.AddExchangeAux(ctx, created.Key, ExchangeAux{
			Seq:      seq,
			ToolCall: &agenttypes.ToolCall{CallID: callID, Title: callID, Kind: agenttypes.ToolKindSearch},
		}); err != nil {
			t.Fatalf("add aux: %v", err)
		}
	}

	// 首次加载建立游标
	addAux(1, "call-1")
	aux, err := manager.GetExchangeAux(ctx, created.Key, 0)
	if err != nil || len(aux) != 1 {
		t.Fatalf("first aux = %#v err=%v, want 1 entry", aux, err)
	}

	// 文件未变增量：GetExchangeAux(seq=1) 返回空
	empty, err := manager.GetExchangeAux(ctx, created.Key, 1)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty aux delta = %#v err=%v, want empty", empty, err)
	}

	// 追加后增量读取
	addAux(2, "call-2")
	delta, err := manager.GetExchangeAux(ctx, created.Key, 1)
	if err != nil {
		t.Fatalf("aux delta: %v", err)
	}
	if len(delta) != 1 || delta[2][0].ToolCall.CallID != "call-2" {
		t.Fatalf("aux delta = %#v, want [call-2] at seq 2", delta)
	}

	// afterSeq=0 全量读取
	full, err := manager.GetExchangeAux(ctx, created.Key, 0)
	if err != nil {
		t.Fatalf("full aux: %v", err)
	}
	if len(full) != 2 {
		t.Fatalf("full aux = %d entries, want 2", len(full))
	}
}
