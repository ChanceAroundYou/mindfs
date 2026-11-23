package feishu

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// commandTasks is a small project whose task #12 is deliberately outside the
// five recent candidates, so a bare `#12` can only resolve through the exact
// catalog lookup.
func commandTasks() keywordTasks {
	return keywordTasks{refs: []TaskRef{
		{NodeID: "fn", RootID: "r1", TaskID: "task_1", TaskNumber: 1, Title: "一"},
		{NodeID: "fn", RootID: "r1", TaskID: "task_2", TaskNumber: 2, Title: "二"},
		{NodeID: "fn", RootID: "r1", TaskID: "task_3", TaskNumber: 3, Title: "三"},
		{NodeID: "fn", RootID: "r1", TaskID: "task_4", TaskNumber: 4, Title: "四"},
		{NodeID: "fn", RootID: "r1", TaskID: "task_5", TaskNumber: 5, Title: "五"},
		{NodeID: "fn", RootID: "r1", TaskID: "task_12", TaskNumber: 12, Title: "十二"},
	}}
}

func commandHarness(t *testing.T, tasks TaskCatalog) *targetHarness {
	t.Helper()
	return newTargetHarness(t, Config{
		Bindings: []Binding{{ChatID: "oc_chat", NodeID: "fn", RootID: "r1"}},
	}, intakeCatalog{refs: []ProjectRef{{NodeID: "fn", RootID: "r1", Name: "docs"}}}, tasks)
}

func sendIntake(t *testing.T, h *targetHarness, id, text string) {
	t.Helper()
	if err := h.intake.Handle(context.Background(), inbound(id, "p2p", "ou_owner", text)); err != nil {
		t.Fatalf("Handle(%q): %v", text, err)
	}
}

// expectNoticeContains scans every notice card. The harness freezes the clock,
// so two receipts written in the same test share CreatedAt and List() order is
// not stable — "the last notice" would be a flaky assertion.
func expectNoticeContains(t *testing.T, h *targetHarness, want string) {
	t.Helper()
	var texts []string
	for _, c := range h.cardsOfKind(KindNotice) {
		if strings.Contains(c.Text, want) {
			return
		}
		texts = append(texts, c.Text)
	}
	t.Fatalf("回执里没有 %q：%q", want, texts)
}

// TestBareTaskNumberCommandSelectsActiveTarget proves `#12` alone switches the
// chat to task #12 — resolved through the catalog even though it is outside the
// five recent candidates — and writes nothing to the task.
func TestBareTaskNumberCommandSelectsActiveTarget(t *testing.T) {
	h := commandHarness(t, commandTasks())

	sendIntake(t, h, "om_cmd1", "#12")

	if n := len(h.cardsOfKind(KindTarget)); n != 0 {
		t.Fatalf("裸 #N 不应发确认卡：%d", n)
	}
	if len(h.rec.appended) != 0 || len(h.rec.created) != 0 {
		t.Fatalf("裸 #N 不应写任务：appended=%+v created=%+v", h.rec.appended, h.rec.created)
	}
	active, ok := h.store.ActiveTarget("oc_chat")
	if !ok || active.TaskID != "task_12" || active.TaskNumber != 12 || active.NodeID != "fn" || active.RootID != "r1" {
		t.Fatalf("活跃目标 = %+v ok=%v", active, ok)
	}
	expectNoticeContains(t, h, "已切换到任务 #12")
}

// TestTaskNumberBodyCommandAppendsStrippedBody proves `#12 再补一段` appends the
// body (without the `#12` token) and makes task #12 the chat's default.
func TestTaskNumberBodyCommandAppendsStrippedBody(t *testing.T) {
	h := commandHarness(t, commandTasks())

	sendIntake(t, h, "om_cmd2", "#12 再补一段")

	if n := len(h.cardsOfKind(KindTarget)); n != 0 {
		t.Fatalf("#N body 不应发确认卡：%d", n)
	}
	if len(h.rec.created) != 0 {
		t.Fatalf("不应新建任务：%+v", h.rec.created)
	}
	if len(h.rec.appended) != 1 {
		t.Fatalf("应追加一次：%+v", h.rec.appended)
	}
	if got := h.rec.appended[0]; got.TaskID != "task_12" || got.Input != "再补一段" {
		t.Fatalf("追加目标/正文 = %+v", got)
	}
	active, ok := h.store.ActiveTarget("oc_chat")
	if !ok || active.TaskID != "task_12" {
		t.Fatalf("活跃目标 = %+v ok=%v", active, ok)
	}
}

