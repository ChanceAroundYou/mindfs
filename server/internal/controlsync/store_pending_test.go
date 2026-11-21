package controlsync

import (
	"encoding/json"
	"testing"
)

func pendingFixture(t *testing.T) *Store {
	t.Helper()
	return openTestStore(t)
}

// 离线期间同一记录改多次只保留最后一份载荷，但**身份必须换新**：
// 主节点按 change_id 去重，若沿用旧 id，「推上去但回执丢了、本地又改了一版」
// 这一版会被当成重复而永远推不上去（两端静默分叉）。
func TestPutPendingSupersedesIntentWithNewChangeID(t *testing.T) {
	store := pendingFixture(t)
	ctx := t.Context()
	if err := store.PutPending(ctx, PendingChange{
		ChangeID: "edge:preferences:agents:1", Domain: DomainPreferences, Key: "agents",
		BaseHash: "sha256:ancestor", Payload: json.RawMessage(`{"v":1}`),
	}); err != nil {
		t.Fatalf("PutPending: %v", err)
	}
	if err := store.PutPending(ctx, PendingChange{
		ChangeID: "edge:preferences:agents:2", Domain: DomainPreferences, Key: "agents",
		BaseHash: "sha256:local-1", Payload: json.RawMessage(`{"v":2}`),
	}); err != nil {
		t.Fatalf("PutPending(2): %v", err)
	}
	p, ok, err := store.PendingChange(ctx, DomainPreferences, "agents")
	if err != nil || !ok {
		t.Fatalf("PendingChange: ok=%v err=%v", ok, err)
	}
	if p.ChangeID != "edge:preferences:agents:2" {
		t.Fatalf("change_id = %q，应换成最后一次改动的身份", p.ChangeID)
	}
	if string(p.Payload) != `{"v":2}` {
		t.Fatalf("载荷 = %s，应是最新值", p.Payload)
	}
	// 祖先值是「主节点上我最后见到的那个值」，本地再改几版都不会变。
	if p.BaseHash != "sha256:ancestor" {
		t.Fatalf("base_hash = %q，应保留第一次的共同祖先", p.BaseHash)
	}
	if n, err := store.CountPending(ctx); err != nil || n != 1 {
		t.Fatalf("pending 条数 = %d, %v", n, err)
	}
}

func TestNextPendingSeqIsMonotonicAndDurable(t *testing.T) {
	store := pendingFixture(t)
	ctx := t.Context()
	first, err := store.NextPendingSeq(ctx)
	if err != nil {
		t.Fatalf("NextPendingSeq: %v", err)
	}
	second, err := store.NextPendingSeq(ctx)
	if err != nil {
		t.Fatalf("NextPendingSeq: %v", err)
	}
	if second != first+1 {
		t.Fatalf("序号 %d → %d，应逐次加一", first, second)
	}

	other, err := Open(store.path)
	if err != nil {
		t.Fatalf("重开 store: %v", err)
	}
	t.Cleanup(func() { _ = other.Close() })
	if err := other.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	third, err := other.NextPendingSeq(ctx)
	if err != nil {
		t.Fatalf("NextPendingSeq(重开): %v", err)
	}
	if third <= second {
		t.Fatalf("重开后序号回到 %d（之前是 %d），身份会重复", third, second)
	}
	if got := PendingChangeID("edge", DomainPreferences, "agents", third); got != PendingChangeID("edge", DomainPreferences, "agents", third) {
		t.Fatalf("PendingChangeID 不稳定")
	}
}

// 推送回执说 applied 时，必须先确认 pending 还是推上去的那一条：期间本地又改了，
// 就不能删，否则新版内容永远上不去。
func TestDeletePendingIfChangeOnlyDropsThePushedIntent(t *testing.T) {
	store := pendingFixture(t)
	ctx := t.Context()
	if err := store.PutPending(ctx, PendingChange{
		ChangeID: "edge:pins:s1:7", Domain: DomainPins, Key: "s1", Payload: json.RawMessage(`{"v":1}`),
	}); err != nil {
		t.Fatalf("PutPending: %v", err)
	}
	ok, err := store.DeletePendingIfChange(ctx, DomainPins, "s1", "edge:pins:s1:6")
	if err != nil {
		t.Fatalf("DeletePendingIfChange(旧 id): %v", err)
	}
	if ok {
		t.Fatalf("用旧 change_id 删掉了一条更新的 pending")
	}
	if _, exists, _ := store.PendingChange(ctx, DomainPins, "s1"); !exists {
		t.Fatalf("pending 被误删")
	}
	ok, err = store.DeletePendingIfChange(ctx, DomainPins, "s1", "edge:pins:s1:7")
	if err != nil || !ok {
		t.Fatalf("DeletePendingIfChange(当前 id) = %v, %v", ok, err)
	}
	if _, exists, _ := store.PendingChange(ctx, DomainPins, "s1"); exists {
		t.Fatalf("pending 应已删除")
	}
}

