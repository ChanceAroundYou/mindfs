package usecase

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"mindfs/server/internal/fs"
	"mindfs/server/internal/gitview"
)


func resolveGitRelatedFileDiffTarget(ctx context.Context, root fs.RootInfo, in GitRelatedFileDiffInput) (string, string, string, string, error) {
	path := strings.TrimSpace(in.Path)
	repoPath := strings.TrimSpace(in.RepoPath)
	rootPath := root.RootPath
	head := strings.TrimSpace(in.Head)
	var normalizedRootRelPath string
	if repoPath != "" {
		if !filepath.IsAbs(repoPath) {
			return "", "", "", "", errors.New("repo path must be absolute")
		}
		rootPath = filepath.Clean(repoPath)
		if value, err := root.NormalizePath(path); err == nil {
			normalizedRootRelPath = value
		}
	} else {
		value, err := root.NormalizePath(path)
		if err != nil {
			return "", "", "", "", err
		}
		path = value
		normalizedRootRelPath = value
	}
	if normalizedRootRelPath != "" {
		if fallbackRepoPath, fallbackRelPath, fallbackHead, ok := resolveTaskWorktreeRelatedFile(ctx, root.RootPath, normalizedRootRelPath); ok {
			rootPath = fallbackRepoPath
			repoPath = fallbackRepoPath
			path = fallbackRelPath
			if head == "" {
				head = fallbackHead
			}
		}
	}
	return rootPath, repoPath, path, head, nil
}

func resolveTaskWorktreeRelatedFile(ctx context.Context, rootPath, relPath string) (string, string, string, bool) {
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	if relPath == "" {
		return "", "", "", false
	}
	if !strings.HasPrefix(relPath, ".worktree/task-") {
		return "", "", "", false
	}
	absPath := filepath.Clean(filepath.Join(rootPath, filepath.FromSlash(relPath)))
	if resolvedAbsPath, err := filepath.EvalSymlinks(absPath); err == nil {
		absPath = filepath.Clean(resolvedAbsPath)
	}
	repo, err := gitview.ResolveRepositoryForPath(ctx, absPath)
	if err != nil || strings.TrimSpace(repo.Path) == "" {
		return "", "", "", false
	}
	repoPath := filepath.Clean(repo.Path)
	rootClean := filepath.Clean(rootPath)
	if resolvedRootPath, err := filepath.EvalSymlinks(rootClean); err == nil {
		rootClean = filepath.Clean(resolvedRootPath)
	}
	if sameManagedDirPath(repoPath, rootClean) {
		return "", "", "", false
	}
	fileRel, err := filepath.Rel(repoPath, absPath)
	if err != nil || fileRel == "." || filepath.IsAbs(fileRel) || strings.HasPrefix(fileRel, ".."+string(filepath.Separator)) || fileRel == ".." {
		return "", "", "", false
	}
	return repoPath, filepath.ToSlash(fileRel), strings.TrimSpace(repo.Head), true
}
