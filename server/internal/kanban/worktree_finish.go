package kanban

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"mindfs/server/internal/gitview"
)

// worktree 收尾：把一个任务在 linked worktree 里的活合回主 checkout，然后拆掉
// worktree 和分支。这是 wt-finish.sh 那套编排的**服务端那一半**（commit / remove /
// prune / branch -d 都已经在 mindfs 里建好了，缺的是把它们串成一次收尾）。
//
// 刻意不做的两件事（脚本侧才有意义，服务端做不了）：
//   - 自动 commit：worktree 里可能有 agent 的半成品中间态，无脑 commit 等于把垃圾
//     写进历史。走独立的 commit 接口，用户看到内容再点。
//   - repoint：第一步是 pool.Close，会杀掉正在调的 agent 进程，调用方永远看不到
//     成功与否 —— 这条只有脚本能做（见 ~/.claude/skills/worktree-finish）。
//
// 顺序不可换：**先合后拆**。反过来会先把分支删掉，合的就不是那份活。

// FinishWorktreeInput 是一次收尾的请求。
type FinishWorktreeInput struct {
	RootID string
	TaskID string
	// Target 是接收合并的分支，空 → "main"。
	Target string
	// DeleteBranch 拆完 worktree 后是否删掉它那个分支。
	// 只对「已合并」的分支有效（gitview.DeleteBranch 用的是 `branch -d`）。
	DeleteBranch bool
	// PruneOrphans 拆完之后扫一遍 .worktree/，把 git 不认、但磁盘上还在的残留
	// 目录列出来。**只列不删**——非空目录里可能是人放的东西。
	PruneOrphans bool
}

// FinishWorktreeConflict 是「合并撞上冲突」的对外形态，**不吞**。
//
// 用一个独立类型而不是把文件列表塞进 error 字符串：前端要按「冲突 / 别的原因」
// 两种形态分别渲染（前者给可点的文件清单和人工处理说明，后者只给一句报错），
// 从字符串里解析文件名迟早会散。
type FinishWorktreeConflict struct {
	// ConflictFiles 是 UU/AA 那些文件。
	ConflictFiles []string `json:"conflict_files"`
	// Output 是 git 自己的原话。
	Output string `json:"output,omitempty"`
}

func (e *FinishWorktreeConflict) Error() string {
	files := strings.Join(e.ConflictFiles, ", ")
	if files == "" {
		files = "（见 git 输出）"
	}
	return "合并撞上冲突，需人工处理：" + files
}

func (e *FinishWorktreeConflict) Unwrap() error { return gitview.ErrMergeConflict }

// FinishWorktreeResult 是一次收尾的结果。
type FinishWorktreeResult struct {
	Task Task `json:"task"`
	// Merged=false 表示源分支已经是 target 的祖先（重复收尾，或改动本来就在 main 上）。
	Merged bool   `json:"merged"`
	Commit string `json:"commit,omitempty"`
	// WorktreeRemoved=false 表示目录本来就不在了（已删或从没建成），不是失败。
	WorktreeRemoved bool `json:"worktree_removed"`
	// BranchDeleted=false 表示跳过删分支、未合并被 git 拒绝、或分支已不存在。
	BranchDeleted bool `json:"branch_deleted"`
	// BranchSkipReason 解释 BranchDeleted=false 的原因（给用户看的，不靠猜）。
	BranchSkipReason string `json:"branch_skip_reason,omitempty"`
	// Orphans 是 .worktree/ 下的残留目录（PruneOrphans 开着才有），只列不删。
	Orphans []gitview.OrphanWorktreeDir `json:"orphans,omitempty"`
	// CleanedLeftovers 是收尾为拆目录而清掉的工具状态目录（.claude/ .omc/ .mindfs/）。
	// 报出来是为了让「它删了什么」可见 —— 静默删目录是最不该有的那种自动化。
	CleanedLeftovers []string `json:"cleaned_leftovers,omitempty"`
}

