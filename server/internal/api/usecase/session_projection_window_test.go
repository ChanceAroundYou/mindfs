package usecase

import (
	"testing"
	"time"

	"mindfs/server/internal/session"
)

// S3 的实测前提：**投影规则 A（按归一化内容配对）没有第二轮判据**，于是「用户两天前
// 问过同一个问题、得到另一个回答」会被判定成「实时/导入双写重复」而整行折叠掉 ——
// 用户在自己会话里**看不见自己说过的话**，且文件里那行数据是好的（要清理得先看过 audit 报告）。
//
// 这是 2026-10-11 实测抓到的（不是推演）：本机会话 1791685987-a50945f94325.jsonl：
//
//	seq=4  "继续"     2026-10-10 18:18:03（import）→ 被折叠到
//	seq=22 "继续"     2026-10-11 05:28:19（live）  ⇒ 相隔 11 小时，且两轮的助手回复不同
//
// 用户真的在反复说「继续」（同一天 18:18/18:28/18:40/18:43 四次），轮次是真的，
// 折叠是错的。而这两条的 proven 是 false —— 说明命中的是规则 A（内容 + 来源启发式），
// 不是规则 B（同时间戳铁证）。
//
// 为什么不用时间窗（试过，更糟）：真双写的两侧时间差本来就很大 ——
// 1791339665 里 live 06:30 与 import 09:15 相隔 2.7 小时，因为导入是把整段历史
// 重读一遍、时间戳落在导入时刻。任何容得下 2.7 小时的窗都容得下 11 小时的误折；
// 挡得住 11 小时的窗都会把真双写放出来变成用户可见的重复行。**时间没有分辨力。**
//
// 真正的判据是**一轮一问一答**：一个双写的单位是一轮。问相同、答不同 ⇒ 两个不同的问题。
// 实测两组数据上这道判据完全分开：6 对真双写的后续 agent 内容全同，
// 2 对误折的后续内容不同或不存在。
func TestProjectExchangesRuleANeedsPerTurnDiscriminator(t *testing.T) {
	day1 := time.Date(2026, 10, 10, 18, 18, 3, 0, time.UTC)
	day2 := day1.Add(11 * time.Hour)
	rows := []session.Exchange{
		// 第一天 18:18 问「继续」，助手先做了一件事（导入侧）
		{Seq: 4, Role: "user", LegacyContent: "继续", Timestamp: day1, Source: session.ExchangeSourceImport},
		{Seq: 5, Role: "agent", LegacyContent: "我先看一下测试", Timestamp: day1.Add(time.Second), Source: session.ExchangeSourceImport},
		// 第二天 05:28 又问「继续」，助手做的是另一件事（实时侧）
		{Seq: 22, Role: "user", LegacyContent: "继续", Timestamp: day2, Source: session.ExchangeSourceLive},
		{Seq: 23, Role: "agent", LegacyContent: "现在开始改 pending 灯", Timestamp: day2.Add(time.Second), Source: session.ExchangeSourceLive},
	}

	kept, hidden, proven := ProjectExchanges(rows)
	if len(proven) != 0 {
		t.Fatalf("这个用例不该命中规则 B（时间戳铁证）：proven=%v", proven)
	}
	if len(hidden) != 0 {
		seqs := make([]int, 0, len(hidden))
		for seq := range hidden {
			seqs = append(seqs, seq)
		}
		t.Fatalf("相隔 11 小时、回复也不同的两句「继续」被判成双写重复，%d 行被折叠：%v\n"+
			"用户两天前说过同一句话是正常对话，不是双写重复", len(hidden), seqs)
	}
	if len(kept) != len(rows) {
		t.Fatalf("保留 %d 行，原始 %d 行", len(kept), len(rows))
	}
}

