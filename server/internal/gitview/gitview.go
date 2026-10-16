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