// FinishTaskWorktree 收尾一个任务在 worktree 里的活：合回主 checkout → 拆 worktree
// → 删分支 → 列残留。
//
// 合不拢就**停在冲突态**并把冲突文件报上去（用户定的口径），不自动 abort：
// 解到一半的取舍连同 MERGE_MSG 一起丢掉，用户得从头再来。worktree 和分支此时都
// 还在，什么都没丢。
func (s *Service) FinishTaskWorktree(ctx context.Context, in FinishWorktreeInput) (FinishWorktreeResult, error) {
	store, task, err := s.loadForMove(ctx, in.RootID, in.TaskID)
	if err != nil {
		return FinishWorktreeResult{}, err
	}
	if !task.CreateWorktree {
		return FinishWorktreeResult{}, errors.New("该任务没有 worktree")
	}
	worktreePath := strings.TrimSpace(task.WorktreePath)
	if worktreePath == "" {
		// 还没建树（首段还是 user 段时路径本来就空），不是失效状态。
		return FinishWorktreeResult{}, errors.New("该任务还没有 worktree")
	}
	// 正在跑的段必须在**动 git 之前**拦下。这是收尾里唯一会改写历史 + 删目录的动作，
	// 而 agent 的 cwd 就在那个 worktree 里：合到一半把目录拆了，agent 后续的
	// git commit 要么失败，要么在别处落一份主 checkout 看不到的提交。
	//
	// 判据是**段**状态而不是 task.status：task.status 在 agent 段跑起来之后才变
	// running，而 stage run 更早一步就 running 了 —— 只看 task.status 会漏掉
	// 「刚启动、还没改到 status」那个窗口，那正是最容易出事的时刻。
	if err := s.assertNotRunning(ctx, store, task); err != nil {
		return FinishWorktreeResult{}, err
	}
	// 独占收尾权：从这里到拆完都挡住新的执行体。
	// assertNotRunning 查的是**落库的**段状态，而 acquireTaskFinish 查的是**在跑的**
	// 执行体 —— 两者查的不是一回事（agent 刚起、状态还没落库时只有一个查得到），
	// 顺序上先查状态再抢锁：抢到锁之后不会有新的执行体插进来，剩下的只是把状态
	// 的判定收窄到「此刻确实没有在跑」。
	release, err := s.acquireTaskFinish(in.RootID, in.TaskID)
	if err != nil {
		return FinishWorktreeResult{}, err
	}
	defer release()
	if err := s.assertNotRunning(ctx, store, task); err != nil {
		return FinishWorktreeResult{}, err
	}
	mainDir, err := s.taskWorktreeMainDir(ctx, in.RootID)
	if err != nil {
		return FinishWorktreeResult{}, err
	}
	// 源分支从任务记录里取，不从 worktree 的 cwd 猜：worktree 可能已经被手工 checkout
	// 到别的分支上，而任务记的是它当初建树时定的那个。
	branch := strings.TrimSpace(task.WorktreeBranch)
	if branch == "" {
		branch = worktreeDirName(worktreePath)
	}
	target := strings.TrimSpace(in.Target)
	if target == "" {
		target = "main"
	}

	result := FinishWorktreeResult{Task: task}

	// 拆 worktree 之前核对身份：任务记的是「建树时那个目录 + 那个分支」，而目录
	// 可能被手工 checkout 到别的分支、或被别的流程挪作他用。不核对就拆，拆掉的
	// 是别人的活 —— 而且分支已经合过了，事后看不出来。
	if err := s.assertWorktreeMatches(ctx, mainDir, worktreePath, branch); err != nil {
		return result, err
	}

	// ── 1. 合并 ──
	merged, err := gitview.MergeBranch(ctx, gitview.MergeOptions{
		Source:       branch,
		Target:       target,
		MainDir:      mainDir,
		WorktreePath: worktreePath,
	})
	if err != nil {
		// 冲突原样上抛，并且挂到任务上：合并现在停在主 checkout 的 MERGE_HEAD，
		// 这是「仓库待人工处理」的状态，必须让人看见。
		var conflict gitview.MergeConflict
		if errors.As(err, &conflict) {
			asConflict := &FinishWorktreeConflict{
				ConflictFiles: conflict.ConflictFiles,
				Output:        conflict.Output,
			}
			_ = s.recordTaskError(ctx, store, task, "", asConflict.Error())
			return result, asConflict
		}
		return result, err
	}
	result.Merged = merged.Merged
	result.Commit = merged.Commit

	// ── 2. 拆 worktree ──
	// 合完了才拆。判据是「git 还认不认这个 worktree」而不是「目录在不在」：
	// `worktree remove --force` 之后目录可能还留着（里面有 git 不跟踪的 .mindfs/
	// 会话库），那种目录已经拆过了，再拆一次会拿到 "current root is not a git
	// worktree"，把一次成功的收尾报成失败。目录压根不在则同样是已拆过（幂等）。
	if isWT, wtErr := gitview.IsWorktree(worktreePath); wtErr == nil && isWT {
		// 先分清「挡住拆除的是工具产物还是用户的活」，再决定动不动手。
		// 2026-10-02 端到端实测：不拆这一步，收尾会被**自己产生的** .claude/ 和
		// .omc/ 挡住 —— agent 在收尾段里跑完留下的目录，下一步要拆的就是它。
		// mindfs 自己的仓库靠 .gitignore 躲过了这一劫，但要管的是用户任意仓库，
		// 那里不会有我们的忽略条目。
		blockers, bErr := gitview.ClassifyWorktreeRemoveBlockers(ctx, worktreePath)
		if bErr != nil {
			return result, fmt.Errorf(
				"合并已成功，但拆除 worktree 失败：读不了 %s 的 git 状态，不敢替你清理，先手工看一眼：%w",
				worktreePath, bErr)
		}
		if len(blockers.UserChanges) > 0 {
			// 用户的活一律不动。这是唯一必须停下来的情形。
			return result, fmt.Errorf(
				"合并已成功，但拆除 worktree 失败：%s 里还有没提交的东西（%s），先处理掉再收尾。工具自己留下的临时目录（.claude/ .omc/ .mindfs/）不用你管，那部分会自己清",
				worktreePath, strings.Join(blockers.UserChanges, "、"))
		}
		if len(blockers.AgentLeftovers) > 0 {
			// 只有工具产物：清掉再拆。删不掉也不拦（可能有会话正在写），
			// 让 git 去报它自己的话。
			result.CleanedLeftovers = gitview.CleanupAgentLeftovers(worktreePath, blockers.AgentLeftovers)
		}
		if err := gitview.RemoveWorktree(ctx, worktreePath); err != nil {
			// 到这儿还失败，剩下的原因就不是「有未提交改动」了 —— 判据已经排除了。
			// 原样透出 git 的话，不替用户猜。
			// 不 --force：--force 删掉的正是那些未提交内容，替用户决定丢不丢他的活不行。
			return result, fmt.Errorf(
				"合并已成功，但拆除 worktree 失败：%w", err)
		}
		result.WorktreeRemoved = true
	}

	// ── 3. 删分支 ──
	// 只在用户点名要删时才删，且用的是 `branch -d`：未合并时 git 自己会拒绝并说明
	// 原因。收尾最坏的情况是默默丢提交，宁可停下来。
	if in.DeleteBranch {
		if err := gitview.DeleteBranch(ctx, mainDir, branch); err != nil {
			// 「分支本来就不在了」不是问题：重复收尾时必然走到这，而 git 的原话是
			// `error: branch 'task-7' not found.` —— 直接透出去的话，一次成功的收尾
			// 会显示成一条红色报错。留空理由，前端就不渲染那行警告。
			if !strings.Contains(strings.ToLower(err.Error()), "not found") {
				result.BranchSkipReason = err.Error()
			}
		} else {
			result.BranchDeleted = true
		}
	} else {
		result.BranchSkipReason = "未请求删除分支"
	}

	// ── 4. 清任务侧归属 ──
	// 与 finishTask 保留 worktree_path 的语义不冲突：那是「目录还在就别擦历史」，
	// 这里是「已经显式收尾了，任务还钉着一个不存在的目录就是纯陈旧状态」。
	// 前端 relatedWorktree 是任务优先（App.tsx），留着会让 git 面板去展开死目录。
	//
	// 列级 UPDATE（见 TaskStore.ClearWorktreeRefs）：上面那几步 git 命令要跑好几秒，
	// 期间 agent 那边可能已经把 aux 标记（has_plan/has_todos/session_error）或
	// worktree 归属写进库了。整行写回那份开头取的快照，会把这些并发写入一起覆盖掉 ——
	// 症状是「agent 明明报过 plan，任务上却什么都没有」。
	if err := store.ClearWorktreeRefs(ctx, task.ID); err != nil {
		return result, err
	}
	fresh, err := store.GetTask(ctx, task.ID)
	if err != nil {
		return result, err
	}
	result.Task = fresh

	// ── 5. 列残留 ──
	// 只列不删：`git worktree remove` / `worktree prune` 只清 git 的元数据和源码，
	// 目录里 git 完全不跟踪的东西（.mindfs/ 会话库、.claude/、.omc/）还在。
	// 删不删由用户看完内容再决定。
	if in.PruneOrphans {
		orphans, err := gitview.PruneOrphanDirs(ctx, mainDir)
		if err != nil {
			return result, err
		}
		result.Orphans = orphans
	}

	if detail, err := store.GetDetail(ctx, task.ID); err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(task.RootID, detail)
	}
	return result, nil
}

