package gitview

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

func ReadDiff(ctx context.Context, rootPath, relPath string) (DiffResult, error) {
	repo, err := loadRepoContext(ctx, rootPath)
	if err != nil {
		return DiffResult{}, err
	}
	items, err := repo.statusItems(ctx)
	if err != nil {
		return DiffResult{}, err
	}
	var matched *StatusItem
	for i := range items {
		if items[i].Path == relPath {
			matched = &items[i]
			break
		}
	}
	if matched == nil {
		return DiffResult{}, errDiffNotFound
	}
	content, err := repo.diffContent(ctx, *matched)
	if err != nil {
		return DiffResult{}, err
	}
	return DiffResult{
		Path:      matched.Path,
		Status:    matched.Status,
		Additions: matched.Additions,
		Deletions: matched.Deletions,
		Content:   content,
	}, nil
}

func ListHistory(ctx context.Context, rootPath string, limit int, beforeCommit, afterCommit string) (HistoryResult, error) {
	repo, err := loadRepoContext(ctx, rootPath)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return HistoryResult{}, err
		}
		if isNotRepoError(err) {
			return HistoryResult{Available: false, Items: []HistoryItem{}}, nil
		}
		return HistoryResult{}, err
	}
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	beforeCommit = strings.TrimSpace(beforeCommit)
	afterCommit = strings.TrimSpace(afterCommit)
	if beforeCommit != "" && !repo.commitExists(ctx, beforeCommit) {
		return HistoryResult{Available: true, Items: []HistoryItem{}, CommitMissing: true, RemoteHead: repo.remoteHistoryHead(ctx)}, nil
	}
	if afterCommit != "" && !repo.commitExists(ctx, afterCommit) {
		return HistoryResult{Available: true, Items: []HistoryItem{}, CommitMissing: true, RemoteHead: repo.remoteHistoryHead(ctx)}, nil
	}
	remoteHead := repo.remoteHistoryHead(ctx)

	args := []string{"log", "--format=%H%x00%s%x00%cI%x00", fmt.Sprintf("--max-count=%d", limit+1)}
	if afterCommit != "" {
		args = append(args, afterCommit+"..HEAD")
	} else if beforeCommit != "" {
		parents, err := repo.commitParents(ctx, beforeCommit)
		if err != nil {
			return HistoryResult{}, err
		}
		if len(parents) == 0 {
			return HistoryResult{Available: true, Items: []HistoryItem{}, HasMore: false, RemoteHead: remoteHead}, nil
		}
		args = append(args, parents...)
	}
	if repo.prefix != "" {
		args = append(args, "--", repo.prefix)
	}
	output, err := runGit(ctx, repo.repoRoot, args...)
	if err != nil {
		return HistoryResult{}, err
	}
	items := parseHistoryItems(output)
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	repo.markRemoteHistoryItems(ctx, items)
	return HistoryResult{Available: true, Items: items, HasMore: hasMore, RemoteHead: remoteHead}, nil
}

func ListCommitFiles(ctx context.Context, rootPath, commit string) (CommitFilesResult, error) {
	repo, err := loadRepoContext(ctx, rootPath)
	if err != nil {
		return CommitFilesResult{}, err
	}
	commit = strings.TrimSpace(commit)
	if commit == "" {
		return CommitFilesResult{}, errors.New("commit required")
	}
	if !repo.commitExists(ctx, commit) {
		return CommitFilesResult{}, errors.New("commit not found")
	}
	statusItems, err := repo.commitNameStatus(ctx, commit)
	if err != nil {
		return CommitFilesResult{}, err
	}
	stats, err := repo.commitNumstat(ctx, commit)
	if err != nil {
		return CommitFilesResult{}, err
	}
	items := make([]StatusItem, 0, len(statusItems))
	for _, item := range statusItems {
		path := repo.fromRepoPath(item.Path)
		if path == "" {
			continue
		}
		oldPath := repo.fromRepoPath(item.OldPath)
		stat := stats[item.Path]
		items = append(items, StatusItem{
			Path:      strings.TrimSuffix(path, "/"),
			OldPath:   oldPath,
			Status:    item.Status,
			Additions: stat[0],
			Deletions: stat[1],
		})
	}
	return CommitFilesResult{Commit: commit, Items: items}, nil
}

