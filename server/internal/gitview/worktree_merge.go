package gitview

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MergeOptions 是一次合并的参数。
type MergeOptions struct {
	// Source 分支名。空 → 由 MainDir 的当前分支兜底（脚本原来的行为）。
	Source string
	// Target 是接收合并的分支，默认 main。
	Target string
	// Message 是合并提交信息。空 → "Merge branch '<Source>'"。
	Message string
	// FastForward=true 才允许 fast-forward。**默认留合并提交**（零值即安全默认）：
	// worktree 的活通常带 commit，直接 ff 过去的话 main 上看不出「这里并过一份
	// worktree 的活」，回溯时会以为那本来就是 main 上的代码。
	FastForward bool
	// MainDir 是主 checkout 路径。所有 git 命令都带 `git -C MainDir`。
	//
	// 为什么必须显式传、不能靠 cwd：在 worktree 里跑 `git merge <自己分支>` 会返回
	// 误导性的 "Already up to date"——分支其实没合进去，代码看着合了其实没合；
	// 而 `git checkout main` 在 worktree 里直接 fatal（分支被主 checkout 占用）。
	// 显式 -C 是唯一能把「合并确实发生在主 checkout」变成结构性保证的写法。
	MainDir string
	// WorktreePath 是这个分支对应的 worktree 目录。只在源分支消失时用来判断
	// 「那份活还有没有别的引用」—— 见 mergeVanishedSource。
	WorktreePath string
}

// MergeResult 是一次成功合并的结果。
type MergeResult struct {
	// Merged=false 表示 Source 已经是 Target 的祖先，跳过合并（幂等）。
	Merged  bool   `json:"merged"`
	Commit  string `json:"commit,omitempty"`
	Message string `json:"message,omitempty"`
}

