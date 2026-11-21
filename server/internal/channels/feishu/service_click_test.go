package feishu

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type pickerTestHarness struct {
	svc        *Service
	cards      *Store
	intake     *IntakeStore
	intakePath string
	logPath    string
}

func newPickerTestHarness(t *testing.T) *pickerTestHarness {
	t.Helper()
	svc, cards, logPath := newTestService(t, Config{OwnerOpenID: "ou_owner"})
	intakePath := filepath.Join(t.TempDir(), "intake.json")
	store, err := NewIntakeStore(intakePath, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	// T4 already recorded the inbound message as a picker; the click must
	// transition that record instead of calling the first-write-wins Record.
	if err := store.Record("om_src", "", "picker"); err != nil {
		t.Fatal(err)
	}
	return &pickerTestHarness{svc: svc, cards: cards, intake: store, intakePath: intakePath, logPath: logPath}
}

func (h *pickerTestHarness) sendCard(t *testing.T) {
	t.Helper()
	if err := h.svc.Notify(context.Background(), pickerNotice("pick-1")); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.DeliverOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(h.logPath, 0); err != nil {
		t.Fatal(err)
	}
}

func pickerNotice(id string) Notice {
	return Notice{
		CardID:          id,
		Kind:            KindPicker,
		ChatID:          "oc_chat",
		SourceMessageID: "om_src",
		IntakeText:      "登录问题放哪个项目？",
		Projects: []PickerProject{
			{NodeID: "fn", RootID: "docs", Label: "docs · fn"},
			{NodeID: "pc", RootID: "proj", Label: "proj · pc"},
		},
		PickerTTL: PickerTTL,
	}
}

type pickerCreatorFunc func(context.Context, string, string, string) (string, error)

func (fn pickerCreatorFunc) Create(ctx context.Context, nodeID, rootID, input string) (string, error) {
	return fn(ctx, nodeID, rootID, input)
}

func pickerRecord(t *testing.T, path, messageID string) intakeRecord {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored intakeStoreFile
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("intake store %s: %v", raw, err)
	}
	for _, rec := range stored.Records {
		if rec.MessageID == messageID {
			return rec
		}
	}
	t.Fatalf("intake record %s missing: %s", messageID, raw)
	return intakeRecord{}
}

func TestPickerClickCreatesTaskOnChosenNode(t *testing.T) {
	h := newPickerTestHarness(t)
	h.sendCard(t)

	type createCall struct{ nodeID, rootID, input string }
	var calls []createCall
	h.svc.SetPickerTaskDeps(h.intake, pickerCreatorFunc(func(_ context.Context, nodeID, rootID, input string) (string, error) {
		appendLine(t, h.logPath, "create", nil)
		calls = append(calls, createCall{nodeID: nodeID, rootID: rootID, input: input})
		return "task_picker_1", nil
	}))
	handled := 0
	h.svc.SetAnswerHandler(func(context.Context, Answer) error {
		handled++
		return nil
	})

	if err := h.svc.HandleAction(context.Background(), Action{
		CardID: "pick-1", Choice: 0, OperatorID: "ou_owner", MessageID: "om_1",
	}); err != nil {
		t.Fatal(err)
	}

	if len(calls) != 1 || calls[0] != (createCall{nodeID: "fn", rootID: "docs", input: "登录问题放哪个项目？"}) {
		t.Fatalf("creator calls = %+v", calls)
	}
	joined := readText(t, h.logPath)
	ackAt := strings.Index(joined, "messages patch")
	createAt := strings.Index(joined, "create")
	if ackAt < 0 || createAt < 0 || ackAt > createAt {
		t.Fatalf("回执必须先于建任务：\n%s", joined)
	}
	if strings.Count(joined, "messages patch") < 2 {
		t.Fatalf("回执 + 终态各应 patch 一次：\n%s", joined)
	}
	if handled != 0 {
		t.Fatalf("picker must not reach the gate/ask answer handler: %d", handled)
	}
	if c, ok := h.cards.Get("pick-1"); !ok || c.State != StateDone {
		t.Fatalf("card = %+v", c)
	}
	rec := pickerRecord(t, h.intakePath, "om_src")
	if rec.Status != "created" || rec.TaskID != "task_picker_1" {
		t.Fatalf("intake record = %+v", rec)
	}
}

func TestPickerClickIgnoresNonOwnerAndExpired(t *testing.T) {
	h := newPickerTestHarness(t)
	h.sendCard(t)

	calls := 0
	h.svc.SetPickerTaskDeps(h.intake, pickerCreatorFunc(func(context.Context, string, string, string) (string, error) {
		calls++
		return "task_picker_1", nil
	}))

	if err := h.svc.HandleAction(context.Background(), Action{
		CardID: "pick-1", Choice: 0, OperatorID: "ou_someone_else", MessageID: "om_1",
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("non-owner click created %d tasks", calls)
	}
	if c, _ := h.cards.Get("pick-1"); c.State != StateOpen {
		t.Fatalf("non-owner click changed state to %s", c.State)
	}

	if _, _, err := h.cards.Update("pick-1", func(c *Card) bool {
		c.ExpiresAt = fixedNow().Add(-time.Minute)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.HandleAction(context.Background(), Action{
		CardID: "pick-1", Choice: 0, OperatorID: "ou_owner", MessageID: "om_1",
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("expired click created %d tasks", calls)
	}
	if c, _ := h.cards.Get("pick-1"); c.State != StateExpired {
		t.Fatalf("expired card state = %s", c.State)
	}
	if rec := pickerRecord(t, h.intakePath, "om_src"); rec.Status != "picker" {
		t.Fatalf("expired click changed intake record = %+v", rec)
	}
}

func TestPickerClickRejectsOutOfRangeSelection(t *testing.T) {
	h := newPickerTestHarness(t)
	h.sendCard(t)

	calls := 0
	h.svc.SetPickerTaskDeps(h.intake, pickerCreatorFunc(func(context.Context, string, string, string) (string, error) {
		calls++
		return "task_picker_1", nil
	}))

	if err := h.svc.HandleAction(context.Background(), Action{
		CardID: "pick-1", Choice: 2, OperatorID: "ou_owner", MessageID: "om_1",
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("out-of-range click created %d tasks", calls)
	}
	c, ok := h.cards.Get("pick-1")
	if !ok || c.State != StateFailed {
		t.Fatalf("card = %+v", c)
	}
	if !strings.Contains(c.Summary, "选择已失效") {
		t.Fatalf("summary = %q", c.Summary)
	}
	if rec := pickerRecord(t, h.intakePath, "om_src"); rec.Status != "picker" {
		t.Fatalf("out-of-range click changed intake record = %+v", rec)
	}

	// A stale click on an already-settled card must not reopen or overwrite it.
	if _, _, err := h.cards.Update("pick-1", func(x *Card) bool {
		x.State = StateDone
		x.Summary = "已完成"
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.HandleAction(context.Background(), Action{
		CardID: "pick-1", Choice: 99, OperatorID: "ou_owner", MessageID: "om_1",
	}); err != nil {
		t.Fatal(err)
	}
	if c, _ := h.cards.Get("pick-1"); c.State != StateDone || c.Summary != "已完成" {
		t.Fatalf("settled card was clobbered: %+v", c)
	}
}
