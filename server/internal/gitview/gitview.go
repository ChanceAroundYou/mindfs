package gitview

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"mindfs/server/internal/fs"
)

type StatusItem struct {
	Path      string `json:"path"`
	OldPath   string `json:"old_path,omitempty"`
	Status    string `json:"status"`
	Staged    bool   `json:"staged,omitempty"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	IsDir     bool   `json:"is_dir,omitempty"`
}

type StatusResult struct {
	Available  bool         `json:"available"`
	Branch     string       `json:"branch,omitempty"`
	DirtyCount int          `json:"dirty_count"`
	Items      []StatusItem `json:"items"`
}

type BranchItem struct {
	Name    string `json:"name"`
	Current bool   `json:"current"`
}

type BranchListResult struct {
	Current  string       `json:"current,omitempty"`
	Branches []BranchItem `json:"branches"`
}

type WorktreeItem struct {
	Path    string `json:"path"`
	Branch  string `json:"branch,omitempty"`
	Head    string `json:"head,omitempty"`
	Current bool   `json:"current"`
}

type WorktreeListResult struct {
	Items []WorktreeItem `json:"items"`
}

type RepositoryInfo struct {
	Path string `json:"path"`
	Head string `json:"head,omitempty"`
}

type DiffResult struct {
	Path      string             `json:"path"`
	OldPath   string             `json:"old_path,omitempty"`
	Status    string             `json:"status"`
	Additions int                `json:"additions"`
	Deletions int                `json:"deletions"`
	Content   string             `json:"content"`
	FileMeta  []fs.FileMetaEntry `json:"file_meta,omitempty"`
}

type RelatedFileDiffResult struct {
	DiffResult
	BaseHead   string `json:"base_head,omitempty"`
	TargetHead string `json:"target_head,omitempty"`
	Source     string `json:"source,omitempty"`
}

// RelatedFileStatTarget 是一次批量统计请求里的单个文件。BaseHead 为空表示
// 「没有记录基线」，按工作区改动统计。
type RelatedFileStatTarget struct {
	Path     string `json:"path"`
	BaseHead string `json:"head,omitempty"`
}

// RelatedFileStat 是批量统计里单个文件的结果。
//
// Status 为空表示「自记录的基线以来没有任何变更可显示」—— 与
// emptyRelatedFileDiff 同一语义，前端据此不渲染 +N −M 徽标。
//
// 单个文件在这个接口里**不报错**：基线提交不在历史里、路径不在区间里，都只让它
// 自己留空，不牵连整批。逐文件调 ReadRelatedFileDiff 时那两种情况会返回错误、
// 前端 catch 成 null 不显示徽标 —— 观感一致。
type RelatedFileStat struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

type HistoryItem struct {
	Hash       string `json:"hash"`
	Message    string `json:"message"`
	CommitTime string `json:"commit_time"`
	Remote     bool   `json:"remote"`
}

type HistoryResult struct {
	Available     bool          `json:"available"`
	Items         []HistoryItem `json:"items"`
	HasMore       bool          `json:"has_more"`
	CommitMissing bool          `json:"commit_missing,omitempty"`
	RemoteHead    string        `json:"remote_head,omitempty"`
}

type CommitFilesResult struct {
	Commit string       `json:"commit"`
	Items  []StatusItem `json:"items"`
}

type ActionResult struct {
	Output string       `json:"output"`
	Status StatusResult `json:"status"`
}

type repoContext struct {
	repoRoot string
	rootPath string
	prefix   string
	branch   string
}

func InspectStatus(ctx context.Context, rootPath string) (StatusResult, error) {
	repo, err := loadRepoContext(ctx, rootPath)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return StatusResult{}, err
		}
		if isNotRepoError(err) {
			return StatusResult{Available: false, Items: []StatusItem{}}, nil
		}
		return StatusResult{}, err
	}
	items, err := repo.statusItems(ctx)
	if err != nil {
		return StatusResult{}, err
	}
	return StatusResult{
		Available:  true,
		Branch:     repo.branch,
		DirtyCount: len(items),
		Items:      items,
	}, nil
}

func HasRepo(ctx context.Context, rootPath string) (bool, error) {
	_, err := loadRepoContext(ctx, rootPath)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return false, err
		}
		if isNotRepoError(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func IsWorktree(rootPath string) (bool, error) {
	gitPath := filepath.Join(filepath.Clean(rootPath), ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return info.Mode().IsRegular(), nil
}

func IsInsideWorktree(ctx context.Context, path string) (bool, error) {
	repo, err := loadRepoContext(ctx, path)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return false, err
		}
		if isNotRepoError(err) {
			return false, nil
		}
		return false, err
	}
	return IsWorktree(repo.repoRoot)
}

func ListBranches(ctx context.Context, rootPath string) (BranchListResult, error) {
	repo, err := loadRepoContext(ctx, rootPath)
	if err != nil {
		return BranchListResult{}, err
	}
	output, err := runGit(ctx, repo.repoRoot, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return BranchListResult{}, err
	}
	seen := map[string]struct{}{}
	branches := make([]BranchItem, 0)
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		name := strings.TrimSpace(scanner.Text())
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		branches = append(branches, BranchItem{Name: name, Current: name == repo.branch})
	}
	if err := scanner.Err(); err != nil {
		return BranchListResult{}, err
	}
	return BranchListResult{Current: repo.branch, Branches: branches}, nil
}

func CheckoutBranch(ctx context.Context, rootPath, branch string) error {
	repo, err := loadRepoContext(ctx, rootPath)
	if err != nil {
		return err
	}
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return errors.New("branch required")
	}
	if strings.ContainsAny(branch, "\x00\r\n") {
		return errors.New("invalid branch")
	}
	found := false
	branches, err := ListBranches(ctx, rootPath)
	if err != nil {
		return err
	}
	for _, item := range branches.Branches {
		if item.Name == branch {
			found = true
			break
		}
	}
	if !found {
		return errors.New("branch not found")
	}
	_, err = runGit(ctx, repo.repoRoot, "checkout", branch)
	return err
}

func Pull(ctx context.Context, rootPath string) (ActionResult, error) {
	repo, err := loadRepoContext(ctx, rootPath)
	if err != nil {
		return ActionResult{}, err
	}
	output, err := runGit(ctx, repo.repoRoot, "pull", "--ff-only")
	if err != nil {
		return ActionResult{}, err
	}
	status, err := InspectStatus(ctx, rootPath)
	if err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Output: strings.TrimSpace(output), Status: status}, nil
}

func Push(ctx context.Context, rootPath string) (ActionResult, error) {
	repo, err := loadRepoContext(ctx, rootPath)
	if err != nil {
		return ActionResult{}, err
	}
	output, err := runGit(ctx, repo.repoRoot, "push")
	if err != nil {
		return ActionResult{}, err
	}
	status, err := InspectStatus(ctx, rootPath)
	if err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Output: strings.TrimSpace(output), Status: status}, nil
}

func Commit(ctx context.Context, rootPath, message string) (ActionResult, error) {
	repo, err := loadRepoContext(ctx, rootPath)
	if err != nil {
		return ActionResult{}, err
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return ActionResult{}, errors.New("commit message required")
	}
	if strings.ContainsAny(message, "\x00\r\n") {
		return ActionResult{}, errors.New("invalid commit message")
	}
	output, err := runGit(ctx, repo.repoRoot, "commit", "-am", message)
	if err != nil {
		return ActionResult{}, err
	}
	status, err := InspectStatus(ctx, rootPath)
	if err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Output: strings.TrimSpace(output), Status: status}, nil
}

func StagePath(ctx context.Context, rootPath, relPath string) (ActionResult, error) {
	repo, repoPath, err := resolveActionPath(ctx, rootPath, relPath)
	if err != nil {
		return ActionResult{}, err
	}
	output, err := runGit(ctx, repo.repoRoot, "add", "--", repoPath)
	if err != nil {
		return ActionResult{}, err
	}
	status, err := InspectStatus(ctx, rootPath)
	if err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Output: strings.TrimSpace(output), Status: status}, nil
}

func UnstagePath(ctx context.Context, rootPath, relPath string) (ActionResult, error) {
	repo, repoPath, err := resolveActionPath(ctx, rootPath, relPath)
	if err != nil {
		return ActionResult{}, err
	}
	output, err := runGit(ctx, repo.repoRoot, "restore", "--staged", "--", repoPath)
	if err != nil {
		return ActionResult{}, err
	}
	status, err := InspectStatus(ctx, rootPath)
	if err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Output: strings.TrimSpace(output), Status: status}, nil
}

func DiscardPath(ctx context.Context, rootPath, relPath, statusCode string) (ActionResult, error) {
	repo, repoPath, err := resolveActionPath(ctx, rootPath, relPath)
	if err != nil {
		return ActionResult{}, err
	}
	var output string
	if strings.TrimSpace(statusCode) == "??" {
		output, err = runGit(ctx, repo.repoRoot, "clean", "-fd", "--", repoPath)
	} else {
		output, err = runGit(ctx, repo.repoRoot, "restore", "--staged", "--worktree", "--", repoPath)
	}
	if err != nil {
		return ActionResult{}, err
	}
	status, err := InspectStatus(ctx, rootPath)
	if err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Output: strings.TrimSpace(output), Status: status}, nil
}

func resolveActionPath(ctx context.Context, rootPath, relPath string) (repoContext, string, error) {
	repo, err := loadRepoContext(ctx, rootPath)
	if err != nil {
		return repoContext{}, "", err
	}
	relPath = strings.TrimSpace(filepath.ToSlash(relPath))
	if relPath == "" {
		return repoContext{}, "", errors.New("path required")
	}
	if strings.ContainsAny(relPath, "\x00\r\n") {
		return repoContext{}, "", errors.New("invalid path")
	}
	cleanPath := filepath.ToSlash(filepath.Clean(relPath))
	if cleanPath == "." || strings.HasPrefix(cleanPath, "../") || cleanPath == ".." {
		return repoContext{}, "", errors.New("invalid path")
	}
	return repo, repo.toRepoPath(cleanPath), nil
}

func ListWorktrees(ctx context.Context, rootPath string) (WorktreeListResult, error) {
	if _, err := loadRepoContext(ctx, rootPath); err != nil {
		if isNotRepoError(err) {
			// 与 ListStatus/ListHistory 一致：非 git 根不是错误，只是没有 worktree。
			// 曾经这里直接把 git 的报错抛成 400，前端把
			// "exit status 128: fatal: not a git repository" 原样渲染到面板上。
			return WorktreeListResult{}, nil
		}
		return WorktreeListResult{}, err
	}
	output, err := runGit(ctx, rootPath, "worktree", "list", "--porcelain")
	if err != nil {
		return WorktreeListResult{}, err
	}
	currentRoot, err := filepath.Abs(rootPath)
	if err != nil {
		currentRoot = rootPath
	}
	currentRoot = filepath.Clean(currentRoot)

	var items []WorktreeItem
	var item WorktreeItem
	flush := func() {
		if strings.TrimSpace(item.Path) == "" {
			item = WorktreeItem{}
			return
		}
		cleanPath := filepath.Clean(item.Path)
		item.Path = cleanPath
		item.Current = cleanPath == currentRoot
		items = append(items, item)
		item = WorktreeItem{}
	}
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			flush()
			continue
		}
		key, value, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		switch key {
		case "worktree":
			if strings.TrimSpace(item.Path) != "" {
				flush()
			}
			item.Path = value
		case "HEAD":
			item.Head = value
		case "branch":
			item.Branch = strings.TrimPrefix(value, "refs/heads/")
		}
	}
	if err := scanner.Err(); err != nil {
		return WorktreeListResult{}, err
	}
	flush()
	return WorktreeListResult{Items: items}, nil
}

func ResolveRepositoryForPath(ctx context.Context, filePath string) (RepositoryInfo, error) {
	dir := nearestExistingDir(filePath)
	if dir == "" {
		return RepositoryInfo{}, errors.New("path required")
	}
	repoRootOutput, err := runGit(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return RepositoryInfo{}, err
	}
	repoRoot := strings.TrimSpace(repoRootOutput)
	if repoRoot == "" {
		return RepositoryInfo{}, errors.New("empty git repo root")
	}
	if resolvedRepoRoot, err := filepath.EvalSymlinks(repoRoot); err == nil {
		repoRoot = filepath.Clean(resolvedRepoRoot)
	} else {
		repoRoot = filepath.Clean(repoRoot)
	}
	headOutput, err := runGit(ctx, repoRoot, "rev-parse", "HEAD")
	if err != nil {
		headOutput = ""
	}
	return RepositoryInfo{
		Path: repoRoot,
		Head: strings.TrimSpace(headOutput),
	}, nil
}

func nearestExistingDir(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	path = filepath.Clean(path)
	if info, err := os.Stat(path); err == nil {
		if info.IsDir() {
			return path
		}
		return filepath.Dir(path)
	}
	dir := filepath.Dir(path)
	for dir != "" && dir != "." && dir != string(filepath.Separator) {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
		next := filepath.Dir(dir)
		if next == dir {
			break
		}
		dir = next
	}
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return dir
	}
	return ""
}

func AddWorktree(ctx context.Context, rootPath, targetPath, branchMode, branch string) error {
	if _, err := loadRepoContext(ctx, rootPath); err != nil {
		return err
	}
	if branchMode != "new" && branchMode != "existing" {
		return errors.New("invalid branch mode")
	}
	if _, err := runGit(ctx, rootPath, "check-ref-format", "--branch", branch); err != nil {
		return fmt.Errorf("invalid branch name: %w", err)
	}
	args := []string{"worktree", "add"}
	if branchMode == "new" {
		if strings.TrimSpace(branch) == "" {
			return errors.New("branch required")
		}
		args = append(args, "-b", branch)
	} else if strings.TrimSpace(branch) == "" {
		return errors.New("branch required")
	}
	args = append(args, targetPath)
	if branchMode != "new" && strings.TrimSpace(branch) != "" {
		args = append(args, branch)
	}
	_, err := runGit(ctx, rootPath, args...)
	return err
}

func RemoveWorktree(ctx context.Context, rootPath string) error {
	isWorktree, err := IsWorktree(rootPath)
	if err != nil {
		return err
	}
	if !isWorktree {
		return errors.New("current root is not a git worktree")
	}
	commonDir, err := runGit(ctx, rootPath, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	commonDir = strings.TrimSpace(commonDir)
	if commonDir == "" {
		return errors.New("empty git common dir")
	}
	cleanRoot := filepath.Clean(rootPath)
	cmd := newGitCommand(ctx, "--git-dir", commonDir, "worktree", "remove", cleanRoot)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[git] command.error dir=%q args=%q err=%v output=%q", "", cmd.Args[1:], err, strings.TrimSpace(string(output)))
		return fmt.Errorf("git %s failed: %w: %s", strings.Join(cmd.Args[1:], " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

// errDiffNotFound 表示该路径当前没有任何可显示的改动（未跟踪/被忽略/工作区干净）。
// 对「关联文件」而言这是「无变更」而非错误，调用方据此回空结果。
var errDiffNotFound = errors.New("git diff not found for path")

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

// ReadRelatedFileStats 批量读取关联文件的增删统计，**不取 diff 正文**。
//
// 存在的理由：会话/任务视图要的只是每个关联文件的 +N −M 徽标，而逐文件调
// ReadRelatedFileDiff 时，一个 80 文件的列表会触发 80 次完整的 git diff —— 正文
// 占响应体积的绝大部分（实测 6473B 里 5073B 是 content），而调用方一个字节都不读。
//
// 逐字段语义与 ReadRelatedFileDiff 的 status/additions/deletions 一致，省在三处：
//  1. loadRepoContext 只做一次（原本每文件一次，含 EvalSymlinks + 两次 rev-parse）；
//  2. 同一 (base, nextHead) 区间的 name-status / numstat 只取一次 —— 一个任务的关联
//     文件通常共享同一个基线，原本每个文件各取一遍，这部分是全量重复；
//  3. 基线合法性（cat-file / merge-base）按基线记忆化，不再逐文件重问；
//  4. 不跑 git diff 取正文。
//
// ponytail: nextCommitTouching 仍是逐文件一条 rev-list —— 它便宜（工作树内）且
// 语义必须与 ReadRelatedFileDiff 完全一致，不拿 `git log --name-only` 一把算：
// 合并提交下 log 默认不列文件、而 rev-list 的 pathspec 限制会把合并算作触及该路径，
// 两者会分叉成静默错误的徽标。要再压这个天花板，先验证 80 个路径是否共享同一个
// nextHead（共享时整批可退化成一次 diff --numstat）。
func ReadRelatedFileStats(ctx context.Context, rootPath string, targets []RelatedFileStatTarget) ([]RelatedFileStat, error) {
	stats := make([]RelatedFileStat, len(targets))
	for i := range targets {
		stats[i] = RelatedFileStat{Path: targets[i].Path}
	}
	if len(targets) == 0 {
		return stats, nil
	}
	repo, err := loadRepoContext(ctx, rootPath)
	if err != nil {
		return nil, err
	}

	// 工作区源的统计整批共用一份：statusItems 内部本来就是一次批量 numstat。
	var worktreeItems map[string]StatusItem
	worktreeLoaded := false
	loadWorktree := func() map[string]StatusItem {
		if worktreeLoaded {
			return worktreeItems
		}
		worktreeLoaded = true
		items, err := repo.statusItems(ctx)
		if err != nil {
			return nil
		}
		worktreeItems = make(map[string]StatusItem, len(items))
		for _, item := range items {
			worktreeItems[item.Path] = item
		}
		return worktreeItems
	}

	// 同一基线的合法性只问一次（原本每个文件 cat-file + merge-base 各一次）。
	baseValid := make(map[string]bool, 2)
	validBase := func(base string) bool {
		if value, ok := baseValid[base]; ok {
			return value
		}
		value := repo.commitExists(ctx, base) && repo.commitInCurrentHistory(ctx, base)
		baseValid[base] = value
		return value
	}

	// 1) 逐个解析目标区间。分支顺序与 ReadRelatedFileDiff 一致：无基线 → 工作区；
	//    基线非法 → 留空（逐文件时是错误，前端同样不显示徽标）；区间里没有它 → 工作区。
	type commitRange struct{ base, target string }
	byRange := make(map[commitRange][]int)
	worktreeIndexes := make([]int, 0, len(targets))
	for i := range targets {
		base := strings.TrimSpace(targets[i].BaseHead)
		if base == "" || !validBase(base) {
			if base == "" {
				worktreeIndexes = append(worktreeIndexes, i)
			}
			continue
		}
		next, err := repo.nextCommitTouching(ctx, base, repo.toRepoPath(targets[i].Path))
		if err != nil || strings.TrimSpace(next) == "" {
			worktreeIndexes = append(worktreeIndexes, i)
			continue
		}
		key := commitRange{base: base, target: strings.TrimSpace(next)}
		byRange[key] = append(byRange[key], i)
	}

	// 2) 每个区间各取一次 name-status + numstat，覆盖该区间内的全部文件。
	//    匹配顺序与 diffBetweenCommits 相同：按 Path 或 OldPath 命中**第一个**。
	for key, indexes := range byRange {
		files, err := repo.nameStatusBetween(ctx, key.base, key.target)
		if err != nil {
			continue // 只让这个区间留空，不牵连其它区间
		}
		numstat, err := repo.numstatBetween(ctx, key.base, key.target)
		if err != nil {
			continue
		}
		for _, i := range indexes {
			repoPath := repo.toRepoPath(targets[i].Path)
			for fi := range files {
				if files[fi].Path != repoPath && files[fi].OldPath != repoPath {
					continue
				}
				stats[i].Status = files[fi].Status
				if value, ok := numstat[files[fi].Path]; ok {
					stats[i].Additions = value[0]
					stats[i].Deletions = value[1]
				}
				break
			}
		}
	}

	// 3) 工作区源的批量落值。
	if len(worktreeIndexes) > 0 {
		if items := loadWorktree(); items != nil {
			for _, i := range worktreeIndexes {
				item, ok := items[targets[i].Path]
				if !ok {
					continue
				}
				stats[i].Status = item.Status
				stats[i].Additions = item.Additions
				stats[i].Deletions = item.Deletions
			}
		}
	}
	return stats, nil
}

func loadRepoContext(ctx context.Context, rootPath string) (repoContext, error) {
	rootPath = filepath.Clean(rootPath)
	if resolvedRootPath, err := filepath.EvalSymlinks(rootPath); err == nil {
		rootPath = filepath.Clean(resolvedRootPath)
	}
	repoRootOutput, err := runGit(ctx, rootPath, "rev-parse", "--show-toplevel")
	if err != nil {
		return repoContext{}, err
	}
	repoRoot := strings.TrimSpace(repoRootOutput)
	if repoRoot == "" {
		return repoContext{}, errors.New("empty git repo root")
	}
	if resolvedRepoRoot, err := filepath.EvalSymlinks(repoRoot); err == nil {
		repoRoot = filepath.Clean(resolvedRepoRoot)
	}
	relPrefix, err := filepath.Rel(repoRoot, rootPath)
	if err != nil {
		return repoContext{}, err
	}
	prefix := filepath.ToSlash(relPrefix)
	if prefix == "." {
		prefix = ""
	}
	branchOutput, err := runGit(ctx, repoRoot, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		branchOutput = ""
	}
	return repoContext{
		repoRoot: repoRoot,
		rootPath: rootPath,
		prefix:   prefix,
		branch:   strings.TrimSpace(branchOutput),
	}, nil
}

func (r repoContext) statusItems(ctx context.Context) ([]StatusItem, error) {
	args := []string{"status", "--porcelain=v1", "-z", "--untracked-files=normal"}
	if r.prefix != "" {
		args = append(args, "--", r.prefix)
	}
	output, err := runGit(ctx, r.repoRoot, args...)
	if err != nil {
		return nil, err
	}
	rawItems, err := parsePorcelainV1Z([]byte(output))
	if err != nil {
		return nil, err
	}

	// Batch fetch numstat for all tracked files in one call
	trackedItems := make([]porcelainItem, 0)
	for _, item := range rawItems {
		if item.Status != "??" {
			trackedItems = append(trackedItems, item)
		}
	}
	numstatCache := make(map[string][2]int) // repoPath -> [additions, deletions]
	if len(trackedItems) > 0 {
		numstatCache, err = r.batchNumstat(ctx, trackedItems)
		if err != nil {
			return nil, err
		}
	}

	items := make([]StatusItem, 0, len(rawItems))
	for _, item := range rawItems {
		path := r.fromRepoPath(item.Path)
		if path == "" {
			continue
		}
		path = strings.TrimSuffix(path, "/")
		if shouldIgnoreStatusPath(path) {
			continue
		}
		oldPath := r.fromRepoPath(item.OldPath)

		var additions, deletions int
		isDir := false
		if item.Status == "??" {
			target := filepath.Join(r.rootPath, filepath.FromSlash(path))
			if info, err := os.Stat(target); err == nil && info.IsDir() {
				isDir = true
			} else {
				lines, err := countFileLines(target)
				if err != nil {
					continue
				}
				additions = lines
			}
		} else {
			repoPath := r.toRepoPath(path)
			if stats, ok := numstatCache[repoPath]; ok {
				additions = stats[0]
				deletions = stats[1]
			}
		}

		items = append(items, StatusItem{
			Path:      path,
			OldPath:   oldPath,
			Status:    item.Status,
			Staged:    item.Staged,
			Additions: additions,
			Deletions: deletions,
			IsDir:     isDir,
		})
	}
	return items, nil
}

func shouldIgnoreStatusPath(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(filepath.Clean(path)), "/") {
		if part == ".mindfs" {
			return true
		}
	}
	return false
}

// batchNumstat fetches line stats for all items with one cached and one worktree git call.
func (r repoContext) batchNumstat(ctx context.Context, items []porcelainItem) (map[string][2]int, error) {
	result := make(map[string][2]int)
	if len(items) == 0 {
		return result, nil
	}

	cachedOutput, err := r.batchNumstatHelper(ctx, true, items)
	if err != nil {
		return nil, err
	}
	parseBatchNumstat(cachedOutput, result)

	workOutput, err := r.batchNumstatHelper(ctx, false, items)
	if err != nil {
		return nil, err
	}
	parseBatchNumstat(workOutput, result)

	return result, nil
}

func (r repoContext) batchNumstatHelper(ctx context.Context, cached bool, items []porcelainItem) (string, error) {
	args := []string{"diff", "--numstat"}
	if cached {
		args = append(args, "--cached")
	}
	args = append(args, "--")
	for _, item := range items {
		repoPath := r.toRepoPath(r.fromRepoPath(item.Path))
		args = append(args, repoPath)
	}
	return runGit(ctx, r.repoRoot, args...)
}

func parseBatchNumstat(output string, result map[string][2]int) {
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) < 3 {
			continue
		}
		var add, del int
		if fields[0] != "-" {
			fmt.Sscanf(fields[0], "%d", &add)
		}
		if fields[1] != "-" {
			fmt.Sscanf(fields[1], "%d", &del)
		}
		path := fields[2]
		stats := result[path]
		stats[0] += add
		stats[1] += del
		result[path] = stats
	}
}

func (r repoContext) diffContent(ctx context.Context, item StatusItem) (string, error) {
	repoPath := r.toRepoPath(item.Path)
	if item.Status == "??" {
		target := filepath.Join(r.rootPath, filepath.FromSlash(item.Path))
		return diffAgainstEmptyFile(ctx, r.repoRoot, target)
	}
	parts := make([]string, 0, 2)
	cachedBytes, err := runGitBytes(ctx, r.repoRoot, "diff", "--no-ext-diff", "--cached", "--", repoPath)
	if err != nil {
		return "", err
	}
	cached := decodeGitDiffOutput(cachedBytes, filepath.Ext(item.Path))
	if strings.TrimSpace(cached) != "" {
		parts = append(parts, strings.TrimRight(cached, "\n"))
	}
	workingTreeBytes, err := runGitBytes(ctx, r.repoRoot, "diff", "--no-ext-diff", "--", repoPath)
	if err != nil {
		return "", err
	}
	workingTree := decodeGitDiffOutput(workingTreeBytes, filepath.Ext(item.Path))
	if strings.TrimSpace(workingTree) != "" {
		parts = append(parts, strings.TrimRight(workingTree, "\n"))
	}
	return strings.Join(parts, "\n\n"), nil
}

func (r repoContext) commitExists(ctx context.Context, commit string) bool {
	if strings.TrimSpace(commit) == "" {
		return false
	}
	_, err := runGit(ctx, r.repoRoot, "cat-file", "-e", commit+"^{commit}")
	return err == nil
}

func (r repoContext) commitInCurrentHistory(ctx context.Context, commit string) bool {
	if strings.TrimSpace(commit) == "" {
		return false
	}
	_, err := runGit(ctx, r.repoRoot, "merge-base", "--is-ancestor", commit, "HEAD")
	return err == nil
}

// nextCommitTouching 返回 base..HEAD 中第一个改动了 repoRelPath 的提交。
// 记录的 base 是「文件被改动那一刻的 HEAD」，真正带上这笔改动的提交与它之间
// 常常夹着若干没碰过该文件的提交，所以不能取紧接着的那一个。
func (r repoContext) nextCommitTouching(ctx context.Context, commit, repoRelPath string) (string, error) {
	args := []string{"rev-list", "--reverse", "--ancestry-path", commit + "..HEAD"}
	if strings.TrimSpace(repoRelPath) != "" {
		args = append(args, "--", repoRelPath)
	}
	output, err := runGit(ctx, r.repoRoot, args...)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(output, "\n") {
		value := strings.TrimSpace(line)
		if value != "" {
			return value, nil
		}
	}
	return "", nil
}

func (r repoContext) diffBetweenCommits(ctx context.Context, base, target, relPath string) (DiffResult, error) {
	path := strings.TrimSpace(relPath)
	if path == "" {
		return DiffResult{}, errors.New("path required")
	}
	repoPath := r.toRepoPath(path)
	files, err := r.nameStatusBetween(ctx, base, target)
	if err != nil {
		return DiffResult{}, err
	}
	stats, err := r.numstatBetween(ctx, base, target)
	if err != nil {
		return DiffResult{}, err
	}
	var matched *porcelainItem
	for i := range files {
		if files[i].Path == repoPath || files[i].OldPath == repoPath {
			matched = &files[i]
			break
		}
	}
	if matched == nil {
		return DiffResult{}, errDiffNotFound
	}
	contentBytes, err := runGitBytes(ctx, r.repoRoot, "diff", "--no-ext-diff", "--find-renames", base, target, "--", repoPath)
	if err != nil {
		return DiffResult{}, err
	}
	content := decodeGitDiffOutput(contentBytes, filepath.Ext(matched.Path))
	stat := stats[matched.Path]
	return DiffResult{
		Path:      r.fromRepoPath(matched.Path),
		OldPath:   r.fromRepoPath(matched.OldPath),
		Status:    matched.Status,
		Additions: stat[0],
		Deletions: stat[1],
		Content:   content,
	}, nil
}

func (r repoContext) commitParents(ctx context.Context, commit string) ([]string, error) {
	output, err := runGit(ctx, r.repoRoot, "rev-list", "--parents", "-n", "1", commit)
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(output)
	if len(fields) <= 1 {
		return []string{}, nil
	}
	return fields[1:], nil
}

func (r repoContext) markRemoteHistoryItems(ctx context.Context, items []HistoryItem) {
	for i := range items {
		output, err := runGit(ctx, r.repoRoot, "branch", "-r", "--contains", items[i].Hash, "--format=%(refname:short)")
		if err != nil {
			continue
		}
		items[i].Remote = strings.TrimSpace(output) != ""
	}
}

func (r repoContext) remoteHistoryHead(ctx context.Context) string {
	upstream, err := runGit(ctx, r.repoRoot, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	if err != nil {
		return ""
	}
	upstream = strings.TrimSpace(upstream)
	if upstream == "" {
		return ""
	}
	mergeBase, err := runGit(ctx, r.repoRoot, "merge-base", "HEAD", upstream)
	if err != nil {
		return ""
	}
	mergeBase = strings.TrimSpace(mergeBase)
	if mergeBase == "" {
		return ""
	}
	if r.prefix == "" {
		return mergeBase
	}
	output, err := runGit(ctx, r.repoRoot, "log", "--format=%H", "--max-count=1", mergeBase, "--", r.prefix)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(output)
}

func (r repoContext) commitNameStatus(ctx context.Context, commit string) ([]porcelainItem, error) {
	output, err := runGit(ctx, r.repoRoot, "diff-tree", "--root", "--no-commit-id", "--name-status", "-r", "-z", "-M", commit)
	if err != nil {
		return nil, err
	}
	return parseNameStatusZ(output), nil
}

func (r repoContext) nameStatusBetween(ctx context.Context, base, target string) ([]porcelainItem, error) {
	output, err := runGit(ctx, r.repoRoot, "diff", "--name-status", "-z", "-M", base, target)
	if err != nil {
		return nil, err
	}
	return parseNameStatusZ(output), nil
}

func parseNameStatusZ(output string) []porcelainItem {
	parts := strings.Split(output, "\x00")
	items := make([]porcelainItem, 0)
	for i := 0; i < len(parts); {
		status := strings.TrimSpace(parts[i])
		i++
		if status == "" {
			continue
		}
		code := status
		if len(code) > 1 {
			code = code[:1]
		}
		normalized := "M"
		switch code {
		case "A":
			normalized = "A"
		case "D":
			normalized = "D"
		case "R", "C":
			normalized = "R"
		}
		if normalized == "R" {
			if i+1 >= len(parts) {
				break
			}
			oldPath := strings.TrimSpace(parts[i])
			newPath := strings.TrimSpace(parts[i+1])
			i += 2
			if newPath != "" {
				items = append(items, porcelainItem{Path: newPath, OldPath: oldPath, Status: normalized})
			}
			continue
		}
		if i >= len(parts) {
			break
		}
		path := strings.TrimSpace(parts[i])
		i++
		if path != "" {
			items = append(items, porcelainItem{Path: path, Status: normalized})
		}
	}
	return items
}

func (r repoContext) commitNumstat(ctx context.Context, commit string) (map[string][2]int, error) {
	output, err := runGit(ctx, r.repoRoot, "diff-tree", "--root", "--no-commit-id", "--numstat", "-r", "-M", commit)
	if err != nil {
		return nil, err
	}
	return parseNumstat(output)
}

func (r repoContext) numstatBetween(ctx context.Context, base, target string) (map[string][2]int, error) {
	output, err := runGit(ctx, r.repoRoot, "diff", "--numstat", "-M", base, target)
	if err != nil {
		return nil, err
	}
	return parseNumstat(output)
}

func parseNumstat(output string) (map[string][2]int, error) {
	result := make(map[string][2]int)
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) < 3 {
			continue
		}
		var add, del int
		if fields[0] != "-" {
			fmt.Sscanf(fields[0], "%d", &add)
		}
		if fields[1] != "-" {
			fmt.Sscanf(fields[1], "%d", &del)
		}
		path := fields[len(fields)-1]
		result[path] = [2]int{add, del}
	}
	return result, scanner.Err()
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

type porcelainItem struct {
	Path    string
	OldPath string
	Status  string
	Staged  bool
}

func parsePorcelainV1Z(data []byte) ([]porcelainItem, error) {
	items := make([]porcelainItem, 0)
	index := 0
	for index < len(data) {
		if index+3 > len(data) {
			return nil, errors.New("invalid git status payload")
		}
		x := data[index]
		y := data[index+1]
		index += 3
		next := bytes.IndexByte(data[index:], 0)
		if next < 0 {
			return nil, errors.New("invalid git status path")
		}
		path := string(data[index : index+next])
		index += next + 1
		item := porcelainItem{Path: path, Status: normalizeStatus(x, y), Staged: x != ' ' && x != '?'}
		if x == 'R' || y == 'R' || x == 'C' || y == 'C' {
			oldNext := bytes.IndexByte(data[index:], 0)
			if oldNext < 0 {
				return nil, errors.New("invalid git status rename path")
			}
			item.OldPath = string(data[index : index+oldNext])
			index += oldNext + 1
		}
		items = append(items, item)
	}
	return items, nil
}

func normalizeStatus(x, y byte) string {
	switch {
	case x == '?' && y == '?':
		return "??"
	case x == 'R' || y == 'R' || x == 'C' || y == 'C':
		return "R"
	case x == 'D' || y == 'D':
		return "D"
	case x == 'A' || y == 'A':
		return "A"
	default:
		return "M"
	}
}

func diffAgainstEmptyFile(ctx context.Context, repoRoot, targetPath string) (string, error) {
	tmpFile, err := os.CreateTemp("", "mindfs-git-empty-*")
	if err != nil {
		return "", err
	}
	tmpName := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpName)
	cmd := newGitCommand(ctx, "-C", repoRoot, "diff", "--no-index", "--no-ext-diff", "--", tmpName, targetPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// git diff --no-index returns exit code 1 when differences exist.
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return decodeGitDiffOutput(out, filepath.Ext(targetPath)), nil
		}
		return "", formatGitError(err, out)
	}
	return decodeGitDiffOutput(out, filepath.Ext(targetPath)), nil
}

func countFileLines(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	if len(data) == 0 {
		return 0, nil
	}
	count := bytes.Count(data, []byte{'\n'})
	if data[len(data)-1] != '\n' {
		count += 1
	}
	return count, nil
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := runGitBytes(ctx, dir, args...)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func runGitBytes(ctx context.Context, dir string, args ...string) ([]byte, error) {
	// Add timeout if not already set (30 seconds for Windows compatibility)
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	cmd := newGitCommand(ctx, append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[git] command.error dir=%q args=%q err=%v output=%q", dir, append([]string{"-C", dir}, args...), err, strings.TrimSpace(string(out)))
		return nil, formatGitError(err, out)
	}
	return out, nil
}

func decodeGitDiffOutput(out []byte, ext string) string {
	if utf8.Valid(out) {
		return string(out)
	}
	if decoded, _, ok := fs.TryDecodeText(out, ext); ok {
		return decoded
	}
	return string(out)
}

func formatGitError(err error, output []byte) error {
	text := strings.TrimSpace(string(output))
	if text == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, text)
}

func isNotRepoError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not a git repository")
}

func pathJoinSlash(parts ...string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			continue
		}
		filtered = append(filtered, strings.Trim(part, "/"))
	}
	return strings.Join(filtered, "/")
}
