package gitview

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

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

// meaningfulDirtyPaths 列主 checkout 的未提交改动，**排除 .worktree/ 容器目录**。
//
// 排除是必须的：`.worktree/` 是 worktree 的容器目录，主 checkout 必然会看到它
// （未 gitignore 时是 `??`）。不排除的话每次都误报「主 checkout 不干净」，
// 收尾永远走不下去。
func meaningfulDirtyPaths(ctx context.Context, dir string) ([]string, error) {
	return dirtyPaths(ctx, dir, false)
}

// trackedDirtyPaths 只列**已跟踪文件**的未提交改动。
//
// 合并的脏检查用这个，不用 meaningfulDirtyPaths：未跟踪文件不会被合并覆盖掉，
// 除非合并恰好要写同名路径 —— 而那一步 git 自己会拦（见 MergeBranch 里的说明）。
// 把它算进来的唯一效果是「主 checkout 里有任何别人留下的散落文件，收尾就永远
// 走不动」，而它一个字节都不会丢。
func trackedDirtyPaths(ctx context.Context, dir string) ([]string, error) {
	return dirtyPaths(ctx, dir, true)
}

// dirtyPaths 是上面两个的公共实现。trackedOnly 时跳过 `??`（未跟踪）那些行。
func dirtyPaths(ctx context.Context, dir string, trackedOnly bool) ([]string, error) {
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
		// porcelain v1 每行固定是「XY<空格>路径」，XY 恰好两个字符。
		//
		// **不能**用 strings.Index(line, " ") 找分隔符：状态带前导空格时
		// （` M foo` = 已跟踪文件改了没暂存）第一个空格在 0 号位，切出来的
		// 「路径」会是 `M foo` —— 带状态前缀的假路径，一路传到 UI 的文件清单里。
		// 实测（2026-10-08）：`dirty_files` 报的是「M note.txt」而不是「note.txt」。
		if len(line) < 4 {
			continue
		}
		status := line[:2]
		path := strings.TrimSpace(line[3:])
		// `?? .worktree/` 和 `?? .worktree` 都算容器目录（末尾斜杠取决于 git 版本）。
		if path == ".worktree" || path == ".worktree/" {
			continue
		}
		// `??` = 未跟踪。收窄模式下跳过：它挡不住任何东西。
		if trackedOnly && status == "??" {
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
