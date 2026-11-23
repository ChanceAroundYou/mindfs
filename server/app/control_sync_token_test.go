package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/server/internal/controlsync"
)

// writeToken 造一份 token 文件并返回路径。
func writeToken(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	return path
}

// TestEnsurePrimarySyncTokenGeneratesOnce 钉「默认开」的另一半：
// 主节点没 token 时自己生成一份，而且**只生成一次** —— 重启换钥匙会让所有边缘掉队。
func TestEnsurePrimarySyncTokenGeneratesOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control_sync.token")
	first, err := ensurePrimarySyncToken(path)
	if err != nil {
		t.Fatalf("ensurePrimarySyncToken: %v", err)
	}
	if err := controlsync.ValidateSyncToken(first); err != nil {
		t.Fatalf("生成的 token %q 不合法：%v", first, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("token 文件没落盘：%v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("文件权限 = %o，应为 0600", got)
	}

	second, err := ensurePrimarySyncToken(path)
	if err != nil {
		t.Fatalf("第二次调用：%v", err)
	}
	if second != first {
		t.Fatalf("第二次生成了新 token：%q != %q（重启不得换钥匙）", second, first)
	}
}

// TestEnsurePrimarySyncTokenKeepsUserSupplied 钉「已有可用 token 不覆盖」：
// 用户手放进去的密钥优先级高于自动生成。
func TestEnsurePrimarySyncTokenKeepsUserSupplied(t *testing.T) {
	path := writeToken(t, t.TempDir(), "sync.token", "  user-supplied-token-0123456789\n")
	got, err := ensurePrimarySyncToken(path)
	if err != nil {
		t.Fatalf("ensurePrimarySyncToken: %v", err)
	}
	if got != "user-supplied-token-0123456789" {
		t.Fatalf("token = %q，应沿用文件里的值（去空白）", got)
	}
}

// TestEnsurePrimarySyncTokenReplacesEmpty 钉「文件在但为空」也算没有：
// 手工 `touch` 出来的空文件不该让主节点拒绝启动。
func TestEnsurePrimarySyncTokenReplacesEmpty(t *testing.T) {
	path := writeToken(t, t.TempDir(), "sync.token", " \n\t\n")
	got, err := ensurePrimarySyncToken(path)
	if err != nil {
		t.Fatalf("ensurePrimarySyncToken: %v", err)
	}
	if strings.TrimSpace(got) == "" {
		t.Fatal("空文件应被替换成一份真 token")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.TrimSpace(string(raw)) != got {
		t.Fatalf("文件内容 %q 与返回值 %q 不一致", raw, got)
	}
}

// TestEnsurePrimarySyncTokenRejectsDirectory 钉「读失败 ≠ 没有」：
// 路径被占成一个目录时不能静默生成，那会写成一份永远读不到的新 token。
func TestEnsurePrimarySyncTokenRejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token-dir")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := ensurePrimarySyncToken(path); err == nil {
		t.Fatal("路径是目录时应报错，而不是覆盖")
	}
}

// TestEnsurePrimarySyncTokenRequiresPath：没有路径就无从落盘，必须报错。
func TestEnsurePrimarySyncTokenRequiresPath(t *testing.T) {
	if _, err := ensurePrimarySyncToken("  "); err == nil {
		t.Fatal("空路径应报错")
	}
}

// TestEnsurePrimarySyncTokenTightensExistingDir 钉权限：配置目录可能是 0644 建的，
// 里面放的是进整份控制面的钥匙。
func TestEnsurePrimarySyncTokenTightensExistingDir(t *testing.T) {
	dir := t.TempDir()
	loose := filepath.Join(dir, "mindfs")
	if err := os.MkdirAll(loose, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := ensurePrimarySyncToken(filepath.Join(loose, "control_sync.token")); err != nil {
		t.Fatalf("ensurePrimarySyncToken: %v", err)
	}
	info, err := os.Stat(loose)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("目录权限 = %o，应被收紧到 0700", got)
	}
}