func ReadCommitDiff(ctx context.Context, rootPath, commit, relPath string) (DiffResult, error) {
	repo, err := loadRepoContext(ctx, rootPath)
	if err != nil {
		return DiffResult{}, err
	}
	commit = strings.TrimSpace(commit)
	if commit == "" {
		return DiffResult{}, errors.New("commit required")
	}
	if !repo.commitExists(ctx, commit) {
		return DiffResult{}, errors.New("commit not found")
	}
	path := strings.TrimSpace(relPath)
	if path == "" {
		return DiffResult{}, errors.New("path required")
	}
	repoPath := repo.toRepoPath(path)
	files, err := repo.commitNameStatus(ctx, commit)
	if err != nil {
		return DiffResult{}, err
	}
	stats, err := repo.commitNumstat(ctx, commit)
	if err != nil {
		return DiffResult{}, err
	}
	var matched *porcelainItem
	for i := range files {
		if files[i].Path == repoPath {
			matched = &files[i]
			break
		}
	}
	if matched == nil {
		return DiffResult{}, errors.New("git commit diff not found for path")
	}
	contentBytes, err := runGitBytes(ctx, repo.repoRoot, "show", "--format=", "--no-ext-diff", "--find-renames", commit, "--", repoPath)
	if err != nil {
		return DiffResult{}, err
	}
	content := decodeGitDiffOutput(contentBytes, filepath.Ext(matched.Path))
	stat := stats[matched.Path]
	return DiffResult{
		Path:      path,
		OldPath:   repo.fromRepoPath(matched.OldPath),
		Status:    matched.Status,
		Additions: stat[0],
		Deletions: stat[1],
		Content:   content,
	}, nil
}

// emptyRelatedFileDiff 表示「这个文件自记录的基线以来没有任何变更可显示」：
// base 之后没有提交碰过它，工作区也没有改动（或被 git 忽略/已删除）。
// 这是空结果而非错误，回 200 空 diff 让前端渲染空面板，不要污染日志。
// Status 故意留空，前端据此判断「无可显示的增删统计」。
func emptyRelatedFileDiff(path string) RelatedFileDiffResult {
	return RelatedFileDiffResult{
		DiffResult: DiffResult{Path: path},
		Source:     "none",
	}
}

func ReadRelatedFileDiff(ctx context.Context, rootPath, baseHead, relPath string) (RelatedFileDiffResult, error) {
	repo, err := loadRepoContext(ctx, rootPath)
	if err != nil {
		return RelatedFileDiffResult{}, err
	}
	baseHead = strings.TrimSpace(baseHead)
	if baseHead == "" {
		diff, err := ReadDiff(ctx, rootPath, relPath)
		if errors.Is(err, errDiffNotFound) {
			return emptyRelatedFileDiff(relPath), nil
		}
		if err != nil {
			return RelatedFileDiffResult{}, err
		}
		return RelatedFileDiffResult{DiffResult: diff, Source: "worktree"}, nil
	}
	if !repo.commitExists(ctx, baseHead) {
		return RelatedFileDiffResult{}, errors.New("记录的提交不存在或不在当前分支历史中")
	}
	if !repo.commitInCurrentHistory(ctx, baseHead) {
		return RelatedFileDiffResult{}, errors.New("记录的提交不存在或不在当前分支历史中")
	}
	path := strings.TrimSpace(relPath)
	if path == "" {
		return RelatedFileDiffResult{}, errors.New("path required")
	}
	nextHead, err := repo.nextCommitTouching(ctx, baseHead, repo.toRepoPath(path))
	if err != nil {
		return RelatedFileDiffResult{}, err
	}
	if strings.TrimSpace(nextHead) == "" {
		diff, err := ReadDiff(ctx, rootPath, path)
		if errors.Is(err, errDiffNotFound) {
			return emptyRelatedFileDiff(path), nil
		}
		if err != nil {
			return RelatedFileDiffResult{}, err
		}
		return RelatedFileDiffResult{
			DiffResult: diff,
			BaseHead:   baseHead,
			Source:     "worktree",
		}, nil
	}
	diff, err := repo.diffBetweenCommits(ctx, baseHead, nextHead, path)
	if err != nil {
		return RelatedFileDiffResult{}, err
	}
	return RelatedFileDiffResult{
		DiffResult: diff,
		BaseHead:   baseHead,
		TargetHead: nextHead,
		Source:     "commit_range",
	}, nil
}

func (r repoContext) toRepoPath(rootRelativePath string) string {
	if r.prefix == "" {
		return filepath.ToSlash(rootRelativePath)
	}
	if rootRelativePath == "" || rootRelativePath == "." {
		return r.prefix
	}
	return filepath.ToSlash(pathJoinSlash(r.prefix, rootRelativePath))
}

func (r repoContext) fromRepoPath(repoRelativePath string) string {
	value := filepath.ToSlash(strings.TrimSpace(repoRelativePath))
	if value == "" {
		return ""
	}
	if r.prefix == "" {
		return value
	}
	if value == r.prefix {
		return "."
	}
	prefix := r.prefix + "/"
	if !strings.HasPrefix(value, prefix) {
		return ""
	}
	return strings.TrimPrefix(value, prefix)
}

func parseHistoryItems(output string) []HistoryItem {
	parts := strings.Split(output, "\x00")
	items := make([]HistoryItem, 0, len(parts)/3)
	for i := 0; i+2 < len(parts); i += 3 {
		hash := strings.TrimSpace(parts[i])
		if hash == "" {
			continue
		}
		items = append(items, HistoryItem{
			Hash:       hash,
			Message:    strings.TrimSpace(parts[i+1]),
			CommitTime: strings.TrimSpace(parts[i+2]),
		})
	}
	return items
}