// 规则 A 的**正当用途必须继续有效**：实时与导入两个写入者各写**一整轮**
// （问 + 答两侧都同内容）—— 这是真双写，要折叠。第二判据一加，不能把这些也挡掉。
func TestProjectExchangesRuleAStillFoldsGenuineDoubleWrite(t *testing.T) {
	base := time.Date(2026, 10, 10, 18, 18, 3, 0, time.UTC)
	rows := []session.Exchange{
		// 导入侧一整轮（问 + 答）
		{Seq: 1, Role: "user", LegacyContent: "帮我看看这个 bug", Timestamp: base, Source: session.ExchangeSourceImport},
		{Seq: 2, Role: "agent", LegacyContent: "找到了，在第 3 行", Timestamp: base.Add(time.Second), Source: session.ExchangeSourceImport},
		// 实时侧把同一轮又写一遍（导入重读整段历史后补写的形状）
		{Seq: 3, Role: "user", LegacyContent: "帮我看看这个 bug", Timestamp: base.Add(3 * time.Hour), Source: session.ExchangeSourceLive},
		{Seq: 4, Role: "agent", LegacyContent: "找到了，在第 3 行", Timestamp: base.Add(3*time.Hour + time.Second), Source: session.ExchangeSourceLive},
	}
	kept, hidden, _ := ProjectExchanges(rows)
	// 问和答**都**折叠：两侧都是一整轮的重复，只折问会让答变成孤行。
	if len(hidden) != 2 {
		t.Fatalf("真双写应该折叠 2 行（导入侧的问+答），实际 hidden=%v", hidden)
	}
	if len(kept) != 2 {
		t.Fatalf("折叠后应剩 2 行（实时侧的问+答），实际 %d", len(kept))
	}
	if len(kept) == 2 && (kept[0].Source != session.ExchangeSourceLive || kept[1].Source != session.ExchangeSourceLive) {
		t.Fatalf("应保留实时侧那一轮，实际 %v/%v", kept[0].Source, kept[1].Source)
	}
}

// **两侧都有答但答不同 ⇒ 拦住**。这是「本轮回复也相同」的主判据：一个双写的单位是
// **一轮**（问 + 答），问相同而答不同就是两个不同的问题。
//
// 实测 1791685987 的 seq=12「继续修复」（19:11，答「让我查看 getSessionUnsafe…」）
// 与 seq=16「继续修复」（次日 02:33，答「找到根因了…」）正是这个形状 —— 用户问了
// 同一个问题两次、得到两个不同回答，那是两轮，不是双写。
func TestProjectExchangesRuleADifferentReplyBlocksFold(t *testing.T) {
	day1 := time.Date(2026, 10, 10, 19, 11, 21, 0, time.UTC)
	day2 := day1.Add(7 * time.Hour)
	rows := []session.Exchange{
		{Seq: 12, Role: "user", LegacyContent: "继续修复", Timestamp: day1, Source: session.ExchangeSourceImport},
		{Seq: 13, Role: "agent", LegacyContent: "让我查看 getSessionUnsafe 是否返回同一个指针", Timestamp: day1.Add(time.Second), Source: session.ExchangeSourceImport},
		{Seq: 16, Role: "user", LegacyContent: "继续修复", Timestamp: day2, Source: session.ExchangeSourceLive},
		{Seq: 17, Role: "agent", LegacyContent: "找到根因了：文件已有内容但无游标时会重新加载", Timestamp: day2.Add(time.Second), Source: session.ExchangeSourceLive},
	}
	_, hidden, _ := ProjectExchanges(rows)
	if len(hidden) != 0 {
		t.Fatalf("问相同但答不同的两轮不许折叠：hidden=%v", hidden)
	}
}

