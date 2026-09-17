package auth

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPasswordFileLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "login.json")

	cfg, generated, err := loadOrCreate(path)
	if err != nil {
		t.Fatalf("loadOrCreate: %v", err)
	}
	if !generated || cfg.Password == "" {
		t.Fatalf("expected generated password, got %q generated=%v", cfg.Password, generated)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("password file perm = %o, want 600", perm)
	}

	again, regenerated, err := loadOrCreate(path)
	if err != nil {
		t.Fatalf("loadOrCreate existing: %v", err)
	}
	if regenerated || again.Password != cfg.Password {
		t.Fatalf("existing password was not reused: %q vs %q", again.Password, cfg.Password)
	}
}

func TestStoreVerifyAndTokens(t *testing.T) {
	store := &Store{password: "s3cret", tokens: map[string]time.Time{}}

	if !store.Verify("s3cret") || !store.Verify("  s3cret  ") {
		t.Fatal("correct password rejected")
	}
	if store.Verify("wrong") || store.Verify("") {
		t.Fatal("wrong password accepted")
	}

	token, err := store.Issue()
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if !store.Valid(token) {
		t.Fatal("freshly issued token rejected")
	}
	if store.Valid("") || store.Valid(token+"x") {
		t.Fatal("bogus token accepted")
	}

	var nilStore *Store
	if nilStore.Verify("") || nilStore.Valid("") {
		t.Fatal("nil store must reject everything")
	}
}
