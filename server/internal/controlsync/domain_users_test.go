package controlsync

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/server/internal/auth"
)

// 账户同步的红线：口令 hash 原样搬，id 按用户名派生，主账户标记单独一条。

func newAuthStore(t *testing.T) *auth.Store {
	t.Helper()
	store, err := auth.EnsureStoreAt(filepath.Join(t.TempDir(), "users.json"))
	if err != nil {
		t.Fatalf("EnsureStoreAt: %v", err)
	}
	return store
}

func usersSnapshot(t *testing.T, store *auth.Store) []Record {
	t.Helper()
	recs, err := NewUsersDomain(store).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	return recs
}

func findRecord(recs []Record, key string) (Record, bool) {
	for _, rec := range recs {
		if rec.Key == key {
			return rec, true
		}
	}
	return Record{}, false
}

func TestUsersDomainSnapshotKeysByNameWithHashAndPrimaryMarker(t *testing.T) {
	store := newAuthStore(t)
	if _, err := store.Create("XiaoKubao", "secret-1", auth.RoleUser); err != nil {
		t.Fatalf("Create: %v", err)
	}
	recs := usersSnapshot(t, store)
	if _, ok := findRecord(recs, "xiaokubao"); !ok {
		t.Fatalf("没有按小写用户名归一后的记录：%v", recordKeys(recs))
	}
	// key 用小写、payload 保留原始大小写。
	rec, _ := findRecord(recs, "xiaokubao")
	var user auth.User
	if err := json.Unmarshal(rec.Payload, &user); err != nil {
		t.Fatalf("Unmarshal payload: %v", err)
	}
	if user.Username != "XiaoKubao" {
		t.Fatalf("payload 里的用户名被改写了: %q", user.Username)
	}
	if !strings.HasPrefix(user.PasswordHash, "$2") {
		t.Fatalf("payload 没带口令 hash，离线登录会直接失效: %q", user.PasswordHash)
	}
	primary, ok := findRecord(recs, usersPrimaryKey)
	if !ok {
		t.Fatalf("没有主账户标记记录：%v", recordKeys(recs))
	}
	var marker primaryUserPayload
	if err := json.Unmarshal(primary.Payload, &marker); err != nil {
		t.Fatalf("Unmarshal primary payload: %v", err)
	}
	if marker.ID != store.PrimaryUserID() {
		t.Fatalf("主账户标记 = %q，实际 %q", marker.ID, store.PrimaryUserID())
	}
}

func TestUsersDomainSnapshotIsStable(t *testing.T) {
	store := newAuthStore(t)
	first := usersSnapshot(t, store)
	second := usersSnapshot(t, store)
	if len(first) != len(second) {
		t.Fatalf("两次快照条数不同：%d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Key != second[i].Key || first[i].Hash != second[i].Hash || first[i].Deleted != second[i].Deleted {
			t.Fatalf("快照不稳定（每次都会以为数据变了）：%v vs %v", first[i], second[i])
		}
	}
}

func TestUsersDomainApplyCopiesHashAndPrimary(t *testing.T) {
	source := newAuthStore(t)
	if _, err := source.Create("bob", "bob-secret", auth.RoleAdmin); err != nil {
		t.Fatalf("Create(bob): %v", err)
	}
	if err := source.SetPrimary(auth.DeriveAccountID("bob")); err != nil {
		t.Fatalf("SetPrimary: %v", err)
	}

	target := newAuthStore(t)
	domain := NewUsersDomain(target)
	for _, rec := range usersSnapshot(t, source) {
		if err := domain.Apply(context.Background(), rec); err != nil {
			t.Fatalf("Apply(%s): %v", rec.Key, err)
		}
	}
	// 口令 hash 必须是复制过来的：拿原始口令能登进去，说明离线的账号是真能用的。
	if _, err := target.Authenticate("bob", "bob-secret"); err != nil {
		t.Fatalf("复制过去的账户登不进去: %v", err)
	}
	if got := target.PrimaryUserID(); got != auth.DeriveAccountID("bob") {
		t.Fatalf("主账户 = %q，应跟随远端标记", got)
	}
}

