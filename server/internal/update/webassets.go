package update

import (
	"os"
	"path/filepath"
	"time"
)

// pruneStaleWebAssets 只按龄清理哈希资源目录。目录不存在或读取失败都直接跳过：
// 清理是尽力而为，不该让更新失败。
func pruneStaleWebAssets(dir string, ttl time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-ttl)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, entry.Name()))
	}
}
