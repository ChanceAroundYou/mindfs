package app

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mindfs/server/internal/agent"
	"mindfs/server/internal/api"
	"mindfs/server/internal/fs"
	"mindfs/server/internal/gitview"
	"mindfs/server/internal/preferences"
	"mindfs/server/internal/tlsutil"
)
func normalizeRegisteredForkSessions(ctx context.Context, services *api.AppContext) error {
	for _, root := range services.ListRoots() {
		manager, err := services.GetSessionManager(root.ID)
		if err != nil {
			return fmt.Errorf("initialize session store for root %s: %w", root.ID, err)
		}
		if _, err := manager.ListMetas(ctx); err != nil {
			return fmt.Errorf("normalize fork sessions for root %s: %w", root.ID, err)
		}
	}
	return nil
}

func autoAddExternalProjectRoots(registry *fs.Registry, prefs *preferences.Store) {
	if registry == nil {
		return
	}
	existing := make(map[string]struct{})
	for _, root := range registry.List() {
		normalized := agent.NormalizeComparablePath(root.RootPath)
		if normalized != "" {
			existing[normalized] = struct{}{}
		}
	}
	// 清理历史遗留：任务工作树被误收进来的那几笔（task-9 之类）。只摘注册表，不动磁盘
	// 上的目录 —— 任务还在用它，文件得留着。
	pruneTaskWorktreeRoots(registry, existing)
	added := 0
	for _, projectPath := range agent.DiscoverExternalProjectPaths() {
		normalized := agent.NormalizeComparablePath(projectPath)
		if normalized == "" {
			continue
		}
		if _, ok := existing[normalized]; ok {
			continue
		}
		if hasMindFSMetadataDir(projectPath) {
			continue
		}
		if agent.IsTemporaryWorkDir(projectPath) {
			continue
		}
		// 任务工作树（<root>/.worktree/task-N）不是项目：任务在里头跑，agent 把这个
		// cwd 记进 ~/.claude/projects，下次扫描就会把它当外部项目收进来（历史上确实
		// 冒出过 task-9）。git 那条判据拦不住它 —— 见 agent.TaskWorktreeParentRoot 注释。
		if isTaskWorktreeOfRegisteredProject(projectPath, existing) {
			continue
		}
		gitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		isWorktree, err := gitview.IsInsideWorktree(gitCtx, projectPath)
		cancel()
		if err == nil && isWorktree {
			continue
		}
		location := fs.MetaLocationProject
		if prefs != nil {
			location = prefs.NewProjectMetaLocation()
		}
		rootID := filepath.Base(filepath.Clean(projectPath))
		pending := fs.NewRootInfo(rootID, rootID, projectPath)
		pending.MetaLocation = location
		if _, err := pending.EnsureMetaDir(); err != nil {
			log.Printf("[startup/projects] auto add metadata skipped path=%s err=%v", projectPath, err)
			continue
		}
		if _, err := registry.UpsertWithMetaLocation(projectPath, location); err != nil {
			log.Printf("[startup/projects] auto add skipped path=%s err=%v", projectPath, err)
			continue
		}
		existing[normalized] = struct{}{}
		added++
	}
	if added > 0 {
		log.Printf("[startup/projects] auto added external project roots count=%d", added)
	}
}

// isTaskWorktreeOfRegisteredProject 判断 path 是不是某个**已注册项目**下的任务工作树。
//
// 只有宿主在册时才当任务工作树排除；只是碰巧长成 <某目录>/.worktree/<name> 这个形状、
// 而宿主并不在注册表里的，照旧按普通外部项目处理，免得误伤真项目。
func isTaskWorktreeOfRegisteredProject(path string, existing map[string]struct{}) bool {
	host := agent.TaskWorktreeParentRoot(path)
	if host == "" {
		return false
	}
	_, ok := existing[host]
	return ok
}

// pruneTaskWorktreeRoots 把历史上被误收进来的任务工作树从注册表里摘掉。
//
// 只在「宿主项目确实已注册」时才摘，且只动注册表不动磁盘 —— 任务可能正在这个目录里跑，
// 删文件会把在跑的任务连坐掉（RemoveManagedDir 同样只摘注册表，正是这个理由）。
func pruneTaskWorktreeRoots(registry *fs.Registry, existing map[string]struct{}) {
	for _, root := range registry.List() {
		host := agent.TaskWorktreeParentRoot(root.RootPath)
		if host == "" {
			continue
		}
		if _, ok := existing[host]; !ok {
			continue
		}
		if _, err := registry.Remove(root.RootPath); err != nil {
			log.Printf("[startup/projects] prune task worktree root failed id=%s path=%s err=%v", root.ID, root.RootPath, err)
			continue
		}
		delete(existing, agent.NormalizeComparablePath(root.RootPath))
		log.Printf("[startup/projects] pruned task worktree root id=%s path=%s host=%s", root.ID, root.RootPath, host)
	}
}

func hasMindFSMetadataDir(projectPath string) bool {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(projectPath, ".mindfs"))
	return err == nil && info.IsDir()
}

func startExternalProjectDiscoveryLoop(ctx context.Context, registry *fs.Registry, prefs *preferences.Store) {
	if registry == nil {
		return
	}
	ticker := time.NewTicker(externalProjectDiscoveryInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				autoAddExternalProjectRoots(registry, prefs)
			}
		}
	}()
}

func resolveStaticDir() string {
	if hinted := strings.TrimSpace(os.Getenv(staticDirEnvKey)); hinted != "" {
		if info, err := os.Stat(hinted); err == nil && info.IsDir() {
			return hinted
		}
	}

	if exe, err := os.Executable(); err == nil {
		return resolveStaticDirFromExecutable(exe)
	}
	return ""
}

func resolveStaticDirFromExecutable(exe string) string {
	exe = strings.TrimSpace(exe)
	if exe == "" {
		return ""
	}
	exeDir := filepath.Dir(exe)
	// 本地构建布局为仓库根目录 mindfs + web/dist。
	candidate := filepath.Join(exeDir, "web", "dist")
	if isFrontendStaticDir(candidate) {
		return candidate
	}
	// 发布 zip 解压后，web 目录和可执行文件在同一层级。
	candidate = filepath.Join(exeDir, "web")
	if isFrontendStaticDir(candidate) {
		return candidate
	}
	// 安装布局为 <prefix>/bin/mindfs + <prefix>/share/mindfs/web。
	candidate = filepath.Join(filepath.Dir(exeDir), "share", "mindfs", "web")
	if isFrontendStaticDir(candidate) {
		return candidate
	}
	return ""
}

func isFrontendStaticDir(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	for _, name := range []string{"index.html", "favicon.svg"} {
		info, err := os.Stat(filepath.Join(path, name))
		if err != nil || info.IsDir() {
			return false
		}
	}
	return true
}

func RemoveManagedDirFromRegistry(path string) error {
	registry, err := fs.NewDefaultRegistry()
	if err != nil {
		return err
	}
	if err := registry.Load(); err != nil {
		return err
	}
	_, err = registry.Remove(path)
	return err
}

// EnsureTLSCert resolves TLS certificate and key file paths for the server.
// When certFlag or keyFlag are empty, a self-signed certificate is generated
// under os.UserConfigDir/mindfs/ and reused across restarts.
func EnsureTLSCert(certFlag, keyFlag string) (string, string, error) {
	return tlsutil.EnsureCert(certFlag, keyFlag)
}