// TestSwitchCommandClearsActiveTarget proves `切换` forgets the default so the
// next message is recognised from scratch.
func TestSwitchCommandClearsActiveTarget(t *testing.T) {
	h := commandHarness(t, commandTasks())

	sendIntake(t, h, "om_cmd3a", "#12")
	if _, ok := h.store.ActiveTarget("oc_chat"); !ok {
		t.Fatal("前置：活跃目标未设置")
	}
	sendIntake(t, h, "om_cmd3b", "切换")

	if active, ok := h.store.ActiveTarget("oc_chat"); ok {
		t.Fatalf("切换后活跃目标应清除：%+v", active)
	}
	expectNoticeContains(t, h, "已清除默认目标")
}

// TestNewTaskCommandClearsAndNextMessageCreates proves `新任务` forgets the
// target and the next ordinary message opens a create-prefilled confirmation.
func TestNewTaskCommandClearsAndNextMessageCreates(t *testing.T) {
	h := commandHarness(t, commandTasks())

	sendIntake(t, h, "om_cmd4a", "#12")
	sendIntake(t, h, "om_cmd4b", "新任务")
	if _, ok := h.store.ActiveTarget("oc_chat"); ok {
		t.Fatal("新任务后活跃目标应清除")
	}
	expectNoticeContains(t, h, "默认新建任务")

	sendIntake(t, h, "om_cmd4c", "建一个登录页")
	cards := h.cardsOfKind(KindTarget)
	if len(cards) != 1 {
		t.Fatalf("新任务后应发一张确认卡：%d", len(cards))
	}
	if cards[0].TargetMode != "create" {
		t.Fatalf("确认卡应为新建模式：%+v", cards[0])
	}
}

// TestSwitchWordInsideSentenceIsOrdinaryText proves only a whole-message `切换`
// is a command; the same word in a sentence stays task text and goes to the
// active target like any other message.
func TestSwitchWordInsideSentenceIsOrdinaryText(t *testing.T) {
	h := commandHarness(t, commandTasks())

	sendIntake(t, h, "om_cmd5a", "#12")
	sendIntake(t, h, "om_cmd5b", "帮我切换一下这个项目的目标")

	if len(h.rec.appended) != 1 {
		t.Fatalf("含切换的整句应作为普通消息追加：%+v", h.rec.appended)
	}
	if h.rec.appended[0].TaskID != "task_12" || h.rec.appended[0].Input != "帮我切换一下这个项目的目标" {
		t.Fatalf("追加内容 = %+v", h.rec.appended[0])
	}
	if _, ok := h.store.ActiveTarget("oc_chat"); !ok {
		t.Fatal("普通消息不应清除活跃目标")
	}
}

// TestTaskNumberBodyCommandStaleDoesNotSetActive proves a command append that
// hits a dead task reports it and does not leave the chat pointing at it.
func TestTaskNumberBodyCommandStaleDoesNotSetActive(t *testing.T) {
	h := commandHarness(t, commandTasks())
	h.rec.appendErr = fmt.Errorf("gone: %w", ErrTargetStale)

	sendIntake(t, h, "om_cmd7", "#12 补一句")

	if _, ok := h.store.ActiveTarget("oc_chat"); ok {
		t.Fatal("失效追加后不应设置活跃目标")
	}
	expectNoticeContains(t, h, "已经不在")
}

// TestTargetCardCarriesReviewHits proves the injected classifier annotates the
// confirmation card before the owner clicks.
func TestTargetCardCarriesReviewHits(t *testing.T) {
	h := commandHarness(t, commandTasks())
	h.intake.SetReviewClassifier(func(text string) []string {
		if strings.Contains(text, "删除数据") {
			return []string{"删除数据"}
		}
		return nil
	})

	sendIntake(t, h, "om_cmd6", "删除数据重建索引")

	cards := h.cardsOfKind(KindTarget)
	if len(cards) != 1 {
		t.Fatalf("应发一张确认卡：%d", len(cards))
	}
	if len(cards[0].ReviewHits) != 1 || cards[0].ReviewHits[0] != "删除数据" {
		t.Fatalf("审核命中 = %+v", cards[0].ReviewHits)
	}
	if !strings.Contains(cards[0].Text, "命中审核分类：删除数据") {
		t.Fatalf("确认卡正文缺少审核标注：%q", cards[0].Text)
	}
}
