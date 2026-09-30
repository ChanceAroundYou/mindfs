package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/server/internal/agent"
	"mindfs/server/internal/fs"
)

// 任务工作树（<root>/.worktree/task-N）曾经被 autoAddExternalProjectRoots 当成外部项目
// 收进注册表，项目列表里就冒出一个叫 task-9 的条目，得手动删。
//
// 拦不住的原因是 git 判据选错了对象：任务工作树不是 `git worktree add` 建的，里面没有
// .git 文件，跑 `git rev-parse --show-toplevel` 返回的是主仓库根，于是 IsInsideWorktree
// 拿主仓库根去问「.git 是不是文件」，主仓库根有的是 .git 目录 → false, nil。
func TestPruneTaskWorktreeRootsRemovesRegisteredWorktree(t *testing.T) {
	tmp := t.TempDir()
	projectRoot := filepath.Join(tmp, "projects", "mindfs")
	worktreeDir := filepath.Join(projectRoot, agent.TaskWorktreeDirName, "task-9")
	for _, dir := range []string{projectRoot, worktreeDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	registry := fs.NewRegistry(filepath.Join(tmp, "registry.json"))
	if _, err := registry.UpsertWithMetaLocation(projectRoot, fs.MetaLocationProject); err != nil {
		t.Fatalf("register project: %v", err)
	}
	worktreeRoot, err := registry.UpsertWithMetaLocation(worktreeDir, fs.MetaLocationProject)
	if err != nil {
		t.Fatalf("register worktree: %v", err)
	}

	existing := map[string]struct{}{agent.NormalizeComparablePath(projectRoot): {}}
	pruneTaskWorktreeRoots(registry, existing)

	if _, ok := registry.Get(worktreeRoot.ID); ok {
		t.Fatal("task worktree root should have been pruned from the registry")
	}
	if _, ok := registry.Get("mindfs"); !ok {
		t.Fatal("the host project must survive the prune")
	}
	// 只摘注册表，不动磁盘：任务可能正在这个目录里跑。
	if info, err := os.Stat(worktreeDir); err != nil || !info.IsDir() {
		t.Fatalf("worktree dir must be left on disk, stat err=%v", err)
	}
}

// 仅仅「碰巧长成 .worktree/<x> 这个形状」、但宿主并不在册的目录，是普通外部项目，
// 不能被这条守卫误伤。
func TestPruneTaskWorktreeRootsKeepsUnregisteredHost(t *testing.T) {
	tmp := t.TempDir()
	hostRoot := filepath.Join(tmp, "elsewhere", "notaproject")
	worktreeDir := filepath.Join(hostRoot, agent.TaskWorktreeDirName, "task-3")
	for _, dir := range []string{hostRoot, worktreeDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	registry := fs.NewRegistry(filepath.Join(tmp, "registry.json"))
	worktreeRoot, err := registry.UpsertWithMetaLocation(worktreeDir, fs.MetaLocationProject)
	if err != nil {
		t.Fatalf("register worktree: %v", err)
	}

	// existing 里没有 hostRoot —— 守卫应当放行。
	pruneTaskWorktreeRoots(registry, map[string]struct{}{})

	if _, ok := registry.Get(worktreeRoot.ID); !ok {
		t.Fatal("a same-shaped dir whose host is not registered must be kept")
	}
}

// 发现循环里那条守卫：扫描到的路径若是已注册项目的任务工作树，就不能再收成项目。
// 这正是 task-9 溜进项目列表的那一步。
func TestIsTaskWorktreeOfRegisteredProject(t *testing.T) {
	host := "/home/u/projects/mindfs"
	worktree := filepath.Join(host, agent.TaskWorktreeDirName, "task-9")
	registered := map[string]struct{}{agent.NormalizeComparablePath(host): {}}

	if !isTaskWorktreeOfRegisteredProject(worktree, registered) {
		t.Fatal("a task worktree under a registered project must be filtered out of discovery")
	}
	if isTaskWorktreeOfRegisteredProject(host, registered) {
		t.Fatal("the host project itself is a real project, not a worktree")
	}
	// 宿主不在册 → 只是碰巧同形状，不排除
	if isTaskWorktreeOfRegisteredProject(
		filepath.Join("/home/u/other", agent.TaskWorktreeDirName, "task-3"),
		registered,
	) {
		t.Fatal("a same-shaped dir whose host is not registered must not be filtered")
	}
	if isTaskWorktreeOfRegisteredProject("", registered) {
		t.Fatal("empty path is not a worktree")
	}
}

// 发现循环必须真的调用那条守卫。单测 isTaskWorktreeOfRegisteredProject 只钉住判断逻辑，
// 删掉调用点它照样绿 —— 这里补一条源码断言把接线也钉住。
func TestAutoAddExternalProjectRootsCallsWorktreeGuard(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}
	start := bytes.Index(src, []byte("func autoAddExternalProjectRoots("))
	if start < 0 {
		t.Fatal("autoAddExternalProjectRoots not found")
	}
	end := bytes.Index(src[start:], []byte("\nfunc "))
	if end < 0 {
		t.Fatal("could not delimit autoAddExternalProjectRoots")
	}
	body := string(src[start : start+end])
	if !strings.Contains(body, "isTaskWorktreeOfRegisteredProject(projectPath, existing)") {
		t.Fatal("the discovery loop must skip task worktrees of registered projects")
	}
	// 清理历史遗留那一步也得在，否则已经在册的 task-9 永远留着。
	if !strings.Contains(body, "pruneTaskWorktreeRoots(registry, existing)") {
		t.Fatal("the discovery loop must prune already-registered task worktrees")
	}
}

func TestTaskWorktreeParentRoot(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/home/u/projects/mindfs/.worktree/task-9", "/home/u/projects/mindfs"},
		{"/home/u/projects/mindfs/.worktree/session-0926-01", "/home/u/projects/mindfs"},
		{"/home/u/projects/mindfs", ""},
		{"/home/u/projects/mindfs/.worktree", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := agent.TaskWorktreeParentRoot(tc.path); got != tc.want {
			t.Errorf("TaskWorktreeParentRoot(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}
