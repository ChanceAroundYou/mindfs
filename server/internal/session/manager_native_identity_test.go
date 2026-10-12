package session

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
	rootfs "mindfs/server/internal/fs"
)

// 方案 §14 C1：repoint / fork 之后 claude 的 uuid **全部改变**（repoint.go:35 明载），
// 所以 native id 必须清空 —— 否则旧 native id 与 fork 后的新转录对不上，判重失效。
// 这是本方案**直接引入**的新步骤：不持久化 native id 时它不存在，一旦持久化就必须清。
func TestClearNativeIdentityWipesExchangeAndBlockNativeIDs(t *testing.T) {
	root := rootfs.NewRootInfo("native-clear", "native-clear", t.TempDir())
	m := NewManager(root)
	created, err := m.Create(context.Background(), CreateInput{Type: TypeChat, Name: "Repoint"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	const turnID = "11111111-2222-3333-4444-555555555555"
	writeExchangeLog(t, m, created.Key, []Exchange{
		{
			Seq: 1, Role: "user", LegacyContent: "hi", MindFSID: turnID, TurnSeq: 1,
			AgentID: "old-turn-1",
		},
		{
			Seq: 2, Role: "agent", LegacyContent: "yo", MindFSID: turnID, TurnSeq: 1,
			AgentID: "old-turn-2",
		},
	})
	if err := m.AppendBlocks(context.Background(), created.Key, []Block{
		{Seq: 1, Pos: 0, Type: BlockTypeText, Text: "hi", Step: 1, MindFSID: turnID, AgentID: "old-native-1"},
		{Seq: 2, Pos: 0, Type: BlockTypeReasoning, Text: "why", Step: 1, MindFSID: turnID, AgentID: "old-native-2"},
	}); err != nil {
		t.Fatalf("AppendBlocks: %v", err)
	}
	// 预热缓存：清理后缓存里也不许留旧 native id。
	if _, err := m.Get(context.Background(), created.Key, 0); err != nil {
		t.Fatalf("warm Get: %v", err)
	}

	if err := m.ClearNativeIdentity(context.Background(), created.Key); err != nil {
		t.Fatalf("ClearNativeIdentity: %v", err)
	}

	got, err := m.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	for _, ex := range got.Exchanges {
		if ex.AgentID != "" {
			t.Fatalf("exchange seq=%d 仍带 native id：%#v", ex.Seq, ex)
		}
		// 权威身份与正文不受影响 —— 清理只碰 native id。
		if ex.MindFSID != turnID {
			t.Fatalf("exchange seq=%d mindfs_id 被误清：%q", ex.Seq, ex.MindFSID)
		}
	}
	blocks, err := m.ReadBlocks(context.Background(), created.Key)
	if err != nil {
		t.Fatalf("ReadBlocks: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("blocks = %#v, want 2（清理不得丢块）", blocks)
	}
	for _, block := range blocks {
		if block.AgentID != "" {
			t.Fatalf("block seq=%d pos=%d 仍带 native_id：%#v", block.Seq, block.Pos, block)
		}
		if block.MindFSID != turnID {
			t.Fatalf("block mindfs_id 被误清：%q", block.MindFSID)
		}
	}
}

// 磁盘上必须真的被清掉，而不是只清了内存缓存 —— 重启后旧 native id 复活就等于没清。
func TestClearNativeIdentityPersistsToDisk(t *testing.T) {
	root := rootfs.NewRootInfo("native-disk", "native-disk", t.TempDir())
	m := NewManager(root)
	created, err := m.Create(context.Background(), CreateInput{Type: TypeChat, Name: "Repoint"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	writeExchangeLog(t, m, created.Key, []Exchange{{
		Seq: 1, Role: "user", LegacyContent: "hi", MindFSID: "t", TurnSeq: 1,
		AgentID: "old-turn",
	}})
	if err := m.AppendBlocks(context.Background(), created.Key, []Block{
		{Seq: 1, Pos: 0, Type: BlockTypeText, Text: "hi", AgentID: "old-native"},
	}); err != nil {
		t.Fatalf("AppendBlocks: %v", err)
	}
	if err := m.ClearNativeIdentity(context.Background(), created.Key); err != nil {
		t.Fatalf("ClearNativeIdentity: %v", err)
	}

	// 换一个 manager（无缓存、无游标）直接读盘。
	fresh := NewManager(root)
	exPath, err := fresh.exchangePath(created.Key)
	if err != nil {
		t.Fatalf("exchangePath: %v", err)
	}
	payload, err := fresh.root.ReadMetaFile(exPath)
	if err != nil {
		t.Fatalf("ReadMetaFile: %v", err)
	}
	body := string(payload)
	if strings.Contains(body, "old-turn") || strings.Contains(body, "old-prompt") {
		t.Fatalf("主日志仍含旧 native id：%s", body)
	}
	if !strings.Contains(body, `"seq":1`) {
		t.Fatalf("主日志行被破坏：%s", body)
	}
	blocksPath, err := fresh.blocksPath(created.Key)
	if err != nil {
		t.Fatalf("blocksPath: %v", err)
	}
	blockPayload, err := fresh.root.ReadMetaFile(blocksPath)
	if err != nil {
		t.Fatalf("ReadMetaFile blocks: %v", err)
	}
	if strings.Contains(string(blockPayload), "old-native") {
		t.Fatalf("块流仍含旧 native_id：%s", blockPayload)
	}
}

// 没有 native id 的会话（本方案落地前的全部历史数据）必须**一个字节都不动**：
// 无谓重写会改 mtime、白白让缓存失效，还会在合上游 diff 时制造噪声。
func TestClearNativeIdentityLeavesCleanFilesUntouched(t *testing.T) {
	root := rootfs.NewRootInfo("native-clean", "native-clean", t.TempDir())
	m := NewManager(root)
	created, err := m.Create(context.Background(), CreateInput{Type: TypeChat, Name: "Clean"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	writeExchangeLog(t, m, created.Key, []Exchange{{Seq: 1, Role: "user", LegacyContent: "hi", MindFSID: "t", TurnSeq: 1}})
	if err := m.AppendBlocks(context.Background(), created.Key, []Block{
		{Seq: 1, Pos: 0, Type: BlockTypeText, Text: "hi", MindFSID: "t"},
	}); err != nil {
		t.Fatalf("AppendBlocks: %v", err)
	}
	exPath, err := m.exchangePath(created.Key)
	if err != nil {
		t.Fatalf("exchangePath: %v", err)
	}
	before, err := m.root.ReadMetaFile(exPath)
	if err != nil {
		t.Fatalf("ReadMetaFile: %v", err)
	}

	if err := m.ClearNativeIdentity(context.Background(), created.Key); err != nil {
		t.Fatalf("ClearNativeIdentity: %v", err)
	}
	after, err := m.root.ReadMetaFile(exPath)
	if err != nil {
		t.Fatalf("ReadMetaFile after: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("干净文件被重写了：\nbefore=%s\nafter=%s", before, after)
	}
}

// 未知字段（别的写入者 / 未来版本加的）不得因为清理而丢失。
func TestClearNativeIdentityPreservesUnknownFields(t *testing.T) {
	root := rootfs.NewRootInfo("native-unknown", "native-unknown", t.TempDir())
	m := NewManager(root)
	created, err := m.Create(context.Background(), CreateInput{Type: TypeChat, Name: "Unknown"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	exPath, err := m.exchangePath(created.Key)
	if err != nil {
		t.Fatalf("exchangePath: %v", err)
	}
	line := `{"seq":1,"role":"user","content":"hi","native_turn":"old","some_future_field":{"a":1}}`
	if err := m.root.WriteMetaFile(exPath, []byte(line+"\n")); err != nil {
		t.Fatalf("WriteMetaFile: %v", err)
	}
	if err := m.ClearNativeIdentity(context.Background(), created.Key); err != nil {
		t.Fatalf("ClearNativeIdentity: %v", err)
	}
	payload, err := m.root.ReadMetaFile(exPath)
	if err != nil {
		t.Fatalf("ReadMetaFile: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(payload))), &decoded); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, payload)
	}
	if _, exists := decoded["native_turn"]; exists {
		t.Fatalf("native_turn 未清除：%s", payload)
	}
	if _, exists := decoded["some_future_field"]; !exists {
		t.Fatalf("未知字段被丢弃：%s", payload)
	}
}

func TestBlockNativeIDRoundTripsThroughJSONL(t *testing.T) {
	root := rootfs.NewRootInfo("native-roundtrip", "native-roundtrip", t.TempDir())
	m := NewManager(root)
	created, err := m.Create(context.Background(), CreateInput{Type: TypeChat, Name: "RT"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	blocksPath, err := m.blocksPath(created.Key)
	if err != nil {
		t.Fatalf("blocksPath: %v", err)
	}
	if err := m.AppendBlocks(context.Background(), created.Key, []Block{{
		Seq: 1, Pos: 0, Type: BlockTypeToolCall, Step: 2, MindFSID: "t",
		AgentID: "agent-msg-7", ParentToolUseID: "toolu-1", TaskID: "task-2", SubagentType: "explore",
		ToolCall: &agenttypes.ToolCall{CallID: "c1", Title: "t"},
	}}); err != nil {
		t.Fatalf("AppendBlocks: %v", err)
	}
	payload, err := m.root.ReadMetaFile(blocksPath)
	if err != nil {
		t.Fatalf("ReadMetaFile: %v", err)
	}
	body := string(payload)
	for _, want := range []string{`"agent_id":"agent-msg-7"`, `"parent_tool_use_id":"toolu-1"`, `"task_id":"task-2"`, `"subagent_type":"explore"`, `"step":2`} {
		if !strings.Contains(body, want) {
			t.Fatalf("块流缺 %s：%s", want, body)
		}
	}
	blocks, err := m.ReadBlocks(context.Background(), created.Key)
	if err != nil {
		t.Fatalf("ReadBlocks: %v", err)
	}
	if len(blocks) != 1 || blocks[0].AgentID != "agent-msg-7" || blocks[0].TaskID != "task-2" {
		t.Fatalf("blocks = %#v", blocks)
	}
}
