package session

import (
	"context"
	"testing"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/fs"
)

// 搜索必须读块流，不能读主日志的 `LegacyContent`。
//
// S2d-2 起新写入的行 `content` 恒空，正文只在块流里 —— 搜索继续扫主日志的话，
// 后果是**搜不到任何新内容**：用户输入一个明确出现在正文里的词，列表页显示无结果。
// 这不是「功能退化」，是静默失效，而且没有任何报错。

// addRow 追加一行并返回它的 seq（AddExchangeForAgentAt 只返回 error）。
func addRow(t *testing.T, m *Manager, s *Session, role, content, agent string, ts time.Time) int {
	t.Helper()
	if err := m.AddExchangeForAgentAt(context.Background(), s, role, content, agent, "", "", "", ts); err != nil {
		t.Fatal(err)
	}
	return MaxExchangeSeq(s.Exchanges)
}

func newSearchSession(t *testing.T, name string) (*Manager, *Session) {
	t.Helper()
	manager := NewManager(fs.NewRootInfo("root", "Root", t.TempDir()))
	created, err := manager.Create(context.Background(), CreateInput{Type: TypeChat, Agent: "dsh", Name: name})
	if err != nil {
		t.Fatal(err)
	}
	return manager, created
}

func mustAppendBlocks(t *testing.T, m *Manager, key string, blocks []Block) {
	t.Helper()
	if err := m.AppendBlocks(context.Background(), key, blocks); err != nil {
		t.Fatal(err)
	}
}

func TestSearchFindsTextThatOnlyLivesInBlocks(t *testing.T) {
	manager, s := newSearchSession(t, "Search")
	ts := time.Now().UTC()
	addRow(t, manager, s, "user", "", "dsh", ts)
	agentSeq := addRow(t, manager, s, "agent", "", "dsh", ts)
	mustAppendBlocks(t, manager, s.Key, BuildTurnBlocks(1, 1, "", "请帮我排查超时问题", nil))
	mustAppendBlocks(t, manager, s.Key, BuildTurnBlocks(agentSeq, 1, "", "我先看看超时设置", nil))

	hit, ok, err := manager.searchSessionContent(s, "超时")
	if err != nil || !ok {
		t.Fatalf("搜不到只存在于块流里的正文 ok=%v err=%v", ok, err)
	}
	if hit.Snippet == "" {
		t.Fatalf("命中了但没有摘要: %#v", hit)
	}
}

// 旧数据（正文只在主日志、块流为空）仍然搜得到 —— 那是回填之前的状态，必须能搜。
func TestSearchStillFindsLegacyMainLogBody(t *testing.T) {
	manager, s := newSearchSession(t, "Legacy")
	ts := time.Now().UTC()
	addRow(t, manager, s, "user", "", "dsh", ts)
	addRow(t, manager, s, "agent", "老正文里的关键词", "dsh", ts)

	hit, ok, err := manager.searchSessionContent(s, "关键词")
	if err != nil || !ok {
		t.Fatalf("搜不到老数据正文 ok=%v err=%v", ok, err)
	}
	if hit.Snippet == "" {
		t.Fatalf("命中了但没有摘要: %#v", hit)
	}
}

// 搜到块里的正文时，命中的 seq 必须指向那一轮（用于跳转定位）。
func TestSearchHitPointsAtTheRightTurn(t *testing.T) {
	manager, s := newSearchSession(t, "Seq")
	ts := time.Now().UTC()
	addRow(t, manager, s, "user", "", "dsh", ts)
	addRow(t, manager, s, "agent", "", "dsh", ts)
	addRow(t, manager, s, "user", "", "dsh", ts)
	addRow(t, manager, s, "agent", "", "dsh", ts)
	wantSeq := addRow(t, manager, s, "agent", "", "dsh", ts)
	mustAppendBlocks(t, manager, s.Key, BuildTurnBlocks(wantSeq, 1, "", "唯一词在这儿", nil))

	hit, ok, err := manager.searchSessionContent(s, "唯一词")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if hit.Seq != wantSeq {
		t.Fatalf("hit.Seq = %d, want %d", hit.Seq, wantSeq)
	}
	if hit.MatchType != "reply" {
		t.Fatalf("MatchType = %q, want reply", hit.MatchType)
	}
}

// user 行的命中优先于 reply（沿用既有打分），且 user 正文同样只在块流里。
func TestSearchPrefersUserTurnHitFromBlocks(t *testing.T) {
	manager, s := newSearchSession(t, "User")
	ts := time.Now().UTC()
	userSeq := addRow(t, manager, s, "user", "", "dsh", ts)
	agentSeq := addRow(t, manager, s, "agent", "", "dsh", ts)
	mustAppendBlocks(t, manager, s.Key, BuildTurnBlocks(agentSeq, 1, "", "共词出现在回复里", nil))
	mustAppendBlocks(t, manager, s.Key, BuildTurnBlocks(userSeq, 1, "", "共词出现在提问里", nil))

	hit, ok, err := manager.searchSessionContent(s, "共词")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if hit.MatchType != "user" {
		t.Fatalf("MatchType = %q, want user（user 命中优先）", hit.MatchType)
	}
}

// 工具卡的 JSON 里有大量与正文无关的字段（title/callId/status…）。
// 不能因此把「工具标题」当成正文命中 —— 那会让搜索结果与用户看到的正文对不上。
func TestSearchIgnoresBlockMetadataNoise(t *testing.T) {
	manager, s := newSearchSession(t, "Noise")
	seq := addRow(t, manager, s, "agent", "", "dsh", time.Now().UTC())
	mustAppendBlocks(t, manager, s.Key, BuildTurnBlocks(seq, 1, "", "正文里没有那个词", []ExchangeAux{
		{Seq: seq, Line: 1, ToolCall: &agenttypes.ToolCall{
			CallID: "c1", Title: "搜索关键词工具", Status: "completed", Kind: agenttypes.ToolKindExecute,
		}},
	}))
	if _, ok, err := manager.searchSessionContent(s, "搜索关键词工具"); err != nil || ok {
		t.Fatalf("工具卡的标题被当成了正文命中 ok=%v err=%v", ok, err)
	}
}

// reasoning 也要能搜到：它现在是一等块（不再藏在 aux 的 thought 里），
// 而「只思考不出话」的轮次全部由它承载 —— 搜不到就等于那 15% 的轮次是空的。
func TestSearchFindsReasoningBlockText(t *testing.T) {
	manager, s := newSearchSession(t, "Reason")
	seq := addRow(t, manager, s, "agent", "", "dsh", time.Now().UTC())
	mustAppendBlocks(t, manager, s.Key, BuildTurnBlocks(seq, 1, "", "", []ExchangeAux{
		{Seq: seq, Line: 1, Thought: "先确认超时是不是网关侧加的"},
	}))
	hit, ok, err := manager.searchSessionContent(s, "网关")
	if err != nil || !ok {
		t.Fatalf("搜不到 reasoning 里的词 ok=%v err=%v", ok, err)
	}
	if hit.Seq != seq {
		t.Fatalf("hit.Seq = %d, want %d", hit.Seq, seq)
	}
}
