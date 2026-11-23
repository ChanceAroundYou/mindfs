package app

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"mindfs/server/internal/controlsync"
)

// syncTokenBytes 是自生成 token 的熵。32 字节 → 43 字符 base64url，
// 与 ValidateSyncToken 的 16 字节下限留足余量。
const syncTokenBytes = 32

// ensurePrimarySyncToken 保证主节点手上有一份可用的同步密钥。
//
// 首次上线不该要求用户先手搓一个 32 字节随机串再写进文件，所以主节点在这条路径上
// 自己生成。语义只有三条：
//
//   - 文件里已有可用 token → 原样用，不覆盖（重启不该换钥匙，换了所有边缘都得重配）；
//   - 文件不存在或为空 → 生成并写 0600；目录 0700；
//   - 其它读失败（权限、是目录）→ 报错，别把「读不到」当成「没有」。
//
// 日志只打路径，**永不打 token 本身** —— 这条日志会和别的东西一起进 journal。
func ensurePrimarySyncToken(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("control_sync: token_file is required to generate a sync token")
	}
	switch existing, err := readSyncTokenFile(path); {
	case err == nil:
		return existing, nil
	case !errors.Is(err, os.ErrNotExist) && !errors.Is(err, errSyncTokenBlank):
		return "", fmt.Errorf("control_sync: read token_file: %w", err)
	}
	token, err := newSyncToken()
	if err != nil {
		return "", err
	}
	if err := controlsync.WriteSyncTokenFile(path, token); err != nil {
		return "", err
	}
	log.Printf("[control-sync] generated sync token at %s (share it with edge nodes)", path)
	return token, nil
}

// errSyncTokenBlank 只在本包内用来区分「文件在但内容为空」与「文件不存在」。
var errSyncTokenBlank = errors.New("control_sync: token file blank")

// readSyncTokenFile 读一份 token 文件；`echo` 生成的文件几乎必然带行尾换行。
func readSyncTokenFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", errSyncTokenBlank
	}
	return token, nil
}

func newSyncToken() (string, error) {
	var raw [syncTokenBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("control_sync: generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}
