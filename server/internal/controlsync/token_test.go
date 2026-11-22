package controlsync

import (
	"errors"
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
}