// assertWorktreeMatches 核对任务记的 worktree 目录仍然挂在那个分支上。
//
// 只在**要拆它**的时候才需要——目录不在 git 登记里（已拆）就随它去，那是幂等；
// 登记在但分支不是任务记的那个，说明有人手工 checkout 过或路径被复用，这时拆掉
// 的是别人的活，必须停下来。
func (s *Service) assertWorktreeMatches(ctx context.Context, mainDir, worktreePath, branch string) error {
	binding, err := gitview.InspectWorktree(ctx, mainDir, worktreePath)
	if err != nil {
		return err
	}
	if !binding.Registered {
		return nil
	}
	actual := binding.BranchName()
	if actual == "" {
		return fmt.Errorf("%s 现在是 detached HEAD（%s），不再是 %s 分支 —— 先确认那里有没有要保留的活再收尾",
			worktreePath, gitview.ShortSHA(binding.Head), branch)
	}
	if branch != "" && actual != branch {
		return fmt.Errorf("%s 现在挂在 %s 上，不是任务记的 %s —— 拆它会连别人的活一起拆掉，先人工确认",
			worktreePath, actual, branch)
	}
	return nil
}

// assertNotRunning 拦下「agent 还在跑」的收尾。
//
// **判据是会话在不在回复**（金标准）：任务可以长时间停在 waiting_user（等审核），
// 那只是等人，不代表 agent 在动；而 task.Status 与「agent 此刻是否真的在动」之间还
// 隔着建会话、过 exchange 表那段窗口。只有「这个会话正在回复」是直接读数。
//
// 探针没装配时（单测、老装配）回落到 task.Status / 段状态那套旧判据：runAgentStage
// 起 agent 和把 status 置 running 之间有时间差，而 stage run 的 running 落得更早，
// 两个都判、取「任一在跑就拦」。
//
// 拿不到段状态时（读失败、指针越界）不当作「在跑」：那是数据问题，不该把用户
// 锁死在收不了尾的盒子里 —— 真正的保护在 git 层，拦不住的话 merge 撞上冲突会
// 自己停下来。
func (s *Service) assertNotRunning(ctx context.Context, store *TaskStore, task Task) error {
	if s.sessionRunning(task.MainSessionKey) {
		return ErrTaskSessionRunning
	}
	if s.sessionProbeConfigured() {
		// 会话说了算：探针说没在回复就是没在跑，不再叠状态判据 ——
		// 那正是「任务卡在 running 却早就没 agent」时收不了尾的根因。
		return nil
	}
	if task.Status == StatusRunning {
		return errors.New("任务正在执行中，先停止再收尾")
	}
	index := task.CurrentStageIndex
	if index < 0 || index >= len(task.Stages) {
		return nil
	}
	run, err := store.LatestStageRun(ctx, task.ID, index)
	if err != nil {
		return nil
	}
	if run.Status == StageStatusRunning {
		return errors.New("当前阶段正在执行中，先停止再收尾")
	}
	return nil
}

