package gitview

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MigrateWorktreeUploads 把 worktree 里上传的附件搬回主 checkout，返回搬过去的
// 相对路径（相对 `.mindfs/upload/`，用 `/` 分隔）。
//
// 为什么必须搬：上传落在 `<root>/.mindfs/upload/<日期>/<文件>`，而 worktree 的
// `.mindfs/` 是**另一份**。收尾要删掉整个 worktree 目录，不搬的话用户在 worktree
// 会话里传的文件会跟着消失 —— 而它们在主 checkout 里根本不存在。
//
// 只搬 `upload/`：会话库 / 任务库 / 文件元数据的权威副本本来就在主 checkout，
// 而且任务 worktree 不作为独立 root 注册（`CreateTaskWorktree` 传 `Register: false`），
// 所以 worktree 的 `.mindfs/` 里通常只有 upload 这一类东西。
//
// 主 checkout 已有同名文件时**跳过不覆盖**：那份是权威的，覆盖等于用旧换新。
func MigrateWorktreeUploads(worktreePath, mainDir string) ([]string, error) {
	worktreePath = strings.TrimSpace(worktreePath)
	mainDir = strings.TrimSpace(mainDir)
	if worktreePath == "" || mainDir == "" {
		return nil, nil
	}
	srcRoot := filepath.Join(worktreePath, ".mindfs", "upload")
	info, err := os.Stat(srcRoot)
	if err != nil {
		// 没有 upload/ 是常态（绝大多数 worktree 都没有），不是错误。
		return nil, nil
	}
	if !info.IsDir() {
		return nil, nil
	}
	dstRoot := filepath.Join(mainDir, ".mindfs", "upload")
	migrated := []string{}
	err = filepath.WalkDir(srcRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(srcRoot, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		target := filepath.Join(dstRoot, rel)
		if _, statErr := os.Lstat(target); statErr == nil {
			// 主 checkout 已经有这份 —— 它是权威的，不覆盖。
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := copyFilePreservingMode(path, target, info.Mode()); err != nil {
			return err
		}
		migrated = append(migrated, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(migrated)
	return migrated, nil
}

// copyFilePreservingMode 复制单个文件并尽量保留权限位。
//
// 不用 io.Copy 直接写：那会按 0666 & ~umask 建文件，上传的文件在 git 里看着就像
// 权限变了。权限位复制失败不致命（文件内容到了就行），所以只记日志不报错。
func copyFilePreservingMode(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if mode != 0 {
		_ = os.Chmod(dst, mode.Perm())
	}
	return nil
}

// RemoveWorktreeDir 删掉 worktree 的**目录本体**，返回是不是真的删了。
//
// `git worktree remove` 删的是 git 登记的那个 worktree，目录里 git 不跟踪的东西
// （`.mindfs/` 会话库、`.omc/`、`.claude/`）会让它拒绝；目录已被手工 prune 掉时
// 更是只剩一个没有 git 记录的孤儿。留着它只会让 `.worktree/` 越积越多 —— 收尾的
// 语义是「这个目录不再需要」。
//
// 唯一必须拒绝的情形：目录里还有 `.git`（仍是活 worktree）。那说明 git 的拆除没
// 成功，这时删目录等于绕过 git 的保护 —— 未提交的活会被无声丢掉。
func RemoveWorktreeDir(worktreePath string) (bool, error) {
	target := strings.TrimSpace(worktreePath)
	if target == "" {
		return false, nil
	}
	if _, err := os.Lstat(target); err != nil {
		// 已经不在了 = 已经拆过（幂等），不是错误。
		return false, nil
	}
	if _, err := os.Lstat(filepath.Join(target, ".git")); err == nil {
		return false, fmt.Errorf("%s 仍是 git 登记的 worktree，先让它拆掉再删目录", target)
	}
	if err := os.RemoveAll(target); err != nil {
		return false, err
	}
	return true, nil
}

// RemoveToolStateOnlyOrphanDirs 删掉 `.worktree/` 下「里面只剩工具状态目录」的
// 空壳，返回删掉的目录路径。
//
// 与 PruneOrphanDirs 的分工：那个**只列**（给 UI 显示「还有什么残留」），这个**删**
// 但只删白名单形状。判据是 `isToolStateOnlyDir` —— 目录里每一项都必须是
// `.mindfs/ .omc/ .claude/` 之一，多一个别的文件就不删。
//
// 为什么不能无脑删所有孤儿：`.worktree/` 下可能有**别的任务正在用**的目录，
// 或者用户手工放进去的东西。`wt-finish.sh` 的 `cleanup_orphan_dirs` 有同一个
// guard（跳过含 `.git` 的、非空且没 `--force` 的）。
func RemoveToolStateOnlyOrphanDirs(ctx context.Context, mainDir string) ([]string, error) {
	orphans, err := PruneOrphanDirs(ctx, mainDir)
	if err != nil {
		return nil, err
	}
	removed := []string{}
	for _, orphan := range orphans {
		if !isToolStateOnlyDir(orphan.Path) {
			continue
		}
		// 删不掉不报错：这只是「顺手清场」，为它把整个收尾判失败不值得。
		if err := RemoveOrphanDir(orphan, true); err != nil {
			continue
		}
		removed = append(removed, orphan.Path)
	}
	return removed, nil
}

// isToolStateOnlyDir 报目录里是不是**只有**工具状态目录（`.mindfs/ .omc/ .claude/`）。
//
// 空目录也算（里面什么都没有，当然「只有」工具状态目录）—— 那种空壳正是
// `git worktree remove` 之后最常见的残留。
func isToolStateOnlyDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	allowed := toolStateDirNameSet()
	for _, entry := range entries {
		if !entry.IsDir() {
			return false
		}
		if !allowed[entry.Name()] {
			return false
		}
	}
	return true
}

func toolStateDirNameSet() map[string]bool {
	set := make(map[string]bool, len(toolStateDirNames))
	for _, name := range toolStateDirNames {
		set[name] = true
	}
	return set
}

func appendUnique(list []string, value string) []string {
	for _, existing := range list {
		if existing == value {
			return list
		}
	}
	return append(list, value)
}
