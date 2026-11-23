package controlsync

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

// WriteSyncTokenFile 把 token 落盘。先写临时文件再改名：中途失败不会留下半份 token
// （半个 token 比没有更难查 —— 它看起来像配置存在，只是永远验不过）。
//
// 目录与文件权限在写完后再收紧一次：目录可能早就存在（0644 建的），
// 而 MkdirAll 不会改已有目录的权限。
//
// 主节点首次生成与边缘配对共用这一份：两处各写一版的话，权限或换行哪天只改了一边，
// 症状是「同一个 token 在一台机器上能用、在另一台上验不过」。
func WriteSyncTokenFile(path, token string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("control_sync: token_file path is empty")
	}
	if strings.TrimSpace(token) == "" {
		return errors.New("control_sync: refusing to write an empty sync token")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("control_sync: create token dir: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("control_sync: chmod token dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".control_sync.token-*")
	if err != nil {
		return fmt.Errorf("control_sync: create token file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.WriteString(strings.TrimSpace(token) + "\n"); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("control_sync: write token file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("control_sync: close token file: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("control_sync: chmod token file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("control_sync: install token file: %w", err)
	}
	return nil
}
