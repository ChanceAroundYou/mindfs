package usecase

import (
	"context"
	"strings"

	"mindfs/server/internal/fs"
)

type ListManagedDirsOutput struct {
	Dirs []fs.RootInfo
}

func (s *Service) ListManagedDirs(_ context.Context) (ListManagedDirsOutput, error) {
	if err := s.ensureRegistry(); err != nil {
		return ListManagedDirsOutput{}, err
	}
	return ListManagedDirsOutput{Dirs: s.Registry.ListRoots()}, nil
}

type AddManagedDirInput struct {
	Path   string
	Create bool
}

type AddManagedDirOutput struct {
	Dir fs.RootInfo
}

type rootMetaLocationUpserter interface {
	UpsertRootWithMetaLocation(path, metaLocation string) (fs.RootInfo, error)
}
func (s *Service) ensureFileWatcher(rootID, dir string) {
	if strings.TrimSpace(rootID) == "" {
		return
	}
	manager, err := s.Registry.GetSessionManager(rootID)
	if err != nil || manager == nil {
		return
	}
	watcher, err := s.Registry.GetFileWatcher(rootID, manager)
	if err != nil || watcher == nil {
		return
	}
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	_ = watcher.WatchDir(dir)
}
