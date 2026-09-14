package usecase

import (
	"testing"
	"time"

	"mindfs/server/internal/session"
)

func projectionExchange(seq int, role, source, content string, ts time.Time) session.Exchange {
	return session.Exchange{Seq: seq, Role: role, Source: source, Content: content, Timestamp: ts}
}

// 规则 A：导入器写的那条如果与实时路径写的是同一句（哪怕带 [Request interrupted by user]
// 这类 CLI 前缀），投影只留实时那条。对应实测的 6.4s / 15.6s / 36.7s 三种漏网。
func TestProjectExchangesHidesImportTwin(t *testing.T) {
	base := time.Date(2026, 9, 14, 15, 39, 24, 0, time.UTC)
	exchanges := []session.Exchange{
		projectionExchange(1, "user", session.ExchangeSourceLive, "先把 v1 那三个数字反推一下", base),
		projectionExchange(2, "agent", session.ExchangeSourceLive, "我先找那个会话", base.Add(time.Minute)),
		// 转录时间戳晚 6.4 秒、还多了一段中断标记 —— 写入侧的 ±5s 窗正是这样漏的
		projectionExchange(3, "user", session.ExchangeSourceImport, "[Request interrupted by user]\n\n先把 v1 那三个数字反推一下", base.Add(6400*time.Millisecond)),
	}
	kept, hidden, _ := ProjectExchanges(exchanges)
	if len(kept) != 2 {
		t.Fatalf("kept = %d, want 2 (%+v)", len(kept), kept)
	}
	if kept[0].Source != session.ExchangeSourceLive || kept[1].Source != session.ExchangeSourceLive {
		t.Fatalf("保留的必须是实时路径写的行: %+v", kept)
	}
	if hidden[3] != 1 {
		t.Fatalf("hidden[3] = %d, want 1", hidden[3])
	}
}

// 一对一配对：用户真的重发过两次（两条 live），只有两条 import 时才允许各吸收一条。
func TestProjectExchangesPairsOneToOne(t *testing.T) {
	base := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	exchanges := []session.Exchange{
		projectionExchange(1, "user", session.ExchangeSourceLive, "继续", base),
		projectionExchange(2, "user", session.ExchangeSourceLive, "继续", base.Add(time.Hour)),
		projectionExchange(3, "user", session.ExchangeSourceImport, "继续", base.Add(time.Second)),
		projectionExchange(4, "user", session.ExchangeSourceImport, "继续", base.Add(time.Hour+time.Second)),
		// 第三条 import 没有被吸收的 live 行 → 必须保留（合法重发的可能）
		projectionExchange(5, "user", session.ExchangeSourceImport, "继续", base.Add(2*time.Hour)),
	}
	kept, _, _ := ProjectExchanges(exchanges)
	if len(kept) != 3 {
		t.Fatalf("kept = %d, want 3: %+v", len(kept), kept)
	}
	if kept[2].Seq != 5 {
		t.Fatalf("未被吸收的 import 行必须保留: %+v", kept)
	}
}

// 规则 B：同一时刻（逐字节相同的时间戳）写下两次 = 同一行，老数据（source 为空）唯一的铁证。
func TestProjectExchangesFoldsIdenticalTimestamps(t *testing.T) {
	stamp := time.Date(2026, 8, 27, 3, 15, 46, 0, time.UTC)
	exchanges := []session.Exchange{
		projectionExchange(35, "user", "", "那一轮的正文", stamp),
		projectionExchange(79, "user", "", "那一轮的正文", stamp),
		// 内容相同但时间戳不同 → 可能是合法重发，一律保留
		projectionExchange(90, "user", "", "那一轮的正文", stamp.Add(2*time.Hour)),
	}
	kept, hidden, _ := ProjectExchanges(exchanges)
	if len(kept) != 2 {
		t.Fatalf("kept = %d, want 2: %+v", len(kept), kept)
	}
	if hidden[79] != 35 {
		t.Fatalf("hidden[79] = %d, want 35", hidden[79])
	}
}

