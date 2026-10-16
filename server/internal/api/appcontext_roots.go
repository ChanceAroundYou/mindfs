package api

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"

	"mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

type RootContext struct {
	Root    fs.RootInfo
	Session *session.Manager
	Watcher *fs.SharedFileWatcher
}

func (s *AppContext) GetRootContext(rootID string) (*RootContext, error) {
	if rootID == "" {
		return nil, errors.New("root id required")
	}
	if s.Dirs == nil {
		return nil, errors.New("registry not configured")
	}
	root, ok := s.Dirs.Get(rootID)
	if !ok {
		return nil, errors.New("root not found")
	}
	if root.ID == "" {
		return nil, errors.New("invalid root")
	}
	exists, err := managedRootExists(root)
	if err != nil {
		return nil, err
	}
	if !exists {
		s.removeMissingRoot(root)
		return nil, errors.New("root not found")
	}

	s.mu.RLock()
	if ctx, ok := s.roots[root.ID]; ok {
		s.mu.RUnlock()
		return ctx, nil
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.roots == nil {
		s.roots = make(map[string]*RootContext)
	}
	if ctx, ok := s.roots[root.ID]; ok {
		return ctx, nil
	}
	ctx := &RootContext{Root: root}
	s.roots[root.ID] = ctx
	return ctx, nil
}

func (s *AppContext) GetRoot(rootID string) (fs.RootInfo, error) {
	rootCtx, err := s.GetRootContext(rootID)
	if err != nil {
		return fs.RootInfo{}, err
	}
	return rootCtx.Root, nil
}

// MetaRoot 是本账户私有的 meta 根（会话库/任务库/文件元数据），主账户为空串。
func (s *AppContext) MetaRoot() string {
	if s == nil || s.Dirs == nil {
		return ""
	}
	return s.Dirs.MetaRoot()
}

// ConfigDir 是本账户私有的配置目录（usecase 侧按账户打开 prompts 等按需 store 用）。
func (s *AppContext) ConfigDir() string {
	if s == nil {
		return ""
	}
	return s.AccountDir
}

func (s *AppContext) UpsertRoot(path string) (fs.RootInfo, error) {
	return s.UpsertRootWithMetaLocation(path, fs.MetaLocationProject)
}

func (s *AppContext) UpsertRootWithMetaLocation(path, metaLocation string) (fs.RootInfo, error) {
	if s.Dirs == nil {
		return fs.RootInfo{}, errors.New("registry not configured")
	}
	dir, err := s.Dirs.UpsertWithMetaLocation(path, metaLocation)
	if err == nil && s.Scheduled != nil {
		if reloadErr := s.Scheduled.ReloadRoot(dir.ID); reloadErr != nil {
			log.Printf("[scheduled-agent] reload.error root=%s err=%v", dir.ID, reloadErr)
		}
	}
	return dir, err
}

func (s *AppContext) RemoveRoot(path string) (fs.RootInfo, error) {
	if s.Dirs == nil {
		return fs.RootInfo{}, errors.New("registry not configured")
	}
	dir, err := s.Dirs.Remove(path)
	if err != nil {
		return fs.RootInfo{}, err
	}
	s.mu.Lock()
	rootCtx := s.roots[dir.ID]
	delete(s.roots, dir.ID)
	s.mu.Unlock()
	if rootCtx != nil && rootCtx.Watcher != nil {
		rootCtx.Watcher.Close()
	}
	if rootCtx != nil && rootCtx.Session != nil {
		_ = rootCtx.Session.Shutdown()
	}
	if s.Scheduled != nil {
		s.Scheduled.RemoveRoot(dir.ID)
	}
	return dir, nil
}

func (s *AppContext) ReleaseRootResources(rootID string) {
	rootID = strings.TrimSpace(rootID)
	if rootID == "" {
		return
	}
	s.mu.Lock()
	rootCtx := s.roots[rootID]
	delete(s.roots, rootID)
	s.mu.Unlock()
	if rootCtx == nil {
		return
	}
	if rootCtx.Watcher != nil {
		rootCtx.Watcher.Close()
	}
	if rootCtx.Session != nil {
		_ = rootCtx.Session.Shutdown()
	}
}

func (s *AppContext) RenameRoot(rootID, name, rootPath string) (fs.RootInfo, error) {
	if s.Dirs == nil {
		return fs.RootInfo{}, errors.New("registry not configured")
	}
	dir, err := s.Dirs.Rename(rootID, name, rootPath)
	if err != nil {
		return fs.RootInfo{}, err
	}
	s.ReleaseRootResources(rootID)
	if s.Scheduled != nil {
		if reloadErr := s.Scheduled.ReloadRoot(dir.ID); reloadErr != nil {
			log.Printf("[scheduled-agent] reload.error root=%s err=%v", dir.ID, reloadErr)
		}
	}
	return dir, nil
}

func (s *AppContext) UpdateDisplayName(rootID, displayName string) (fs.RootInfo, error) {
	if s.Dirs == nil {
		return fs.RootInfo{}, errors.New("registry not configured")
	}
	return s.Dirs.UpdateDisplayName(rootID, displayName)
}

func (s *AppContext) ListRoots() []fs.RootInfo {
	if s.Dirs == nil {
		return []fs.RootInfo{}
	}
	roots := s.Dirs.List()
	active := make([]fs.RootInfo, 0, len(roots))
	for _, root := range roots {
		exists, err := managedRootExists(root)
		if err != nil {
			log.Printf("[registry] stat.root.error root=%s path=%s err=%v", root.ID, root.RootPath, err)
			active = append(active, root)
			continue
		}
		if exists {
			active = append(active, root)
			continue
		}
		s.removeMissingRoot(root)
	}
	return active
}

func managedRootExists(root fs.RootInfo) (bool, error) {
	path := strings.TrimSpace(root.RootPath)
	if path == "" {
		return false, nil
	}
	info, err := os.Stat(filepath.Clean(path))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return info.IsDir(), nil
}

func (s *AppContext) removeMissingRoot(root fs.RootInfo) {
	if s == nil || s.Dirs == nil {
		return
	}
	dir, err := s.RemoveRoot(root.RootPath)
	if err != nil {
		log.Printf("[registry] remove.missing_root.error root=%s path=%s err=%v", root.ID, root.RootPath, err)
		s.ReleaseRootResources(root.ID)
		return
	}
	log.Printf("[registry] remove.missing_root root=%s path=%s", dir.ID, dir.RootPath)
}