// MergeBranch 把 Source 合进 Target（默认 main），**在 MainDir 里执行**。
//
// 幂等：Source 已在 Target 里时返回 Merged=false 而不报错。worktree 收尾脚本原来
// 就是这个行为（`merge-base --is-ancestor` 判了就不合），重复跑一次不会把
// Already up to date 误当成「合过了」。
func MergeBranch(ctx context.Context, opts MergeOptions) (MergeResult, error) {
	if err := validateMergeDirs(opts.MainDir); err != nil {
		return MergeResult{}, err
	}
	target := strings.TrimSpace(opts.Target)
	if target == "" {
		target = "main"
	}
	source := strings.TrimSpace(opts.Source)

	// Source 留空时取 MainDir 自己的当前分支——那才是「要落地的分支」。
	if source == "" {
		out, err := runGit(ctx, opts.MainDir, "rev-parse", "--abbrev-ref", "HEAD")
		if err != nil {
			return MergeResult{}, err
		}
		source = strings.TrimSpace(out)
		if source == "" || source == "HEAD" {
			return MergeResult{}, errors.New("处于 detached HEAD，无法确定要合并的分支")
		}
	}

	// target 来自请求体，不校验就是「往任意分支上合并用户的工作树」。
	// 三个便宜的拒绝：不存在（打了错字 / 分支真被删了）、就是源自己（往自己里并自己，
	// 静默无操作）、正被别的 worktree 占用（合完那个 worktree 会在半路坏掉）。
	if err := validateTarget(ctx, opts.MainDir, source, target); err != nil {
		return MergeResult{}, err
	}

	// Source 分支已经不在了。**不能**直接当「已合入」：分支可以被 `git branch -D`
	// 强删，而 worktree 当时还停在 detached HEAD 上 —— 那份活唯一的引用就是
	// worktree 的 HEAD，main 上没有。这次「收尾」再顺手把 worktree 拆掉，那些提交
	// 就一个引用都不剩了（实测过）。所以这里只放行一种情况：worktree 的 HEAD 已经
	// 在 Target 里，或 worktree 压根不存在（没有任何东西可丢）。
	if !branchExists(ctx, opts.MainDir, source) {
		return mergeVanishedSource(ctx, opts, source, target)
	}

	// 上次合并还挂着就先说清楚，而不是再跑一次 git merge 把状态搅乱。
	// 挂起状态下重跑会得到误导性的 "local changes would be overwritten"。
	//
	// 分两种情况说，因为它们要用户做的事完全不同：还有 unmerged 文件 = 冲突没解完；
	// 一个都没有但 MERGE_HEAD 还在 = 冲突**已经解完、只是没提交**（用户正打着提交
	// 信息呢）。后者报成「冲突」会让前端渲染一个空文件清单 + 「去解冲突」，
	// 而实际上没冲突可解 —— 用户反复点这个按钮永远不会成功。
	if mergeInProgress(ctx, opts.MainDir) {
		if files := unmergedFiles(ctx, opts.MainDir); len(files) > 0 {
			return MergeResult{}, MergeConflict{
				ConflictFiles: files,
				Output:        "MERGE_HEAD 仍存在：上一次的合并还有冲突没解完",
			}
		}
		return MergeResult{}, ErrMergeUncommitted
	}

	// 已经是祖先 = 改动全在目标分支里了，无需再合。
	//
	// **必须排在脏检查之前**（2026-10-08 实测）：没有东西要合的时候，主 checkout
	// 脏不脏完全无关 —— 下面那个 checkout + merge 一步都不会发生。排在后面的话，
	// 「分支早就合完了、只差拆目录」这种最常见的收尾会被主 checkout 里一个不相干的
	// 改动挡死，用户看到「主 checkout 有未提交改动」，而其实这里什么都不用做。
	if isAncestor, err := isAncestorOf(ctx, opts.MainDir, source, target); err != nil {
		return MergeResult{}, err
	} else if isAncestor {
		return MergeResult{Merged: false, Message: source + " 已经在 " + target + " 里了，跳过合并"}, nil
	}

	// 先切到目标分支。合并必须在目标分支上发生，不然就是往源分支里并自己。
	//
	// 判据只算**已跟踪文件**的改动（trackedDirtyPaths）。未跟踪文件挡在这里是过宽
	// 的：合并只在「要写入同名路径」时才碰得到它，而那一刻 git 自己会拦下
	// （"untracked working tree files would be overwritten by merge"），`git checkout`
	// 也一样会拦 —— 下面两条命令都没带 -f，不存在静默覆盖。挡在这里的后果是主
	// checkout 里任何一个别人留下的未跟踪文件都会让收尾永远走不动（实测：两个 e2e
	// 探针脚本把 task-37 的收尾卡死），而它一个字节都不会丢。
	dirty, err := trackedDirtyPaths(ctx, opts.MainDir)
	if err != nil {
		return MergeResult{}, err
	}
	if len(dirty) > 0 {
		return MergeResult{}, &MainCheckoutDirtyError{Files: dirty}
	}
	if _, err := runGit(ctx, opts.MainDir, "checkout", "-q", target); err != nil {
		return MergeResult{}, fmt.Errorf("切到 %s 失败：%w", target, err)
	}

	args := []string{"merge"}
	if !opts.FastForward {
		args = append(args, "--no-ff")
	}
	msg := strings.TrimSpace(opts.Message)
	if msg == "" {
		msg = fmt.Sprintf("Merge branch '%s'", source)
	}
	args = append(args, source, "-m", msg)

	// runGitBytes 失败时把 git 的原话（CONFLICT (content): ...）折进了 err，所以只取
	// err 就够。分开判是必须的：冲突（MERGE_HEAD 已置）和「分支名打错」都会让 git
	// 非零退出，但只有前者要人工处理。
	out, err := runGitBytes(ctx, opts.MainDir, args...)
	if err != nil {
		if mergeInProgress(ctx, opts.MainDir) {
			return MergeResult{}, MergeConflict{
				ConflictFiles: unmergedFiles(ctx, opts.MainDir),
				Output:        err.Error(),
			}
		}
		return MergeResult{}, err
	}
	commit, _ := runGit(ctx, opts.MainDir, "log", "--oneline", "-1")
	return MergeResult{
		Merged:  true,
		Commit:  strings.TrimSpace(commit),
		Message: strings.TrimSpace(string(out)) + "\n" + strings.TrimSpace(commit),
	}, nil
}

