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

func (m *Manager) ExchangeLogAbsolutePath(key string) string {
	path, err := m.exchangePath(key)
	if err != nil {
		return ""
	}
	metaDir := m.root.MetaDir()
	if metaDir == "" {
		return ""
	}
	return filepath.Join(metaDir, filepath.FromSlash(path))
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
