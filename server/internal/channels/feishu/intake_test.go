package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type intakeCatalog struct {
	refs []ProjectRef
	err  error
}

func (c intakeCatalog) List(context.Context) ([]ProjectRef, error) { return c.refs, c.err }

type intakeCreator struct {
	node, root, input string
	calls             int
	err               error
}

func (c *intakeCreator) Create(_ context.Context, nodeID, rootID, input string) (string, error) {
	c.calls++
	c.node, c.root, c.input = nodeID, rootID, input
	if c.err != nil {
		return "", c.err
	}
	return "task_created", nil
}

type intakeHarness struct {
	service  *IntakeService
	store    *IntakeStore
	creator  *intakeCreator
	notices  []Notice
	limiter  func(string) bool
	findOpen func(string, time.Time) (Card, bool)
}

func newIntakeHarness(t *testing.T, cfg Config, catalog ProjectCatalog) *intakeHarness {
	t.Helper()
	store, err := NewIntakeStore(filepath.Join(t.TempDir(), "intake.json"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	h := &intakeHarness{store: store, creator: &intakeCreator{}}
	clock := time.Now
	h.service = NewIntakeService(cfg, catalog, store, h.creator, func(_ context.Context, n Notice) error {
		h.notices = append(h.notices, n)
		return nil
	}, clock, func(key string) bool {
		if h.limiter == nil {
			return true
		}
		return h.limiter(key)
	})
	h.service.SetPickerFinder(func(chatID string, now time.Time) (Card, bool) {
		if h.findOpen == nil {
			return Card{}, false
		}
		return h.findOpen(chatID, now)
	})
	return h
}

func inbound(id, chatType, sender, text string) InboundMessage {
	return InboundMessage{
		EventID: "ei_" + id, MessageID: id, ChatID: "oc_chat", ChatType: chatType,
		SenderOpenID: sender, Text: text,
	}
}

func TestIntakeIgnoresNonOwnerGroupAndNonText(t *testing.T) {
	h := newIntakeHarness(t, Config{OwnerOpenID: "ou_owner"}, intakeCatalog{})
	for _, msg := range []InboundMessage{
		inbound("om_1", "group", "ou_owner", "do it"),
		inbound("om_2", "p2p", "ou_other", "do it"),
		inbound("om_3", "p2p", "ou_owner", ""),
	} {
		if err := h.service.Handle(context.Background(), msg); err != nil {
			t.Fatal(err)
		}
	}
	if h.creator.calls != 0 || len(h.notices) != 0 {
		t.Fatalf("非 owner / 群聊 / 非文本必须忽略：calls=%d notices=%d", h.creator.calls, len(h.notices))
	}
}

func TestIntakeDedupAndRateLimit(t *testing.T) {
	h := newIntakeHarness(t, Config{
		OwnerOpenID: "ou_owner", Bindings: []Binding{{ChatID: "oc_chat", NodeID: "fn", RootID: "r1"}},
	}, intakeCatalog{refs: []ProjectRef{{NodeID: "fn", RootID: "r1", Name: "docs · fn"}}})
	msg := inbound("om_1", "p2p", "ou_owner", "fix the docs")
	if err := h.service.Handle(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if err := h.service.Handle(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if h.creator.calls != 1 || len(h.notices) != 1 {
		t.Fatalf("同一 message_id 只能处理一次：calls=%d notices=%d", h.creator.calls, len(h.notices))
	}
	if h.notices[0].SourceMessageID != "om_1" {
		t.Errorf("成功回执必须带原消息 id：%+v", h.notices[0])
	}

	h.limiter = func(string) bool { return false }
	if err := h.service.Handle(context.Background(), inbound("om_2", "p2p", "ou_owner", "fix")); err != nil {
		t.Fatalf("限流是已处理的拒绝，不应让 supervisor 记错：%v", err)
	}
	if h.creator.calls != 1 || len(h.notices) != 2 {
		t.Fatalf("限流后不能建任务，但必须给 owner 回执：calls=%d notices=%d", h.creator.calls, len(h.notices))
	}
	if !strings.Contains(h.notices[1].Text, "稍后") {
		t.Errorf("限流回执文案不对：%q", h.notices[1].Text)
	}
}

func TestIntakeCreatesTaskFromBindingAndCapsInput(t *testing.T) {
	h := newIntakeHarness(t, Config{
		OwnerOpenID: "ou_owner", Bindings: []Binding{{ChatID: "oc_chat", NodeID: "fn", RootID: "r1"}},
	}, intakeCatalog{refs: []ProjectRef{{NodeID: "fn", RootID: "r1", Name: "docs · fn"}}})
	long := strings.Repeat("a", intakeMaxInputBytes+1000)
	if err := h.service.Handle(context.Background(), inbound("om_1", "p2p", "ou_owner", long)); err != nil {
		t.Fatal(err)
	}
	if h.creator.calls != 1 || h.creator.node != "fn" || h.creator.root != "r1" {
		t.Fatalf("绑定没有落到建任务参数：%+v", h.creator)
	}
	if !strings.HasPrefix(h.creator.input, strings.Repeat("a", intakeMaxInputBytes)) ||
		!strings.Contains(h.creator.input, "截断") {
		t.Fatalf("超长输入应截到 32 KiB 并明说被截断，实际长度 %d", len(h.creator.input))
	}
	if !h.store.Seen("om_1") {
		t.Fatal("建任务后必须记去重")
	}
}

func TestIntakePickerAndOneOpenPickerPerChat(t *testing.T) {
	h := newIntakeHarness(t, Config{OwnerOpenID: "ou_owner"}, intakeCatalog{refs: []ProjectRef{
		{NodeID: "fn", RootID: "r1", Name: "docs · fn"},
		{NodeID: "", RootID: "r2", Name: "notes"},
	}})
	if err := h.service.Handle(context.Background(), inbound("om_1", "p2p", "ou_owner", "please fix notes and docs")); err != nil {
		t.Fatal(err)
	}
	if h.creator.calls != 0 || len(h.notices) != 1 || h.notices[0].Kind != KindPicker {
		t.Fatalf("两个候选应发选择卡：%+v", h.notices)
	}
	if len(h.notices[0].Projects) != 2 || h.notices[0].SourceMessageID != "om_1" ||
		h.notices[0].IntakeText != "please fix notes and docs" {
		t.Fatalf("选择卡必须带候选和原消息：%+v", h.notices[0])
	}
	if h.notices[0].CardID == "" || len(h.notices[0].CardID) > 128 {
		t.Errorf("选择卡 id 应当由消息确定性派生且不能过长：%q", h.notices[0].CardID)
	}

	h.findOpen = func(string, time.Time) (Card, bool) {
		return Card{ID: h.notices[0].CardID, Kind: KindPicker, State: StateOpen}, true
	}
	if err := h.service.Handle(context.Background(), inbound("om_2", "p2p", "ou_owner", "fix notes and docs again")); err != nil {
		t.Fatal(err)
	}
	if len(h.notices) != 2 || h.notices[1].Kind == KindPicker {
		t.Fatalf("同一 chat 已有未答选择卡时不能再发第二张：%+v", h.notices)
	}
	if !strings.Contains(h.notices[1].Text, "选择") {
		t.Errorf("应提示 owner 先完成选择：%+v", h.notices[1])
	}
}

func TestIntakeTooManyCandidatesGetsAnswer(t *testing.T) {
	refs := []ProjectRef{}
	for i := 0; i < 6; i++ {
		refs = append(refs, ProjectRef{NodeID: "fn", RootID: "r" + string(rune('a'+i)), Name: "docs · fn"})
	}
	h := newIntakeHarness(t, Config{OwnerOpenID: "ou_owner"}, intakeCatalog{refs: refs})
	if err := h.service.Handle(context.Background(), inbound("om_1", "p2p", "ou_owner", "docs")); err != nil {
		t.Fatal(err)
	}
	if len(h.notices) != 1 || h.notices[0].Kind == KindPicker ||
		!strings.Contains(h.notices[0].Text, "具体") {
		t.Fatalf("超过 5 个候选只能回文字，不能截断发卡：%+v", h.notices)
	}
}

func TestIntakeLocalProjectUsesConfiguredNodeID(t *testing.T) {
	h := newIntakeHarness(t, Config{
		OwnerOpenID: "ou_owner", NodeID: "home", DefaultRootID: "r_local", PickerMax: 2,
	}, intakeCatalog{refs: []ProjectRef{
		{NodeID: "", RootID: "r_local", Name: "notes"},
		{NodeID: "", RootID: "r_other", Name: "journal"},
	}})
	if err := h.service.Handle(context.Background(), inbound("om_1", "p2p", "ou_owner", "do it")); err != nil {
		t.Fatal(err)
	}
	if h.creator.node != "home" || h.creator.root != "r_local" {
		t.Fatalf("本地项目的 node_id 必须对齐 feishu_node_id：%+v", h.creator)
	}

	// PickerMax is a hard ceiling, not a truncation of the choice list.
	h2 := newIntakeHarness(t, Config{OwnerOpenID: "ou_owner", NodeID: "home", PickerMax: 1},
		intakeCatalog{refs: []ProjectRef{
			{NodeID: "", RootID: "r_local", Name: "notes"},
			{NodeID: "", RootID: "r_other", Name: "journal"},
		}})
	if err := h2.service.Handle(context.Background(), inbound("om_1", "p2p", "ou_owner", "notes journal")); err != nil {
		t.Fatal(err)
	}
	if len(h2.notices) != 1 || h2.notices[0].Kind == KindPicker {
		t.Fatalf("候选超过 PickerMax 只能回文字：%+v", h2.notices)
	}
}

func TestIntakeErrorsAreVisibleAndReturned(t *testing.T) {
	t.Run("creation", func(t *testing.T) {
		h := newIntakeHarness(t, Config{
			OwnerOpenID: "ou_owner", Bindings: []Binding{{ChatID: "oc_chat", NodeID: "fn", RootID: "r1"}},
		}, intakeCatalog{refs: []ProjectRef{{NodeID: "fn", RootID: "r1", Name: "docs"}}})
		h.creator.err = errors.New("boom")
		if err := h.service.Handle(context.Background(), inbound("om_1", "p2p", "ou_owner", "do it")); err == nil {
			t.Fatal("建任务失败必须返回 error 供 supervisor 记日志")
		}
		if len(h.notices) != 1 || !strings.Contains(h.notices[0].Text, "失败") {
			t.Fatalf("建任务失败必须让 owner 看见：%+v", h.notices)
		}
		if !h.store.Seen("om_1") {
			t.Fatal("失败消息也应记去重，避免重放造成重复回执")
		}
	})
	t.Run("catalog", func(t *testing.T) {
		h := newIntakeHarness(t, Config{OwnerOpenID: "ou_owner"}, intakeCatalog{err: errors.New("offline")})
		if err := h.service.Handle(context.Background(), inbound("om_2", "p2p", "ou_owner", "do it")); err == nil {
			t.Fatal("项目目录不可用必须返回 error")
		}
		if len(h.notices) != 1 || !strings.Contains(h.notices[0].Text, "项目") {
			t.Fatalf("未知项目必须有用户可见回执：%+v", h.notices)
		}
	})
}

func TestIntakeRateLimiterBurstAndRefill(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	advance := time.Duration(0)
	allow := NewInboundLimiter(60, 2, func() time.Time { return now.Add(advance) })
	if !allow("chat") || !allow("chat") || allow("chat") {
		t.Fatal("burst=2：第三次应被限流")
	}
	if !allow("other") {
		t.Fatal("另一个 chat 的桶应独立")
	}
	advance = time.Second
	if !allow("chat") {
		t.Fatal("每秒补 1 个 token，1 秒后应放行")
	}
}

func TestServiceNotifyCarriesPickerFieldsToCard(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	store := NewStore(filepath.Join(t.TempDir(), "cards.json"))
	svc := New(Config{Now: func() time.Time { return now }}, store, nil)
	n := Notice{
		CardID: "intake-picker-1", Kind: KindPicker, ChatID: "oc_chat",
		SourceMessageID: "om_1", IntakeText: "修一下",
		Projects:  []PickerProject{{NodeID: "fn", RootID: "r1", Label: "docs · fn"}},
		PickerTTL: 24 * time.Hour,
	}
	if err := svc.Notify(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	card, ok := store.Get(n.CardID)
	if !ok || card.ChatID != "oc_chat" || card.SourceMessageID != "om_1" ||
		card.IntakeText != "修一下" || len(card.Projects) != 1 {
		t.Fatalf("选择卡丢了入站字段：%+v", card)
	}
	if !card.ExpiresAt.Equal(now.Add(24 * time.Hour)) {
		t.Errorf("选择卡 TTL 应取 Notice.PickerTTL：%v", card.ExpiresAt)
	}
	if found, ok := store.FindOpenPickerByChat("oc_chat"); !ok || found.ID != n.CardID {
		t.Fatalf("同一 chat 的未答选择卡查不到：%+v", found)
	}
}

func TestServiceRoutesSupervisorMessageToIntake(t *testing.T) {
	h := newIntakeHarness(t, Config{
		OwnerOpenID: "ou_owner", Bindings: []Binding{{ChatID: "oc_p2p_1", NodeID: "fn", RootID: "r1"}},
	}, intakeCatalog{})
	svc := &Service{}
	svc.SetIntake(h.service)
	line, err := os.ReadFile("testdata/message_p2p.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.handleRawMessage(context.Background(), line); err != nil {
		t.Fatal(err)
	}
	if h.creator.calls != 1 || h.creator.node != "fn" {
		t.Fatalf("supervisor 回调没有接到入站服务：%+v", h.creator)
	}
}

func TestRemoteTaskBodyMatchesTaskCreationContract(t *testing.T) {
	body, err := RemoteTaskBody("root-1", "请处理", "飞书任务")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"root_id":           "root-1",
		"input":             "请处理",
		"name":              "飞书任务",
		"workflow_choice":   "empty",
		"start_immediately": true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("远端建任务载荷形状错误：got %#v want %#v", got, want)
	}
}

func TestRemoteTaskCreationUsesSharedBody(t *testing.T) {
	src, err := os.ReadFile("../../../app/feishu_intake.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "feishu.RemoteTaskBody(") {
		t.Fatal("远端建任务必须走 RemoteTaskBody，避免载荷形状漂移")
	}
}
