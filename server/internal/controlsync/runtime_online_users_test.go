package controlsync

import (
	"context"
	"testing"

	"mindfs/server/internal/auth"
)

// edgeWithUsersRuntime 造一个只带 users 域的边缘运行时。
//
// 为什么单独造一份而不复用 newRuntimeFixture：那套夹具只注册一个 memDomain（preferences），
// users 域的 apply 路径走的是真实 *auth.Store，要断言的是「权威 users.json 里有没有这个人」，
// 用假 store 断言只会把实现复述一遍。
func edgeWithUsersRuntime(t *testing.T) (*Runtime, *Store, *auth.Store) {
	t.Helper()
	store := openTestStore(t)
	authStore, err := auth.EnsureStoreAt(t.TempDir() + "/users.json")
	if err != nil {
		t.Fatalf("auth store: %v", err)
	}
	// 边缘自己的账户表只有它自己的 admin —— 这正是主控上的 xiaokubao 找不到的原因。
	users := NewUsersDomain(authStore)
	registry, err := NewRegistry(users)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	capturer, err := NewCapturer(store, registry, "")
	if err != nil {
		t.Fatalf("NewCapturer: %v", err)
	}
	mode, err := NewModeController(t.Context(), store, probeUp{})
	if err != nil {
		t.Fatalf("NewModeController: %v", err)
	}
	rt, err := NewRuntime(t.Context(), edgeConfig(), Deps{
		Store:    store,
		Registry: registry,
		Capturer: capturer,
		Mode:     mode,
		Source:   &fakeReplicaSource{},
		Sender:   &fakePushSender{},
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	return rt, store, authStore
}

// probeUp 永远报可达：这些用例只关心「在线时账户该不该落下来」，
// 探活本身由 mode 的用例覆盖。
type probeUp struct{}

func (probeUp) Probe(context.Context) error { return nil }

// 阶段 1（在线）：主控的账户必须在这台机器上解析得出来。
//
// 场景：手机经主控 /relay/hasee/ 访问 hasee，前端跨机器带的是 user=<用户名>。
// 如果 hasee 的权威 users.json 里只有它自己的 admin，Workspace("xiaokubao") 会落到
// emptyWorkspace —— 页面打得开、全是空的，「连上了但看不见东西」。
//
// 这里断言的是权威账户表（users.json 背后的 auth.Store），不是副本表：账户解析走的就是它。
func TestRuntimeOnlineAppliesUsersReplicaToAuthoritativeStore(t *testing.T) {
	rt, store, authStore := edgeWithUsersRuntime(t)
	ctx := context.Background()

	primary := auth.User{
		ID:           auth.DeriveAccountID("xiaokubao"),
		Username:     "xiaokubao",
		PasswordHash: "$2a$10$abcdefghijklmnopqrstuv",
		Role:         auth.RoleAdmin,
		CreatedAt:    "2026-11-24T00:00:00Z",
	}
	rec, err := newRecord(DomainUsers, "xiaokubao", primary)
	if err != nil {
		t.Fatalf("newRecord: %v", err)
	}
	seedReplicaRecord(t, store, rec)

	rt.tick(ctx)

	if got := authStore.Resolve("xiaokubao"); got != primary.ID {
		t.Fatalf("Resolve(xiaokubao) = %q，want %q —— 在线时边缘没采纳主控账户",
			got, primary.ID)
	}
}

// 阶段 1 附带：主账户标记（@primary）在副本里排在账户之前时也不能把整批卡住。
//
// ListRecords 按 key 升序返回，"@primary" 排在 "xiaokubao" 前面。若先应用 @primary，
// SetPrimary 会因为「本机还没有这个账户」失败，整批 Apply 中止 —— 账户也没落下来。
// 这就是 offline 交接（Handover）现存的行为：应用顺序必须由域自己决定。
func TestRuntimeOnlineAppliesPrimaryMarkerAfterAccounts(t *testing.T) {
	rt, store, authStore := edgeWithUsersRuntime(t)
	ctx := context.Background()

	primary := auth.User{
		ID:           auth.DeriveAccountID("xiaokubao"),
		Username:     "xiaokubao",
		PasswordHash: "$2a$10$abcdefghijklmnopqrstuv",
		Role:         auth.RoleAdmin,
		CreatedAt:    "2026-11-24T00:00:00Z",
	}
	userRec, err := newRecord(DomainUsers, "xiaokubao", primary)
	if err != nil {
		t.Fatalf("newRecord user: %v", err)
	}
	markerRec, err := newRecord(DomainUsers, usersPrimaryKey, primaryUserPayload{ID: primary.ID})
	if err != nil {
		t.Fatalf("newRecord marker: %v", err)
	}
	// 故意先写 @primary 再写账户，模拟「副本里的顺序不利」。
	seedReplicaRecord(t, store, markerRec)
	seedReplicaRecord(t, store, userRec)

	rt.tick(ctx)

	if got := authStore.Resolve("xiaokubao"); got != primary.ID {
		t.Fatalf("Resolve(xiaokubao) = %q，want %q —— @primary 先应用把整批卡住了",
			got, primary.ID)
	}
	if got := authStore.PrimaryUserID(); got != primary.ID {
		t.Fatalf("PrimaryUserID = %q，want %q —— 主账户标记没生效", got, primary.ID)
	}
}

// 交接（离线）同样走这条顺序规则：现有实现按 ListRecords 的 key 序应用，
// 修好在线应用后交接必须共用同一份「先账户、后标记」逻辑。
func TestHandoverAppliesPrimaryMarkerAfterAccounts(t *testing.T) {
	rt, store, authStore := edgeWithUsersRuntime(t)
	ctx := context.Background()

	primary := auth.User{
		ID:           auth.DeriveAccountID("xiaokubao"),
		Username:     "xiaokubao",
		PasswordHash: "$2a$10$abcdefghijklmnopqrstuv",
		Role:         auth.RoleAdmin,
		CreatedAt:    "2026-11-24T00:00:00Z",
	}
	userRec, err := newRecord(DomainUsers, "xiaokubao", primary)
	if err != nil {
		t.Fatalf("newRecord user: %v", err)
	}
	markerRec, err := newRecord(DomainUsers, usersPrimaryKey, primaryUserPayload{ID: primary.ID})
	if err != nil {
		t.Fatalf("newRecord marker: %v", err)
	}
	seedReplicaRecord(t, store, markerRec)
	seedReplicaRecord(t, store, userRec)

	if err := rt.Handover(ctx); err != nil {
		t.Fatalf("Handover: %v", err)
	}
	if got := authStore.Resolve("xiaokubao"); got != primary.ID {
		t.Fatalf("交接后 Resolve(xiaokubao) = %q，want %q", got, primary.ID)
	}
}

// 在线采纳是按记录做的增量更新：主控删掉的账户，边缘也必须跟着消失，
// 否则「主控上没有这个人」在边缘仍然能解析出账户、翻出数据目录。
func TestRuntimeOnlineRemovesDeletedUsers(t *testing.T) {
	rt, store, authStore := edgeWithUsersRuntime(t)
	ctx := context.Background()

	primary := auth.User{
		ID:           auth.DeriveAccountID("xiaokubao"),
		Username:     "xiaokubao",
		PasswordHash: "$2a$10$abcdefghijklmnopqrstuv",
		Role:         auth.RoleAdmin,
		CreatedAt:    "2026-11-24T00:00:00Z",
	}
	userRec, err := newRecord(DomainUsers, "xiaokubao", primary)
	if err != nil {
		t.Fatalf("newRecord: %v", err)
	}
	seedReplicaRecord(t, store, userRec)
	rt.tick(ctx)
	if got := authStore.Resolve("xiaokubao"); got != primary.ID {
		t.Fatalf("前置失败：账户没落下来，Resolve = %q", got)
	}

	tomb := Record{Domain: DomainUsers, Key: "xiaokubao", Deleted: true}
	hash, err := HashRecord(tomb)
	if err != nil {
		t.Fatalf("HashRecord: %v", err)
	}
	tomb.Hash = hash
	seedReplicaRecord(t, store, tomb)

	rt.tick(ctx)
	if got := authStore.Resolve("xiaokubao"); got != "" {
		t.Fatalf("Resolve(xiaokubao) = %q，want 空 —— 墓碑没落到权威存储", got)
	}
}
