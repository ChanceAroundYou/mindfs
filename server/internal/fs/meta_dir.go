package fs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"mindfs/server/internal/apperr"
)

func (r RootInfo) EnsureMetaDir() (string, error) {
	metaDir := r.MetaDir()
	if metaDir == "" {
		return "", errors.New("root required")
	}
	isProjectMeta := r.effectiveMetaLocation() == MetaLocationProject
	mode := os.FileMode(0o755)
	if !isProjectMeta {
		mode = 0o700
	}
	if err := os.MkdirAll(metaDir, mode); err != nil {
		return "", apperr.Wrap("mkdir", metaDir, err)
	}
	if isProjectMeta {
		rootDir, err := r.rootDir()
		if err != nil {
			return "", err
		}
		if err := ensureProjectMetaExcluded(rootDir); err != nil {
			return "", err
		}
	}
	if err := r.ensureHomeMetaIdentity(); err != nil {
		return "", err
	}
	return metaDir, nil
}

func ensureProjectMetaExcluded(rootDir string) error {
	gitDir := filepath.Join(rootDir, ".git")
	info, err := os.Stat(gitDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return nil
	}

	gitInfoDir := filepath.Join(gitDir, "info")
	if err := os.MkdirAll(gitInfoDir, 0o755); err != nil {
		return err
	}
	excludePath := filepath.Join(gitInfoDir, "exclude")
	lock := metaFileLock(excludePath)
	lock.Lock()
	defer lock.Unlock()

	data, err := os.ReadFile(excludePath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	const entry = "/.mindfs/"
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == entry {
			return nil
		}
	}

	file, err := os.OpenFile(excludePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		if _, err := file.WriteString("\n"); err != nil {
			_ = file.Close()
			return err
		}
	}
	if _, err := file.WriteString(entry + "\n"); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func (r RootInfo) resolveMetaPath(path string) (string, error) {
	if path == "" {
		path = "."
	}
	clean := filepath.Clean(path)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("path outside metadata directory")
	}
	// 走共享 meta：上传的文件与文件批注两个账户共用一份
	metaDir := r.SharedMetaDir()
	if metaDir == "" {
		return "", errors.New("managed dir required")
	}
	return filepath.Join(metaDir, clean), nil
}

func isMetaLogicalPath(path string) bool {
	clean := filepath.ToSlash(filepath.Clean(path))
	return clean == metaDirName || strings.HasPrefix(clean, metaDirName+"/")
}

// ResolvePath resolves a project-logical path. In home mode, .mindfs paths are
// virtual project paths backed by ~/.mindfs/<rootId>.
func (r RootInfo) ResolvePath(path string) (string, error) {
	clean, err := r.NormalizePath(path)
	if err != nil {
		return "", err
	}
	if r.effectiveMetaLocation() == MetaLocationHome && isMetaLogicalPath(clean) {
		rel := strings.TrimPrefix(filepath.ToSlash(clean), metaDirName)
		rel = strings.TrimPrefix(rel, "/")
		return r.resolveMetaPath(filepath.FromSlash(rel))
	}
	return r.resolveRelativePath(clean)
}

func (r RootInfo) ListMetaEntries(path string) ([]os.DirEntry, error) {
	resolved, err := r.resolveMetaPath(path)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(resolved)
	return entries, apperr.Wrap("list", resolved, err)
}

func (r RootInfo) OpenMetaFile(path string) (*os.File, error) {
	resolved, err := r.resolveMetaPath(path)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(resolved)
	return file, apperr.Wrap("open", resolved, err)
}

func (r RootInfo) OpenMetaFileAppend(path string) (*os.File, error) {
	resolved, err := r.resolveMetaPath(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return nil, apperr.Wrap("mkdir", filepath.Dir(resolved), err)
	}
	file, err := os.OpenFile(resolved, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	return file, apperr.Wrap("write", resolved, err)
}

func (r RootInfo) ReadMetaFile(path string) ([]byte, error) {
	resolved, err := r.resolveMetaPath(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(resolved)
	return data, apperr.Wrap("read", resolved, err)
}

func (r RootInfo) StatMetaFile(path string) (os.FileInfo, error) {
	resolved, err := r.resolveMetaPath(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(resolved)
	return info, apperr.Wrap("stat", resolved, err)
}

func (r RootInfo) WriteMetaFile(path string, data []byte) error {
	resolved, err := r.resolveMetaPath(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return apperr.Wrap("mkdir", filepath.Dir(resolved), err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(resolved), filepath.Base(resolved)+".tmp-*")
	if err != nil {
		return apperr.Wrap("create", filepath.Dir(resolved), err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return apperr.Wrap("write", tmpName, err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return apperr.Wrap("write", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return apperr.Wrap("write", tmpName, err)
	}
	if err := os.Rename(tmpName, resolved); err != nil {
		return apperr.Wrap("rename", resolved, err)
	}
	cleanup = false
	return nil
}