// 空内容是占位行（只调工具没有正文的助手轮），不能按内容折叠 —— 红线。
func TestProjectExchangesKeepsEmptyContentRows(t *testing.T) {
	stamp := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	exchanges := []session.Exchange{
		projectionExchange(1, "agent", "", "", stamp),
		projectionExchange(2, "agent", "", "", stamp),
		projectionExchange(3, "agent", "", "", stamp),
	}
	kept, hidden, _ := ProjectExchanges(exchanges)
	if len(kept) != 3 || len(hidden) != 0 {
		t.Fatalf("空内容行必须原样保留: kept=%d hidden=%v", len(kept), hidden)
	}
}

// 幂等：对投影结果再投影一次，结果不变。
func TestProjectExchangesIsIdempotent(t *testing.T) {
	stamp := time.Date(2026, 8, 27, 3, 15, 46, 0, time.UTC)
	exchanges := []session.Exchange{
		projectionExchange(35, "user", "", "同一段话", stamp),
		projectionExchange(79, "user", "", "同一段话", stamp),
		projectionExchange(80, "agent", session.ExchangeSourceLive, "回复", stamp.Add(time.Minute)),
		projectionExchange(81, "agent", session.ExchangeSourceImport, "回复", stamp.Add(time.Minute+time.Second)),
	}
	once, _, _ := ProjectExchanges(exchanges)
	twice, hidden, _ := ProjectExchanges(once)
	if len(twice) != len(once) {
		t.Fatalf("投影不幂等: %d -> %d", len(once), len(twice))
	}
	if len(hidden) != 0 {
		t.Fatalf("二次投影不应再隐藏任何行: %v", hidden)
	}
}

// 被隐藏行的 aux 必须重挂到保留行，不能丢。
func TestRemapExchangeAuxKeepsHiddenRowsAux(t *testing.T) {
	aux := map[int][]session.ExchangeAux{
		1: {{Seq: 1, Line: 1, Thought: "live"}},
		3: {{Seq: 3, Line: 2, Thought: "import"}},
	}
	remapped := RemapExchangeAux(aux, map[int]int{3: 1}, map[int]bool{1: true})
	if len(remapped[1]) != 2 {
		t.Fatalf("隐藏行的 aux 应重挂到保留行: %+v", remapped)
	}
	if _, ok := remapped[3]; ok {
		t.Fatalf("隐藏行的 seq 不应再出现: %+v", remapped)
	}
}

// 老数据（没有 source 标注）也要能折叠：靠实时路径独有的字段认出哪侧是实时的。
// 对应用户实测的两例——本会话 6.4s 那对、1789390688 的 15.6s 那对。
func TestProjectExchangesFoldsLegacyRowsBySignature(t *testing.T) {
	base := time.Date(2026, 9, 14, 15, 39, 24, 0, time.UTC)
	live := session.Exchange{Seq: 145, Role: "user", Content: "重启好了。你继续检查吧", Timestamp: base, ModelDisplayName: "of", Effort: "max"}
	imported := session.Exchange{Seq: 147, Role: "user", Content: "重启好了。你继续检查吧", Timestamp: base.Add(6400 * time.Millisecond)}
	kept, hidden, proven := ProjectExchanges([]session.Exchange{live, imported})
	if len(kept) != 1 {
		t.Fatalf("kept = %d, want 1: %+v", len(kept), kept)
	}
	if kept[0].Seq != 145 {
		t.Fatalf("保留的必须是带实时标记的那条: %+v", kept[0])
	}
	if hidden[147] != 145 {
		t.Fatalf("hidden[147] = %d, want 145", hidden[147])
	}
	if len(proven) != 0 {
		t.Fatalf("这是规则 A 的判定，不该算进「时间戳铁证」: %v", proven)
	}
}

// 两侧都没有实时标记（纯导入会话里的合法重发）→ 一律保留。
func TestProjectExchangesKeepsUnmarkedTwins(t *testing.T) {
	base := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	rows := []session.Exchange{
		{Seq: 1, Role: "user", Content: "继续", Timestamp: base},
		{Seq: 2, Role: "user", Content: "继续", Timestamp: base.Add(time.Hour)},
	}
	kept, hidden, _ := ProjectExchanges(rows)
	if len(kept) != 2 || len(hidden) != 0 {
		t.Fatalf("没有实时标记的孪生行必须都保留: kept=%d hidden=%v", len(kept), hidden)
	}
}

