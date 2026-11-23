package feishu

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// keywordTasks behaves like the real catalog: an empty keyword returns the
// recent (limited) candidates, a "#N" keyword returns the exact match. This
// lets a test prove a typed number resolves even when it is not on the card.
type keywordTasks struct {
	refs []TaskRef
}

func (k keywordTasks) Match(_ context.Context, _, _, keyword string) ([]TaskRef, error) {
	return matchTaskRefs(k.refs, keyword, taskCatalogLimit), nil
}

// TestTargetClickClaimsBeforeCreate proves the intake record moves to a
// claimed status before the remote Create runs, and lands on a terminal status
// afterward — the window a restart would otherwise replay.
func TestTargetClickClaimsBeforeCreate(t *testing.T) {
	h := newTargetHarness(t, Config{
		Bindings: []Binding{{ChatID: "oc_chat", NodeID: "fn", RootID: "r1"}},
	}, intakeCatalog{refs: []ProjectRef{{NodeID: "fn", RootID: "r1", Name: "docs"}}}, staticTasks{})

	if err := h.intake.Handle(context.Background(), inbound("om_c1", "p2p", "ou_owner", "建一个")); err != nil {
		t.Fatal(err)
	}
	cards := h.cardsOfKind(KindTarget)
	if len(cards) != 1 {
		t.Fatalf("应先发一张确认卡：%+v", cards)
	}
	var duringCreate string
	h.rec.onCreate = func() { duringCreate = h.store.records["om_c1"].Status }
	clickTarget(t, h, cards[0], "confirm", nil)

	if duringCreate != "creating" {
		t.Fatalf("远端建任务前必须先认领为 creating，实际状态 %q", duringCreate)
	}
	if got := h.store.records["om_c1"].Status; got != "created" {
		t.Fatalf("建任务成功后记录应为 created，实际 %q", got)
	}
	if len(h.rec.created) != 1 {
		t.Fatalf("应恰好建一个任务：%+v", h.rec.created)
	}
}

// TestTargetClaimBlocksRedeliveredClick simulates the click whose finish never
// persisted (record left claimed) and a redelivered click: the second click must
// fail closed instead of creating a second task.
func TestTargetClaimBlocksRedeliveredClick(t *testing.T) {
	h := newTargetHarness(t, Config{
		Bindings: []Binding{{ChatID: "oc_chat", NodeID: "fn", RootID: "r1"}},
	}, intakeCatalog{refs: []ProjectRef{{NodeID: "fn", RootID: "r1", Name: "docs"}}}, staticTasks{})

	if err := h.intake.Handle(context.Background(), inbound("om_c2", "p2p", "ou_owner", "建一个")); err != nil {
		t.Fatal(err)
	}
	cards := h.cardsOfKind(KindTarget)
	if len(cards) != 1 {
		t.Fatalf("应先发一张确认卡：%+v", cards)
	}
	// Crash after the claim but before finish wrote a terminal status.
	if err := h.store.SetStatus("om_c2", "", "creating"); err != nil {
		t.Fatal(err)
	}
	clickTarget(t, h, cards[0], "confirm", nil)

	if len(h.rec.created) != 0 {
		t.Fatalf("已认领的记录重投不得再建一个任务：%+v", h.rec.created)
	}
	settled, _ := h.cards.Get(cards[0].ID)
	if settled.State != StateFailed || !strings.Contains(settled.Summary, "已在处理") {
		t.Fatalf("重投应 fail closed：%+v", settled)
	}
}

