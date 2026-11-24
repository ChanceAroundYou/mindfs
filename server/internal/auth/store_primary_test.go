package auth

import (
	"os"
	"testing"
	"time"
)

// 同步侧每拍都会重放整份账户副本（controlsync.Runtime 的在线采纳与离线交接）。
// SetPrimary 若每次都落盘，这台机器每 5 秒重写一次 users.json —— 文件 mtime
// 永远在变，备份与「谁改过账户表」的判断随之失效。
//
// 判据只能用 mtime：内容逐字节相同，比较内容看不出「写过没写」。
func TestSetPrimaryIsIdempotentOnDisk(t *testing.T) {
	store := newSyncStore(t)
	_, primary := store.ExportUsers()
	if primary == "" {
		t.Fatal("夹具没有主账户")
	}

	// 把 mtime 拨到过去，任何一次重写都会把它拉回现在。
	past := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(store.path, past, past); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	if err := store.SetPrimary(primary); err != nil {
		t.Fatalf("SetPrimary(已是主账户): %v", err)
	}

	info, err := os.Stat(store.path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.ModTime().Equal(past) {
		t.Fatalf("mtime = %v，want %v —— 主账户没变却重写了 users.json",
			info.ModTime(), past)
	}
}

// 换了主账户时必须真的落盘（幂等优化不能把真正的变更也吞掉）。
func TestSetPrimaryWritesWhenChanged(t *testing.T) {
	store := newSyncStore(t)
	bob, err := store.Create("bob", "bob-secret", RoleAdmin)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	past := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(store.path, past, past); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	if err := store.SetPrimary(bob.ID); err != nil {
		t.Fatalf("SetPrimary(bob): %v", err)
	}
	if got := store.PrimaryUserID(); got != bob.ID {
		t.Fatalf("PrimaryUserID = %q，want %q", got, bob.ID)
	}
	info, err := os.Stat(store.path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.ModTime().Equal(past) {
		t.Fatal("主账户换了却没落盘：重启后回到旧主账户")
	}
}