// **有一侧没有答时不否决**（照旧折叠）。绝大多数真实双写里那一轮的 agent 行在别处写
// 或还没落盘；改成「两侧答相同才折叠」会把 S2c 之前的历史残留重新显示给用户。
func TestProjectExchangesRuleAMissingReplyDoesNotVeto(t *testing.T) {
	day1 := time.Date(2026, 10, 10, 18, 18, 3, 0, time.UTC)
	rows := []session.Exchange{
		{Seq: 4, Role: "user", LegacyContent: "继续", Timestamp: day1, Source: session.ExchangeSourceImport},
		{Seq: 5, Role: "agent", LegacyContent: "我先看一下测试", Timestamp: day1.Add(time.Second), Source: session.ExchangeSourceImport},
		// 实时侧刚问完、答还没落盘（那一轮还在跑）—— 没有反证，照旧折叠
		{Seq: 22, Role: "user", LegacyContent: "继续", Timestamp: day1.Add(11 * time.Hour), Source: session.ExchangeSourceLive},
	}
	_, hidden, _ := ProjectExchanges(rows)
	if len(hidden) != 1 {
		t.Fatalf("没有反证时照旧折叠，实际 hidden=%v", hidden)
	}
}

// 秒级相邻的同内容两侧行仍要折叠（真实双写的形状），这条现在由「答也相同」覆盖。
func TestProjectExchangesRuleAWindowKeepsSecondsApartTwin(t *testing.T) {
	base := time.Date(2026, 10, 10, 18, 18, 3, 0, time.UTC)
	rows := []session.Exchange{
		{Seq: 1, Role: "user", LegacyContent: "继续", Timestamp: base, Source: session.ExchangeSourceLive},
		{Seq: 2, Role: "agent", LegacyContent: "好", Timestamp: base.Add(time.Second), Source: session.ExchangeSourceLive},
		{Seq: 3, Role: "user", LegacyContent: "继续", Timestamp: base.Add(30 * time.Second), Source: session.ExchangeSourceImport},
		{Seq: 4, Role: "agent", LegacyContent: "好", Timestamp: base.Add(31 * time.Second), Source: session.ExchangeSourceImport},
	}
	kept, hidden, _ := ProjectExchanges(rows)
	if len(hidden) != 2 {
		t.Fatalf("30 秒内的同内容两侧轮次要折叠（问+答共 2 行），实际 hidden=%v", hidden)
	}
	// 保留实时侧那一轮。
	if len(kept) != 2 || kept[0].Source != session.ExchangeSourceLive {
		t.Fatalf("应保留实时侧那一轮，实际 %v", kept)
	}
}

// 归一化本身要维持：流式写短、导入写长时只差空白，靠归一化认得出。
func TestProjectExchangesRuleAFoldsWhitespaceOnlyDifference(t *testing.T) {
	base := time.Date(2026, 10, 10, 18, 18, 3, 0, time.UTC)
	// 流式写短、导入写长时只差空白 —— 靠归一化认得出。同一轮的问也相同。
	rows := []session.Exchange{
		{Seq: 1, Role: "user", LegacyContent: "讲讲这两段", Timestamp: base, Source: session.ExchangeSourceLive},
		{Seq: 2, Role: "agent", LegacyContent: "第一段\n\n第二段", Timestamp: base.Add(time.Second), Source: session.ExchangeSourceLive},
		{Seq: 3, Role: "user", LegacyContent: "讲讲这两段", Timestamp: base.Add(2 * time.Second), Source: session.ExchangeSourceImport},
		{Seq: 4, Role: "agent", LegacyContent: "第一段\n第二段", Timestamp: base.Add(3 * time.Second), Source: session.ExchangeSourceImport},
	}
	_, hidden, _ := ProjectExchanges(rows)
	if len(hidden) != 2 {
		t.Fatalf("只差空白的同内容轮次要折叠（问+答共 2 行），实际 hidden=%v", hidden)
	}
}

