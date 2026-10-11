package session

import (
	"context"
	"strings"
	"testing"

	rootfs "mindfs/server/internal/fs"
)

// 主日志退化为**轮次元数据**（方案 §3.1）：正文搬去块流，这里只留身份与展示字段。
// turn_id 是 MindFS 权威身份（落盘即定、永不改），turn_seq 是人读用的排序键，
// native_* 是 agent 侧回查键（可空、可后补、fork 后可变更），partial 标记未收尾轮次。
func TestExchangeIdentityFieldsRoundTripThroughJSONL(t *testing.T) {
	root := rootfs.NewRootInfo("identity", "identity", t.TempDir())
	m := NewManager(root)
	created, err := m.Create(context.Background(), CreateInput{Type: TypeChat, Name: "Identity"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	writeExchangeLog(t, m, created.Key, []Exchange{{
		Seq:           2,
		Role:          "agent",
		LegacyContent: "old body",
		TurnID:        "11111111-2222-3333-4444-555555555555",
		TurnSeq:       1,
		NativeTurn:    "agent-turn-9",
		NativePrompt:  "agent-prompt-9",
		Partial:       true,
	}})

	path, err := m.exchangePath(created.Key)
	if err != nil {
		t.Fatalf("exchangePath: %v", err)
	}
	payload, err := m.root.ReadMetaFile(path)
	if err != nil {
		t.Fatalf("ReadMetaFile: %v", err)
	}
	body := string(payload)
	for _, want := range []string{
		`"turn_id":"11111111-2222-3333-4444-555555555555"`,
		`"turn_seq":1`,
		`"native_turn":"agent-turn-9"`,
		`"native_prompt":"agent-prompt-9"`,
		`"partial":true`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("exchange log missing %s\n%s", want, body)
		}
	}

	// 读侧必须能解析回来（主日志仍然是一轮一行，正文已搬去块流）。
	got, err := m.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Exchanges) != 1 {
		t.Fatalf("exchanges = %d, want 1", len(got.Exchanges))
	}
	ex := got.Exchanges[0]
	if ex.TurnID != "11111111-2222-3333-4444-555555555555" || ex.TurnSeq != 1 ||
		ex.NativeTurn != "agent-turn-9" || ex.NativePrompt != "agent-prompt-9" || !ex.Partial {
		t.Fatalf("identity fields not parsed back: %#v", ex)
	}
}

// 没有身份的行（历史数据 / 手写行）必须读成零值，不能因为 omitempty 崩掉。
func TestExchangeIdentityFieldsDefaultToZero(t *testing.T) {
	root := rootfs.NewRootInfo("identity-zero", "identity-zero", t.TempDir())
	m := NewManager(root)
	created, err := m.Create(context.Background(), CreateInput{Type: TypeChat, Name: "Legacy"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	writeExchangeLog(t, m, created.Key, []Exchange{{Seq: 1, Role: "user", LegacyContent: "hi"}})

	got, err := m.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	ex := got.Exchanges[0]
	if ex.TurnID != "" || ex.TurnSeq != 0 || ex.NativeTurn != "" || ex.NativePrompt != "" || ex.Partial {
		t.Fatalf("legacy row should read as zero values: %#v", ex)
	}
}

// 块流与主日志共用 turn_id：同一轮的元数据行与块必须带**同一个** turn_id，
// 否则读侧无法把块挂回轮次（方案 §3.3「turn_id 落盘即定」）。
func TestBlockAndExchangeShareTurnID(t *testing.T) {
	root := rootfs.NewRootInfo("turn-shared", "turn-shared", t.TempDir())
	m := NewManager(root)
	created, err := m.Create(context.Background(), CreateInput{Type: TypeChat, Name: "Shared"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	const turnID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	writeExchangeLog(t, m, created.Key, []Exchange{{
		Seq: 3, Role: "agent", LegacyContent: "body", TurnID: turnID, TurnSeq: 2, Partial: true,
	}})
	if err := m.AppendBlocks(context.Background(), created.Key, []Block{{
		Seq: 3, Pos: 0, Type: BlockTypeReasoning, Text: "why", Step: 3, TurnID: turnID,
	}}); err != nil {
		t.Fatalf("AppendBlocks: %v", err)
	}

	got, err := m.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Exchanges) != 1 || got.Exchanges[0].TurnID != turnID {
		t.Fatalf("exchange turn_id = %#v, want %s", got.Exchanges, turnID)
	}
	blocks, err := m.ReadBlocks(context.Background(), created.Key)
	if err != nil {
		t.Fatalf("ReadBlocks: %v", err)
	}
	if len(blocks) != 1 || blocks[0].TurnID != turnID {
		t.Fatalf("block turn_id = %#v, want %s", blocks, turnID)
	}
}
