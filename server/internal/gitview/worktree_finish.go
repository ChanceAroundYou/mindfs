package gitview

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// worktree finish：把 linked worktree 里的活收尾掉所需的底层能力。
//
// 拆成独立文件而不是塞进 gitview.go：这个场景的每条命令都有「**必须**在主 checkout
// 里执行」或「**绝不能**在 worktree 里执行」的方向性约束，跟普通 status/diff 那些
// 「在哪儿跑都行」的读操作不是一类。放一起会让「哪些命令对位置敏感」看不出来。

// ErrMergeConflict 表示合并撞上冲突、仓库停在 MERGE_HEAD 未收尾的状态。
//
// 刻意让「冲突」和「合并失败」能被上层分开：前者要给用户人工处理步骤（哪边内容对、
// 怎么合、要不要拆成别的改法，机器判断不了），后者是别的原因。合成一个错误的话
// 上层只能一律回一句「合并失败」，用户还得自己去猜是哪种。
//
// 不是自动 abort 的理由：手工解决到一半的冲突取舍会连同 MERGE_MSG 一起丢掉。
var ErrMergeConflict = errors.New("merge conflict")

// ErrMergeUncommitted 表示仓库停在一个「冲突已解完、但合并还没提交」的中间态。
//
// 单独一个错误而不是塞进 ErrMergeConflict：这时 `git diff --diff-filter=U` 是空的，
// 没有冲突文件可列，报成冲突只会让前端渲染一个空清单加一句「去解冲突」—— 而用户
// 要做的其实是提交（或 abort）。两者的下一步完全不同，混在一起用户会反复点
// 这个按钮却永远成功不了。
var ErrMergeUncommitted = errors.New("merge resolved but not committed")

// ErrSourceBranchGone 表示要合并的分支在仓库里已经不存在了。
//
// 当成「已合入」而不是错误：git 自己不允许删掉未合并的分支（`branch -d` 会拒绝），
// 所以一个消失的分支要么是已经合过、要么是压根没有提交 —— 两种情况都不需要再合。
// 而**重复收尾必然走到这里**：第一次已经 `branch -d` 掉了，第二次如果在这里报错，
// 用户会看到一次成功的收尾第二次点却失败。
var ErrSourceBranchGone = errors.New("源分支已不存在（视为已合入）")

// ErrMergeDirty 表示主 checkout 有未提交改动，git 不敢替用户合并。
var ErrMergeDirty = errors.New("main checkout dirty")

// MainCheckoutDirtyError 表示主 checkout 有未提交改动，合并会覆盖它。
//
// 单独一个类型而不是塞进 error 字符串：那不是「收尾炸了」，是「你自己的活还没收」——
// 下一步是提交或暂存，不是重试。前端要按文件清单渲染，从字符串里解析文件名迟早会散。
type MainCheckoutDirtyError struct {
	Files []string
}

func (e *MainCheckoutDirtyError) Error() string {
	files := strings.Join(e.Files, ", ")
	if files == "" {
		files = "（见 git 输出）"
	}
	return "主 checkout 有未提交改动，先处理：" + files
}

func (e *MainCheckoutDirtyError) Unwrap() error { return ErrMergeDirty }

// MergeConflict 描述一次撞上冲突的合并，ConflictFiles 是 UU/AA 那些文件。
type MergeConflict struct {
	// ConflictFiles 是 unmerged 的路径。冲突可能由三方内容导致，未必每个都进这里，
	// 所以它只当「先看这几个」用，不是全集。
	ConflictFiles []string
	// Output 是 git 自己的原话（CONFLICT (content): Merge conflict in ...）。
	Output string
}

func (e MergeConflict) Error() string {
	files := strings.Join(e.ConflictFiles, ", ")
	if files == "" {
		files = "(见下方 git 输出)"
	}
	return fmt.Sprintf("%v: %s", ErrMergeConflict, files)
}

