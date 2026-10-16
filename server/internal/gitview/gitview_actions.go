package gitview

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

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
