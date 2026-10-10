package usecase

import (
	"testing"
	"time"

	"mindfs/server/internal/session"
)

// S2c：投影身份化。
//
// 投影原本是「读时把双写折叠回去」的兜底。S2c 之后**新数据不会再双写**（写入侧有
// native id 身份判重），所以投影对新数据必须是**完全不作为**的 —— 否则它就成了
// 一层掩盖真实缺陷的纱：真出现重复时看不见，也无从知道它为什么看不见。
//
// 所以规则是：**只折叠双方都没有 native 身份的对**。那正是 S2c 之前的历史行。
// 投影从此只剩一个作用 —— 把「S2c 之前留下的重复」藏住，并**报出来**（审计端点
// 的 LiveOwned/Proven 计数就是这份待迁移清单的规模）。
//
// 为什么不直接删投影：实测本机 `1791339665-fb5134041392` 就有 9 对
// 「live 写一遍 + import 又写一遍」的历史行（导入器伪造时间戳 + 合并内容，纯内容
// 判重当时判不出来）。删掉投影，这些行立刻变成用户可见的重复正文 —— 那是一次
// 实打实的回退。删投影必须与「历史行处理」同一步做，那是 S2d 的事（S2d 要删
// `Exchange.Content`，投影的内容比对届时无论如何都活不下去）。

func projectExchange(seq int, role, content, nativeTurn, nativePrompt, source string, ts time.Time) session.Exchange {
	return session.Exchange{
		Seq:          seq,
		Role:         role,
		Content:      content,
		NativeTurn:   nativeTurn,
		NativePrompt: nativePrompt,
		Source:       source,
		Timestamp:    ts,
	}
}

// 有身份的一对（live 写过的行 + 导入重放的行，native id 相等）→ 判重已在写入侧
// 拦住，库里不会同时存在。这里防御性地钉住：万一同时存在，投影**不得**再折叠。
func TestProjectionLeavesIdentityBearingPairsAlone(t *testing.T) {
	ts := time.Date(2026, 10, 7, 6, 30, 5, 0, time.UTC)
	rows := []session.Exchange{
		projectExchange(12, "agent", "回答正文", "6ba7b810-9dad-11d1-80b4-00c04fd430c8", "", string(session.ExchangeSourceLive), ts),
		projectExchange(25, "agent", "回答正文", "6ba7b810-9dad-11d1-80b4-00c04fd430c8", "", string(session.ExchangeSourceImport), ts),
	}
	kept, hidden, _ := ProjectExchanges(rows)
	if len(kept) != 2 {
		t.Fatalf("kept = %d, want 2（有身份的行不折叠 —— 判重是写入侧的事）", len(kept))
	}
	if len(hidden) != 0 {
		t.Fatalf("hidden = %v, want 空", hidden)
	}
}

// 一侧有身份、另一侧没有 → 也不折叠。「有身份」已经说明这一侧知道自己是谁，
// 再按内容把它认领掉就是猜。
func TestProjectionDoesNotFoldWhenOnlyOneSideHasIdentity(t *testing.T) {
	ts := time.Date(2026, 10, 7, 6, 30, 5, 0, time.UTC)
	rows := []session.Exchange{
		projectExchange(12, "agent", "回答正文", "6ba7b810-9dad-11d1-80b4-00c04fd430c8", "", string(session.ExchangeSourceLive), ts),
		projectExchange(13, "agent", "回答正文", "", "", string(session.ExchangeSourceImport), ts),
	}
	kept, _, _ := ProjectExchanges(rows)
	if len(kept) != 2 {
		t.Fatalf("kept = %d, want 2（一侧有身份即不得按内容折叠）", len(kept))
	}
}

// 双方都没有身份的历史行：继续折叠 —— 这正是投影现在唯一的用途。
func TestProjectionStillFoldsIdentitylessLegacyDuplicates(t *testing.T) {
	ts := time.Date(2026, 10, 7, 6, 30, 5, 0, time.UTC)
	rows := []session.Exchange{
		projectExchange(12, "agent", "同一段历史正文，内容完全相同。", "", "", string(session.ExchangeSourceLive), ts),
		projectExchange(25, "agent", "同一段历史正文，内容完全相同。", "", "", string(session.ExchangeSourceImport), ts),
	}
	kept, hidden, _ := ProjectExchanges(rows)
	if len(kept) != 1 || kept[0].Seq != 12 {
		t.Fatalf("kept = %+v, want 只保留 seq=12", kept)
	}
	if hidden[25] != 12 {
		t.Fatalf("hidden = %v, want {25:12}", hidden)
	}
}

// 用户真的重发过：历史数据里没有身份，但内容相同、时间相隔数小时。
// 规则 A 本来就靠「一侧实时、一侧不实时」区分，这里把边界钉住。
func TestProjectionKeepsGenuineUserRepeats(t *testing.T) {
	base := time.Date(2026, 10, 7, 6, 30, 5, 0, time.UTC)
	rows := []session.Exchange{
		projectExchange(5, "user", "请继续", "", "", string(session.ExchangeSourceLive), base),
		projectExchange(11, "user", "请继续", "", "", string(session.ExchangeSourceLive), base.Add(2*time.Hour)),
	}
	kept, _, _ := ProjectExchanges(rows)
	if len(kept) != 2 {
		t.Fatalf("kept = %d, want 2（同为实时来源的两条是用户重发，不是重复）", len(kept))
	}
}
