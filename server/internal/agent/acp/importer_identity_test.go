package acp

import (
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
	"mindfs/server/internal/agent/types"
)

// S2c：ACP 导入器身份接线。三条都是**行为**断言 —— 跑真实的 importCollector。
//
// 根因现场（实测 2026-10-07，本机会话 `1791339665-fb5134041392`）：
// Full 同步把 9 对已存在的 dsh 轮次又写了一遍，另有 3 对 import+import（两次导入相隔
// 20s）。原因不是「导入不干净」，而是 **ACP 每一次同步都是整段重放**（协议只有
// `LoadSession`，没有字节游标、没有增量锚点），而重放出来的行：
//   · 时间戳是 `time.Now()` 伪造的 → `AfterTimestamp` / `TimestampFloor` 挡不住；
//   · 内容被导入器合并过 → 与实时路径逐字不同 → 「内容 + ±5s」也判不出来。
// 于是两个写入者之间唯一的共同键只能是 **messageId 身份**（方案 D3 / §6）。

func textBlock(text string) acpsdk.ContentBlock {
	return acpsdk.TextBlock(text)
}

func msgID(id string) *string { return &id }

func userChunk(text, id string) SessionUpdate {
	chunk := acpsdk.SessionUpdateUserMessageChunk{
		Content:       textBlock(text),
		SessionUpdate: "user_message_chunk",
	}
	if id != "" {
		chunk.MessageId = msgID(id)
	}
	return SessionUpdate{SessionID: "s1", Raw: acpsdk.SessionUpdate{UserMessageChunk: &chunk}}
}

func agentChunk(text, id string) SessionUpdate {
	chunk := acpsdk.SessionUpdateAgentMessageChunk{
		Content:       textBlock(text),
		SessionUpdate: "agent_message_chunk",
	}
	if id != "" {
		chunk.MessageId = msgID(id)
	}
	return SessionUpdate{SessionID: "s1", Raw: acpsdk.SessionUpdate{AgentMessageChunk: &chunk}}
}

const (
	turnA = "550e8400-e29b-41d4-a716-446655440000"
	turnB = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
	turnC = "7c9e6679-7425-40de-944b-e07fc1f90ae7"
)

// ① messageId 必须带成 NativeID —— 这是判重唯一的共同键。
func TestACPImportCollectorKeepsMessageIDAsNativeID(t *testing.T) {
	c := &importCollector{}
	c.handle(userChunk("请继续", turnA))
	c.handle(agentChunk("好", turnB))
	got := c.snapshot()
	if len(got) != 2 {
		t.Fatalf("exchanges = %d, want 2", len(got))
	}
	if got[0].NativeID != turnA {
		t.Fatalf("user NativeID = %q, want %q", got[0].NativeID, turnA)
	}
	if got[1].NativeID != turnB {
		t.Fatalf("agent NativeID = %q, want %q", got[1].NativeID, turnB)
	}
}

// 同一轮的多次重放必须产出**同一个** NativeID —— 否则判重每次都判不出来。
// 这是幂等性的全部：重放两次 ≠ 写两次。
func TestACPImportReplayProducesStableNativeIDs(t *testing.T) {
	replay := func() []types.ImportedExchange {
		c := &importCollector{}
		c.handle(userChunk("请继续", turnA))
		c.handle(agentChunk("好", turnB))
		return c.snapshot()
	}
	first, second := replay(), replay()
	for i := range first {
		if first[i].NativeID != second[i].NativeID || first[i].Content != second[i].Content {
			t.Fatalf("replay %d 不稳定: %+v vs %+v", i, first[i], second[i])
		}
	}
}

// messageId 变化 = 新消息开始（ACP 规范原文），不能合并成一行。
// 合并不报错，但会让一条行承载多条消息，行级身份只能取其中一个，
// 于是实时路径（取最后一条）与导入路径对不上 → 又变成猜。
func TestACPImportStartsNewRowWhenMessageIDChanges(t *testing.T) {
	c := &importCollector{}
	c.handle(userChunk("第一段", turnA))
	c.handle(agentChunk("回答第一段", turnB))
	c.handle(agentChunk("回答第二段", turnC))
	got := c.snapshot()
	if len(got) != 3 {
		t.Fatalf("exchanges = %d, want 3（messageId 变了必须开新行）", len(got))
	}
	if got[1].Content != "回答第一段" || got[2].Content != "回答第二段" {
		t.Fatalf("两段被粘成了一行: %+v", got)
	}
}

// 没有 messageId 的相邻同角色文本仍要合并：ACP 标该字段 UNSTABLE，
// 未必每个 agent 都发。一条没有身份的行，合并总比碎成多行好（碎行会让
// 库里多出许多「同一句话」的行）。
func TestACPImportStillMergesAdjacentChunksWithoutMessageID(t *testing.T) {
	c := &importCollector{}
	c.handle(agentChunk("前半", ""))
	c.handle(agentChunk("后半", ""))
	got := c.snapshot()
	if len(got) != 1 || got[0].Content != "前半后半" {
		t.Fatalf("无 messageId 时相邻同角色应合并: %+v", got)
	}
}

// 非 UUID 形态的 messageId 一律当作**没有身份**（空串），不许当身份用。
// MindFS 递出去的是自己 mint 的 UUIDv4，别的形态对不上 —— 拿它当身份会让
// 判重比对一个永远匹配不上的字符串，于是**连内容兜底都被跳过**（D10：永不猜）。
func TestACPImportRejectsNonUUIDMessageID(t *testing.T) {
	c := &importCollector{}
	c.handle(agentChunk("回答", "msg-123"))
	if got := c.snapshot(); len(got) != 1 || got[0].NativeID != "" {
		t.Fatalf("非 UUID 必须被拒绝为无身份: %+v", got)
	}
}