// TestTargetCancelLeavesTerminalRecord proves cancel writes a terminal intake
// status so a later redelivery (after a restart reopens the card) is swallowed.
func TestTargetCancelLeavesTerminalRecord(t *testing.T) {
	h := newTargetHarness(t, Config{
		Bindings: []Binding{{ChatID: "oc_chat", NodeID: "fn", RootID: "r1"}},
	}, intakeCatalog{refs: []ProjectRef{{NodeID: "fn", RootID: "r1", Name: "docs"}}}, staticTasks{})

	if err := h.intake.Handle(context.Background(), inbound("om_c3", "p2p", "ou_owner", "建一个")); err != nil {
		t.Fatal(err)
	}
	cards := h.cardsOfKind(KindTarget)
	if len(cards) != 1 {
		t.Fatalf("应先发一张确认卡：%+v", cards)
	}
	clickTarget(t, h, cards[0], "cancel", nil)
	if got := h.store.records["om_c3"].Status; got != "cancelled" {
		t.Fatalf("取消后入站记录应为终态 cancelled，实际 %q", got)
	}

	// A restart reopens the card (applying→open); the redelivered click must not
	// create anything because the record is already terminal.
	if _, _, err := h.cards.Update(cards[0].ID, func(x *Card) bool {
		x.State = StateOpen
		return true
	}); err != nil {
		t.Fatal(err)
	}
	reopened, _ := h.cards.Get(cards[0].ID)
	clickTarget(t, h, reopened, "confirm", nil)
	if len(h.rec.created) != 0 || len(h.rec.appended) != 0 {
		t.Fatalf("取消是终态，重投不得有副作用：%+v %+v", h.rec.created, h.rec.appended)
	}
}

// TestPickerAndTargetDepsSettersMerge proves calling the two dependency setters
// in either order keeps all four fields — neither wipes the other's.
func TestPickerAndTargetDepsSettersMerge(t *testing.T) {
	for _, order := range []string{"picker-first", "target-first"} {
		t.Run(order, func(t *testing.T) {
			svc, _, _ := newTestService(t, Config{OwnerOpenID: "ou_owner"})
			store, err := NewIntakeStore(filepath.Join(t.TempDir(), "intake.json"), fixedNow)
			if err != nil {
				t.Fatal(err)
			}
			rec := &targetRecorder{}
			tasks := staticTasks{}
			if order == "picker-first" {
				svc.SetPickerTaskDeps(store, rec)
				svc.SetTargetTaskDeps(rec, tasks)
			} else {
				svc.SetTargetTaskDeps(rec, tasks)
				svc.SetPickerTaskDeps(store, rec)
			}
			deps := svc.pickerTaskDeps()
			if deps.store != store || deps.creator != rec || deps.appender != rec || deps.tasks == nil {
				t.Fatalf("setter 顺序 %s 丢了字段：store=%v creator=%v appender=%v tasks=%v",
					order, deps.store != nil, deps.creator != nil, deps.appender != nil, deps.tasks != nil)
			}
		})
	}
}

// TestTargetTypedNumberResolvesViaCatalogBeyondCandidates proves a task number
// typed into the form resolves against the live catalog even when it is not in
// the card's ≤5 displayed candidates.
func TestTargetTypedNumberResolvesViaCatalogBeyondCandidates(t *testing.T) {
	var refs []TaskRef
	for n := 1; n <= 7; n++ {
		refs = append(refs, TaskRef{NodeID: "fn", RootID: "r1", TaskID: fmt.Sprintf("task_%d", n), TaskNumber: n})
	}
	h := newTargetHarness(t, Config{}, intakeCatalog{}, keywordTasks{refs: refs})
	card := h.notifyTarget(t, Notice{
		CardID: "tgt-cat", SourceMessageID: "om_cat", IntakeText: "追加到 7",
		Target:     &TargetRef{NodeID: "fn", RootID: "r1", ProjectLabel: "docs · fn"},
		Candidates: targetRefsFromTasks(matchTaskRefs(refs, "", taskCatalogLimit)),
		TargetMode: "create",
	})
	if len(card.Candidates) != taskCatalogLimit {
		t.Fatalf("卡片最多列 %d 个候选，实际 %d", taskCatalogLimit, len(card.Candidates))
	}
	for _, cand := range card.Candidates {
		if cand.TaskNumber == 7 {
			t.Fatalf("测试前提是 #7 不在候选里：%+v", card.Candidates)
		}
	}
	clickTarget(t, h, card, "confirm", map[string]string{"task_no": "#7"})

	if len(h.rec.appended) != 1 || h.rec.appended[0].TaskID != "task_7" {
		t.Fatalf("应按任务号追加到 catalog 里的 #7：%+v", h.rec.appended)
	}
	if len(h.rec.created) != 0 {
		t.Fatalf("填写任务号绝不能变成新建：%+v", h.rec.created)
	}
}

