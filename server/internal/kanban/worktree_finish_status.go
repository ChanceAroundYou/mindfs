package kanban

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"mindfs/server/internal/gitview"
)

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
	return s.taskBusyErr(ctx, store, task)
}

// taskBusyErr 是「这个任务现在有没有执行体在动」的判据，**与 ctx/store 解耦**地写成
// 一个纯函数式的内部方法，好让 api 层的收尾分流（TaskBusy）复用同一套判据 ——
// 两处各写一份的话，迟早有一处漏掉「段在跑」这个窗口。
func (s *Service) taskBusyErr(ctx context.Context, store *TaskStore, task Task) error {
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

// TaskBusy 报任务现在有没有执行体在动（会话在回复，或当前段还在跑）。
//
// 给 api 层的收尾分流用：那是**正常等待**，不是错误 —— 以前只看会话探针，段在跑时
// 会漏过去、让请求撞进收尾准入变成 409，界面上表现为「收尾一直转、点不动」。
//
// 读不到任务一律返回 false（不 busy）：数据问题不该把用户锁死在收不了尾的盒子里。
func (s *Service) TaskBusy(ctx context.Context, rootID, taskID string) bool {
	if s == nil {
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
	return s.taskBusyErr(ctx, store, task) != nil
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

// TaskHasAgentStage 报任务流水里有没有 agent 段。
//
// 给 api 层的收尾分流用：收尾段要继承上一段的 agent/模型，没有 agent 段时追加收尾段
// 必然失败（BeginFinishWorktree 会 409）。与其给一个必然失败的按钮，不如直接机械清场。
func (s *Service) TaskHasAgentStage(ctx context.Context, rootID, taskID string) (bool, error) {
	_, task, err := s.loadForMove(ctx, rootID, taskID)
	if err != nil {
		return false, err
	}
	for _, stage := range task.Stages {
		if stage.Role == RoleAgent {
			return true, nil
		}
	}
	return false, nil
}

// FinishPlan 是「这次收尾该走哪条路」的服务端只读判定。
//
// 收尾是 agent + 机械清场两个阶段的整合，但很多情况下 agent 那半已经没活可干了
// （分支早合进主干、worktree 也干净），再跑一遍只会多一个空提交、让用户白等一轮。
// 所以先判定，能机械清场就直接做掉。
type FinishPlan struct {
	// Mechanical=true 表示机械清场能直接做完，不必等 agent。
	Mechanical bool `json:"mechanical"`
	// Reason 是 Mechanical=false 的原因（短句，给用户看）。
	Reason string `json:"reason,omitempty"`
	// Files 是相关文件清单（未提交的文件 / 会冲突的文件），给 UI 列表渲染。
	Files []string `json:"files,omitempty"`
}

// PlanFinishWorktree 判定这次收尾能不能直接走机械清场。**只读**，不碰仓库状态。
//
// 判定顺序（任一条不满足就退给 agent）：
//  1. 没有 worktree / 目录已不在 → 机械（清场自己会处理「本来就没树」的幂等情形）
//  2. worktree 里还有没提交的活 → **不**机械，交给 agent 判断哪些是成品要提交
//  3. 分支已在主干 → 机械（只剩清场）
//  4. 合并会冲突 → **不**机械，交给 agent 解释冲突并问用户
//  5. 都过 → 机械
//
// ② 必须排在 ③ 之前：**分支合过不等于树里没活**。任务可以在合并之后继续改
// （2026-10-08 实测 task-9：merge 在 21:41，而 12 个文件 161 增 115 删是 23:30–00:41
// 做的），这时「只剩清场」是假象 —— 拆目录会把它们一起删掉。反过来排在后面的话，
// 判定说「机械能做」而下层 FinishTaskWorktree 拦住，白试一次清场，还会把那份失败
// 当成主报错抛给用户（真正该说的是「有活要交出去」）。
//
// 刻意**不**要求主 checkout 干净：那是「合并能不能做完」的一部分，真不干净时
// MergeBranch 会自己拦下并列出文件（MainCheckoutDirtyError → dirty_files），
// 让用户看见即可。在这里提前拦会把「主 checkout 有改动」误报成「要跑 agent」，
// 而 agent 同样合不进去 —— 白等一轮，问题还在。
func (s *Service) PlanFinishWorktree(ctx context.Context, rootID, taskID string) (FinishPlan, error) {
	store, task, err := s.loadForMove(ctx, rootID, taskID)
	if err != nil {
		return FinishPlan{}, err
	}
	if !task.CreateWorktree {
		return FinishPlan{Mechanical: true, Reason: "该任务没有 worktree"}, nil
	}
	worktreePath := strings.TrimSpace(task.WorktreePath)
	if worktreePath == "" {
		return FinishPlan{Mechanical: true, Reason: "该任务还没有 worktree"}, nil
	}
	// 共享 worktree 一律机械清场（= FinishTaskWorktree 的解绑分支）。
	//
	// 否则后面两条判据都会看**兄弟任务的**状态：树里的未提交改动是别人的半成品，
	// 合并能不能成也取决于别人干到哪一步。据此退给本任务的 agent，等于让它去提交
	// 别人的活。只有最后一个持有者才需要做真正的合并判定。
	if refs, refErr := store.WorktreeReferenceCount(ctx, worktreePath, task.ID); refErr == nil && refs > 0 {
		return FinishPlan{Mechanical: true, Reason: "worktree 还有别的任务在用，这次只解绑"}, nil
	}
	// 目录已经不在了：没有活可提交，直接清场（清场会清掉任务侧归属，幂等）。
	if isWT, wtErr := gitview.IsWorktree(worktreePath); wtErr != nil {
		// 读不到 git 状态时不替用户决定 —— 交给 agent 看一眼。
		return FinishPlan{Mechanical: false, Reason: "读不到 worktree 的 git 状态，交给 agent 确认"}, nil
	} else if !isWT {
		if _, statErr := os.Stat(worktreePath); os.IsNotExist(statErr) {
			return FinishPlan{Mechanical: true, Reason: "worktree 目录已不在"}, nil
		}
		// 目录在、但没有 .git：已经是拆过一半的残留，交给清场删干净。
		return FinishPlan{Mechanical: true, Reason: "worktree 已拆，只剩残留目录"}, nil
	}
	mainDir, err := s.taskWorktreeMainDir(ctx, rootID)
	if err != nil {
		return FinishPlan{}, err
	}
	branch := strings.TrimSpace(task.WorktreeBranch)
	if branch == "" {
		branch = worktreeDirName(worktreePath)
	}
	target := "main"

	// worktree 里还有没提交的活 → 交给 agent：只有它能判断哪些是成品、该怎么提交。
	// 读不到状态时也退给 agent（判据失效时宁可让人看一眼，别替用户删东西）。
	//
	// 这一步**必须在「分支已在主干」之前**，理由见上面判定顺序的注释：分支合过不代表
	// 树里没活，而拆目录是不可逆的。
	blockers, blockerErr := gitview.ClassifyWorktreeRemoveBlockers(ctx, worktreePath)
	if blockerErr != nil {
		return FinishPlan{
			Mechanical: false,
			Reason:     "读不到 worktree 的 git 状态，交给 agent 确认",
		}, nil
	}
	if len(blockers.UserChanges) > 0 {
		return FinishPlan{
			Mechanical: false,
			Reason:     "worktree 里还有没提交的改动",
			Files:      blockers.UserChanges,
		}, nil
	}

	// 树干净、分支也已在主干 → 真的只剩清场了。
	if merged, mergedErr := gitview.BranchMergedInto(ctx, mainDir, branch, target); mergedErr == nil && merged {
		return FinishPlan{Mechanical: true, Reason: "分支已合进主干"}, nil
	}

	// 合并会冲突 → 交给 agent：冲突要解释、要给取舍判断，机器替不了。
	feasibility, feasibleErr := gitview.CanMergeCleanly(ctx, mainDir, branch, target)
	if feasibleErr != nil {
		return FinishPlan{
			Mechanical: false,
			Reason:     "判定合并可行性失败，交给 agent 确认",
		}, nil
	}
	if !feasibility.Clean {
		reason := feasibility.Reason
		if reason == "" {
			reason = "合并不能直接完成"
		}
		return FinishPlan{Mechanical: false, Reason: reason, Files: feasibility.Files}, nil
	}
	return FinishPlan{Mechanical: true, Reason: "worktree 干净且合并无冲突"}, nil
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
