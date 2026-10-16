package fs

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

func (sw *SharedFileWatcher) SetOnFileChange(handler func(FileChangeEvent)) {
	sw.mu.Lock()
	sw.onFileChange = handler
	sw.mu.Unlock()
}

func (sw *SharedFileWatcher) SetOnFileChangeBatch(handler func(FileChangeBatchEvent)) {
	sw.mu.Lock()
	sw.onFileChangeBatch = handler
	sw.mu.Unlock()
}

func (sw *SharedFileWatcher) SetOnRelatedFile(handler func(RelatedFileEvent)) {
	sw.mu.Lock()
	sw.onRelatedFile = handler
	sw.mu.Unlock()
}

func (sw *SharedFileWatcher) run() {
	for {
		select {
		case event, ok := <-sw.watcher.Events:
			if !ok {
				return
			}
			if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Rename|fsnotify.Remove) == 0 {
				continue
			}
			if sw.shouldIgnore(event.Name) {
				continue
			}
			rel, err := sw.root.NormalizePath(event.Name)
			if err != nil {
				continue
			}
			if event.Op&fsnotify.Remove != 0 {
				sw.emitFileChange(FileChangeEvent{
					RootID: sw.root.ID,
					Path:   rel,
					Op:     event.Op.String(),
					IsDir:  false,
				})
				continue
			}
			info, err := os.Stat(event.Name)
			if err != nil {
				// File might disappear quickly during rename/remove races.
				sw.emitFileChange(FileChangeEvent{
					RootID: sw.root.ID,
					Path:   rel,
					Op:     event.Op.String(),
					IsDir:  false,
				})
				continue
			}
			if info.IsDir() {
				sw.emitFileChange(FileChangeEvent{
					RootID: sw.root.ID,
					Path:   rel,
					Op:     event.Op.String(),
					IsDir:  true,
				})
				_ = sw.WatchDir(rel)
				continue
			}
			sw.emitFileChange(FileChangeEvent{
				RootID: sw.root.ID,
				Path:   rel,
				Op:     event.Op.String(),
				IsDir:  false,
			})
			sessionKey := sw.resolveSessionKey(rel)
			if sessionKey == "" {
				continue
			}
			sw.RecordSessionFile(sessionKey, rel)
		case _, ok := <-sw.watcher.Errors:
			if !ok {
				return
			}
		case <-sw.done:
			return
		}
	}
}

func (sw *SharedFileWatcher) resolveSessionKey(relPath string) string {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	if sessionKey, ok := sw.pendingWrites[relPath]; ok {
		delete(sw.pendingWrites, relPath)
		return sessionKey
	}
	return ""
}

func (sw *SharedFileWatcher) emitFileChange(change FileChangeEvent) {
	sw.queueFileChangeBatch(change)
}

func (sw *SharedFileWatcher) queueFileChangeBatch(change FileChangeEvent) {
	change.Path = filepath.ToSlash(change.Path)
	if change.Path == "" {
		return
	}
	sw.mu.Lock()
	select {
	case <-sw.done:
		sw.mu.Unlock()
		return
	default:
	}
	if sw.pendingChanges == nil {
		sw.pendingChanges = make(map[string]FileChangeEvent)
	}
	if sw.pendingChangeDirs == nil {
		sw.pendingChangeDirs = make(map[string]struct{})
	}
	sw.pendingChanges[change.Path] = change
	sw.pendingChangeDirs[parentDir(change.Path)] = struct{}{}
	if change.IsDir || strings.Contains(change.Op, "REMOVE") || strings.Contains(change.Op, "RENAME") {
		sw.pendingChangeDirs[change.Path] = struct{}{}
	}
	sw.fileChangeVersion++
	version := sw.fileChangeVersion
	if sw.fileChangeTimer != nil {
		sw.fileChangeTimer.Stop()
	}
	sw.fileChangeTimer = time.AfterFunc(fileChangeBatchDelay, func() {
		sw.flushFileChangeBatch(version)
	})
	sw.mu.Unlock()
}

func (sw *SharedFileWatcher) flushFileChangeBatch(version uint64) {
	sw.mu.Lock()
	if version != 0 && version != sw.fileChangeVersion {
		sw.mu.Unlock()
		return
	}
	if sw.fileChangeTimer != nil {
		sw.fileChangeTimer.Stop()
		sw.fileChangeTimer = nil
	}
	if len(sw.pendingChanges) == 0 {
		sw.mu.Unlock()
		return
	}
	changesByPath := sw.pendingChanges
	dirsByPath := sw.pendingChangeDirs
	sw.pendingChanges = make(map[string]FileChangeEvent)
	sw.pendingChangeDirs = make(map[string]struct{})
	batchHandler := sw.onFileChangeBatch
	singleHandler := sw.onFileChange
	sw.mu.Unlock()

	paths := make([]string, 0, len(changesByPath))
	for path := range changesByPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	dirs := make([]string, 0, len(dirsByPath))
	for dir := range dirsByPath {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)

	events := make([]FileChangeEvent, 0, len(paths))
	for _, path := range paths {
		events = append(events, changesByPath[path])
	}

	if batchHandler != nil {
		batchHandler(FileChangeBatchEvent{
			RootID: sw.root.ID,
			Paths:  paths,
			Dirs:   dirs,
			Events: events,
		})
		return
	}
	if singleHandler != nil {
		for _, change := range events {
			singleHandler(change)
		}
	}
}

func (sw *SharedFileWatcher) emitRelatedFile(change RelatedFileEvent) {
	sw.mu.RLock()
	handler := sw.onRelatedFile
	sw.mu.RUnlock()
	if handler != nil {
		handler(change)
	}
}
