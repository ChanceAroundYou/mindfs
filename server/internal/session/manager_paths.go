package session

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"mindfs/server/internal/fs"

	_ "modernc.org/sqlite"
)

func (m *Manager) MetaDir() string {
	return m.root.MetaDir()
}

func (m *Manager) Root() fs.RootInfo {
	return m.root
}

func (m *Manager) ExchangeLogPath(key string) string {
	path, err := m.exchangePath(key)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(filepath.Join(".mindfs", path))
}

// ExchangeLogAbsolutePath 必须与 appendExchange / loadExchanges 用同一个解析器：
// 提示路径是给 agent 去读的，拼错目录就等于让它读一个不存在的文件。
func (m *Manager) ExchangeLogAbsolutePath(key string) string {
	path, err := m.exchangePath(key)
	if err != nil {
		return ""
	}
	abs, err := m.root.MetaFileAbsolutePath(path)
	if err != nil {
		return ""
	}
	return abs
}

func (m *Manager) exchangePath(key string) (string, error) {
	if strings.TrimSpace(m.root.MetaDir()) == "" {
		return "", errors.New("managed dir required")
	}
	if key == "" {
		return "", errors.New("session key required")
	}
	if strings.Contains(key, "..") || strings.ContainsRune(key, filepath.Separator) || strings.Contains(key, "/") {
		return "", fmt.Errorf("invalid session key: %s", key)
	}
	return filepath.ToSlash(fmt.Sprintf(exchangeFileTpl, key)), nil
}
