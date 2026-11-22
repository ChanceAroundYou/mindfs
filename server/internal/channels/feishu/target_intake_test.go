package feishu

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Intake-side behaviour of the explicit-target flow: what the inbound service
// does before any card exists (append to the confirmed target, fall back to a
// confirmation card, override with an explicit #N). Card-click behaviour lives
// in target_confirm_test.go; the shared harness and fakes live there too.

func TestTargetActiveTargetAppendsDirectly(t *testing.T) {
	h := newTargetHarness(t, Config{
		Bindings: []Binding{{ChatID: "oc_chat", NodeID: "fn", RootID: "r1"}},
	}, intakeCatalog{refs: []ProjectRef{{NodeID: "fn", RootID: "r1", Name: "docs"}}}, staticTasks{})
	if err := h.store.SetActiveTarget(ActiveTarget{
		ChatID: "oc_chat", NodeID: "fn", RootID: "r1",
		TaskID: "task_9", TaskNumber: 9, TaskTitle: "登录",
	}); err != nil {
		t.Fatal(err)
	}

	msg := inbound("om_t2", "p2p", "ou_owner", "再补一句：要支持微信登录")
	if err := h.intake.Handle(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if len(h.rec.appended) != 1 {
		t.Fatalf("有活跃目标时应直接追加：%+v", h.rec.appended)
	}
	if got := h.rec.appended[0]; got.TaskID != "task_9" || got.Input != "再补一句：要支持微信登录" {
		t.Fatalf("追加参数 = %+v", got)
	}
	if len(h.rec.created) != 0 {
		t.Fatalf("直接追加不得新建任务：%+v", h.rec.created)
	}
	if n := len(h.cardsOfKind(KindTarget)); n != 0 {
		t.Fatalf("直接追加不应再发确认卡，got %d", n)
	}
	if got := h.store.records["om_t2"].Status; got != "appended" {
		t.Fatalf("入站状态 = %q, want appended", got)
	}
	notices := h.cardsOfKind(KindNotice)
	if len(notices) != 1 || !strings.Contains(notices[0].Text, "已追加") {
		t.Fatalf("直接追加必须有可见回执：%+v", notices)
	}
}

func TestTargetAppendFailureFallsBackToConfirmCard(t *testing.T) {
	h := newTargetHarness(t, Config{
		Bindings: []Binding{{ChatID: "oc_chat", NodeID: "fn", RootID: "r1"}},
	}, intakeCatalog{refs: []ProjectRef{{NodeID: "fn", RootID: "r1", Name: "docs"}}}, staticTasks{})
	if err := h.store.SetActiveTarget(ActiveTarget{
		ChatID: "oc_chat", NodeID: "fn", RootID: "r1", TaskID: "task_9", TaskNumber: 9,
	}); err != nil {
		t.Fatal(err)
	}
	h.rec.appendErr = errors.New("task vanished")

	if err := h.intake.Handle(context.Background(), inbound("om_t2b", "p2p", "ou_owner", "再补一句")); err != nil {
		t.Fatal(err)
	}
	targets := h.cardsOfKind(KindTarget)
	if len(targets) != 1 {
		t.Fatalf("追加失败不能丢消息，必须回落到确认卡：%d", len(targets))
	}
	if got := h.store.records["om_t2b"].Status; got != "confirming" {
		t.Fatalf("回落后的入站状态 = %q, want confirming", got)
	}
}

func TestTargetExplicitTaskNumberOverridesActiveTarget(t *testing.T) {
	catalog := intakeCatalog{refs: []ProjectRef{{NodeID: "fn", RootID: "r1", Name: "docs"}}}
	tasks := staticTasks{refs: []TaskRef{
		{NodeID: "fn", RootID: "r1", TaskID: "task_seven", TaskNumber: 7, Title: "登录"},
	}}
	h := newTargetHarness(t, Config{
		Bindings: []Binding{{ChatID: "oc_chat", NodeID: "fn", RootID: "r1"}},
	}, catalog, tasks)
	if err := h.store.SetActiveTarget(ActiveTarget{
		ChatID: "oc_chat", NodeID: "fn", RootID: "r1", TaskID: "task_9", TaskNumber: 9,
	}); err != nil {
		t.Fatal(err)
	}

	if err := h.intake.Handle(context.Background(), inbound("om_t12", "p2p", "ou_owner", "再补一句到 #7")); err != nil {
		t.Fatal(err)
	}
	// 带显式任务号时不得走活跃目标快路径，必须让 owner 在卡上确认。
	if len(h.rec.appended) != 0 {
		t.Fatalf("显式 #7 不应直接追加到活跃目标 #9：%+v", h.rec.appended)
	}
	targets := h.cardsOfKind(KindTarget)
	if len(targets) != 1 {
		t.Fatalf("显式任务号应发确认卡，got %d", len(targets))
	}
	if targets[0].TargetMode != "append" || targets[0].Target == nil ||
		targets[0].Target.TaskID != "task_seven" {
		t.Fatalf("确认卡应预填 #7 的追加目标：%+v", targets[0])
	}
}

func TestTargetRedeliveryConfirmsOnceAndCreatesOne(t *testing.T) {
	h := newTargetHarness(t, Config{
		Bindings: []Binding{{ChatID: "oc_chat", NodeID: "fn", RootID: "r1"}},
	}, intakeCatalog{}, staticTasks{})
	msg := inbound("om_t4", "p2p", "ou_owner", "只建一次")
	for i := 0; i < 2; i++ {
		if err := h.intake.Handle(context.Background(), msg); err != nil {
			t.Fatal(err)
		}
	}
	targets := h.cardsOfKind(KindTarget)
	if len(targets) != 1 {
		t.Fatalf("重投只能有一张确认卡，got %d", len(targets))
	}
	clickTarget(t, h, targets[0], "confirm", nil)
	if len(h.rec.created) != 1 {
		t.Fatalf("重投后点确认只能建一个任务：%+v", h.rec.created)
	}
}
