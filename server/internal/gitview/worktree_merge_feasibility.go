package gitview

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// mergeInProgress 报仓库是否停在一个没收尾的合并里。
//
// 直接看 .git/MERGE_HEAD 文件在不在，**不用 `git rev-parse --verify MERGE_HEAD`**：
// 那条命令验的是「MERGE_HEAD 指向的对象存不存在」，不是「文件在不在」——里面写个
// 悬空 sha 它照样成功退出，于是「上次合并没收尾」这个守卫会静默失效。真撞上时得到
// 的是一句误导性的 "local changes would be overwritten"。
//
// gitdir 路径要用 `git rev-parse --git-dir` 求，不能拼 <dir>/.git：主 checkout 之外
// （分离头指针、GIT_DIR 指向别处）那个路径不成立。validateMergeDirs 已经保证
// MainDir 是主 checkout，所以这里拿到的就是真的 .git 目录。
func mergeInProgress(ctx context.Context, dir string) bool {
	gitDir, err := runGit(ctx, dir, "rev-parse", "--git-dir")
	if err != nil {
		return false
	}
	gitDir = strings.TrimSpace(gitDir)
	if gitDir == "" {
		return false
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(dir, gitDir)
	}
	_, statErr := os.Stat(filepath.Join(filepath.Clean(gitDir), "MERGE_HEAD"))
	return statErr == nil
}

// branchExists 报分支名能否解析成一个 commit。ref 用 ^{commit} 收窄，避免
// 同名的 tag/远程跟踪分支被当成源分支。
func branchExists(ctx context.Context, dir, branch string) bool {
	_, err := runGit(ctx, dir, "rev-parse", "-q", "--verify", branch+"^{commit}")
	return err == nil
}

// BranchMergedInto 报 source 分支是否已经全部合进 target 分支（source 是 target 的祖先）。
//
// 给「收尾要不要再让 agent 跑一遍」用：已合就是活已经在主干里，只剩机械清场。
// 分支不存在返回 error（区分「没合」和「压根没这个分支」—— 后者不该被当成要合）。
func BranchMergedInto(ctx context.Context, dir, source, target string) (bool, error) {
	return isAncestorOf(ctx, dir, source, target)
}

// MergeFeasibility 是一次「只读合并判定」的结论。
//
// 为什么需要单独一个类型：收尾要决定「能不能不等 agent、直接机械清场」，而那个决定
// 只需要两个答案 —— 能不能合、不能合是哪些文件。把它做成类型而不是散落的返回值，
// 是为了让「判定」和「真合并」在代码里长得不一样：真合并会动仓库，判定一个字节都
// 不该动（见 CanMergeCleanly 的注释）。
type MergeFeasibility struct {
	// Clean=true 表示机械合并能成功。
	Clean bool
	// Reason 是 Clean=false 的原因（短句，给用户看）。
	Reason string
	// Files 是相关文件清单（会冲突的文件），给 UI 列表渲染。
	Files []string
}

// CanMergeCleanly 判定「把 branch 合进 target 会不会冲突」，**不碰仓库状态**。
//
// 用 `git merge-tree --write-tree <target> <branch>` 干跑：成功时只打印目标树 OID、
// 退出 0；有冲突时额外打印每个冲突文件的 stage 行 + `CONFLICT (content): Merge
// conflict in <path>`，退出 1。全程不写 index、不产生 MERGE_HEAD、不改工作区 ——
// 所以可以在「用户还没决定要不要收尾」的时候安全调用。
//
// 为什么不用 `git merge --no-commit --no-ff` 再 abort：那会真的动 index 和工作区，
// 中途失败（比如磁盘满）会留下一个半截合并；而 abort 又会把用户解到一半的冲突
// 取舍连同 MERGE_MSG 一起丢掉。干跑没有这些副作用。
//
// 分支已在 target 里（source 是 target 的祖先）直接返回 Clean=true：那不是冲突，
// 是「没什么要合的」，机械清场仍然可以做。
func CanMergeCleanly(ctx context.Context, mainDir, branch, target string) (MergeFeasibility, error) {
	mainDir = strings.TrimSpace(mainDir)
	if mainDir == "" {
		return MergeFeasibility{}, errors.New("主 checkout 路径为空")
	}
	target = strings.TrimSpace(target)
	if target == "" {
		target = "main"
	}
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return MergeFeasibility{}, errors.New("没有可合并的分支")
	}
	// 已经是祖先 = 没什么要合的，机械清场照做。
	if merged, err := isAncestorOf(ctx, mainDir, branch, target); err == nil && merged {
		return MergeFeasibility{Clean: true}, nil
	}
	out, err := runGitBytes(ctx, mainDir, "merge-tree", "--write-tree", target, branch)
	if err != nil {
		// 退出码 1 有两种含义：真冲突，或者仓库本身有问题（比如分支不存在）。
		// runGitBytes 在出错时把输出塞进 error（formatGitError），所以从错误里抽
		// CONFLICT 行 —— 有就是冲突，没有就把 git 的原话交回去。
		files := parseMergeTreeConflicts(err.Error())
		if len(files) > 0 {
			return MergeFeasibility{Reason: "合并会冲突", Files: files}, nil
		}
		return MergeFeasibility{}, fmt.Errorf("合并判定失败: %w", err)
	}
	_ = out
	return MergeFeasibility{Clean: true}, nil
}

// parseMergeTreeConflicts 从 `git merge-tree --write-tree` 的输出里抽出冲突文件。
//
// 只认 `CONFLICT (content): Merge conflict in <path>` 这一行：stage 行
// （`<mode> <oid> <stage>	<path>`）在**部分冲突**（比如一边删一边改）时也会出现，
// 但那时同样有 CONFLICT 行，所以按 CONFLICT 行取就够，且不会把「只是 stage 不同」
// 误报成冲突。
func parseMergeTreeConflicts(output string) []string {
	const marker = "CONFLICT (content): Merge conflict in "
	files := []string{}
	for _, line := range strings.Split(output, "\n") {
		idx := strings.Index(line, marker)
		if idx < 0 {
			continue
		}
		path := strings.TrimSpace(line[idx+len(marker):])
		if path == "" {
			continue
		}
		files = appendUnique(files, path)
	}
	return files
}

// isAncestorOf 报 source 是否已经是 target 的祖先（改动全在 target 里了）。
func isAncestorOf(ctx context.Context, dir, source, target string) (bool, error) {
	if _, err := runGit(ctx, dir, "merge-base", "--is-ancestor", source, target); err != nil {
		// --is-ancestor 靠退出码表达「不是祖先」，非零有两种含义混在一起：
		// 真·不是祖先（正常，要合）vs 分支名不存在（该报错）。
		// 用 rev-parse 复核一下：能解析出 source 才轮到把非零当「不是祖先」。
		if _, probeErr := runGit(ctx, dir, "rev-parse", "-q", "--verify", source+"^{commit}"); probeErr != nil {
			return false, fmt.Errorf("分支 %s 不存在：%w", source, err)
		}
		return false, nil
	}
	return true, nil
}

// unmergedFiles 列出 UU/AA/AU/UA/DD 这些冲突文件。
func unmergedFiles(ctx context.Context, dir string) []string {
	out, err := runGit(ctx, dir, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil
	}
	files := []string{}
	for _, line := range strings.Split(out, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			files = append(files, trimmed)
		}
	}
	return files
}