// TaskSessionRunning 报任务绑定的会话此刻是否在回复中。
//
// 给 api 层做收尾分发用：会话在回复时收尾**什么都不做**，只回一句「agent 正在收尾中」，
// 而不是让请求撞进 assertNotRunning 变成 409 —— 那是正常等待，不是错误。
func (s *Service) TaskSessionRunning(ctx context.Context, rootID, taskID string) bool {
	if s == nil || !s.sessionProbeConfigured() {
		return false
	}
	store, err := s.taskStore(rootID)
	if err != nil {
		return false
	}
	task, err := store.GetTask(ctx, strings.TrimSpace(taskID))
	if err != nil {
		return false
	}
	return s.sessionRunning(task.MainSessionKey)
}

// TaskWorktreeBranchMerged 报任务的 worktree 分支是否已经全部合进主 checkout 的目标分支。
//
// 「已合并」= 活已经在主干里了，收尾只剩清场，不必再让 agent 跑一遍（重跑那段会
// 追加一次没意义的提交）。分支不存在（从没建过树）返回 false，让调用方走正常收尾流程。
func (s *Service) TaskWorktreeBranchMerged(ctx context.Context, rootID, taskID, target string) (bool, error) {
	_, task, err := s.loadForMove(ctx, rootID, taskID)
	if err != nil {
		return false, err
	}
	if !task.CreateWorktree {
		return false, nil
	}
	worktreePath := strings.TrimSpace(task.WorktreePath)
	if worktreePath == "" {
		return false, nil
	}
	mainDir, err := s.taskWorktreeMainDir(ctx, rootID)
	if err != nil {
		return false, err
	}
	branch := strings.TrimSpace(task.WorktreeBranch)
	if branch == "" {
		branch = worktreeDirName(worktreePath)
	}
	if strings.TrimSpace(target) == "" {
		target = "main"
	}
	return gitview.BranchMergedInto(ctx, mainDir, branch, target)
}

