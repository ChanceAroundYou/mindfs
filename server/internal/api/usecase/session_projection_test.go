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