// TestTargetTypedGarbageNumberFailsWithoutSideEffect proves a typo in the task
// number does not silently become "confirm" and create a new task.
func TestTargetTypedGarbageNumberFailsWithoutSideEffect(t *testing.T) {
	h := newTargetHarness(t, Config{}, intakeCatalog{}, staticTasks{})
	for _, raw := range []string{"#abc", "#999"} {
		card := h.notifyTarget(t, Notice{
			CardID: "tgt-bad-" + strings.TrimPrefix(raw, "#"), SourceMessageID: "om_bad_" + raw, IntakeText: "写错号",
			Target: &TargetRef{NodeID: "fn", RootID: "r1", ProjectLabel: "docs · fn"}, TargetMode: "create",
		})
		clickTarget(t, h, card, "confirm", map[string]string{"task_no": raw})

		settled, _ := h.cards.Get(card.ID)
		if settled.State != StateFailed {
			t.Fatalf("任务号 %q 无法解析/找不到时必须失败：%+v", raw, settled)
		}
	}
	if len(h.rec.created) != 0 || len(h.rec.appended) != 0 {
		t.Fatalf("无法解析的任务号不得建或追加：%+v %+v", h.rec.created, h.rec.appended)
	}
}

// TestTargetAppendTransientReopensCardAndKeepsTarget proves a non-stale append
// error keeps the active target and reopens the card for a retry.
func TestTargetAppendTransientReopensCardAndKeepsTarget(t *testing.T) {
	h := newTargetHarness(t, Config{}, intakeCatalog{}, staticTasks{})
	if err := h.store.SetActiveTarget(ActiveTarget{
		ChatID: "oc_chat", NodeID: "fn", RootID: "r1", TaskID: "task_9", TaskNumber: 9,
	}); err != nil {
		t.Fatal(err)
	}
	h.rec.appendErr = errors.New("remote temporarily unavailable")
	card := h.notifyTarget(t, Notice{
		CardID: "tgt-transient", SourceMessageID: "om_tr", IntakeText: "追加到老任务",
		Target:     &TargetRef{NodeID: "fn", RootID: "r1", TaskID: "task_9", TaskNumber: 9},
		TargetMode: "append",
	})
	clickTarget(t, h, card, "confirm", nil)

	active, ok := h.store.ActiveTarget("oc_chat")
	if !ok || active.TaskID != "task_9" {
		t.Fatalf("瞬时失败必须保留活跃目标：%+v ok=%v", active, ok)
	}
	settled, _ := h.cards.Get(card.ID)
	if settled.State != StateOpen || !strings.Contains(settled.LastError, "暂时追加不上") {
		t.Fatalf("瞬时失败应重开卡片并写明可重试：%+v", settled)
	}
	if got := h.store.records["om_tr"].Status; got != "confirming" {
		t.Fatalf("瞬时失败应回滚认领到 confirming，实际 %q", got)
	}
}

// TestTargetClickFailsWhenActiveTargetMoved proves a stale card snapshot is
// rejected: confirming card B after the active target moved to a different task
// must not create a second task.
func TestTargetClickFailsWhenActiveTargetMoved(t *testing.T) {
	now := fixedNow()
	clock := func() time.Time { return now }
	h := newTargetHarnessClock(t, Config{}, intakeCatalog{}, staticTasks{}, clock)

	cardB := h.notifyTarget(t, Notice{
		CardID: "tgt-b", SourceMessageID: "om_b", IntakeText: "B 的需求",
		Target: &TargetRef{NodeID: "fn", RootID: "r1", ProjectLabel: "docs · fn"}, TargetMode: "create",
	})
	// After B was sent, another card set the chat's target to task_A.
	now = now.Add(time.Minute)
	if err := h.store.SetActiveTarget(ActiveTarget{
		ChatID: "oc_chat", NodeID: "fn", RootID: "r1", TaskID: "task_A", TaskNumber: 1,
	}); err != nil {
		t.Fatal(err)
	}
	clickTarget(t, h, cardB, "confirm", nil)

	if len(h.rec.created) != 0 {
		t.Fatalf("过期快照不得再建一个任务：%+v", h.rec.created)
	}
	settled, _ := h.cards.Get(cardB.ID)
	if settled.State != StateFailed || !strings.Contains(settled.Summary, "目标已变化") {
		t.Fatalf("目标已变时必须 fail closed：%+v", settled)
	}
}
