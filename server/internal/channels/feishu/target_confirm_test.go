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

// staticTasks is a TaskCatalog that answers the same list every time, so a
// confirmation card's candidates are exactly what the test declared.
type staticTasks struct {
	refs []TaskRef
	err  error
}

func (s staticTasks) Match(context.Context, string, string, string) ([]TaskRef, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.refs, nil
}

type targetCall struct {
	NodeID, RootID, TaskID, Input string
	TaskNumber                    int
}

// targetRecorder is both the task creator and the appender a confirmation card
// uses. Splitting them here is what lets a test assert that nothing was created
// before the owner confirmed.
type targetRecorder struct {
	created   []targetCall
	appended  []targetCall
	createID  string
	createErr error
	appendErr error
	onCreate  func() // lets a test inspect state just before the side effect
	onAppend  func()
}

func (r *targetRecorder) Create(_ context.Context, nodeID, rootID, input string) (string, error) {
	if r.onCreate != nil {
		r.onCreate()
	}
	if r.createErr != nil {
		return "", r.createErr
	}
	r.created = append(r.created, targetCall{NodeID: nodeID, RootID: rootID, Input: input})
	if r.createID != "" {
		return r.createID, nil
	}
	return "task_new", nil
}

func (r *targetRecorder) Append(_ context.Context, target TargetRef, input string) error {
	if r.onAppend != nil {
		r.onAppend()
	}
	if r.appendErr != nil {
		return r.appendErr
	}
	r.appended = append(r.appended, targetCall{
		NodeID: target.NodeID, RootID: target.RootID, TaskID: target.TaskID,
		TaskNumber: target.TaskNumber, Input: input,
	})
	return nil
}

type targetHarness struct {
	svc    *Service
	cards  *Store
	intake *IntakeService
	store  *IntakeStore
	rec    *targetRecorder
}

func newTargetHarness(t *testing.T, cfg Config, catalog ProjectCatalog, tasks TaskCatalog) *targetHarness {
	t.Helper()
	clock := func() time.Time { return fixedNow() }
	return newTargetHarnessClock(t, cfg, catalog, tasks, clock)
}

// newTargetHarnessClock builds the harness around one clock so the card store,
// the intake ledger and the service all agree on "now". Tests that need to move
// time (stale-snapshot checks) capture and advance a closure.
func newTargetHarnessClock(t *testing.T, cfg Config, catalog ProjectCatalog, tasks TaskCatalog, clock func() time.Time) *targetHarness {
	t.Helper()
	cfg.OwnerOpenID = "ou_owner"
	cfg.Now = clock
	svc, cards, _ := newTestService(t, cfg)
	// The card store has its own clock separate from Config.Now; point it at the
	// same source so CreatedAt/ExpiresAt compare cleanly with the active target.
	cards.now = clock
	store, err := NewIntakeStore(filepath.Join(t.TempDir(), "intake.json"), clock)
	if err != nil {
		t.Fatal(err)
	}
	rec := &targetRecorder{}
	h := &targetHarness{svc: svc, cards: cards, store: store, rec: rec}
	h.intake = NewIntakeService(cfg, catalog, store, rec, svc.Notify, clock, func(string) bool { return true })
	h.intake.SetTargetDeps(rec, tasks)
	h.intake.SetTargetFinder(func(chatID string, _ time.Time) (Card, bool) {
		return cards.FindOpenTargetByChat(chatID)
	})
	svc.SetPickerTaskDeps(store, rec)
	svc.SetTargetTaskDeps(rec, tasks)
	return h
}

func (h *targetHarness) cardsOfKind(kind Kind) []Card {
	var out []Card
	for _, c := range h.cards.List() {
		if c.Kind == kind {
			out = append(out, c)
		}
	}
	return out
}

