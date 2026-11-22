package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
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
	service   *IntakeService
	store     *IntakeStore
	creator   *intakeCreator
	notices   []Notice
	notifyErr error
	limiter   func(string) bool
	findOpen  func(string, time.Time) (Card, bool)
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
		if h.notifyErr != nil {
			return h.notifyErr
		}
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
	// 新语义：唯一识别只发一张确认卡，确认前不建任务。
	if h.creator.calls != 0 || len(h.notices) != 1 || h.notices[0].Kind != KindTarget {
		t.Fatalf("同一 message_id 只能处理一次：calls=%d notices=%+v", h.creator.calls, h.notices)
	}
	if h.notices[0].SourceMessageID != "om_1" {
		t.Errorf("确认卡必须带原消息 id：%+v", h.notices[0])
	}

	h.limiter = func(string) bool { return false }
	if err := h.service.Handle(context.Background(), inbound("om_2", "p2p", "ou_owner", "fix")); err != nil {
		t.Fatalf("限流是已处理的拒绝，不应让 supervisor 记错：%v", err)
	}
	if h.creator.calls != 0 || len(h.notices) != 2 {
		t.Fatalf("限流后不能建任务，但必须给 owner 回执：calls=%d notices=%d", h.creator.calls, len(h.notices))
	}
	if !strings.Contains(h.notices[1].Text, "稍后") {
		t.Errorf("限流回执文案不对：%q", h.notices[1].Text)
	}
}

