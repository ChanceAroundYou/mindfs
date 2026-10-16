package api

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/fs"
	"mindfs/server/internal/gitview"
	"mindfs/server/internal/kanban"
)

func (s *AppContext) CreateTaskWorktree(ctx context.Context, rootID, name, branchMode, branch string) (kanban.WorktreeInfo, error) {
	root, err := s.GetRoot(rootID)
	if err != nil {
		return kanban.WorktreeInfo{}, err
	}
	branchMode = strings.TrimSpace(branchMode)
	if branchMode == "" {
		branchMode = "new"
	}
	parentPath := filepath.Join(root.RootPath, ".worktree")
	if err := os.MkdirAll(parentPath, 0o755); err != nil {
		return kanban.WorktreeInfo{}, err
	}
	if err := ensureTaskWorktreeExcluded(root.RootPath); err != nil {
		log.Printf("[kanban] worktree.exclude.error root=%s err=%v", root.RootPath, err)
	}
	uc := &usecase.Service{Registry: s}
	out, err := uc.CreateGitWorktree(ctx, usecase.CreateGitWorktreeInput{
		RootID:     rootID,
		ParentPath: parentPath,
		Name:       name,
		BranchMode: branchMode,
		Branch:     branch,
		Register:   false,
	})
	if err != nil {
		return kanban.WorktreeInfo{}, err
	}
	return kanban.WorktreeInfo{RootID: rootID, Path: out.Dir.RootPath}, nil
}

func (s *AppContext) CreateSessionWorktree(ctx context.Context, rootID, branchMode, branch string) (kanban.WorktreeInfo, error) {
	root, err := s.GetRoot(rootID)
	if err != nil {
		return kanban.WorktreeInfo{}, err
	}
	s.sessionWorktreeMu.Lock()
	defer s.sessionWorktreeMu.Unlock()

	names := make([]string, 0, 16)
	worktreeParent := filepath.Join(root.RootPath, ".worktree")
	if entries, readErr := os.ReadDir(worktreeParent); readErr == nil {
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
	} else if !os.IsNotExist(readErr) {
		return kanban.WorktreeInfo{}, readErr
	}
	branches, err := gitview.ListBranches(ctx, root.RootPath)
	if err != nil {
		return kanban.WorktreeInfo{}, err
	}
	for _, item := range branches.Branches {
		names = append(names, item.Name)
	}
	name := nextSessionWorktreeName(time.Now(), names)
	return s.CreateTaskWorktree(ctx, rootID, name, branchMode, branch)
}

func nextSessionWorktreeName(now time.Time, existingNames []string) string {
	prefix := "session-" + now.Format("0102") + "-"
	maxSequence := 0
	for _, name := range existingNames {
		name = strings.TrimSpace(name)
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		suffix := strings.TrimPrefix(name, prefix)
		sequence, err := strconv.Atoi(suffix)
		if err == nil && sequence > maxSequence {
			maxSequence = sequence
		}
	}
	return fmt.Sprintf("%s%02d", prefix, maxSequence+1)
}

