package auth

import (
	"path/filepath"
	"testing"
)

func newSyncStore(t *testing.T) *Store {
	t.Helper()
	store, err := EnsureStoreAt(filepath.Join(t.TempDir(), "users.json"))
	if err != nil {
		t.Fatalf("EnsureStoreAt: %v", err)
	}
	return store
}

func TestExportUsersReturnsCopy(t *testing.T) {
	store := newSyncStore(t)
	users, primary := store.ExportUsers()
	if len(users) == 0 || primary == "" {
		t.Fatalf("导出为空：users=%d primary=%q", len(users), primary)
	}
	users[0].Username = "mutated"
	again, _ := store.ExportUsers()
	if again[0].Username == "mutated" {
		t.Fatalf("导出的是内部切片本身：调用方改一下就改了账户表")
	}
}

func TestApplyUserUpsertsByUsername(t *testing.T) {
	store := newSyncStore(t)
	if _, err := store.Create("bob", "bob-secret", RoleUser); err != nil {
		t.Fatalf("Create: %v", err)
	}
	users, _ := store.ExportUsers()
	var bob User
	for _, u := range users {
		if u.Username == "bob" {
			bob = u
		}
	}
	bob.Role = RoleAdmin
	if err := store.ApplyUser(bob); err != nil {
		t.Fatalf("ApplyUser: %v", err)
	}
	after, _ := store.ExportUsers()
	seen := 0
	for _, u := range after {
		if u.Username == "bob" {
			seen++
			if u.Role != RoleAdmin {
				t.Fatalf("角色没更新: %+v", u)
			}
		}
	}
	if seen != 1 {
		t.Fatalf("按用户名 upsert 变成了追加：bob 出现 %d 次", seen)
	}
}

func TestRemoveUserByNameIsIdempotentAndProtectsPrimary(t *testing.T) {
	store := newSyncStore(t)
	if err := store.RemoveUserByName("nobody"); err != nil {
		t.Fatalf("删不存在的账户应成功: %v", err)
	}
	primary := store.PrimaryUserID()
	users, _ := store.ExportUsers()
	for _, u := range users {
		if u.ID == primary {
			if err := store.RemoveUserByName(u.Username); err != ErrPrimaryUser {
				t.Fatalf("删主账户 err = %v, want ErrPrimaryUser", err)
			}
		}
	}
}
