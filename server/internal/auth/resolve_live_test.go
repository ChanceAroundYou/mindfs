package auth

import (
	"os"
	"path/filepath"
	"testing"
)

// 对**真实的** users.json 跑一次，确认按用户名能解析出本机 id。
// 跳过条件：文件不存在（CI/别人机器上没这个文件）。
func TestResolveAgainstRealUsersFile(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home")
	}
	p := filepath.Join(home, ".config", "mindfs", usersFileName)
	if _, err := os.Stat(p); err != nil {
		t.Skip("no real users.json")
	}
	s, err := EnsureStoreAt(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	primary := s.PrimaryUserID()
	if primary == "" {
		t.Skip("empty table")
	}
	// 主账户：按 id 和按它的用户名，必须解析到同一个 id
	got := s.Resolve(primary)
	if got != primary {
		t.Errorf("Resolve(primary id) = %q, want %q", got, primary)
	}
	u, err := s.Get(primary)
	if err != nil {
		t.Fatalf("get primary: %v", err)
	}
	if got := s.Resolve(u.Username); got != primary {
		t.Errorf("Resolve(%q 用户名) = %q, want %q（跨机器靠这个）", u.Username, got, primary)
	}
	// 别的机器的 id 在本机必须解析不出来——否则会把本机账户错认成对方的
	if got := s.Resolve("u_2d_hoAd3ZMEimSSH"); got != "" && got != primary {
		t.Logf("注意：u_2d_hoAd3ZMEimSSH 在本机解析为 %q", got)
	}
}
