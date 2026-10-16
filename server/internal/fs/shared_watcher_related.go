package fs

import (
	"context"
	"path/filepath"
	"strings"
)

func (sw *SharedFileWatcher) RegisterSession(sessionKey string) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	sw.sessions[sessionKey] = &sessionInfo{key: sessionKey}
}

func (sw *SharedFileWatcher) UnregisterSession(sessionKey string) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	delete(sw.sessions, sessionKey)
	for path, key := range sw.pendingWrites {
		if key == sessionKey {
			delete(sw.pendingWrites, path)
		}
	}
}

func (sw *SharedFileWatcher) MarkSessionActive(_ string) {
}

func (sw *SharedFileWatcher) RecordPendingWrite(sessionKey, filePath string) {
	watchDir := ""
	sw.mu.Lock()
	if rel, err := sw.root.NormalizePath(filePath); err == nil {
		filePath = rel
	}
	filePath = filepath.ToSlash(filePath)
	sw.pendingWrites[filePath] = sessionKey
	watchDir = parentDir(filePath)
	sw.mu.Unlock()
	if watchDir != "" {
		sw.watchNearestExistingDir(watchDir)
	}
}

func (sw *SharedFileWatcher) RecordSessionFile(sessionKey, filePath string) {
	if sw.sessionStore == nil || sessionKey == "" || filePath == "" {
		return
	}
	sw.recordSessionWorktree(context.Background(), sessionKey, filePath)
	record, ok := sw.resolveRelatedFileRecord(context.Background(), filePath)
	if !ok {
		return
	}
	if err := sw.sessionStore.RecordOutputFileInRepo(
		context.Background(),
		sessionKey,
		record.rootID,
		record.repoKind,
		record.repoPath,
		record.repoName,
		record.path,
		record.head,
	); err != nil {
		return
	}
	if record.repoKind != "git" || sameCleanPath(record.repoPath, sw.root.RootPath) {
		sw.root.UpdateFileMeta(record.path, sessionKey, "agent")
	}
	sw.emitRelatedFile(RelatedFileEvent{
		RootID:     sw.root.ID,
		SessionKey: sessionKey,
		Path:       record.path,
	})
}

func (sw *SharedFileWatcher) resolveRelatedFileRecord(ctx context.Context, filePath string) (relatedFileRecord, bool) {
	absFilePath := absoluteRelatedFilePath(sw.root.RootPath, filePath)
	if sw.worktreeResolver != nil && filePath != "" {
		if match, ok := sw.worktreeResolver(ctx, sw.root, filePath); ok {
			relPath, ok := relativePathInside(match.Path, absFilePath)
			if ok && validRelatedPath(relPath) {
				repoPath := filepath.Clean(match.Path)
				return relatedFileRecord{
					rootID:   sw.root.ID,
					repoKind: "git",
					repoPath: repoPath,
					repoName: filepath.Base(repoPath),
					path:     relPath,
					head:     strings.TrimSpace(match.Head),
				}, true
			}
		}
	}

	relPath, err := sw.root.NormalizePath(filePath)
	if err != nil {
		return relatedFileRecord{}, false
	}
	relPath = filepath.ToSlash(relPath)
	if !validRelatedPath(relPath) {
		return relatedFileRecord{}, false
	}
	rootPath := filepath.Clean(sw.root.RootPath)
	return relatedFileRecord{
		rootID:   sw.root.ID,
		repoKind: "plain",
		repoPath: rootPath,
		repoName: sw.root.Name,
		path:     relPath,
	}, true
}

func absoluteRelatedFilePath(rootPath, filePath string) string {
	cleanPath := strings.TrimSpace(filePath)
	if cleanPath == "" {
		return ""
	}
	if filepath.IsAbs(cleanPath) {
		return filepath.Clean(cleanPath)
	}
	return filepath.Clean(filepath.Join(rootPath, cleanPath))
}

func validRelatedPath(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "." || path == ".." || path == "" {
		return false
	}
	return !(len(path) >= len(".mindfs") && path[:len(".mindfs")] == ".mindfs")
}

func relativePathInside(rootPath, filePath string) (string, bool) {
	rootPath = filepath.Clean(strings.TrimSpace(rootPath))
	if rootPath == "" {
		return "", false
	}
	cleanPath := strings.TrimSpace(filePath)
	if cleanPath == "" {
		return "", false
	}
	if !filepath.IsAbs(cleanPath) {
		cleanPath = filepath.Join(rootPath, cleanPath)
	}
	cleanPath = filepath.Clean(cleanPath)
	rel, err := filepath.Rel(rootPath, cleanPath)
	if err != nil || rel == "." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func sameCleanPath(a, b string) bool {
	return filepath.Clean(strings.TrimSpace(a)) == filepath.Clean(strings.TrimSpace(b))
}

func (sw *SharedFileWatcher) recordSessionWorktree(ctx context.Context, sessionKey, filePath string) {
	if sw.sessionStore == nil || sw.worktreeResolver == nil || sessionKey == "" || filePath == "" {
		return
	}
	match, ok := sw.worktreeResolver(ctx, sw.root, filePath)
	if !ok {
		return
	}
	if added, err := sw.sessionStore.RecordRelatedWorktree(ctx, sessionKey, sw.root.ID, match.Path, match.Branch, match.Head); err == nil && added {
		sw.emitRelatedFile(RelatedFileEvent{
			RootID:          sw.root.ID,
			SessionKey:      sessionKey,
			RelatedWorktree: &match,
		})
	}
}
