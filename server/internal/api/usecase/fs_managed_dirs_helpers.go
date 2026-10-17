package usecase

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"mindfs/server/internal/apperr"
	"mindfs/server/internal/fs"
)


func preferredNewProjectMetaLocation(registry Registry) string {
	if prefs := registry.GetPreferences(); prefs != nil {
		return prefs.NewProjectMetaLocation()
	}
	return fs.MetaLocationProject
}

func upsertRootWithMetaLocation(registry Registry, path, location string) (fs.RootInfo, error) {
	if upserter, ok := registry.(rootMetaLocationUpserter); ok {
		return upserter.UpsertRootWithMetaLocation(path, location)
	}
	return registry.UpsertRoot(path)
}

func (s *Service) AddManagedDir(_ context.Context, in AddManagedDirInput) (AddManagedDirOutput, error) {
	if err := s.ensureRegistry(); err != nil {
		return AddManagedDirOutput{}, err
	}
	if in.Path == "" {
		return AddManagedDirOutput{}, errors.New("path required")
	}
	abs, err := resolveManagedDirPath(s.Registry, in.Path, in.Create)
	if err != nil {
		return AddManagedDirOutput{}, err
	}
	if err := ensureManagedDirNameAvailable(s.Registry, abs); err != nil {
		return AddManagedDirOutput{}, err
	}
	name := filepath.Base(abs)
	location, err := fs.MetaLocationForNewRoot(abs, preferredNewProjectMetaLocation(s.Registry))
	if err != nil {
		return AddManagedDirOutput{}, err
	}
	pending := fs.NewRootInfo(name, name, abs)
	pending.MetaLocation = location
	// 非主账户：pending 也要带账户 meta 根，否则下面这行会在项目里建出 .mindfs
	// CUSTOM(G-P): 非主账户的 meta 根必须显式带上，否则会在项目里建出 .mindfs
	pending.MetaRoot = accountMetaRoot(s.Registry)
	if _, err := pending.EnsureMetaDir(); err != nil {
		return AddManagedDirOutput{}, err
	}
	dir, err := upsertRootWithMetaLocation(s.Registry, abs, location)
	if err != nil {
		return AddManagedDirOutput{}, err
	}
	return AddManagedDirOutput{Dir: dir}, nil
}

func ensureManagedDirNameAvailable(registry Registry, path string) error {
	name := filepath.Base(filepath.Clean(path))
	for _, existing := range registry.ListRoots() {
		if existing.ID != name {
			continue
		}
		if sameManagedDirPath(existing.RootPath, path) {
			return nil
		}
		return fmt.Errorf("%w:\n\t%q is already managed at %s;\n\trename the directory before adding %s", fs.ErrRootNameConflict, name, existing.RootPath, path)
	}
	return nil
}

func sameManagedDirPath(a, b string) bool {
	left := cleanManagedDirPath(a)
	right := cleanManagedDirPath(b)
	if runtime.GOOS == "windows" {
		left = strings.ToLower(left)
		right = strings.ToLower(right)
	}
	return left == right
}

func cleanManagedDirPath(path string) string {
	cleaned := filepath.Clean(strings.TrimSpace(path))
	if abs, err := filepath.Abs(cleaned); err == nil {
		return abs
	}
	return cleaned
}

func resolveManagedDirPath(registry Registry, raw string, create bool) (string, error) {
	if create {
		return createManagedDir(registry, raw)
	}
	if !filepath.IsAbs(raw) {
		return "", errors.New("path must be absolute")
	}
	abs := filepath.Clean(raw)
	info, err := os.Stat(abs)
	if err != nil {
		return "", apperr.Wrap("stat", abs, err)
	}
	if !info.IsDir() {
		return "", errors.New("path must be a directory")
	}
	return abs, nil
}

func createManagedDir(registry Registry, name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", errors.New("path required")
	}
	if filepath.IsAbs(trimmed) {
		abs := filepath.Clean(trimmed)
		for _, existing := range registry.ListRoots() {
			existingPath := filepath.Clean(existing.RootPath)
			if existingPath == abs {
				return "", errors.New("root already exists")
			}
		}
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return "", apperr.Wrap("mkdir", abs, err)
		}
		return abs, nil
	}
	if trimmed == "." || trimmed == ".." {
		return "", errors.New("invalid root name")
	}
	if strings.Contains(trimmed, "/") || strings.Contains(trimmed, "\\") {
		return "", errors.New("root name must not contain path separators")
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	baseDir := filepath.Join(homeDir, "mindfs")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return "", apperr.Wrap("mkdir", baseDir, err)
	}

	rootPath := filepath.Join(baseDir, trimmed)
	cleanRootPath := filepath.Clean(rootPath)
	for _, existing := range registry.ListRoots() {
		existingPath := filepath.Clean(existing.RootPath)
		if existing.ID == trimmed && existingPath != cleanRootPath {
			return "", errors.New("root name already exists")
		}
		if existingPath == cleanRootPath {
			return "", errors.New("root already exists")
		}
	}

	if info, err := os.Stat(rootPath); err == nil {
		if info.IsDir() {
			return cleanRootPath, nil
		}
		return "", errors.New("root path already exists and is not a directory")
	} else if !os.IsNotExist(err) {
		return "", apperr.Wrap("stat", rootPath, err)
	}

	if err := os.Mkdir(rootPath, 0o755); err != nil {
		return "", apperr.Wrap("mkdir", rootPath, err)
	}
	return cleanRootPath, nil
}

