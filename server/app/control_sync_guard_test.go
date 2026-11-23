package app

import (
	"path/filepath"
	"strings"
	"testing"

	"mindfs/server/internal/controlsync"
	"mindfs/server/internal/nodeinfo"
)

// seedReplicaState 在 cfgDir 上造一份「曾经是谁家副本」的 control_sync.db。
// 认这一列有据可依：全仓唯一写者是边缘的 Replicator.verifyOrigin。
func seedReplicaState(t *testing.T, cfgDir, primaryInstanceID string) {
	t.Helper()
	store, err := controlsync.Open(filepath.Join(cfgDir, "control_sync.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	if err := store.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := store.SetPrimaryInstanceID(t.Context(), primaryInstanceID); err != nil {
		t.Fatalf("SetPrimaryInstanceID: %v", err)
	}
}

// 这份库曾经是别人家的副本（primary_instance_id 记着上一台主节点），现在被配置
// 成 primary：起得来的话两个主控各自往自己的变更流里追加 rev，边缘拉到谁的就是
// 谁的那半份控制面 —— 而且两侧日志都写着 enabled role=primary。
func TestControlSyncPrimaryGuardRejectsFormerReplica(t *testing.T) {
	cfgDir, stores := controlSyncFixture(t)
	seedReplicaState(t, cfgDir, "home-instance-abc")
	w, err := startControlSync(t.Context(), &ControlSyncOptions{
		Enabled:      true,
		Role:         controlsync.RolePrimary,
		InstanceID:   "second-control",
		Token:        syncTestToken,
		BuildVersion: "test",
	}, nodeinfo.RoleControl, cfgDir, stores)
	if err == nil {
		t.Fatalf("拿一份曾是副本的库当主控居然起来了：%+v", w)
	}
	if !strings.Contains(err.Error(), "home-instance-abc") {
		t.Fatalf("错误里没说清是哪个主节点的副本，排查时只能盯着库猜：%v", err)
	}
}

// 边缘的库里 primary_instance_id 本来就是「我的主节点是谁」—— 那是正常状态，
// 守卫只能对 primary 生效，否则边缘一台都起不来。
func TestControlSyncPrimaryGuardIgnoresEdgeRole(t *testing.T) {
	cfgDir, stores := controlSyncFixture(t)
	seedReplicaState(t, cfgDir, "home-instance-abc")
	w, err := startControlSync(t.Context(), &ControlSyncOptions{
		Enabled:      true,
		Role:         controlsync.RoleEdge,
		InstanceID:   "hasee",
		PrimaryURL:   "https://home.example:7331",
		Token:        syncTestToken,
		BuildVersion: "test",
	}, nodeinfo.RoleControl, cfgDir, stores)
	if err != nil {
		t.Fatalf("边缘带着自己的主节点身份被守卫误杀了：%v", err)
	}
	if w == nil {
		t.Fatalf("边缘没起同步")
	}
}

// 全新库（从没同步过别人）当主控必须照常起来。
func TestControlSyncPrimaryGuardAcceptsFreshStore(t *testing.T) {
	cfgDir, stores := controlSyncFixture(t)
	w, err := startControlSync(t.Context(), &ControlSyncOptions{
		Enabled:      true,
		Role:         controlsync.RolePrimary,
		InstanceID:   "home",
		Token:        syncTestToken,
		BuildVersion: "test",
	}, nodeinfo.RoleControl, cfgDir, stores)
	if err != nil {
		t.Fatalf("全新库被拒了：%v", err)
	}
	if w == nil || w.pusher == nil {
		t.Fatalf("主节点没接上 push 端点")
	}
}

// 读不出状态时不能当成「空」：读失败可能是「曾是谁的副本」这份事实读不出来，
// 把它当空正是两个写者的起步方式。
func TestGuardSinglePrimaryFailsClosedOnReadError(t *testing.T) {
	store, err := controlsync.Open(filepath.Join(t.TempDir(), "control_sync.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := store.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	_ = store.Close()
	if err := guardSinglePrimary(t.Context(), store, controlsync.Config{Role: controlsync.RolePrimary}); err == nil {
		t.Fatal("读不出状态却放行当了主控")
	}
}