// 推送期间本地又改了一版：主节点已接受前一份，新意图其实基于刚推上去的值，
// 只把共同祖先往前挪，不改载荷也不换身份。
func TestRebasePendingAfterAppliedMovesOnlyAncestor(t *testing.T) {
	store := pendingFixture(t)
	ctx := t.Context()
	if err := store.PutPending(ctx, PendingChange{
		ChangeID: "edge:preferences:agents:9", Domain: DomainPreferences, Key: "agents",
		BaseHash: "sha256:ancestor", Payload: json.RawMessage(`{"v":2}`),
	}); err != nil {
		t.Fatalf("PutPending: %v", err)
	}
	ok, err := store.RebasePendingAfterApplied(ctx, DomainPreferences, "agents", "sha256:applied")
	if err != nil || !ok {
		t.Fatalf("RebasePendingAfterApplied = %v, %v", ok, err)
	}
	p, exists, err := store.PendingChange(ctx, DomainPreferences, "agents")
	if err != nil || !exists {
		t.Fatalf("pending 应保留：%v", err)
	}
	if p.BaseHash != "sha256:applied" {
		t.Fatalf("base_hash = %q，应重定到刚被主节点接受的值", p.BaseHash)
	}
	if p.ChangeID != "edge:preferences:agents:9" {
		t.Fatalf("change_id = %q，不该换身份", p.ChangeID)
	}
	if string(p.Payload) != `{"v":2}` {
		t.Fatalf("载荷被改成了 %s", p.Payload)
	}
	if ok, err := store.RebasePendingAfterApplied(ctx, DomainPreferences, "nope", "sha256:x"); err != nil || ok {
		t.Fatalf("不存在的行应返回 false：%v, %v", ok, err)
	}
}

// 裁决时用户选了合并值：载荷变了，身份也必须换，否则主节点会把这份新内容
// 当成「已经应用过的旧变更」而拒绝落地。
func TestRebasePendingCanReplaceChangeID(t *testing.T) {
	store := pendingFixture(t)
	ctx := t.Context()
	if err := store.PutPending(ctx, PendingChange{
		ChangeID: "edge:pins:s1:3", Domain: DomainPins, Key: "s1",
		BaseHash: "sha256:ancestor", Payload: json.RawMessage(`{"v":"local"}`),
	}); err != nil {
		t.Fatalf("PutPending: %v", err)
	}
	if _, err := store.RebasePending(ctx, DomainPins, "s1", "sha256:remote",
		json.RawMessage(`{"v":"merged"}`), false); err != nil {
		t.Fatalf("RebasePending: %v", err)
	}
	p, _, _ := store.PendingChange(ctx, DomainPins, "s1")
	if p.ChangeID != "edge:pins:s1:3" {
		t.Fatalf("空 change_id 应保留原身份，得到 %q", p.ChangeID)
	}
	if _, err := store.RebasePending(ctx, DomainPins, "s1", "sha256:remote",
		json.RawMessage(`{"v":"merged-2"}`), false); err != nil {
		t.Fatalf("RebasePending: %v", err)
	}
	p, _, _ = store.PendingChange(ctx, DomainPins, "s1")
	if p.ChangeID != "edge:pins:s1:3" {
		t.Fatalf("不带新身份时仍应保留原身份，得到 %q", p.ChangeID)
	}
	if _, err := store.RebasePendingWithID(ctx, DomainPins, "s1", "sha256:remote",
		"edge:pins:s1:4", json.RawMessage(`{"v":"merged-3"}`), false); err != nil {
		t.Fatalf("RebasePendingWithID: %v", err)
	}
	p, _, _ = store.PendingChange(ctx, DomainPins, "s1")
	if p.ChangeID != "edge:pins:s1:4" || string(p.Payload) != `{"v":"merged-3"}` {
		t.Fatalf("重定后 = %+v", p)
	}
}