func TestIntakeConfirmCardFromBindingAndCapsInput(t *testing.T) {
	h := newIntakeHarness(t, Config{
		OwnerOpenID: "ou_owner", Bindings: []Binding{{ChatID: "oc_chat", NodeID: "fn", RootID: "r1"}},
	}, intakeCatalog{refs: []ProjectRef{{NodeID: "fn", RootID: "r1", Name: "docs · fn"}}})
	long := strings.Repeat("a", intakeMaxInputBytes+1000)
	if err := h.service.Handle(context.Background(), inbound("om_1", "p2p", "ou_owner", long)); err != nil {
		t.Fatal(err)
	}
	// 绑定仍决定目标，只是现在要 owner 点「确认」才建任务。
	if h.creator.calls != 0 {
		t.Fatalf("确认前不得建任务：%+v", h.creator)
	}
	if len(h.notices) != 1 || h.notices[0].Kind != KindTarget || h.notices[0].Target == nil ||
		h.notices[0].Target.NodeID != "fn" || h.notices[0].Target.RootID != "r1" {
		t.Fatalf("绑定没有落到确认卡目标：%+v", h.notices)
	}
	got := h.notices[0].IntakeText
	if !strings.HasPrefix(got, strings.Repeat("a", intakeMaxInputBytes)) ||
		!strings.Contains(got, "截断") {
		t.Fatalf("超长输入应截到 32 KiB 并明说被截断，实际长度 %d", len(got))
	}
	if !h.store.Seen("om_1") {
		t.Fatal("确认卡发出后必须记去重，重投不能再发一张")
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
	if len(h.notices) != 1 || h.notices[0].Kind != KindTarget || h.notices[0].Target == nil ||
		h.notices[0].Target.NodeID != "home" || h.notices[0].Target.RootID != "r_local" {
		t.Fatalf("本地项目的 target 必须对齐 feishu_node_id：%+v", h.notices)
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
	// 唯一识别不再立即建任务，所以「建任务失败」的可见性现在属于确认卡点击
	// （见 target_confirm_test.go 的 TestTargetCreateFailureFailsCard）。
	t.Run("confirm_card_send", func(t *testing.T) {
		h := newIntakeHarness(t, Config{
			OwnerOpenID: "ou_owner", Bindings: []Binding{{ChatID: "oc_chat", NodeID: "fn", RootID: "r1"}},
		}, intakeCatalog{refs: []ProjectRef{{NodeID: "fn", RootID: "r1", Name: "docs"}}})
		h.notifyErr = errors.New("boom")
		if err := h.service.Handle(context.Background(), inbound("om_1", "p2p", "ou_owner", "do it")); err == nil {
			t.Fatal("确认卡发不出去必须返回 error 供 supervisor 记日志")
		}
		if h.creator.calls != 0 {
			t.Fatalf("卡片没发出去就建了任务：%+v", h.creator)
		}
		if h.store.Seen("om_1") {
			t.Fatal("卡片没发出去应释放占坑，让重投还能再试")
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
	// 服务与卡片库必须共用同一只时钟：服务按冻结时间盖 ExpiresAt，
	// 卡片库若按真实时间判断过期，这个用例会在 24h TTL 到期那一刻凭空变红。
	store.now = func() time.Time { return now }
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
	// 入站服务认出了绑定的项目并发出确认卡（确认前不建任务）。
	if len(h.notices) != 1 || h.notices[0].Kind != KindTarget || h.notices[0].Target == nil ||
		h.notices[0].Target.NodeID != "fn" {
		t.Fatalf("supervisor 回调没有接到入站服务：%+v", h.notices)
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

type countingCreator struct{ calls atomic.Int64 }

func (c *countingCreator) Create(context.Context, string, string, string) (string, error) {
	c.calls.Add(1)
	return "task-once", nil
}

func TestIntakeConcurrentRedeliverySendsOneConfirmCard(t *testing.T) {
	store, err := NewIntakeStore(filepath.Join(t.TempDir(), "intake.json"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	creator := &countingCreator{}
	var notices atomic.Int64
	svc := NewIntakeService(
		Config{OwnerOpenID: "ou_owner"},
		intakeCatalog{refs: []ProjectRef{{NodeID: "fn", RootID: "r1", Name: "docs"}}}, store, creator,
		func(context.Context, Notice) error { notices.Add(1); return nil }, time.Now, nil,
	)
	msg := inbound("om-dup", "p2p", "ou_owner", "fix docs")
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := svc.Handle(context.Background(), msg); err != nil {
				t.Errorf("Handle: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	// 确认前不建任务；并发重投只能发一张确认卡。
	if got := creator.calls.Load(); got != 0 {
		t.Fatalf("确认前不得建任务，got %d", got)
	}
	if got := notices.Load(); got != 1 {
		t.Fatalf("并发重投只应发一张确认卡，got %d", got)
	}
	rec := store.records["om-dup"]
	if rec.Status != "confirming" || rec.TaskID != "" {
		t.Fatalf("去重记录 = %+v, want confirming", rec)
	}
}

func TestIntakePickerStatusSurvivesClickClaim(t *testing.T) {
	store, err := NewIntakeStore(filepath.Join(t.TempDir(), "intake.json"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetStatus("om-pick", "", "picker"); err != nil {
		t.Fatal(err)
	}
	if err := claimPickerIntake(store, "om-pick"); err != nil {
		t.Fatalf("picker 状态不能因为改成 SetStatus 而失效：%v", err)
	}
	if got := store.records["om-pick"].Status; got != "creating" {
		t.Fatalf("picker claim status = %q, want creating", got)
	}
}

// 选择卡发送失败时消息还没有任何可见结果，占坑必须放开：同一条消息重投
// 要能补发一次，否则用户永远收不到卡片，去重记录却显示它已被处理。
func TestIntakePickerNotifyFailureReleasesClaim(t *testing.T) {
	h := newIntakeHarness(t, Config{OwnerOpenID: "ou_owner"}, intakeCatalog{refs: []ProjectRef{
		{NodeID: "fn", RootID: "r_docs", Name: "docs"},
		{NodeID: "fn", RootID: "r_notes", Name: "notes"},
	}})
	msg := inbound("om_picker_retry", "p2p", "ou_owner", "在 docs 和 notes 里挑一个")

	h.notifyErr = errors.New("lark send failed")
	if err := h.service.Handle(context.Background(), msg); err == nil {
		t.Fatal("选择卡发送失败必须把错误交回 supervisor")
	}
	if len(h.notices) != 0 {
		t.Fatalf("发送失败时不该记下卡片：%+v", h.notices)
	}

	h.notifyErr = nil
	if err := h.service.Handle(context.Background(), msg); err != nil {
		t.Fatalf("同一条消息重投必须能补发选择卡：%v", err)
	}
	if len(h.notices) != 1 || h.notices[0].Kind != KindPicker {
		t.Fatalf("重投后应补发一张选择卡，got %+v", h.notices)
	}
	if got := h.store.records[msg.MessageID].Status; got != "picker" {
		t.Fatalf("补发成功后状态 = %q, want picker", got)
	}
}