// TaskFinishStageIndex 报任务流水里收尾段的下标；没有则 ok=false。
// api 层据此决定「重跑那段继续收尾」还是「追加一段新收尾段」。
func (s *Service) TaskFinishStageIndex(ctx context.Context, rootID, taskID string) (int, bool, error) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return 0, false, err
	}
	task, err := store.GetTask(ctx, strings.TrimSpace(taskID))
	if err != nil {
		return 0, false, err
	}
	for i, stage := range task.Stages {
		if IsFinishStage(stage) {
			return i, true, nil
		}
	}
	return 0, false, nil
}

// taskWorktreeMainDir 报任务 worktree 所在项目的主 checkout 路径。
//
// 合并的每条 git 命令都必须跑在主 checkout 里（见 gitview.MergeOptions.MainDir 的
// 说明），所以这里要把任务的项目路径和 worktree 路径对上，不能直接用 WorktreePath。
func (s *Service) taskWorktreeMainDir(ctx context.Context, rootID string) (string, error) {
	if s.Roots == nil {
		return "", errors.New("root provider not configured")
	}
	root, err := s.Roots.GetRoot(rootID)
	if err != nil {
		return "", err
	}
	// 任务本身可能注册在 worktree 里（worktree 会话 + 看板任务并存的情况）。
	// 真 worktree 往下走一层就是主 checkout。
	return gitview.MainCheckoutPath(ctx, root.RootPath)
}

// worktreeDirName 取 worktree 目录名（task-24 → task-24）。任务没记分支名时用它兜底。
func worktreeDirName(path string) string {
	base := strings.TrimSpace(path)
	if base == "" {
		return ""
	}
	return filepath.Base(filepath.Clean(base))
}
