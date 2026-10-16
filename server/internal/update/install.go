package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type installLayout struct {
	Mode   string
	Prefix string
	ExeDir string
}

func (s *Service) installUpdate(ctx context.Context, version string, restart bool) error {
	release, err := s.fetchLatestRelease(ctx)
	if err != nil {
		return err
	}
	asset, err := s.pickAsset(release, version)
	if err != nil {
		return err
	}
	manifest, err := s.fetchAndVerifyManifest(ctx, version)
	if err != nil {
		return err
	}
	artifact, err := manifest.findArtifact(asset.Name)
	if err != nil {
		return err
	}

	tmpDir, err := os.MkdirTemp("", "mindfs-update-*")
	if err != nil {
		return err
	}
	cleanupTmpDir := true
	defer func() {
		if cleanupTmpDir {
			_ = os.RemoveAll(tmpDir)
		}
	}()

	archivePath := filepath.Join(tmpDir, asset.Name)
	if err := s.downloadReleaseAsset(ctx, asset, archivePath); err != nil {
		return err
	}
	if err := verifyFileSHA256(archivePath, artifact.SHA256, artifact.Size); err != nil {
		return err
	}

	s.updateStatus(func(st *Status) {
		st.Status = "installing"
		st.Message = "Installing update..."
	})

	extractDir := filepath.Join(tmpDir, "extract")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		return err
	}
	if err := extractArchive(archivePath, extractDir); err != nil {
		return err
	}
	pkgDir, err := findPackageDir(extractDir)
	if err != nil {
		return err
	}
	if restart && runtime.GOOS == "windows" {
		s.updateStatus(func(st *Status) {
			st.Status = "restarting"
			st.Message = "Restarting service..."
		})
		if err := s.restartInstalledBinary(pkgDir); err != nil {
			return err
		}
		cleanupTmpDir = false
		return nil
	}
	if err := s.installPackage(pkgDir); err != nil {
		return err
	}
	if !restart {
		return nil
	}

	s.updateStatus(func(st *Status) {
		st.Status = "restarting"
		st.Message = "Restarting service..."
	})
	if err := s.restartInstalledBinary(""); err != nil {
		return err
	}
	return nil
}

func (s *Service) installPackage(pkgDir string) error {
	layout, err := s.installLayout()
	if err != nil {
		return err
	}
	binName := "mindfs"
	if runtime.GOOS == "windows" {
		binName = "mindfs.exe"
	}
	srcBin := filepath.Join(pkgDir, binName)
	if _, err := os.Stat(srcBin); err != nil {
		return fmt.Errorf("updated binary missing: %w", err)
	}
	dstBin, dstAgents, dstTaskTemplate, dstWeb := layout.destinationPaths(binName)
	if err := os.MkdirAll(filepath.Dir(dstBin), 0o755); err != nil {
		return err
	}
	if err := replaceFile(srcBin, dstBin, 0o755); err != nil {
		return err
	}
	if err := installWebAssets(filepath.Join(pkgDir, "web"), dstWeb); err != nil {
		return err
	}
	srcAgents := filepath.Join(pkgDir, "agents.json")
	if info, err := os.Stat(srcAgents); err == nil && !info.IsDir() {
		if err := os.MkdirAll(filepath.Dir(dstAgents), 0o755); err != nil {
			return err
		}
		if err := replaceFile(srcAgents, dstAgents, 0o644); err != nil {
			return err
		}
	}
	srcTaskTemplate := filepath.Join(pkgDir, "task_template.json")
	if info, err := os.Stat(srcTaskTemplate); err == nil && !info.IsDir() {
		if err := os.MkdirAll(filepath.Dir(dstTaskTemplate), 0o755); err != nil {
			return err
		}
		if err := replaceFile(srcTaskTemplate, dstTaskTemplate, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) restartInstalledBinary(pkgDir string) error {
	exe := s.executable
	if strings.TrimSpace(exe) == "" {
		return errors.New("current executable path unavailable")
	}
	layout, err := s.installLayout()
	if err != nil {
		return err
	}
	binName := "mindfs"
	if runtime.GOOS == "windows" {
		binName = "mindfs.exe"
	}
	dstBin, dstAgents, dstTaskTemplate, dstWeb := layout.destinationPaths(binName)
	log.Printf("[update] restart.begin exe=%s args=%q", exe, s.args)
	if err := startReplacementProcess(os.Getpid(), exe, s.args, os.Stdout, os.Stderr, pkgDir, dstBin, dstAgents, dstTaskTemplate, dstWeb); err != nil {
		return err
	}
	go func() {
		time.Sleep(500 * time.Millisecond)
		os.Exit(0)
	}()
	return nil
}

func (s *Service) canAutoUpdate() bool {
	if strings.TrimSpace(s.executable) == "" {
		return false
	}
	if _, err := updatePublicKey(); err != nil {
		return false
	}
	base := strings.ToLower(filepath.Base(s.executable))
	if base != "mindfs" && base != "mindfs.exe" {
		return false
	}
	_, err := s.installLayout()
	return err == nil
}

func (s *Service) unsupportedMessage() string {
	if _, err := updatePublicKey(); err != nil {
		return "Auto update is unavailable because the release verification public key is not configured."
	}
	base := strings.ToLower(filepath.Base(s.executable))
	if base != "mindfs" && base != "mindfs.exe" {
		return "Auto update is only available for mindfs release binaries."
	}
	return "Auto update is unavailable for the current install path."
}

func (l installLayout) destinationPaths(binName string) (string, string, string, string) {
	if l.Mode == "installed" {
		return filepath.Join(l.Prefix, "bin", binName),
			filepath.Join(l.Prefix, "share", "mindfs", "agents.json"),
			filepath.Join(l.Prefix, "share", "mindfs", "task_template.json"),
			filepath.Join(l.Prefix, "share", "mindfs", "web")
	}
	return filepath.Join(l.ExeDir, binName),
		filepath.Join(l.ExeDir, "agents.json"),
		filepath.Join(l.ExeDir, "task_template.json"),
		filepath.Join(l.ExeDir, "web")
}

func findPackageDir(root string) (string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "mindfs_") {
			return filepath.Join(root, entry.Name()), nil
		}
	}
	return "", errors.New("unexpected archive structure")
}

func replaceFile(src, dst string, mode os.FileMode) error {
	tmp := dst + ".tmp"
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(tmp, dst)
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return replaceFile(path, target, info.Mode())
	})
}

// webAssetTTL 是旧构建残留的保留期，与 Makefile 的 WEB_ASSET_TTL_DAYS 对齐。
const webAssetTTL = 14 * 24 * time.Hour

// installWebAssets 覆盖复制 web 资源，绝不清空 dstWeb。
//
// 哈希资源对外是 immutable + 一年缓存，所以浏览器会一直按旧哈希取；一旦更新时把整棵
// 目录删掉，更新期间已打开的页面在懒加载（动态 import）旧 chunk 时就是 404。磁盘占用
// 改用按龄清理兜住。包内没有 web/ 时不动 dstWeb。
func installWebAssets(srcWeb, dstWeb string) error {
	info, err := os.Stat(srcWeb)
	if err != nil || !info.IsDir() {
		return nil
	}
	if err := copyDir(srcWeb, dstWeb); err != nil {
		return err
	}
	pruneStaleWebAssets(filepath.Join(dstWeb, "assets"), webAssetTTL)
	return nil
}
