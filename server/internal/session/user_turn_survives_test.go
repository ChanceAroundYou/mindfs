package session

import (
	"context"
	"testing"
	"time"

	rootfs "mindfs/server/internal/fs"
)

// S2d-2 删掉 Exchange.Content 之后，主日志一行块流外不写正文 —— user 行也不例外。
//
// 旧的跳过 guard（`content == "" && role 不是 agent/assistant` 就返回）在删 Content 之前
// 是无害的：user 行总带着用户输入的 content。之后它会把**每一行 user 都静默丢掉** ——
// 轮次元数据（mindfs_id / native_prompt）一起没了，前端按 seq 合并时用户那句话直接不显示，
// 搜索的 user 命中优先也因找不到对应行而失效（role 退回默认 "agent"）。
//
// 一行的存在意义在**轮次身份**，不在正文：正文现在归块流。
func TestUserTurnExchangeSurvivesWithEmptyLegacyContent(t *testing.T) {
	root := rootfs.NewRootInfo("test", "test", t.TempDir())
	m := NewManager(root)
	created, err := m.Create(context.Background(), CreateInput{Type: TypeChat, Agent: "dsh", Name: "UserRow"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	before := maxExchangeSeq(created.Exchanges)
	if err := m.AddExchangeForAgentAt(context.Background(), created, "user", "", "dsh", "", "", "", time.Now().UTC()); err != nil {
		t.Fatalf("addUser: %v", err)
	}
	after := maxExchangeSeq(created.Exchanges)
	if after <= before {
		t.Fatalf("user 行没落盘：seq 停在 %d（跳过 guard 仍在拦空正文 user 行）", after)
	}

	got, err := m.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Exchanges) != 1 || got.Exchanges[0].Role != "user" || got.Exchanges[0].Seq != after {
		t.Fatalf("user 行丢失或错位: %#v", got.Exchanges)
	}
}

// agent 行原来就被 guard 放过（`role != agent/assistant` 才跳过），这条钉住它不变 ——
// 免得将来「顺手统一 guard」时把 agent 行也拦了。
func TestAgentExchangeEmptyAlsoSurvives(t *testing.T) {
	root := rootfs.NewRootInfo("test", "test", t.TempDir())
	m := NewManager(root)
	created, err := m.Create(context.Background(), CreateInput{Type: TypeChat, Agent: "dsh", Name: "AgentRow"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := m.AddExchangeForAgentAt(context.Background(), created, "agent", "", "dsh", "", "", "", time.Now().UTC()); err != nil {
		t.Fatalf("addAgent: %v", err)
	}
	after := maxExchangeSeq(created.Exchanges)
	if after == 0 {
		t.Fatal("agent 行没落盘")
	}
}