// notifyTarget puts a confirmation card straight into the card store, the way
// production does after the intake resolved a project. It records the intake
// record as "confirming" so a later click can claim it (a real card is always
// preceded by that transition).
func (h *targetHarness) notifyTarget(t *testing.T, n Notice) Card {
	t.Helper()
	if n.Kind == "" {
		n.Kind = KindTarget
	}
	if n.ChatID == "" {
		n.ChatID = "oc_chat"
	}
	if err := h.svc.Notify(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	if n.SourceMessageID != "" && h.store != nil {
		if err := h.store.SetStatus(n.SourceMessageID, "", "confirming"); err != nil {
			t.Fatal(err)
		}
	}
	c, ok := h.cards.Get(n.CardID)
	if !ok {
		t.Fatalf("确认卡 %s 没有落库", n.CardID)
	}
	return c
}

func choiceIndex(c Card, value string) int {
	for i, ch := range c.Choices {
		if ch.Value == value {
			return i
		}
	}
	return -1
}

func clickTarget(t *testing.T, h *targetHarness, c Card, value string, fields map[string]string) {
	t.Helper()
	i := choiceIndex(c, value)
	if i < 0 {
		t.Fatalf("确认卡没有「%s」这个动作：%+v", value, c.Choices)
	}
	if err := h.svc.HandleAction(context.Background(), Action{
		CardID: c.ID, Choice: i, OperatorID: "ou_owner", Fields: fields,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTargetConfirmCardThenClickCreatesTask(t *testing.T) {
	h := newTargetHarness(t, Config{
		Bindings: []Binding{{ChatID: "oc_chat", NodeID: "fn", RootID: "r1"}},
	}, intakeCatalog{refs: []ProjectRef{{NodeID: "fn", RootID: "r1", Name: "docs"}}}, staticTasks{})

	msg := inbound("om_t1", "p2p", "ou_owner", "修一下登录页")
	if err := h.intake.Handle(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if len(h.rec.created) != 0 {
		t.Fatalf("确认前不得建任务：%+v", h.rec.created)
	}
	targets := h.cardsOfKind(KindTarget)
	if len(targets) != 1 {
		t.Fatalf("唯一识别应发一张确认卡，got %d", len(targets))
	}
	card := targets[0]
	if card.TargetMode != "create" {
		t.Fatalf("没识别到任务时默认「新建」：%+v", card)
	}
	if card.IntakeText != "修一下登录页" {
		t.Fatalf("确认卡必须带原消息：%q", card.IntakeText)
	}
	if card.Target == nil || card.Target.NodeID != "fn" || card.Target.RootID != "r1" {
		t.Fatalf("确认卡目标 = %+v", card.Target)
	}
	if got := h.store.records["om_t1"].Status; got != "confirming" {
		t.Fatalf("确认卡发出后入站状态 = %q, want confirming", got)
	}

	clickTarget(t, h, card, "confirm", nil)
	if len(h.rec.created) != 1 {
		t.Fatalf("点确认后应建一个任务：%+v", h.rec.created)
	}
	if got := h.rec.created[0]; got.NodeID != "fn" || got.RootID != "r1" || got.Input != "修一下登录页" {
		t.Fatalf("建任务参数 = %+v", got)
	}
	active, ok := h.store.ActiveTarget("oc_chat")
	if !ok || active.TaskID != "task_new" || active.NodeID != "fn" || active.RootID != "r1" {
		t.Fatalf("确认后必须记住活跃目标：%+v ok=%v", active, ok)
	}
	settled, _ := h.cards.Get(card.ID)
	if settled.State != StateDone {
		t.Fatalf("确认后卡片应收尾：%+v", settled)
	}
	if got := h.store.records["om_t1"]; got.Status != "created" || got.TaskID != "task_new" {
		t.Fatalf("入站记录 = %+v", got)
	}
}

func TestTargetCardTypedTaskNumberAppendsThere(t *testing.T) {
	h := newTargetHarness(t, Config{}, intakeCatalog{}, staticTasks{})
	card := h.notifyTarget(t, Notice{
		CardID: "tgt-typed", SourceMessageID: "om_t3", IntakeText: "补一句要支持微信",
		Target:     &TargetRef{NodeID: "fn", RootID: "r1", ProjectLabel: "docs · fn"},
		Candidates: []TargetRef{{NodeID: "fn", RootID: "r1", TaskID: "task_seven", TaskNumber: 7, TaskTitle: "登录"}, {NodeID: "fn", RootID: "r1", TaskID: "task_eight", TaskNumber: 8}},
		TargetMode: "create", PickerTTL: PickerTTL,
	})
	clickTarget(t, h, card, "confirm", map[string]string{"task_no": "#7"})

	if len(h.rec.created) != 0 {
		t.Fatalf("写了任务号就应追加而不是新建：%+v", h.rec.created)
	}
	if len(h.rec.appended) != 1 || h.rec.appended[0].TaskID != "task_seven" {
		t.Fatalf("编辑任务号没有切换目标：%+v", h.rec.appended)
	}
	if got := h.store.records["om_t3"].Status; got != "appended" {
		t.Fatalf("入站状态 = %q, want appended", got)
	}
	active, ok := h.store.ActiveTarget("oc_chat")
	if !ok || active.TaskID != "task_seven" || active.TaskNumber != 7 {
		t.Fatalf("活跃目标 = %+v ok=%v", active, ok)
	}
}

func TestTargetTypedUnknownTaskNumberFails(t *testing.T) {
	h := newTargetHarness(t, Config{}, intakeCatalog{}, staticTasks{})
	card := h.notifyTarget(t, Notice{
		CardID: "tgt-unknown", SourceMessageID: "om_t3b", IntakeText: "补一句",
		Target:     &TargetRef{NodeID: "fn", RootID: "r1", ProjectLabel: "docs · fn"},
		Candidates: []TargetRef{{NodeID: "fn", RootID: "r1", TaskID: "task_seven", TaskNumber: 7}},
		TargetMode: "create",
	})
	clickTarget(t, h, card, "confirm", map[string]string{"task_no": "#99"})
	if len(h.rec.created) != 0 || len(h.rec.appended) != 0 {
		t.Fatalf("认不出的任务号不得建/追加：created=%+v appended=%+v", h.rec.created, h.rec.appended)
	}
	settled, _ := h.cards.Get(card.ID)
	if settled.State != StateFailed || !strings.Contains(settled.Summary, "#99") {
		t.Fatalf("应明确告诉 owner 没找到这个任务号：%+v", settled)
	}
}

func TestTargetExpiredClickChangesNothing(t *testing.T) {
	h := newTargetHarness(t, Config{}, intakeCatalog{}, staticTasks{})
	card := h.notifyTarget(t, Notice{
		CardID: "tgt-exp", SourceMessageID: "om_t5", IntakeText: "过期",
		Target: &TargetRef{NodeID: "fn", RootID: "r1", ProjectLabel: "docs · fn"}, TargetMode: "create",
	})
	if _, _, err := h.cards.Update(card.ID, func(c *Card) bool {
		c.ExpiresAt = fixedNow().Add(-time.Minute)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	clickTarget(t, h, card, "confirm", nil)
	if len(h.rec.created) != 0 || len(h.rec.appended) != 0 {
		t.Fatalf("过期卡点击不得有副作用：created=%+v appended=%+v", h.rec.created, h.rec.appended)
	}
	settled, _ := h.cards.Get(card.ID)
	if settled.State != StateExpired {
		t.Fatalf("过期卡状态 = %q, want expired", settled.State)
	}
}

func TestTargetClickIgnoresNonOwner(t *testing.T) {
	h := newTargetHarness(t, Config{}, intakeCatalog{}, staticTasks{})
	card := h.notifyTarget(t, Notice{
		CardID: "tgt-owner", SourceMessageID: "om_t6", IntakeText: "不是主人点的",
		Target: &TargetRef{NodeID: "fn", RootID: "r1"}, TargetMode: "create",
	})
	if err := h.svc.HandleAction(context.Background(), Action{
		CardID: card.ID, Choice: choiceIndex(card, "confirm"), OperatorID: "ou_other",
	}); err != nil {
		t.Fatal(err)
	}
	if len(h.rec.created) != 0 {
		t.Fatalf("非 owner 点击不得建任务：%+v", h.rec.created)
	}
	after, _ := h.cards.Get(card.ID)
	if after.State != StateOpen || after.Selected != -1 {
		t.Fatalf("非 owner 点击不得改动卡片：%+v", after)
	}
}

func TestTargetAppendFailureClearsActiveAndFailsCard(t *testing.T) {
	h := newTargetHarness(t, Config{}, intakeCatalog{}, staticTasks{})
	if err := h.store.SetActiveTarget(ActiveTarget{
		ChatID: "oc_chat", NodeID: "fn", RootID: "r1", TaskID: "task_9", TaskNumber: 9,
	}); err != nil {
		t.Fatal(err)
	}
	h.rec.appendErr = fmt.Errorf("task moved: %w", ErrTargetStale)
	card := h.notifyTarget(t, Notice{
		CardID: "tgt-append-fail", SourceMessageID: "om_t7", IntakeText: "追加到老任务",
		Target:     &TargetRef{NodeID: "fn", RootID: "r1", TaskID: "task_9", TaskNumber: 9, TaskTitle: "登录"},
		TargetMode: "append",
	})
	clickTarget(t, h, card, "confirm", nil)

	settled, _ := h.cards.Get(card.ID)
	if settled.State != StateFailed || !strings.Contains(settled.Summary, "目标已失效") {
		t.Fatalf("追加失败要明确告知目标已失效：%+v", settled)
	}
	if _, ok := h.store.ActiveTarget("oc_chat"); ok {
		t.Fatal("三元组校验失败后必须清掉活跃目标，否则后续消息继续打空目标")
	}
	if len(h.rec.created) != 0 {
		t.Fatalf("追加失败不得悄悄改成新建：%+v", h.rec.created)
	}
}

func TestTargetCreateFailureFailsCard(t *testing.T) {
	h := newTargetHarness(t, Config{}, intakeCatalog{}, staticTasks{})
	h.rec.createErr = errors.New("remote node offline")
	card := h.notifyTarget(t, Notice{
		CardID: "tgt-create-fail", SourceMessageID: "om_t11", IntakeText: "建不出来",
		Target: &TargetRef{NodeID: "fn", RootID: "r1", ProjectLabel: "docs · fn"}, TargetMode: "create",
	})
	clickTarget(t, h, card, "confirm", nil)

	settled, _ := h.cards.Get(card.ID)
	if settled.State != StateFailed || !strings.Contains(settled.Summary, "建任务失败") {
		t.Fatalf("建任务失败必须让 owner 看见：%+v", settled)
	}
	if len(h.rec.created) != 0 {
		t.Fatalf("失败发生在远端调用里，不能既报错又记成功：%+v", h.rec.created)
	}
	if got := h.store.records["om_t11"].Status; got == "created" {
		t.Fatalf("建任务失败不能把入站记录标成 created：%q", got)
	}
}

func TestTargetSwitchRefreshesCandidates(t *testing.T) {
	tasks := staticTasks{refs: []TaskRef{
		{NodeID: "fn", RootID: "r1", TaskID: "task_x", TaskNumber: 11, Title: "新的候选"},
	}}
	h := newTargetHarness(t, Config{}, intakeCatalog{}, tasks)
	card := h.notifyTarget(t, Notice{
		CardID: "tgt-switch", SourceMessageID: "om_t8", IntakeText: "换个目标",
		Target: &TargetRef{NodeID: "fn", RootID: "r1", ProjectLabel: "docs · fn"}, TargetMode: "create",
	})
	clickTarget(t, h, card, "switch", nil)

	refreshed, _ := h.cards.Get(card.ID)
	if refreshed.State != StateOpen || refreshed.Selected != -1 {
		t.Fatalf("切换目标后卡片必须回到可点状态：%+v", refreshed)
	}
	if len(refreshed.Candidates) != 1 || refreshed.Candidates[0].TaskNumber != 11 ||
		refreshed.Candidates[0].TaskID != "task_x" {
		t.Fatalf("候选没有刷新：%+v", refreshed.Candidates)
	}
	if !strings.Contains(refreshed.Text, "已刷新候选") {
		t.Fatalf("刷新后要告诉 owner 可以重新确认：%q", refreshed.Text)
	}
	if len(h.rec.created) != 0 || len(h.rec.appended) != 0 {
		t.Fatalf("切换目标本身不建也不追加：%+v %+v", h.rec.created, h.rec.appended)
	}
}

func TestTargetCancelLeavesActiveTargetAlone(t *testing.T) {
	h := newTargetHarness(t, Config{}, intakeCatalog{}, staticTasks{})
	if err := h.store.SetActiveTarget(ActiveTarget{
		ChatID: "oc_chat", NodeID: "fn", RootID: "r1", TaskID: "task_9", TaskNumber: 9,
	}); err != nil {
		t.Fatal(err)
	}
	card := h.notifyTarget(t, Notice{
		CardID: "tgt-cancel", SourceMessageID: "om_t9", IntakeText: "算了",
		Target: &TargetRef{NodeID: "fn", RootID: "r1"}, TargetMode: "create",
	})
	clickTarget(t, h, card, "cancel", nil)

	settled, _ := h.cards.Get(card.ID)
	if settled.State != StateDone || !strings.Contains(settled.Summary, "取消") {
		t.Fatalf("取消后卡片应说明没有建任务：%+v", settled)
	}
	if len(h.rec.created) != 0 || len(h.rec.appended) != 0 {
		t.Fatalf("取消不得有副作用：%+v %+v", h.rec.created, h.rec.appended)
	}
	active, ok := h.store.ActiveTarget("oc_chat")
	if !ok || active.TaskID != "task_9" {
		t.Fatalf("取消本次不该动既有的活跃目标：%+v ok=%v", active, ok)
	}
}

func TestTargetCardOffersFourChoicesAndTaskNumberForm(t *testing.T) {
	h := newTargetHarness(t, Config{}, intakeCatalog{}, staticTasks{})
	card := h.notifyTarget(t, Notice{
		CardID: "tgt-shape", SourceMessageID: "om_t10", IntakeText: "看清楚再点",
		Target:     &TargetRef{NodeID: "fn", RootID: "r1", TaskNumber: 42, TaskID: "task_42", TaskTitle: "登录", ProjectLabel: "docs · fn"},
		ReviewHits: []string{"代码变更"},
		TargetMode: "append", PickerTTL: PickerTTL,
	})
	want := []string{"confirm", "create", "switch", "cancel"}
	if len(card.Choices) != len(want) {
		t.Fatalf("确认卡应有四个动作：%+v", card.Choices)
	}
	for i, value := range want {
		if card.Choices[i].Value != value {
			t.Fatalf("动作顺序 = %+v", card.Choices)
		}
	}
	if got := card.ExpiresAt.Sub(fixedNow()); got != PickerTTL {
		t.Fatalf("确认卡有效期 = %s, want %s", got, PickerTTL)
	}
	content, err := RenderJSON(card)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"target_form_submit_tgt-shape", "task_no", "#42", "该项目需要审核", "docs · fn"} {
		if !strings.Contains(content, want) {
			t.Fatalf("确认卡渲染缺少 %q：\n%s", want, content)
		}
	}
}
