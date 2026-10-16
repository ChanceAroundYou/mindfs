package gitview

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// OrphanWorktreeDir 是一个「git 不再认、但磁盘上还在」的 worktree 目录。
type OrphanWorktreeDir struct {
	Path string `json:"path"`
	// NonEmpty 表示目录里还有内容。非空的孤儿目录默认**不删**——
	// 可能是人放进去的东西，删了不可恢复。要删得显式 force。
	NonEmpty bool `json:"non_empty"`
	// Files 是残留内容（最多列几个，供用户判断能不能删）。仅 NonEmpty 时有意义。
	Files []string `json:"files,omitempty"`
}

// PruneOrphanDirs 扫出 <mainDir>/.worktree/ 下 git 已经不认的目录。
//
// 为什么需要：worktree 目录里常留着 git 完全不跟踪的东西——`.mindfs/`（会话库）、
// `.omc/`（编排状态）、`.claude/`（本地配置）。`git worktree remove` 删的是
// **git 记录的那个 worktree**，会因为未跟踪文件拒绝执行；即使 --force 成功，也可能
// 留下这些目录。而 `git worktree prune` 只清 git 的**元数据**（.git/worktrees/ 里的
// 登记），不碰磁盘。结果就是 `.worktree/task-N/` 变成没有源码、没有 git 记录、
// 只有一堆状态文件的孤儿——worktree 和分支都没了，目录还在。
//
// 判据是「目录里没有 .git 文件」：真 worktree 一定有一个指向 .git/worktrees/<name>
// 的文件，所以这个判据绝不可能误报一个还活着的 worktree。
//
// 只扫不删——删除是破坏性动作，得由调用方明确要求。
func PruneOrphanDirs(ctx context.Context, mainDir string) ([]OrphanWorktreeDir, error) {
	if err := validateMergeDirs(mainDir); err != nil {
		return nil, err
	}
	listed, err := ListWorktrees(ctx, mainDir)
	if err != nil {
		if isNotRepoError(err) {
			return nil, nil
		}
		return nil, err
	}
	known := map[string]bool{}
	for _, item := range listed.Items {
		if abs, err := filepath.Abs(item.Path); err == nil {
			known[filepath.Clean(abs)] = true
		}
		known[filepath.Clean(item.Path)] = true
	}

	root := filepath.Join(filepath.Clean(mainDir), ".worktree")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	orphans := []OrphanWorktreeDir{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		// 有 .git 文件 = git 认得的活 worktree（也可能 git 记录里有但磁盘上没 .git，
		// 那由 known 兜住）——两种都不归这里管。
		if _, statErr := os.Lstat(filepath.Join(dir, ".git")); statErr == nil {
			continue
		}
		if known[filepath.Clean(dir)] {
			continue
		}
		orphan := OrphanWorktreeDir{Path: dir}
		if inner, readErr := os.ReadDir(dir); readErr == nil && len(inner) > 0 {
			orphan.NonEmpty = true
			for i, f := range inner {
				if i >= 8 {
					break
				}
				orphan.Files = append(orphan.Files, f.Name())
			}
		}
		orphans = append(orphans, orphan)
	}
	return orphans, nil
}

// 二次确认的唯一执行点：判据（没有 .git 文件 + 不在 git 登记里）已由 PruneOrphanDirs
// 做完，这里在真删之前**再判一次**——扫描到删除之间完全可能有人建了 worktree 进来。
// 「不确认就删」是这类操作最坏的一种失败。
//
// 故意不做「删之前重读一次目录」：非空判定只需要一个方向 —— 扫出来非空就必须
// force 才能删。扫出来为空、删除时却非空的方向是**安全**的（os.Remove 对非空目录
// 报错，什么都不会丢）。再读一次只会凭空多一个 TOCTOU 窗口，还让这段读起来像
// 「检查过了才敢删」，而下一次「顺手简化」很容易把那道 force 闸门一起删掉。
func RemoveOrphanDir(orphan OrphanWorktreeDir, force bool) error {
	if _, err := os.Lstat(filepath.Join(orphan.Path, ".git")); err == nil {
		return fmt.Errorf("%s 现在是一个 git 登记的 worktree，不删", orphan.Path)
	}
	if orphan.NonEmpty {
		if !force {
			return fmt.Errorf("%s 非空，确认是残留再删（force=true）", orphan.Path)
		}
		// RemoveAll 而不是 Remove：非空目录 Remove 会直接报错，那这个 force 就白给了。
		return os.RemoveAll(orphan.Path)
	}
	// 空目录用 Remove：万一它已经不空了就该失败，而不是顺手把别人的东西删掉。
	return os.Remove(orphan.Path)
}

// MainCheckoutPath 报一个 linked worktree 所在**主 checkout** 的路径。
//
// 用 `--path-format=absolute --git-common-dir`：worktree 的 gitdir 指向
// .git/worktrees/<name>，而 common-dir 指向真正那个 .git 目录，它的父目录就是主
// checkout。这是 git 自己给的答案，不用去猜目录名或遍历父目录。
//
// MainDir（主 checkout 自己）传进来原样返回：它的 common-dir 就是自己的 .git，
// 往上走一层会退到仓库的父目录去 —— 那个目录不是 checkout。
func MainCheckoutPath(ctx context.Context, path string) (string, error) {
	out, err := runGit(ctx, path, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	commonDir := strings.TrimSpace(out)
	if commonDir == "" {
		return "", errors.New("empty git common dir")
	}
	// 主 checkout：common-dir 就是 <checkout>/.git，往上走一层是错的。
	if gitInfo, statErr := os.Stat(filepath.Join(filepath.Clean(path), ".git")); statErr == nil && gitInfo.IsDir() {
		return filepath.Clean(path), nil
	}
	// linked worktree：common-dir 是 <main>/.git，父目录才是主 checkout。
	return filepath.Dir(filepath.Clean(commonDir)), nil
}
