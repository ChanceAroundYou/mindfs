package kanban

import (
	"context"
	"strings"
	"testing"
)

// S6：事件摘录改成「头 500 + 尾 500」。结论段几乎总在开头、待办与未验证常在
// 结尾 —— 只取尾部会把开头那段结论整个丢掉，旧 400 字窗口丢掉的是待拍板事项
// （#68），改成只取尾部又把开头的结论丢了。
func TestTurnSettledExcerptKeepsHeadAndTail(t *testing.T) {
	ctx := context.Background()
	f := newTurnSettledFixture(t, "task-excerpt-head-tail", "sess-head-tail")
	head := "【结论】改了 router 的三个入口"
	tail := "【下一步】等用户重启后再看一轮"
	excerpt := head + strings.Repeat("过程记录。", 300) + tail
	in := TurnSettledInput{SessionKey: "sess-head-tail", TurnID: "turn-head-tail", Excerpt: excerpt}
	if _, err := f.svc.SettleSessionTurn(ctx, f.rootID, in); err != nil {
		t.Fatalf("SettleSessionTurn: %v", err)
	}
	events := f.events(t, RouterEventTurnSettled)
	if len(events) != 1 {
		t.Fatalf("turn_settled events = %d, want 1", len(events))
	}
	brief := &SituationBrief{}
	if err := attachRouterEventBrief(ctx, f.store, brief, events[0]); err != nil {
		t.Fatalf("attachRouterEventBrief: %v", err)
	}
	if !strings.Contains(brief.Event.Excerpt, head) {
		t.Fatalf("brief 丢了开头结论：\n%s", brief.Event.Excerpt)
	}
	if !strings.Contains(brief.Event.Excerpt, tail) {
		t.Fatalf("brief 丢了结尾下一步：\n%s", brief.Event.Excerpt)
	}
	// 中间省略标记必须出现：两段否则会被当成连续文本，Router 会把省略处读成
	// 「这里就是这么写的」。标记里还要写清省掉多少字。
	if !strings.Contains(brief.Event.Excerpt, "中间省略") {
		t.Fatalf("brief 缺少中间省略标记：\n%s", brief.Event.Excerpt)
	}
	if n := len([]rune(brief.Event.Excerpt)); n > routerSettlementExcerptRunes+40 {
		t.Fatalf("excerpt keeps %d runes, want <= %d（含省略说明）",
			n, routerSettlementExcerptRunes+40)
	}
}