func ensureTaskWorktreeExcluded(rootPath string) error {
	gitDir := filepath.Join(rootPath, ".git")
	if info, err := os.Stat(gitDir); err != nil || !info.IsDir() {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	gitInfoDir := filepath.Join(gitDir, "info")
	if err := os.MkdirAll(gitInfoDir, 0o755); err != nil {
		return err
	}
	excludePath := filepath.Join(gitInfoDir, "exclude")
	const entry = "/.worktree/"
	if data, err := os.ReadFile(excludePath); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) == entry {
				return nil
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	file, err := os.OpenFile(excludePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	if stat, err := file.Stat(); err == nil && stat.Size() > 0 {
		if _, err := file.WriteString("\n"); err != nil {
			return err
		}
	}
	_, err = file.WriteString(entry + "\n")
	return err
}

// resolveRelatedWorktree 从「某个文件路径」反推它属于哪个 worktree，供 watcher 在
// 会话动了文件时回填 related_worktree。
//
// **只认 linked worktree，主 checkout 一律不认** —— 这条边界是这个函数存在的全部意义。
//
// 为什么不认主 checkout：related_worktree 这个字段的语义是「这个会话被绑在某个
// worktree 上」，不是「这个会话当前在哪个仓库」。主 checkout 也往里写，就会出现
// 2026-10-02 实测的那个来回：watcher 因为你动了文件把主 checkout 填回去 → repoint
// 清空它 → 下次动文件又填回来。清空成了非持久操作，而 ClearRelatedWorktree 的整个
// 存在意义就是清空它。sessionRuntimeRootPath 也只在「有 task_id 或 source=worktree」
// 时才采信这个字段，主 checkout 记进去对它毫无用处，却让字段状态永远脏着。
//
// 判断用 ListWorktrees 自带的 Current（= 该路径就是这个 root 本身），不是自己比较
// 路径字符串 —— Current 就是为此存在的，此前一直没有消费者。
func resolveRelatedWorktree(ctx context.Context, root fs.RootInfo, filePath string) (fs.RelatedWorktreeMatch, bool) {
	cleanPath := cleanToolFilePath(filePath)
	if cleanPath == "" {
		return fs.RelatedWorktreeMatch{}, false
	}
	if !filepath.IsAbs(cleanPath) {
		cleanPath = filepath.Join(root.RootPath, cleanPath)
	}
	cleanPath = filepath.Clean(cleanPath)
	worktrees, err := gitview.ListWorktrees(ctx, root.RootPath)
	if err != nil {
		return fs.RelatedWorktreeMatch{}, false
	}
	var best fs.RelatedWorktreeMatch
	bestPathLen := -1
	for _, item := range worktrees.Items {
		if !pathInsideDir(cleanPath, item.Path) {
			continue
		}
		// 主 checkout 落选，但**继续找**：worktree 可以嵌套在 root 下，
		// 文件落在 `.worktree/x/...` 里时，外层那条 Current 的记录不该把它吃掉。
		if item.Current {
			continue
		}
		candidate := fs.RelatedWorktreeMatch{
			Path:    item.Path,
			Branch:  item.Branch,
			Head:    item.Head,
			Current: item.Current,
		}
		pathLen := len(filepath.Clean(item.Path))
		if pathLen > bestPathLen {
			best = candidate
			bestPathLen = pathLen
		}
	}
	if bestPathLen >= 0 {
		return best, true
	}
	if repo, err := gitview.ResolveRepositoryForPath(ctx, cleanPath); err == nil && strings.TrimSpace(repo.Path) != "" {
		repoPath := filepath.Clean(repo.Path)
		// 同一条边界，落在这个出口上：ResolveRepositoryForPath 会把普通仓库
		// （包括 root 自己）也返回，那不是 worktree 归属，别记。
		// 复用 pathInsideDir 而不是自己比字符串 —— 它解析过 symlink，
		// root 配了软链时裸比会不相等。
		if sameDirPath(repoPath, root.RootPath) {
			return fs.RelatedWorktreeMatch{}, false
		}
		return fs.RelatedWorktreeMatch{
			Path:    repoPath,
			Head:    strings.TrimSpace(repo.Head),
			Current: false,
		}, true
	}
	return fs.RelatedWorktreeMatch{}, false
}

func cleanToolFilePath(path string) string {
	path = strings.TrimSpace(strings.SplitN(path, "#", 2)[0])
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

// sameDirPath 判两个目录是不是同一个（解析 symlink 之后比）。
func sameDirPath(a, b string) bool {
	return pathInsideDir(a, b) && pathInsideDir(b, a)
}

func pathInsideDir(path, dir string) bool {
	path = filepath.Clean(path)
	dir = filepath.Clean(strings.TrimSpace(dir))
	if resolvedPath, err := filepath.EvalSymlinks(path); err == nil {
		path = filepath.Clean(resolvedPath)
	}
	if resolvedDir, err := filepath.EvalSymlinks(dir); err == nil {
		dir = filepath.Clean(resolvedDir)
	}
	if path == "" || dir == "" {
		return false
	}
	if path == dir {
		return true
	}
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