func (e MergeConflict) Unwrap() error { return ErrMergeConflict }

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

	// 先切到目标分支。合并必须在目标分支上发生，不然就是往源分支里并自己。
	// clean 模式会静默丢掉本地改动，所以必须确认没有别人的东西被毁：
	// 只在确实有改动且不是 .worktree 容器目录时报错。
	dirty, err := meaningfulDirtyPaths(ctx, opts.MainDir)
	if err != nil {
		return MergeResult{}, err
	}
	if len(dirty) > 0 {
		return MergeResult{}, &MainCheckoutDirtyError{Files: dirty}
	}
	if _, err := runGit(ctx, opts.MainDir, "checkout", "-q", target); err != nil {
		return MergeResult{}, fmt.Errorf("切到 %s 失败：%w", target, err)
	}

	// 已经是祖先 = 改动全在目标分支里了，无需再合。
	if isAncestor, err := isAncestorOf(ctx, opts.MainDir, source, target); err != nil {
		return MergeResult{}, err
	} else if isAncestor {
		return MergeResult{Merged: false, Message: source + " 已经在 " + target + " 里了，跳过合并"}, nil
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

// WorktreeBinding 是 git 对某个 worktree 目录的登记信息。
type WorktreeBinding struct {
	// Registered 报这个目录现在还是不是一个 git 登记的 worktree。false 有两种：
	// 从没建成过，或者已经被拆/被 prune 掉了。
	Registered bool
	// Branch 是登记的分支名。ListWorktrees 已经去掉 refs/heads/ 前缀；detached 时为空。
	Branch string
	// Head 是这个 worktree 当前的 commit，detached 时也一样有值。
	Head string
}

// BranchName 拿登记的分支名。ListWorktrees 已经剥过前缀，这里只兜住直调
// rev-parse 的形状，detached 时返回空。
func (b WorktreeBinding) BranchName() string {
	return strings.TrimPrefix(b.Branch, "refs/heads/")
}

// ShortSHA 把 commit 缩到 7 位，供错误文案用。
func ShortSHA(sha string) string {
	sha = strings.TrimSpace(sha)
	if sha == "" {
		return "(空)"
	}
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// InspectWorktree 查一个 worktree 目录当前的登记状态。
//
// 收尾在动 `git worktree remove` 之前**必须**问一次这个：任务记的是「建树时那个
// 目录 + 那个分支」，而目录可能已经被手工 checkout 到别的分支上、或者被别的流程
// 挪作他用。不核对就拆，拆掉的是别人的活。
func InspectWorktree(ctx context.Context, mainDir, path string) (WorktreeBinding, error) {
	target := strings.TrimSpace(path)
	if target == "" {
		return WorktreeBinding{}, nil
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return WorktreeBinding{}, err
	}
	abs = filepath.Clean(abs)
	listed, err := ListWorktrees(ctx, mainDir)
	if err != nil {
		return WorktreeBinding{}, err
	}
	for _, item := range listed.Items {
		if !samePath(item.Path, abs) {
			continue
		}
		return WorktreeBinding{Registered: true, Branch: item.Branch, Head: item.Head}, nil
	}
	return WorktreeBinding{}, nil
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

// meaningfulDirtyPaths 列主 checkout 的未提交改动，**排除 .worktree/ 容器目录**。
//
// 排除是必须的：`.worktree/` 是 worktree 的容器目录，主 checkout 必然会看到它
// （未 gitignore 时是 `??`）。不排除的话每次都误报「主 checkout 不干净」，
// 收尾永远走不下去。
func meaningfulDirtyPaths(ctx context.Context, dir string) ([]string, error) {
	out, err := runGit(ctx, dir, "status", "--porcelain")
	if err != nil {
		return nil, err
	}
	paths := []string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		// porcelain 每行是「XY 路径」。取空格后的路径，X/Y 是暂存/工作区状态。
		idx := strings.Index(line, " ")
		if idx < 0 {
			continue
		}
		path := strings.TrimSpace(line[idx+1:])
		// `?? .worktree/` 和 `?? .worktree` 都算容器目录（末尾斜杠取决于 git 版本）。
		if path == ".worktree" || path == ".worktree/" {
			continue
		}
		paths = append(paths, path)
	}
	return paths, nil
}

// 收尾时挡住 `git worktree remove` 的东西要分成两类，处置方式完全相反。
//
// 2026-10-02 端到端实测撞到的：agent 在收尾段里跑完，worktree 里留下 `.claude/`
// 和 `.omc/` 两个未跟踪目录。它们不是用户的活，是**收尾流程自己的产物** —— 服务端
// 却在下一步要拆掉这个目录，被自己的产物挡住了。而 git 报的错是
// 「contains modified or untracked files」，把这两类和「用户还有没提交的改动」
// 混成一句话，用户看完只知道「有东西没清」，不知道该删哪个、该不该删。
//
// 另一类必须**绝不**自动清理：被跟踪文件的未提交改动、用户的未跟踪文件。
// 那是别人的活，替用户决定丢不丢不行。
//
// 为什么不能靠 gitignore 一劳永逸：mindfs 自己的仓库确实忽略了 .claude/ 和 .omc/
// （实测被忽略的目录不挡 remove），但 mindfs 要管理的是**用户的任意仓库** —— 里面
// 不会有我们的 .gitignore 条目。所以这个判据必须长在收尾这一侧。
type WorktreeRemoveBlockers struct {
	// AgentLeftovers 是工具/编排自己留在 worktree 里的状态目录（.mindfs/、.omc/、
	// .claude/）。收尾可以安全清掉：它们不属于这个任务该交付的东西，而且正是
	// 它们挡住拆除。删不掉不报错（可能有正在跑的会话在写）。
	AgentLeftovers []string `json:"agent_leftovers"`
	// UserChanges 是**用户的活**：被跟踪文件的未提交改动，加上非状态目录的未跟踪
	// 文件。空切片 = 拦路的只有工具产物，收尾可以放心拆。
	UserChanges []string `json:"user_changes"`
}

// CleanupAgentLeftovers 删掉 names 列出的状态目录（若存在），返回删掉了哪些。
//
// 刻意只按白名单里的目录名删，不按「未跟踪」一刀切：未跟踪文件里可能有用户自己
// 写的脚本、笔记、导出结果。名单是代码里的常量，不来自任何外部输入。
func CleanupAgentLeftovers(worktreePath string, names []string) []string {
	target := strings.TrimSpace(worktreePath)
	if target == "" {
		return nil
	}
	var removed []string
	for _, name := range names {
		clean := strings.TrimSpace(name)
		if clean == "" || filepath.IsAbs(clean) || strings.Contains(clean, "..") {
			continue
		}
		if err := os.RemoveAll(filepath.Join(target, clean)); err != nil {
			continue
		}
		removed = append(removed, clean)
	}
	return removed
}

// toolStateDirNames 是「工具自己会在项目里留下」的目录名（相对 worktree 根）。
//
// 与本文件里 PruneOrphanDirs 的注释保持一致：`.mindfs/` 是会话库、`.omc/` 是编排
// 状态、`.claude/` 是 agent 的本地配置目录。三者都不是任务成品。
var toolStateDirNames = []string{".mindfs", ".omc", ".claude"}

// ClassifyWorktreeRemoveBlockers 列出挡住 `git worktree remove` 的东西，分成
// 「工具产物」和「用户的活」两类。
//
// 判据用 `git status --porcelain -uall`：`-uall` 把未跟踪目录展开成一个个文件，
// 否则只看到 `?? .omc/` 一个目录名，没法判断里面是不是有用户的东西。
func ClassifyWorktreeRemoveBlockers(ctx context.Context, worktreePath string) (WorktreeRemoveBlockers, error) {
	var out WorktreeRemoveBlockers
	target := strings.TrimSpace(worktreePath)
	if target == "" {
		return out, nil
	}
	raw, err := runGit(ctx, target, "status", "--porcelain", "-uall")
	if err != nil {
		// 读不到状态就当「有东西挡着」：宁可让收尾停下来问一句，也不要在判据
		// 失效时替用户删东西。
		return out, err
	}
	stateDirs := make(map[string]bool, len(toolStateDirNames))
	for _, name := range toolStateDirNames {
		stateDirs[name] = true
	}
	for _, line := range strings.Split(raw, "\n") {
		// porcelain v1 的行 = 「两个状态字符 + 一个空格 + 路径」。状态字符里**可能有
		// 空格**（工作区已改是 " M"），所以必须按固定偏移取路径，**不能先 TrimSpace**
		// —— 先 trim 掉那个前导空格再 [3:]，会把文件名第一个字符也吃掉
		// （note.txt 变成 ote.txt，报错里给用户看的就是这个）。
		if len(line) < 4 {
			continue
		}
		code := line[:2]
		entry := strings.TrimSpace(line[3:])
		// 重命名写成 `old -> new`，取新路径。
		if idx := strings.Index(entry, " -> "); idx >= 0 {
			entry = strings.TrimSpace(entry[idx+4:])
		}
		entry = strings.Trim(strings.TrimSpace(entry), `"`)
		if entry == "" {
			continue
		}
		top := entry
		if idx := strings.Index(top, "/"); idx > 0 {
			top = top[:idx]
		}
		if code == "??" && stateDirs[top] {
			out.AgentLeftovers = appendUnique(out.AgentLeftovers, top)
			continue
		}
		out.UserChanges = appendUnique(out.UserChanges, entry)
	}
	return out, nil
}

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
