package auth

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// newTestStore 在临时目录里建账户表，并用一份 login.json 指定初始管理员密码。
func newTestStore(t *testing.T, legacyPassword string) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	if legacyPassword != "" {
		payload, err := json.Marshal(map[string]string{"password": legacyPassword})
		if err != nil {
			t.Fatalf("marshal legacy: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, legacyLogin), payload, 0o600); err != nil {
			t.Fatalf("write legacy: %v", err)
		}
	}
	path := filepath.Join(dir, usersFileName)
	store, err := EnsureStoreAt(path)
	if err != nil {
		t.Fatalf("EnsureStoreAt: %v", err)
	}
	return store, path
}

func TestMigratesLegacyPasswordIntoAdmin(t *testing.T) {
	store, path := newTestStore(t, "Xkb111717!")

	user, err := store.Authenticate("admin", "Xkb111717!")
	if err != nil {
		t.Fatalf("admin should authenticate with legacy password: %v", err)
	}
	if user.Role != RoleAdmin {
		t.Fatalf("role = %q, want admin", user.Role)
	}
	if _, err := store.Authenticate("admin", "wrong"); err != ErrInvalidCredentials {
		t.Fatalf("wrong password err = %v, want ErrInvalidCredentials", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat users.json: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("users.json perm = %o, want 600", perm)
	}

	// 重新加载应复用同一个账户，不重复迁移
	reloaded, err := EnsureStoreAt(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, err := reloaded.Authenticate("admin", "Xkb111717!"); err != nil {
		t.Fatalf("reloaded store lost admin: %v", err)
	}
	if got := len(reloaded.List()); got != 1 {
		t.Fatalf("reload produced %d users, want 1", got)
	}
}

func TestGenerateAdminWhenNoLegacyFile(t *testing.T) {
	store, _ := newTestStore(t, "")
	users := store.List()
	if len(users) != 1 || users[0].Role != RoleAdmin {
		t.Fatalf("generated users = %#v, want a single admin", users)
	}
	// 随机密码只进日志，这里只验证错误口令被拒
	if _, err := store.Authenticate("admin", "guess"); err != ErrInvalidCredentials {
		t.Fatalf("err = %v, want ErrInvalidCredentials", err)
	}
}

func TestCreateUpdateDeleteUser(t *testing.T) {
	store, path := newTestStore(t, "root-secret")

	created, err := store.Create("  alice  ", "alice-secret", RoleUser)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Username != "alice" {
		t.Fatalf("username = %q, want trimmed alice", created.Username)
	}
	// 明文绝不能落盘：PublicUser 没有 hash 字段，这里再验一次文件内容
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read users.json: %v", err)
	}
	if bytes.Contains(raw, []byte("alice-secret")) || bytes.Contains(raw, []byte("root-secret")) {
		t.Fatal("users.json contains a plaintext password")
	}
	if !bytes.Contains(raw, []byte("$2a$")) && !bytes.Contains(raw, []byte("$2b$")) {
		t.Fatal("users.json does not look like it stores bcrypt hashes")
	}

	if _, err := store.Create("ALICE", "other-secret", RoleUser); err != ErrUsernameTaken {
		t.Fatalf("duplicate err = %v, want ErrUsernameTaken", err)
	}
	if _, err := store.Create("bob", "short", RoleUser); err == nil {
		t.Fatal("expected rejection for a too-short password")
	}

	if _, err := store.Authenticate("alice", "alice-secret"); err != nil {
		t.Fatalf("new user cannot log in: %v", err)
	}

	disabled := true
	if _, err := store.Update(created.ID, UpdateInput{Disabled: &disabled}); err != nil {
		t.Fatalf("Update disable: %v", err)
	}
	if _, err := store.Authenticate("alice", "alice-secret"); err != ErrDisabled {
		t.Fatalf("disabled login err = %v, want ErrDisabled", err)
	}

	newPassword := "alice-2nd-secret"
	reenabled := false
	if _, err := store.Update(created.ID, UpdateInput{Password: &newPassword, Disabled: &reenabled}); err != nil {
		t.Fatalf("Update password: %v", err)
	}
	if _, err := store.Authenticate("alice", "alice-2nd-secret"); err != nil {
		t.Fatalf("password change did not take effect: %v", err)
	}
	if _, err := store.Authenticate("alice", "alice-secret"); err != ErrInvalidCredentials {
		t.Fatalf("old password still works: %v", err)
	}

	if err := store.Delete(created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Authenticate("alice", "alice-2nd-secret"); err != ErrInvalidCredentials {
		t.Fatalf("deleted user still authenticates: %v", err)
	}
}

func TestLastAdminIsProtected(t *testing.T) {
	store, _ := newTestStore(t, "root-secret")
	admin := store.List()[0]

	if err := store.Delete(admin.ID); err != ErrLastAdmin {
		t.Fatalf("delete last admin err = %v, want ErrLastAdmin", err)
	}
	role := RoleUser
	if _, err := store.Update(admin.ID, UpdateInput{Role: &role}); err != ErrLastAdmin {
		t.Fatalf("demote last admin err = %v, want ErrLastAdmin", err)
	}
	disabled := true
	if _, err := store.Update(admin.ID, UpdateInput{Disabled: &disabled}); err != ErrLastAdmin {
		t.Fatalf("disable last admin err = %v, want ErrLastAdmin", err)
	}

	// 有第二个管理员后，第一位才允许被删/降级——但它同时是主账户（存量数据归属），
	// 得先转移主账户身份才能删。
	second, err := store.Create("ops", "ops-secret", RoleAdmin)
	if err != nil {
		t.Fatalf("create second admin: %v", err)
	}
	if err := store.Delete(admin.ID); err != ErrPrimaryUser {
		t.Fatalf("delete primary err = %v, want ErrPrimaryUser", err)
	}
	if err := store.SetPrimary(second.ID); err != nil {
		t.Fatalf("SetPrimary: %v", err)
	}
	if store.PrimaryUserID() != second.ID {
		t.Fatalf("primary = %q, want %q", store.PrimaryUserID(), second.ID)
	}
	if err := store.Delete(admin.ID); err != nil {
		t.Fatalf("delete after transferring primacy: %v", err)
	}
	if _, err := store.Get(second.ID); err != nil {
		t.Fatalf("second admin vanished: %v", err)
	}
	if err := store.Delete(second.ID); err != ErrLastAdmin {
		t.Fatalf("delete final admin err = %v, want ErrLastAdmin", err)
	}
}

func TestPrimaryUserGuards(t *testing.T) {
	store, _ := newTestStore(t, "root-secret")
	admin := store.List()[0]

	if !admin.Primary {
		t.Fatal("migrated admin should be the primary account")
	}
	if store.PrimaryUserID() != admin.ID {
		t.Fatalf("PrimaryUserID = %q, want %q", store.PrimaryUserID(), admin.ID)
	}

	// 迁移出来的 admin 就是主账户；普通人不能当主账户
	plain, err := store.Create("bob", "bob-secret", RoleUser)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.SetPrimary(plain.ID); err == nil {
		t.Fatal("a plain user must not be allowed to become primary")
	}
	if err := store.SetPrimary("u_missing"); err != ErrUserNotFound {
		t.Fatalf("SetPrimary unknown err = %v, want ErrUserNotFound", err)
	}

	// 主账户标记要跟着 List/Get 走
	listed := store.List()
	for _, u := range listed {
		if u.ID == admin.ID && !u.Primary {
			t.Fatal("List lost the primary flag")
		}
		if u.ID == plain.ID && u.Primary {
			t.Fatal("List marked a plain user as primary")
		}
	}
	got, err := store.Get(admin.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Primary {
		t.Fatal("Get lost the primary flag")
	}

	// 主账户身份要落盘，重载后仍然有效
	reloaded, err := EnsureStoreAt(store.path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.PrimaryUserID() != admin.ID {
		t.Fatalf("primary did not survive reload: %q", reloaded.PrimaryUserID())
	}
}

func TestUnknownUserAndNilStore(t *testing.T) {
	store, _ := newTestStore(t, "root-secret")

	if _, err := store.Get("u_missing"); err != ErrUserNotFound {
		t.Fatalf("Get unknown err = %v, want ErrUserNotFound", err)
	}
	if store.Exists("u_missing") {
		t.Fatal("Exists reported true for an unknown id")
	}
	if _, err := store.Update("u_missing", UpdateInput{}); err != ErrUserNotFound {
		t.Fatalf("Update unknown err = %v, want ErrUserNotFound", err)
	}

	var nilStore *Store
	if _, err := nilStore.Authenticate("admin", "x"); err != ErrInvalidCredentials {
		t.Fatalf("nil store err = %v, want ErrInvalidCredentials", err)
	}
	if nilStore.List() != nil || nilStore.Exists("u_1") {
		t.Fatal("nil store must report nothing")
	}
}
