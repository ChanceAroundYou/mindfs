package session

import (
	"testing"

	"mindfs/server/internal/fs"
)

// newCompatTestRoot 建一个指向临时目录的 RootInfo，供 compat 测试写原始 JSONL 行。
func newCompatTestRoot(t *testing.T) fs.RootInfo {
	t.Helper()
	return fs.NewRootInfo("root", "Compat", t.TempDir())
}
