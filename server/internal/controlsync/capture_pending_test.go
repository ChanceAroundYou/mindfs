package controlsync

import (
	"context"
	"strings"
	"testing"
)

// 离线时本地改动只能进 pending：边缘没有 changes 的读者，写进去等于伪造
// 一段没人追的变更流。
func TestCaptureWritesPendingInsteadOfChangesWhenOffline(t *testing.T) {
	store, domain, capturer := newCaptureFixture(t)
	capturer.PendingWhen = func() bool { return true }
	ctx := context.Background()
	if n, err := capturer.CaptureDomain(ctx, DomainPreferences); err != nil || n != 0 {
		t.Fatalf("空库首次采集 = %d, %v", n, err)
	}
	domain.set("agents", `{"v":1}`)
	if n, err := capturer.CaptureDomain(ctx, DomainPreferences); err != nil || n != 1 {
		t.Fatalf("离线采集 = %d, %v，应为 1", n, err)
	}
	p, ok, err := store.PendingChange(ctx, DomainPreferences, "agents")
	if err != nil || !ok {
		t.Fatalf("pending 未写入：ok=%v err=%v", ok, err)
	}
	if string(p.Payload) != `{"v":1}` || p.Deleted {
		t.Fatalf("pending = %+v", p)
	}
	if p.BaseHash != "" {
		t.Fatalf("base_hash = %q，主节点本来没有这条记录，祖先应为空", p.BaseHash)
	}
	if !strings.HasPrefix(p.ChangeID, "home:preferences:agents:") {
		t.Fatalf("change_id = %q，应带实例前缀与序号", p.ChangeID)
	}
	if got := countRows(t, store, "changes"); got != 0 {
		t.Fatalf("changes = %d，边缘不该写变更流", got)
	}
	if got := countRows(t, store, "records"); got != 0 {
		t.Fatalf("records = %d，副本不该被本地改动覆盖", got)
	}
}

// 祖先只认第一次：离线连改几版，base_hash 仍是最初那个祖先，载荷与身份换成最新。
func TestCaptureKeepsFirstAncestorAcrossOfflineEdits(t *testing.T) {
	store, domain, capturer := newCaptureFixture(t)
	capturer.PendingWhen = func() bool { return true }
	ctx := context.Background()
	domain.set("agents", `{"v":1}`)
	if _, err := capturer.CaptureDomain(ctx, DomainPreferences); err != nil {
		t.Fatalf("第一次采集: %v", err)
	}
	first, _, _ := store.PendingChange(ctx, DomainPreferences, "agents")
	domain.set("agents", `{"v":2}`)
	if _, err := capturer.CaptureDomain(ctx, DomainPreferences); err != nil {
		t.Fatalf("第二次采集: %v", err)
	}
	second, _, _ := store.PendingChange(ctx, DomainPreferences, "agents")
	if second.ChangeID == first.ChangeID {
		t.Fatalf("change_id 没换新：%+v", second)
	}
	if string(second.Payload) != `{"v":2}` {
		t.Fatalf("载荷 = %s，应是最新值", second.Payload)
	}
	if second.BaseHash != "" {
		t.Fatalf("base_hash = %q，应保留第一次的祖先", second.BaseHash)
	}
	if n, _ := store.CountPending(ctx); n != 1 {
		t.Fatalf("pending = %d，同一条记录只占一行", n)
	}
}

// 副本里已经有这条记录时，祖先必须是副本里那个值（主节点最后一次给的），
// 而不是「本地改之前的那个本地值」。
func TestCaptureUsesReplicaHashAsAncestor(t *testing.T) {
	store, domain, capturer := newCaptureFixture(t)
	ctx := context.Background()
	replica := testRecord(t, DomainPreferences, "agents", `{"v":"primary"}`)
	if _, err := store.WriteRecord(ctx, replica); err != nil {
		t.Fatalf("seed 副本: %v", err)
	}
	capturer.PendingWhen = func() bool { return true }
	domain.set("agents", `{"v":"local"}`)
	if _, err := capturer.CaptureDomain(ctx, DomainPreferences); err != nil {
		t.Fatalf("采集: %v", err)
	}
	p, _, _ := store.PendingChange(ctx, DomainPreferences, "agents")
	if p.BaseHash != replica.Hash {
		t.Fatalf("base_hash = %q，应为主节点最后给的值 %q", p.BaseHash, replica.Hash)
	}
}

// 在线期间只有副本在追，内存基线不动。切到离线前必须刷新，否则 base_hash 会
// 指到一个早已被覆盖的祖先，用户基于最新值做的改动恢复后会被误判成冲突。
func TestRefreshBaselineAdoptsReplicaProgress(t *testing.T) {
	store, domain, capturer := newCaptureFixture(t)
	ctx := context.Background()
	domain.set("agents", `{"v":0}`)
	if _, err := capturer.CaptureDomain(ctx, DomainPreferences); err != nil {
		t.Fatalf("在线采集: %v", err)
	}
	remote := testRecord(t, DomainPreferences, "agents", `{"v":"primary"}`)
	if _, err := store.WriteRecord(ctx, remote); err != nil {
		t.Fatalf("副本前进: %v", err)
	}
	if err := capturer.RefreshBaseline(ctx); err != nil {
		t.Fatalf("RefreshBaseline: %v", err)
	}
	capturer.PendingWhen = func() bool { return true }
	domain.set("agents", `{"v":"local"}`)
	if _, err := capturer.CaptureDomain(ctx, DomainPreferences); err != nil {
		t.Fatalf("离线采集: %v", err)
	}
	p, _, _ := store.PendingChange(ctx, DomainPreferences, "agents")
	if p.BaseHash != remote.Hash {
		t.Fatalf("base_hash = %q，应为刷新后的副本值 %q", p.BaseHash, remote.Hash)
	}
}

// 离线删除：pending 记删除，祖先仍是副本里的值（墓碑要保留最后一次内容 hash）。
func TestCapturePendingDeleteKeepsAncestor(t *testing.T) {
	store, domain, capturer := newCaptureFixture(t)
	ctx := context.Background()
	replica := testRecord(t, DomainPreferences, "agents", `{"v":"primary"}`)
	if _, err := store.WriteRecord(ctx, replica); err != nil {
		t.Fatalf("seed 副本: %v", err)
	}
	domain.set("agents", `{"v":"primary"}`)
	if _, err := capturer.CaptureDomain(ctx, DomainPreferences); err != nil {
		t.Fatalf("基线采集: %v", err)
	}
	capturer.PendingWhen = func() bool { return true }
	domain.remove("agents")
	if _, err := capturer.CaptureDomain(ctx, DomainPreferences); err != nil {
		t.Fatalf("离线删除采集: %v", err)
	}
	p, ok, _ := store.PendingChange(ctx, DomainPreferences, "agents")
	if !ok || !p.Deleted {
		t.Fatalf("pending = %+v，应记成删除", p)
	}
	if p.BaseHash != replica.Hash {
		t.Fatalf("base_hash = %q，应保留祖先 hash", p.BaseHash)
	}
	if len(p.Payload) != 0 {
		t.Fatalf("删除不该带载荷：%s", p.Payload)
	}
}
