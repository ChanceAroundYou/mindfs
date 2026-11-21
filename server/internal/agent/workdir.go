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

// TaskWorktreeDirName 是任务工作树目录名（appcontext.CreateTaskWorktree 固定建在
// <root>/.worktree/<name>），任务工作树的 name 默认是 "task-{task_number}"。
const TaskWorktreeDirName = ".worktree"

// TaskWorktreeParentRoot 判断 path 是否是某个项目下的**任务工作树目录**，是则返回
// 那个宿主项目的路径，否则返回 ""。
//
// 为什么不靠 git 判：任务工作树不是 `git worktree add` 建的，目录里没有 .git 文件
// （只有 .mindfs/.claude/.omc），它是被 .git/info/exclude 里的 /.worktree/ 排除掉的
// 普通目录。在里面跑 `git rev-parse --show-toplevel` 返回的是**主仓库根**，于是
// gitview.IsInsideWorktree 拿主仓库根去查「.git 是不是文件」，主仓库根有的是 .git
// 目录，判据不成立 → 返回 false。实测 .worktree/task-9 的
// IsInsideWorktree = false, err=nil，所以那条守卫拦不住它。
//
// 布局是 mindfs 自己定的（appcontext.CreateTaskWorktree 拼 <root>/.worktree），所以按
// 路径形状判定可靠、不依赖 git 状态；真 git worktree 仍由 .git 文件那条判据管。
// 调用方拿宿主项目路径去比对已注册项目，只在「确实是某个已知项目的任务工作树」时才
// 排除，免得误伤恰好落在这种路径形状上的真项目。
func TaskWorktreeParentRoot(path string) string {
	normalized := NormalizeComparablePath(path)
	if normalized == "" {
		return ""
	}
	parent := filepath.Dir(normalized)
	if filepath.Base(parent) != TaskWorktreeDirName {
		return ""
	}
	return filepath.Dir(parent)
}
