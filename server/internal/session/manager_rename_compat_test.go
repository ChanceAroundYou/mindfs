package session

import (
	"context"
	"strings"
	"testing"
)

// 重命名 turn_id/native_* → mindfs_id/agent_id 之后的**存量数据兼容**。
//
// 为什么这条必须单独钉：老 JSONL 里写的是旧键名，Exchange/Block 换字段之后
// 若无 UnmarshalJSON 兼容层，133 个存量会话读出来身份全空 —— S2d-1 回填的
// 39517 个块会与轮次脱钩，用户历史正文当场消失。
// 这不是「未来可能踩」，是重命名当天就会踩。

// 老格式 exchange 行（S2d-2 之前的键名）必须还能读出身份。
func TestExchangeReadsLegacyTurnIDAndNativeKeys(t *testing.T) {
	m, key := newRenameCompatManager(t)
	exPath, err := m.exchangePath(key)
	if err != nil {
		t.Fatalf("exchangePath: %v", err)
	}
	// user 行带 turn_id + native_prompt，agent 行带 turn_id + native_turn。
	lines := []string{
		`{"seq":1,"role":"user","content":"hi","turn_id":"11111111-2222-3333-4444-555555555555","native_prompt":"prompt-aa"}`,
		`{"seq":2,"role":"agent","content":"yo","turn_id":"11111111-2222-3333-4444-555555555555","native_turn":"turn-bb"}`,
	}
	if err := m.root.WriteMetaFile(exPath, []byte(strings.Join(lines, "\n")+"\n")); err != nil {
		t.Fatalf("WriteMetaFile: %v", err)
	}
	got, err := m.Get(context.Background(), key, 0)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Exchanges) != 2 {
		t.Fatalf("读回 %d 行，want 2", len(got.Exchanges))
	}
	if got.Exchanges[0].MindFSID != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("老 turn_id 没读进 MindFSID：%q", got.Exchanges[0].MindFSID)
	}
	if got.Exchanges[0].AgentID != "prompt-aa" {
		t.Fatalf("老 native_prompt 没读进 AgentID：%q", got.Exchanges[0].AgentID)
	}
	if got.Exchanges[1].AgentID != "turn-bb" {
		t.Fatalf("老 native_turn 没读进 AgentID：%q", got.Exchanges[1].AgentID)
	}
}

// 老格式块行同样要能读回身份（块流才是正文载体）。
func TestBlockReadsLegacyTurnIDAndNativeKeys(t *testing.T) {
	m, key := newRenameCompatManager(t)
	blocksPath, err := m.blocksPath(key)
	if err != nil {
		t.Fatalf("blocksPath: %v", err)
	}
	if err := m.root.WriteMetaFile(blocksPath, []byte(
		`{"seq":1,"pos":0,"revision":0,"type":"text","text":"正文","step":1,"turn_id":"turn-1","native_id":"native-1"}`+"\n")); err != nil {
		t.Fatalf("WriteMetaFile: %v", err)
	}
	blocks, err := m.ReadBlocks(context.Background(), key)
	if err != nil {
		t.Fatalf("ReadBlocks: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("读回 %d 块，want 1", len(blocks))
	}
	if blocks[0].MindFSID != "turn-1" || blocks[0].AgentID != "native-1" {
		t.Fatalf("老键名没兼容：mindfs_id=%q agent_id=%q", blocks[0].MindFSID, blocks[0].AgentID)
	}
}

// 新键名优先于老键名：同一行两个都在时不许被旧的盖掉。
func TestNewKeysWinOverLegacyOnSameRow(t *testing.T) {
	m, key := newRenameCompatManager(t)
	exPath, err := m.exchangePath(key)
	if err != nil {
		t.Fatalf("exchangePath: %v", err)
	}
	if err := m.root.WriteMetaFile(exPath, []byte(
		`{"seq":1,"role":"user","content":"hi","mindfs_id":"new-id","turn_id":"old-id","agent_id":"new-agent","native_turn":"old-agent"}`+"\n")); err != nil {
		t.Fatalf("WriteMetaFile: %v", err)
	}
	got, err := m.Get(context.Background(), key, 0)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Exchanges[0].MindFSID != "new-id" || got.Exchanges[0].AgentID != "new-agent" {
		t.Fatalf("老键名盖住了新键名：mindfs_id=%q agent_id=%q",
			got.Exchanges[0].MindFSID, got.Exchanges[0].AgentID)
	}
}

func newRenameCompatManager(t *testing.T) (*Manager, string) {
	t.Helper()
	root := newCompatTestRoot(t)
	m := NewManager(root)
	created, err := m.Create(context.Background(), CreateInput{Type: TypeChat, Name: "compat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return m, created.Key
}