// 规则 A2：实时路径写的是流式快照（写短了），导入器拿到的是最终全文 —— 保留更长的那条，
// 它包含被隐藏行的全部内容。
func TestProjectExchangesFoldsPrefixSnapshot(t *testing.T) {
	base := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	full := "我先找那个会话，然后核对转录与库里的行数是否一致，最后把结论写进报告里。这是一段足够长的正文。"
	snapshot := "我先找那个会话，然后核对转录与库里的行数是否一致"
	rows := []session.Exchange{
		{Seq: 1, Role: "agent", Content: snapshot, Timestamp: base, ModelDisplayName: "of"},
		{Seq: 2, Role: "agent", Content: full, Timestamp: base.Add(30 * time.Second)},
	}
	kept, hidden, _ := ProjectExchanges(rows)
	if len(kept) != 1 || kept[0].Seq != 2 {
		t.Fatalf("应保留更长的那条: kept=%+v", kept)
	}
	if hidden[1] != 2 {
		t.Fatalf("hidden[1] = %d, want 2", hidden[1])
	}
}

// 相隔十几小时的前缀关系不是重复（实测 1787758959 里 17-20 与 166 相隔 32 小时）。
func TestProjectExchangesKeepsDistantPrefixRows(t *testing.T) {
	base := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	short := "API Error: 502 Provider returned 400 Bad Request: 这是一段足够长的错误信息，用于触发前缀判定。"
	long := short + " 后面还会多出一截内容，因此构成前缀关系。"
	rows := []session.Exchange{
		{Seq: 1, Role: "agent", Content: short, Timestamp: base},
		{Seq: 2, Role: "agent", Content: long, Timestamp: base.Add(32 * time.Hour), ModelDisplayName: "of"},
	}
	kept, hidden, _ := ProjectExchanges(rows)
	if len(kept) != 2 || len(hidden) != 0 {
		t.Fatalf("相隔 32 小时的前缀行必须都保留: kept=%d hidden=%v", len(kept), hidden)
	}
}

// 来源相同的前缀行不动（同一个人写的两段相似文本）。
func TestProjectExchangesKeepsSameProvenancePrefixRows(t *testing.T) {
	base := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	short := "这一轮的结论是：内存缓存与磁盘不一致时，必须重新读盘。"
	long := short + " 补充：读盘后还要把游标推到新位置。"
	rows := []session.Exchange{
		{Seq: 1, Role: "agent", Content: short, Timestamp: base, ModelDisplayName: "of"},
		{Seq: 2, Role: "agent", Content: long, Timestamp: base.Add(10 * time.Second), ModelDisplayName: "of"},
	}
	kept, hidden, _ := ProjectExchanges(rows)
	if len(kept) != 2 || len(hidden) != 0 {
		t.Fatalf("来源相同的两条必须都保留: kept=%d hidden=%v", len(kept), hidden)
	}
}

// 规则之间会串成链（A 把 32→30，A2 又把 30→33）：隐藏映射必须一跳到底，
// 否则调用方拿 32→30 去重挂 aux 会落空（30 自己也被隐藏了）。
func TestProjectExchangesResolvesHiddenChains(t *testing.T) {
	base := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	content := "那请对这些文件进行深度的修改，并且在修改完成后，向我总结改动内容。如果有需要我决策的立刻用 ask 提出。"
	longer := content + " 另外把涉及的测试一并补上，谢谢。"
	rows := []session.Exchange{
		// 30：带标记（被 A2 判定比 33 短）
		{Seq: 30, Role: "user", Content: content, Timestamp: base, ModelDisplayName: "of"},
		// 32：不带标记，内容与 30 相同 → 被 A 吸收
		{Seq: 32, Role: "user", Content: content, Timestamp: base.Add(time.Second)},
		// 33：更长（A2 的保留方）
		{Seq: 33, Role: "user", Content: longer, Timestamp: base.Add(2 * time.Second)},
	}
	kept, hidden, _ := ProjectExchanges(rows)
	if len(kept) != 1 || kept[0].Seq != 33 {
		t.Fatalf("最终只应留下 seq=33: %+v", kept)
	}
	for hiddenSeq, keptSeq := range hidden {
		if keptSeq != 33 {
			t.Fatalf("hidden[%d] = %d，必须一跳到底指向 33", hiddenSeq, keptSeq)
		}
	}
}