type RemoveManagedDirInput struct {
	Path string
}

type RemoveManagedDirOutput struct {
	Dir fs.RootInfo
}

func (s *Service) RemoveManagedDir(_ context.Context, in RemoveManagedDirInput) (RemoveManagedDirOutput, error) {
	if err := s.ensureRegistry(); err != nil {
		return RemoveManagedDirOutput{}, err
	}
	if in.Path == "" {
		return RemoveManagedDirOutput{}, errors.New("path required")
	}
	if !filepath.IsAbs(in.Path) {
		return RemoveManagedDirOutput{}, errors.New("path must be absolute")
	}
	dir, err := s.Registry.RemoveRoot(filepath.Clean(in.Path))
	if err != nil {
		return RemoveManagedDirOutput{}, err
	}
	return RemoveManagedDirOutput{Dir: dir}, nil
}

type RenameManagedDirInput struct {
	RootID string
	Name   string
}

type RenameManagedDirOutput struct {
	OldRootID string
	Dir       fs.RootInfo
}

type rootResourceReleaser interface {
	ReleaseRootResources(rootID string)
}

func (s *Service) RenameManagedDir(_ context.Context, in RenameManagedDirInput) (RenameManagedDirOutput, error) {
	if err := s.ensureRegistry(); err != nil {
		return RenameManagedDirOutput{}, err
	}
	rootID := strings.TrimSpace(in.RootID)
	nextName := strings.TrimSpace(in.Name)
	if rootID == "" {
		return RenameManagedDirOutput{}, errors.New("root id required")
	}
	if nextName == "" {
		return RenameManagedDirOutput{}, errors.New("root name required")
	}
	if nextName == "." || nextName == ".." {
		return RenameManagedDirOutput{}, errors.New("invalid root name")
	}
	if strings.Contains(nextName, "/") || strings.Contains(nextName, "\\") {
		return RenameManagedDirOutput{}, errors.New("root name must not contain path separators")
	}

	root, err := s.Registry.GetRoot(rootID)
	if err != nil {
		return RenameManagedDirOutput{}, err
	}
	oldPath := filepath.Clean(root.RootPath)
	if info, err := os.Stat(oldPath); err != nil || !info.IsDir() {
		if err != nil {
			return RenameManagedDirOutput{}, apperr.Wrap("stat", oldPath, err)
		}
		return RenameManagedDirOutput{}, errors.New("root path must be a directory")
	}
	newPath := filepath.Join(filepath.Dir(oldPath), nextName)
	if sameManagedDirPath(oldPath, newPath) {
		return RenameManagedDirOutput{OldRootID: rootID, Dir: root}, nil
	}
	for _, existing := range s.Registry.ListRoots() {
		if existing.ID == rootID {
			continue
		}
		if existing.ID == nextName {
			return RenameManagedDirOutput{}, fmt.Errorf("%w: %q is already managed at %s", fs.ErrRootNameConflict, nextName, existing.RootPath)
		}
		if sameManagedDirPath(existing.RootPath, newPath) {
			return RenameManagedDirOutput{}, errors.New("root path already exists")
		}
	}
	if _, err := os.Stat(newPath); err == nil {
		return RenameManagedDirOutput{}, errors.New("root path already exists")
	} else if !os.IsNotExist(err) {
		return RenameManagedDirOutput{}, apperr.Wrap("stat", newPath, err)
	}

	if releaser, ok := s.Registry.(rootResourceReleaser); ok {
		releaser.ReleaseRootResources(rootID)
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		return RenameManagedDirOutput{}, apperr.Wrap("rename", oldPath, err)
	}
	dir, err := s.Registry.RenameRoot(rootID, nextName, newPath)
	if err != nil {
		if rollbackErr := os.Rename(newPath, oldPath); rollbackErr != nil {
			return RenameManagedDirOutput{}, fmt.Errorf("registry rename failed: %w; rollback failed: %v", err, apperr.Wrap("rename", newPath, rollbackErr))
		}
		return RenameManagedDirOutput{}, err
	}
	return RenameManagedDirOutput{OldRootID: rootID, Dir: dir}, nil
}

type UpdateRootDisplayNameInput struct {
	RootID      string
	DisplayName string
}

type UpdateRootDisplayNameOutput struct {
	Dir fs.RootInfo
}

func (s *Service) UpdateRootDisplayName(_ context.Context, in UpdateRootDisplayNameInput) (UpdateRootDisplayNameOutput, error) {
	if err := s.ensureRegistry(); err != nil {
		return UpdateRootDisplayNameOutput{}, err
	}
	rootID := strings.TrimSpace(in.RootID)
	displayName := strings.TrimSpace(in.DisplayName)
	if rootID == "" {
		return UpdateRootDisplayNameOutput{}, errors.New("root id required")
	}
	if _, err := s.Registry.GetRoot(rootID); err != nil {
		return UpdateRootDisplayNameOutput{}, err
	}
	// Empty displayName means clear alias and fall back to Name
	dir, err := s.Registry.UpdateDisplayName(rootID, displayName)
	if err != nil {
		return UpdateRootDisplayNameOutput{}, err
	}
	return UpdateRootDisplayNameOutput{Dir: dir}, nil
}
