package fs

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	fileChangeBatchDelay = time.Second
	maxWatchDirs         = 1024
)

func NewSharedFileWatcher(root RootInfo, sessions SessionFileRecorder, worktreeResolvers ...WorktreeResolver) (*SharedFileWatcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	var resolver WorktreeResolver
	if len(worktreeResolvers) > 0 {
		resolver = worktreeResolvers[0]
	}
	sw := &SharedFileWatcher{
		root:              root,
		watcher:           w,
		sessionStore:      sessions,
		worktreeResolver:  resolver,
		sessions:          make(map[string]*sessionInfo),
		watchedDirs:       make(map[string]struct{}),
		pendingWrites:     make(map[string]string),
		pendingChanges:    make(map[string]FileChangeEvent),
		pendingChangeDirs: make(map[string]struct{}),
		done:              make(chan struct{}),
	}
	if err := sw.WatchDir("."); err != nil {
		if closeErr := w.Close(); closeErr != nil {
		}
		return nil, err
	}
	go sw.run()
	return sw, nil
}

func (sw *SharedFileWatcher) SessionCount() int {
	sw.mu.RLock()
	defer sw.mu.RUnlock()
	return len(sw.sessions)
}

func (sw *SharedFileWatcher) Close() {
	sw.mu.Lock()
	select {
	case <-sw.done:
		sw.mu.Unlock()
		return
	default:
		close(sw.done)
	}
	sw.mu.Unlock()
	sw.flushFileChangeBatch(0)
	sw.watcher.Close()
}

func parentDir(path string) string {
	clean := strings.Trim(filepath.ToSlash(path), "/")
	if clean == "" || clean == "." {
		return "."
	}
	idx := strings.LastIndex(clean, "/")
	if idx <= 0 {
		return "."
	}
	return clean[:idx]
}

func (sw *SharedFileWatcher) WatchDir(dirRel string) error {
	dirAbs, err := sw.root.resolveRelativePath(dirRel)
	if err != nil {
		return err
	}
	if sw.shouldIgnore(dirAbs) {
		return nil
	}
	info, err := os.Stat(dirAbs)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return nil
	}
	clean := filepath.Clean(dirAbs)
	sw.mu.Lock()
	defer sw.mu.Unlock()
	select {
	case <-sw.done:
		return nil
	default:
	}
	if _, ok := sw.watchedDirs[clean]; ok {
		return nil
	}
	if len(sw.watchedDirs) >= maxWatchDirs {
		return nil
	}
	if err := sw.watcher.Add(clean); err != nil {
		return err
	}
	sw.watchedDirs[clean] = struct{}{}
	return nil
}

func (sw *SharedFileWatcher) watchNearestExistingDir(dirRel string) {
	dirRel = strings.TrimSpace(filepath.ToSlash(dirRel))
	if dirRel == "" {
		dirRel = "."
	}
	for {
		if err := sw.WatchDir(dirRel); err == nil {
			return
		}
		next := parentDir(dirRel)
		if next == dirRel {
			return
		}
		dirRel = next
	}
}

func (sw *SharedFileWatcher) shouldIgnore(path string) bool {
	metaDir := sw.root.MetaDir()
	if metaDir != "" && isPathWithin(path, metaDir) {
		return true
	}
	checkPath := path
	if filepath.IsAbs(path) {
		if rel, err := sw.root.relativeFromAbsolute(path); err == nil {
			checkPath = rel
		}
	}
	for _, part := range strings.Split(filepath.ToSlash(filepath.Clean(checkPath)), "/") {
		if part == "" {
			continue
		}
		if _, ignored := ignoredWatchNames[part]; ignored {
			return true
		}
	}
	return false
}

func isPathWithin(path, dir string) bool {
	path = filepath.Clean(path)
	dir = filepath.Clean(dir)
	if path == dir {
		return true
	}
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
