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

// samePath 判两个路径是否指向同一个目录，**先解析符号链接**再比。
//
// git 报的路径是解析后的（`git worktree list --porcelain` 与
// `git rev-parse --path-format=absolute --git-common-dir` 都会解析符号链接），
// 而 MainCheckoutPath 对主 checkout 返回的是**调用方给的那个路径**（可能是
// 符号链接本身）。两边直接字符串比较会在「项目根是符号链接」时误判成
// 「两个不同的目录」—— 实测 /home/xiaokubao/family → /mnt/fnos/family，
// 收尾时 validateTarget 报「main 正被另一个 worktree 占用」，而那个
// 「另一个 worktree」就是主 checkout 自己。
//
// EvalSymlinks 失败（路径不存在等）时返回 false —— 那种情况下两个路径至少
// 还在同一套比较口径里，不会比解析成功时更糟。
func samePath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	resolvedA, errA := filepath.EvalSymlinks(a)
	if errA != nil {
		return false
	}
	resolvedB, errB := filepath.EvalSymlinks(b)
	if errB != nil {
		return false
	}
	return filepath.Clean(resolvedA) == filepath.Clean(resolvedB)
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
		item.Current = samePath(cleanPath, currentRoot)
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
