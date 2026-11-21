package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mindfs/server/internal/api"
	"mindfs/server/internal/fs"
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