// ② 时间戳是合成的，必须标记出来。
//
// ACP 的 `SessionUpdate` **不带任何时间戳**（SDK 里 SessionNotification 只有
// sessionId + update）。所以导入行的 Timestamp 只能是「读到它的那一刻」，而它
// 被下游当成真时间去比较过 —— 重放时永远「比库里新」，时间地板形同虚设。
// 标出来，至少没人会拿它当权威时间。
func TestACPImportMarksSynthesizedTimestamp(t *testing.T) {
	c := &importCollector{}
	before := time.Now().UTC().Add(-time.Second)
	c.handle(agentChunk("回答", turnB))
	got := c.snapshot()
	if len(got) != 1 {
		t.Fatalf("exchanges = %d, want 1", len(got))
	}
	if got[0].SynthesizedAt.IsZero() {
		t.Fatal("ACP 时间戳是合成的，必须打 SynthesizedAt 标记")
	}
	if !got[0].Timestamp.Equal(got[0].SynthesizedAt) {
		t.Fatalf("合成标记应与 Timestamp 同一时刻: ts=%v synth=%v", got[0].Timestamp, got[0].SynthesizedAt)
	}
	if got[0].Timestamp.Before(before) {
		t.Fatalf("Timestamp = %v，早于采集开始时间 %v", got[0].Timestamp, before)
	}
}

// ③ 合并不得改写时间戳：原先每收一块就把上一行的时间刷成 now，
// 于是同一行的时间会随「这一轮攒了多久」漂移，连「这一轮大概什么时候发生」
// 都读不出来。
func TestACPImportDoesNotRewriteTimestampWhileMerging(t *testing.T) {
	c := &importCollector{}
	c.handle(agentChunk("前半", ""))
	first := c.snapshot()[0].Timestamp
	time.Sleep(2 * time.Millisecond)
	c.handle(agentChunk("后半", ""))
	if got := c.snapshot()[0].Timestamp; !got.Equal(first) {
		t.Fatalf("合并改写了时间戳: %v → %v", first, got)
	}
}

// UsesPromptMessageID 恒真 —— MindFS 递出去的 messageId 只有这一层能接住。
func TestACPImporterReportsPromptMessageIDSupport(t *testing.T) {
	i := NewImporter(ImporterOptions{AgentName: "dsh", Command: "dsh"})
	if !i.UsesPromptMessageID() {
		t.Fatal("ACP 必须自称支持 prompt messageId")
	}
	// 没有真时间戳，所以时间地板对它无效 —— 调用方据此不设地板。
	if i.HasTranscriptTimestamps() {
		t.Fatal("ACP 的 SessionUpdate 不带时间戳，不能自称有")
	}
}

// reasoning（`AgentThoughtChunk`）必须导入。
//
// 原先这里只有 UserMessageChunk / AgentMessageChunk 两个分支，thought 被整个丢掉。
// 丢它的后果与线上那个 bug 同源：一轮里「只思考、不出文本」（`os` 下 15% 的轮次），
// 导入后正文是空的 —— 会话看起来「回复完就消失」。
func TestACPImportKeepsThoughtChunks(t *testing.T) {
	c := &importCollector{}
	thought := acpsdk.SessionUpdateAgentThoughtChunk{
		Content:       textBlock("先想想"),
		SessionUpdate: "agent_thought_chunk",
		MessageId:     msgID(turnB),
	}
	c.handle(SessionUpdate{SessionID: "s1", Raw: acpsdk.SessionUpdate{AgentThoughtChunk: &thought}})
	got := c.snapshot()
	if len(got) != 1 {
		t.Fatalf("exchanges = %d, want 1（纯 reasoning 轮次也必须留一行）", len(got))
	}
	if got[0].Content != "" {
		t.Fatalf("纯 reasoning 轮次没有文本，Content 应为空: %q", got[0].Content)
	}
	if len(got[0].Aux) != 1 || got[0].Aux[0].Thought != "先想想" {
		t.Fatalf("reasoning 应落成 aux.Thought（与实时路径同一形状）: %+v", got[0].Aux)
	}
}

// 「先思考 → 再出文本」且 messageId 相同：必须还是**一行**。
// 分成两行会让一轮变两轮 —— 时间线上凭空多出一轮「只有思考」的助手回复。
func TestACPImportMergesThoughtIntoSameMessage(t *testing.T) {
	c := &importCollector{}
	thought := acpsdk.SessionUpdateAgentThoughtChunk{
		Content:       textBlock("先想想"),
		SessionUpdate: "agent_thought_chunk",
		MessageId:     msgID(turnB),
	}
	c.handle(SessionUpdate{SessionID: "s1", Raw: acpsdk.SessionUpdate{AgentThoughtChunk: &thought}})
	c.handle(agentChunk("结论如下", turnB))
	got := c.snapshot()
	if len(got) != 1 {
		t.Fatalf("exchanges = %d, want 1（同一 messageId 的 thought 与文本属于同一轮）", len(got))
	}
	if got[0].Content != "结论如下" || len(got[0].Aux) != 1 {
		t.Fatalf("同轮应合并为一行且保留思考: %+v", got[0])
	}
}
