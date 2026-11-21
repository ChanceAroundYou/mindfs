package controlsync

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"os"
	"strings"
)

// TokenEnvVar 是同步 token 的环境变量名。环境变量优先于配置与文件 ——
// 设备专属密钥不该写进会被复制/提交的 config.json（spec §9）。
const TokenEnvVar = "MINDFS_CONTROL_SYNC_TOKEN"

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

// ResolveSyncToken 按「环境变量 → token_file → 内联值」取同步 token。
//
// 配了文件却读不出来直接报错，不静默退到内联值：那会让「换成了新 token、却还在用
// 旧的」表现成对端 401，比启动失败难查得多。
func ResolveSyncToken(inline, file string) (string, error) {
	if v := strings.TrimSpace(os.Getenv(TokenEnvVar)); v != "" {
		return v, ValidateSyncToken(v)
	}
	if file = strings.TrimSpace(file); file != "" {
		raw, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("control_sync: read token_file: %w", err)
		}
		token := strings.TrimSpace(string(raw))
		if token == "" {
			return "", fmt.Errorf("%w: token_file %q is empty", ErrSyncToken, file)
		}
		return token, ValidateSyncToken(token)
	}
	inline = strings.TrimSpace(inline)
	if inline == "" {
		return "", nil
	}
	return inline, ValidateSyncToken(inline)
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
