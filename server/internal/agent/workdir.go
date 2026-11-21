package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func EnsureStableWorkDir(kind, agentName string) (string, error) {
	base := filepath.Join(os.TempDir(), "mindfs-"+strings.TrimSpace(kind))
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", err
	}
	name := strings.TrimSpace(agentName)
	if name == "" {
		name = "default"
	}
	path := filepath.Join(base, name)
	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", err
	}
	return path, nil
}

// temporaryRootDirs 返回所有应当看作临时/草稿空间的根目录。
//
// 为什么不止 os.TempDir()：生产 systemd 单元为大文件上传把 TMPDIR 指到
// /var/tmp，而 /tmp 仍是系统的临时盘（tmpfs）。只比较 os.TempDir() 会让
// /tmp/** 下的隔离实例、探针与崩溃注入目录被当成正式项目自动注册。
func temporaryRootDirs() []string {
	roots := []string{os.TempDir()}
	if runtime.GOOS != "windows" {
		roots = append(roots, "/tmp", "/var/tmp")
	}
	if runtimeDir := strings.TrimSpace(os.Getenv("XDG_RUNTIME_DIR")); runtimeDir != "" {
		roots = append(roots, runtimeDir)
	}
	return roots
}

func IsTemporaryWorkDir(path string) bool {
	normalizedPath := NormalizeComparablePath(path)
	if normalizedPath == "" {
		return false
	}
	for _, root := range temporaryRootDirs() {
		normalizedRoot := NormalizeComparablePath(root)
		if normalizedRoot == "" {
			continue
		}
		if isPathWithin(normalizedPath, normalizedRoot) {
			return true
		}
	}
	return false
}

func isPathWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || filepath.IsAbs(rel) {
		return false
	}
	return rel == "." || !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