func TestUsersDomainApplyIsIdempotent(t *testing.T) {
	source := newAuthStore(t)
	if _, err := source.Create("carol", "carol-secret", auth.RoleUser); err != nil {
		t.Fatalf("Create: %v", err)
	}
	recs := usersSnapshot(t, source)

	target := newAuthStore(t)
	domain := NewUsersDomain(target)
	apply := func() {
		for _, rec := range recs {
			if err := domain.Apply(context.Background(), rec); err != nil {
				t.Fatalf("Apply(%s): %v", rec.Key, err)
			}
		}
	}
	apply()
	once, primaryOnce := target.ExportUsers()
	apply()
	twice, primaryTwice := target.ExportUsers()
	if primaryOnce != primaryTwice {
		t.Fatalf("重复应用改了主账户: %q -> %q", primaryOnce, primaryTwice)
	}
	if len(once) != len(twice) {
		t.Fatalf("重复应用改了账户条数: %d -> %d", len(once), len(twice))
	}
	for i := range once {
		if once[i] != twice[i] {
			t.Fatalf("重复应用改了账户内容: %+v -> %+v", once[i], twice[i])
		}
	}
}

func TestUsersDomainTombstoneRemovesAccount(t *testing.T) {
	target := newAuthStore(t)
	if _, err := target.Create("dave", "dave-secret", auth.RoleUser); err != nil {
		t.Fatalf("Create: %v", err)
	}
	domain := NewUsersDomain(target)
	rec := Record{Domain: DomainUsers, Key: "dave", Deleted: true}
	if err := domain.Apply(context.Background(), rec); err != nil {
		t.Fatalf("Apply tombstone: %v", err)
	}
	if _, err := target.Authenticate("dave", "dave-secret"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("墓碑之后还能登录: %v", err)
	}
}

func TestUsersDomainRejectsBadRecords(t *testing.T) {
	store := newAuthStore(t)
	domain := NewUsersDomain(store)
	mustRecord := func(payload any) Record {
		t.Helper()
		rec, err := newRecord(DomainUsers, "eve", payload)
		if err != nil {
			t.Fatalf("newRecord: %v", err)
		}
		return rec
	}
	cases := []struct {
		name string
		rec  Record
		want error
	}{
		{
			name: "id 与用户名派生值不符",
			rec: mustRecord(auth.User{
				ID: "u_other", Username: "eve", PasswordHash: "$2a$10$x", Role: auth.RoleUser,
			}),
			want: auth.ErrDerivedIDMismatch,
		},
		{
			name: "key 与载荷用户名不符",
			rec: mustRecord(auth.User{
				Username: "mallory", PasswordHash: "$2a$10$x", Role: auth.RoleUser,
			}),
			want: errUsersKeyMismatch,
		},
		{
			name: "没有口令 hash",
			rec: mustRecord(auth.User{
				Username: "eve", Role: auth.RoleUser,
			}),
			want: nil, // 只要求报错，具体文案由 store 决定
		},
		{
			name: "未知域",
			rec:  Record{Domain: DomainNodes, Key: "eve"},
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := domain.Apply(context.Background(), tc.rec)
			if err == nil {
				t.Fatalf("坏记录被接受了")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
	// 一条都没落盘。
	users, _ := store.ExportUsers()
	if len(users) != 1 || !strings.EqualFold(users[0].Username, "admin") {
		t.Fatalf("坏记录改了账户表: %+v", users)
	}
}

func TestUsersDomainRefusesPrimaryTombstone(t *testing.T) {
	store := newAuthStore(t)
	domain := NewUsersDomain(store)
	err := domain.Apply(context.Background(), Record{Domain: DomainUsers, Key: usersPrimaryKey, Deleted: true})
	if err == nil {
		t.Fatalf("主账户墓碑被接受了：远端一条坏记录就能删掉存量数据归属")
	}
	if store.PrimaryUserID() == "" {
		t.Fatalf("主账户标记被清空")
	}
}