// 规则 A 的**一对一**语义（每条实时行只吸收一条）：用户在同一次会话里真的连发
// 两次「继续」不会被多吞。本机实测的形状。
func TestProjectExchangesRuleAOneToOneWithinWindow(t *testing.T) {
	base := time.Date(2026, 10, 10, 18, 0, 0, 0, time.UTC)
	rows := []session.Exchange{
		// 导入侧两条「继续」，各自有各自的回复
		{Seq: 4, Role: "user", LegacyContent: "继续", Timestamp: base, Source: session.ExchangeSourceImport},
		{Seq: 5, Role: "agent", LegacyContent: "第一次的回复", Timestamp: base.Add(time.Second), Source: session.ExchangeSourceImport},
		{Seq: 6, Role: "user", LegacyContent: "继续", Timestamp: base.Add(9 * time.Minute), Source: session.ExchangeSourceImport},
		{Seq: 7, Role: "agent", LegacyContent: "第二次的回复", Timestamp: base.Add(9*time.Minute + time.Second), Source: session.ExchangeSourceImport},
		// 实时侧一条「继续」= 与第二轮同轮的真双写
		{Seq: 22, Role: "user", LegacyContent: "继续", Timestamp: base.Add(9*time.Minute + 5*time.Second), Source: session.ExchangeSourceLive},
		{Seq: 23, Role: "agent", LegacyContent: "第二次的回复", Timestamp: base.Add(9*time.Minute + 6*time.Second), Source: session.ExchangeSourceLive},
	}
	kept, hidden, _ := ProjectExchanges(rows)
	// 一对一：实时侧那轮只吸收与它同轮的那一对导入行（seq 6/7），
	// 另一轮导入行（seq 4/5，回复不同）必须保留。
	if len(hidden) != 2 {
		t.Fatalf("只折叠实时侧对应的那一对导入行，实际 hidden=%v", hidden)
	}
	if _, folded := hidden[6]; !folded {
		t.Fatalf("应折叠 seq=6（与实时侧同轮），hidden=%v", hidden)
	}
	if _, folded := hidden[4]; folded {
		t.Fatalf("seq=4 是另一个问题（回复不同），不许折叠：hidden=%v", hidden)
	}
	if len(kept) != 4 {
		t.Fatalf("应剩 4 行，实际 %d", len(kept))
	}
}

// 归一化后为空的行（只调工具、没有正文的占位轮）不参与任何折叠 —— 与现有约定一致。
func TestProjectExchangesRuleAEmptyBodyStillIgnored(t *testing.T) {
	base := time.Date(2026, 10, 10, 18, 18, 3, 0, time.UTC)
	rows := []session.Exchange{
		{Seq: 1, Role: "agent", LegacyContent: "   ", Timestamp: base, Source: session.ExchangeSourceImport},
		{Seq: 2, Role: "agent", LegacyContent: "", Timestamp: base.Add(time.Second), Source: session.ExchangeSourceLive},
	}
	_, hidden, _ := ProjectExchanges(rows)
	if len(hidden) != 0 {
		t.Fatalf("空正文行不参与折叠，实际 hidden=%v", hidden)
	}
}

// 带 native 身份的行整段不参与任何规则（S2c 的「不猜」）—— 第二判据不能绕过它。
func TestProjectExchangesRuleATimeWindowDoesNotTouchIdentityRows(t *testing.T) {
	base := time.Date(2026, 10, 10, 18, 18, 3, 0, time.UTC)
	rows := []session.Exchange{
		{Seq: 1, Role: "agent", LegacyContent: "同一个回答", Timestamp: base, Source: session.ExchangeSourceImport, AgentID: "uuid-1"},
		{Seq: 2, Role: "agent", LegacyContent: "同一个回答", Timestamp: base.Add(3 * time.Second), Source: session.ExchangeSourceLive, AgentID: "uuid-2"},
	}
	_, hidden, _ := ProjectExchanges(rows)
	if len(hidden) != 0 {
		t.Fatalf("两侧都有 native 身份时不许折叠（各有各的真身份），实际 hidden=%v", hidden)
	}
}
