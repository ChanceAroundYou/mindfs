package controlsync

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateSyncTokenRejectsWeakShapes(t *testing.T) {
	if err := ValidateSyncToken(""); err != nil {
		t.Fatalf("空 token 表示未启用，不该报错：%v", err)
	}
	if err := ValidateSyncToken("0123456789abcdef"); err != nil {
		t.Fatalf("16 字节应合法：%v", err)
	}
	for _, bad := range []string{"short", "has space in the token value", "line\nbreak-in-token-value"} {
		if err := ValidateSyncToken(bad); !errors.Is(err, ErrSyncToken) {
			t.Fatalf("token %q err = %v，应被拒", bad, err)
		}
	}
}

func TestResolveSyncTokenPrecedence(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "token")
	if err := os.WriteFile(file, []byte("  from-file-token-0001\n"), 0o600); err != nil {
		t.Fatalf("写 token 文件: %v", err)
	}
	inline := "from-inline-token-01"

	got, err := ResolveSyncToken(inline, file)
	if err != nil || got != "from-file-token-0001" {
		t.Fatalf("文件优先于内联：%q, %v", got, err)
	}

	t.Setenv(TokenEnvVar, "from-env-token-00001")
	got, err = ResolveSyncToken(inline, file)
	if err != nil || got != "from-env-token-00001" {
		t.Fatalf("环境变量优先于文件：%q, %v", got, err)
	}
}

func TestResolveSyncTokenFailsClosedOnBadFile(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope")
	if _, err := ResolveSyncToken("from-inline-token-01", missing); err == nil {
		t.Fatalf("token_file 读不出来时应报错，而不是退回内联值")
	}
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, []byte("   \n"), 0o600); err != nil {
		t.Fatalf("写空文件: %v", err)
	}
	if _, err := ResolveSyncToken("", empty); !errors.Is(err, ErrSyncToken) {
		t.Fatalf("空 token_file err = %v，应被拒", err)
	}
	if got, err := ResolveSyncToken("", ""); err != nil || got != "" {
		t.Fatalf("什么都没配时应返回空 token 且不报错：%q, %v", got, err)
	}
}

func TestAuthorizeSyncRequest(t *testing.T) {
	const token = "0123456789abcdef"
	cases := []struct {
		name   string
		header string
		want   bool
	}{
		{"正确", "Bearer " + token, true},
		{"大小写不敏感", "bearer " + token, true},
		{"两侧空白", "  Bearer   " + token + "  ", true},
		{"错 token", "Bearer fedcba9876543210", false},
		{"没头", "", false},
		{"不是 bearer", "Basic " + token, false},
		{"只有前缀", "Bearer ", false},
		{"前缀粘着 token", "Bearer" + token, false},
		{"token 被截断", "Bearer 0123456789abcd", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AuthorizeSyncRequest(token, tc.header); got != tc.want {
				t.Fatalf("AuthorizeSyncRequest(%q) = %v，应为 %v", tc.header, got, tc.want)
			}
		})
	}
	if AuthorizeSyncRequest("", "Bearer ") {
		t.Fatalf("未配置 token 时必须一律拒绝")
	}
	if !strings.Contains(TokenEnvVar, "MINDFS") {
		t.Fatalf("环境变量名不带项目前缀，容易和别的服务撞：%s", TokenEnvVar)
	}
}
