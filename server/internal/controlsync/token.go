package controlsync

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
)

// MinSyncTokenLen 是允许的最小 token 长度。这不是强度评估，只是拦住「随手写了
// 一个 abc」这类明显误配：同步通道搬运的是含口令 hash 在内的全部控制面数据。
const MinSyncTokenLen = 16

// ErrSyncToken 是 token 配置/校验的统一错误。
var ErrSyncToken = errors.New("control_sync: invalid token")

// ValidateSyncToken 校验配置里的 token 形状；空 token 表示「不启用同步通道」，
// 由调用方处理，这里只拒绝「非空但不可用」。
func ValidateSyncToken(token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}
	if len(token) < MinSyncTokenLen {
		return fmt.Errorf("%w: must be at least %d bytes", ErrSyncToken, MinSyncTokenLen)
	}
	if strings.ContainsAny(token, " \t\r\n") {
		return fmt.Errorf("%w: must not contain whitespace", ErrSyncToken)
	}
	return nil
}

// AuthorizeSyncRequest 校验一条请求的 `Authorization: Bearer <token>`。
//
// 比对用常量时间，避免按前缀逐字节试探；长度不等时直接失败（长度本身不算秘密，
// 且 ConstantTimeCompare 对不等长也返回 0）。
func AuthorizeSyncRequest(configured string, header string) bool {
	configured = strings.TrimSpace(configured)
	if configured == "" {
		return false
	}
	const prefix = "Bearer "
	header = strings.TrimSpace(header)
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return false
	}
	got := strings.TrimSpace(header[len(prefix):])
	return subtle.ConstantTimeCompare([]byte(got), []byte(configured)) == 1
}
