package usecase

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"mindfs/server/internal/apperr"
)

type ListLocalDirsInput struct {
	Path string
}

type LocalDirItem struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	IsDir       bool   `json:"is_dir"`
	IsAddedRoot bool   `json:"is_added_root"`
	RootID      string `json:"root_id,omitempty"`
}

type ListLocalDirsOutput struct {
	Path    string         `json:"path"`
	Parent  string         `json:"parent,omitempty"`
	Volumes []LocalDirItem `json:"volumes,omitempty"`
	Items   []LocalDirItem `json:"items"`
}

func (s *Service) ListLocalDirs(_ context.Context, in ListLocalDirsInput) (ListLocalDirsOutput, error) {
	if err := s.ensureRegistry(); err != nil {
		return ListLocalDirsOutput{}, err
	}
	cleaned := strings.TrimSpace(in.Path)
	if cleaned == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return ListLocalDirsOutput{}, err
		}
		cleaned = homeDir
	}
	absPath, err := filepath.Abs(filepath.Clean(cleaned))
	if err != nil {
		return ListLocalDirsOutput{}, err
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return ListLocalDirsOutput{}, apperr.Wrap("stat", absPath, err)
	}
	if !info.IsDir() {
		return ListLocalDirsOutput{}, errors.New("path is not a directory")
	}
	rootPathMap := s.localDirRootPathMap()
	entries, err := os.ReadDir(absPath)
	if err != nil {
		return ListLocalDirsOutput{}, apperr.Wrap("list", absPath, err)
	}
	items := make([]LocalDirItem, 0, len(entries))
	for _, entry := range entries {
		name := strings.TrimSpace(entry.Name())
		if name == "" {
			continue
		}
		childPath := filepath.Join(absPath, name)
		info, err := entry.Info()
		if err != nil {
			// Permission-gated or special entries: skip rather than blank the listing.
			continue
		}
		isDir := entry.IsDir()
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Stat(childPath)
			if err != nil {
				// Broken symlink.
				continue
			}
			isDir = target.IsDir()
		}
		if !isDir {
			continue
		}
		normalizedChild := normalizeLocalDirPath(childPath)
		item := LocalDirItem{
			Name:  name,
			Path:  childPath,
			IsDir: true,
		}
		if rootID, ok := rootPathMap[normalizedChild]; ok {
			item.IsAddedRoot = true
			item.RootID = rootID
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
	parent := filepath.Dir(absPath)
	if parent == absPath {
		parent = ""
	}
	return ListLocalDirsOutput{
		Path:    absPath,
		Parent:  parent,
		Volumes: listLocalDirVolumes(rootPathMap),
		Items:   items,
	}, nil
}

func (s *Service) localDirRootPathMap() map[string]string {
	rootPathMap := make(map[string]string)
	for _, root := range s.Registry.ListRoots() {
		normalized := normalizeLocalDirPath(root.RootPath)
		if normalized == "" {
			continue
		}
		rootPathMap[normalized] = root.ID
	}
	return rootPathMap
}

type CreateLocalDirInput struct {
	// Path 是已经存在的父目录绝对路径；空 = 家目录，与 ListLocalDirs 同口径。
	Path string
	// Name 是单段目录名，不接受路径分隔符。
	Name string
}

type CreateLocalDirOutput struct {
	Path   string `json:"path"`
	Parent string `json:"parent"`
	Name   string `json:"name"`
}

// CreateLocalDir 在尚未注册为项目的普通目录下新建一层文件夹。
//
// 为什么不复用现有入口：CreateEntry 要求 rootID（目录得先被注册成项目，正是本次
// 要绕开的循环），AddManagedDir(create) 会在建目录的同时把目录注册为项目 ——
// 用户此刻还在浏览、还没决定导入哪个。这里只建目录，注册仍由随后的「添加」完成。
func (s *Service) CreateLocalDir(_ context.Context, in CreateLocalDirInput) (CreateLocalDirOutput, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		return CreateLocalDirOutput{}, errors.New("invalid folder name")
	}
	if name == ".mindfs" {
		// 与 CreateEntry 同一条护栏：项目根下的 .mindfs 归程序所有（AGENTS.md #12）。
		return CreateLocalDirOutput{}, errors.New("cannot create .mindfs folder")
	}
	parent := strings.TrimSpace(in.Path)
	if parent == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return CreateLocalDirOutput{}, err
		}
		parent = homeDir
	}
	absParent, err := filepath.Abs(filepath.Clean(parent))
	if err != nil {
		return CreateLocalDirOutput{}, err
	}
	info, err := os.Stat(absParent)
	if err != nil {
		return CreateLocalDirOutput{}, apperr.Wrap("stat", absParent, err)
	}
	if !info.IsDir() {
		return CreateLocalDirOutput{}, errors.New("path is not a directory")
	}
	target := filepath.Join(absParent, name)
	if err := os.Mkdir(target, 0o755); err != nil {
		// os.ErrExist / os.ErrNotExist / 权限错误原样上抛，由 handler 映射成 409/404/403。
		return CreateLocalDirOutput{}, err
	}
	return CreateLocalDirOutput{Path: target, Parent: absParent, Name: name}, nil
}

func normalizeLocalDirPath(path string) string {
	cleaned := strings.TrimSpace(path)
	if cleaned == "" {
		return ""
	}
	absPath, err := filepath.Abs(filepath.Clean(cleaned))
	if err != nil {
		return filepath.Clean(cleaned)
	}
	if runtime.GOOS == "windows" {
		return strings.ToLower(absPath)
	}
	return absPath
}