// mergeVanishedSource 处理「源分支已经不在」的收尾。
//
// 判定标准只有一条：**这份活还在不在别处存着**。worktree 登记在、且它的 HEAD 不是
// target 的祖先 → 那份活只有 worktree 这一个引用，报错停下，让用户自己决定；
// 否则（worktree 已拆、或 HEAD 已在 target 里）才算「已合入」，重复点收尾才幂等。
func mergeVanishedSource(ctx context.Context, opts MergeOptions, source, target string) (MergeResult, error) {
	// 仓库还停在上一次没收尾的合并里，先说清楚 —— 别在这时候报「分支没了」。
	if mergeInProgress(ctx, opts.MainDir) {
		if files := unmergedFiles(ctx, opts.MainDir); len(files) > 0 {
			return MergeResult{}, MergeConflict{
				ConflictFiles: files,
				Output:        "MERGE_HEAD 仍存在：上一次的合并还有冲突没解完",
			}
		}
		return MergeResult{}, ErrMergeUncommitted
	}
	if opts.WorktreePath == "" {
		return MergeResult{Merged: false, Message: fmt.Sprintf("%v：%s", ErrSourceBranchGone, source)}, nil
	}
	binding, err := InspectWorktree(ctx, opts.MainDir, opts.WorktreePath)
	if err != nil {
		return MergeResult{}, err
	}
	if !binding.Registered {
		// 连 worktree 都没了，没有任何提交只被它指着。
		return MergeResult{Merged: false, Message: fmt.Sprintf("%v：%s", ErrSourceBranchGone, source)}, nil
	}
	if strings.TrimSpace(binding.Head) != "" {
		if ancestor, err := commitIsAncestorOf(ctx, opts.MainDir, binding.Head, target); err == nil && ancestor {
			return MergeResult{Merged: false, Message: fmt.Sprintf("%v：%s", ErrSourceBranchGone, source)}, nil
		}
	}
	// 提交只存在于这个 worktree 里，拆掉就没了。
	return MergeResult{}, fmt.Errorf(
		"%v：%s，且 %s 停在 %s 上、这份提交没有并进 %s —— 那是它唯一的引用，拆掉就没了。先把它取回来（git log / git cherry-pick），再收尾",
		ErrSourceBranchGone, source, opts.WorktreePath, ShortSHA(binding.Head), target)
}

// commitIsAncestorOf 报一个 commit 是否已在 target 的历史里。
func commitIsAncestorOf(ctx context.Context, dir, commit, target string) (bool, error) {
	if _, err := runGit(ctx, dir, "merge-base", "--is-ancestor", commit, target); err != nil {
		if _, probeErr := runGit(ctx, dir, "rev-parse", "-q", "--verify", target+"^{commit}"); probeErr != nil {
			return false, fmt.Errorf("分支 %s 不存在：%w", target, err)
		}
		return false, nil
	}
	return true, nil
}

// validateTarget 校验接收合并的分支。target 来自 HTTP 请求体，所以每一项都要查。
func validateTarget(ctx context.Context, mainDir, source, target string) error {
	if source != "" && source == target {
		return fmt.Errorf("%s 就是源分支，没什么可合并的（不会往自己里并自己）", source)
	}
	if !branchExists(ctx, mainDir, target) {
		return fmt.Errorf("目标分支 %s 不存在", target)
	}
	// 正被别的 worktree 占用时 checkout 会失败，而那时已经改了一半状态。
	listed, err := ListWorktrees(ctx, mainDir)
	if err != nil {
		return err
	}
	mainAbs, _ := filepath.Abs(mainDir)
	for _, item := range listed.Items {
		if strings.TrimPrefix(item.Branch, "refs/heads/") != target {
			continue
		}
		itemAbs, absErr := filepath.Abs(item.Path)
		if absErr != nil {
			continue
		}
		if !samePath(itemAbs, mainAbs) {
			return fmt.Errorf("%s 正被另一个 worktree 占用（%s），先把它移开", target, item.Path)
		}
	}
	return nil
}

// DeleteBranch 用 `git branch -d` 删分支：**已合并才能删**。
//
// 刻意不用 -D：worktree 收尾最坏的情况是删错分支导致提交丢失，而 `branch -d` 在
// 未合并时自己会拒绝并说明原因。宁可收尾停下来让人看一眼，也不要默默丢提交。
func DeleteBranch(ctx context.Context, rootPath, branch string) error {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return errors.New("branch required")
	}
	// 当前分支不能删自己，git 会拒绝；提前说清楚免得用户以为是 bug。
	current, err := runGit(ctx, rootPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return err
	}
	if strings.TrimSpace(current) == branch {
		return fmt.Errorf("不能删当前所在的分支 %s（先 checkout 到别的分支）", branch)
	}
	_, err = runGit(ctx, rootPath, "branch", "-d", branch)
	return err
}

// validateMergeDirs 校验 MainDir 是个可用的主 checkout。
func validateMergeDirs(mainDir string) error {
	dir := strings.TrimSpace(mainDir)
	if dir == "" {
		return errors.New("main checkout path required")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("主 checkout 不可用：%w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("主 checkout 不是目录：%s", dir)
	}
	// 是不是主 checkout：worktree 里 .git 是文件，主 checkout 里是目录。
	// 在 worktree 里当 MainDir 用等于「在 worktree 里合并」——那正是脚本注释里
	// 反复警告的误导性 "Already up to date"，得从入口堵死。
	gitPath := filepath.Join(filepath.Clean(dir), ".git")
	gitInfo, statErr := os.Stat(gitPath)
	if statErr == nil && !gitInfo.IsDir() {
		return fmt.Errorf("%s 是个 linked worktree，不是主 checkout", dir)
	}
	return nil
}
