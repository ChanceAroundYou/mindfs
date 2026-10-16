package kanban

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (s *Service) ensureTaskWorktree(ctx context.Context, store *TaskStore, task Task) (Task, error) {
	if !task.CreateWorktree {
		return task, nil
	}
	// 失效时**不自动重建**：重建等于替用户决定那个已删目录里的分支怎么办（分支可能还在、
	// 也可能已经被删），所以报明确错误，由 RebuildTaskWorktree 显式触发。
	if p := strings.TrimSpace(task.WorktreePath); p != "" {
		if worktreeDirUsable(p) {
			return task, nil
		}
		return task, fmt.Errorf("worktree 目录已不存在（%s），点「重建 worktree」恢复后再执行", p)
	}
	if s.Runner == nil {
		return task, errors.New("task runner not configured")
	}
	name := renderWorktreeName("", task)
	branchMode, branch := normalizeTaskWorktreeBranch(task.WorktreeBranchMode, task.WorktreeBranch)
	wt, err := s.Runner.CreateTaskWorktree(ctx, task.RootID, name, branchMode, branch)
	if err != nil {
		return task, err
	}
	// 拿到一个不可用的路径就当没建过：**不写库、不写 worktree_built**。
	//
	// 为什么必须在这里拦：空路径会让下游两处同时说谎 —— agent 拿到空的
	// RuntimeRootPath（service.go 取的就是 task.WorktreePath），于是**在主 checkout
	// 里跑**，改的是主干；界面那边看到「建过 + 路径为空」，把没建过树的活说成
	// 「已收尾」。两个后果都不可逆，所以宁可当失败、让用户重建。
	// 不自动重建的理由与上面「失效时不自动重建」一致：不替用户决定那个目录怎么办。
	if !worktreeDirUsable(wt.Path) {
		return task, fmt.Errorf("worktree 创建没有产出可用目录（路径=%q），请点「重建 worktree」重试", strings.TrimSpace(wt.Path))
	}
	now := time.Now().UTC()
	task.WorktreeRootID = wt.RootID
	task.WorktreePath = wt.Path
	// 记住「建过」：之后清归属（收尾拆目录）会清掉路径，但不该把这条事实一起抹掉，
	// 否则界面分不出「从没建过」和「建过、记录丢了」。
	task.WorktreeBuilt = true
	task.AuxFlags.SessionError = ""
	task.UpdatedAt = now
	// 归属只有一个写入出口（SetWorktreeRefsAndTask），且与状态更新同一事务 ——
	// 理由见 task_store.go 的 updateTaskCore。
	if err := store.SetWorktreeRefsAndTask(ctx, task); err != nil {
		return task, err
	}
	return task, nil
}

// ClearTaskWorktree 清掉任务记录的 worktree 归属（路径 + root id）。
//
// **只给收尾流程用**（它拆掉目录之后清归属）。搬会话不要调它 —— repoint 曾经调过，
// 一次纯搬会话把还在用的任务目录记录清成空，卡片随即显示「已收尾」。见
// usecase/session_repoint.go 里那段刻意不调的注释。
//
// 保留 worktree_built：拆掉目录不等于没建过，界面要靠它显示「已收尾」而不是
// 退回「没建过」。
func (s *Service) ClearTaskWorktree(ctx context.Context, rootID, taskID string) error {
	store, task, err := s.loadForMove(ctx, rootID, taskID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(task.WorktreePath) == "" && strings.TrimSpace(task.WorktreeRootID) == "" {
		return nil
	}
	// 只清归属两列，不整行写回 —— 理由见 TaskStore.ClearWorktreeRefs。
	if err := store.ClearWorktreeRefs(ctx, task.ID); err != nil {
		return err
	}
	if detail, err := store.GetDetail(ctx, task.ID); err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(task.RootID, detail)
	}
	return nil
}

// RebuildTaskWorktree 重建已删除的任务 worktree。
//
// 为什么不自动重建：目录没了之后，那个分支可能还在（代码还在分支上）也可能已经跟着
// 删了。自动重建等于替用户决定哪一种，而 gitview.AddWorktree 的 -b 在分支仍存在时会
// 报 branch already exists —— 那种错必须让人看见，不该被吞掉。所以这里是显式入口：
// 用户点了才重建，报错了就原样报上去。
//
// 幂等：目录还在时直接返回当前详情，不重复建树。
func (s *Service) RebuildTaskWorktree(ctx context.Context, in MoveInput) (TaskDetail, error) {
	store, task, err := s.loadForMove(ctx, in.RootID, in.TaskID)
	if err != nil {
		return TaskDetail{}, err
	}
	if !task.CreateWorktree {
		return TaskDetail{}, errors.New("task has no worktree")
	}
	if worktreeDirUsable(task.WorktreePath) {
		return store.GetDetail(ctx, task.ID)
	}
	// 清掉陈旧路径，让 ensureTaskWorktree 走建树分支而不是撞上「目录已不存在」那条例外。
	task.WorktreePath = ""
	task.WorktreeRootID = ""
	updated, err := s.ensureTaskWorktree(ctx, store, task)
	if err != nil {
		// 失败原因记到任务上：卡片本来就会渲染 session_error，用户不必去翻日志
		// （典型是 branch already exists，需要他决定怎么处理那个分支）。
		_ = s.recordTaskError(ctx, store, task, "", err.Error())
		return store.GetDetail(ctx, task.ID)
	}
	detail, err := store.GetDetail(ctx, updated.ID)
	if err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(updated.RootID, detail)
	}
	return detail, err
}

func renderWorktreeName(tpl string, task Task) string {
	name := strings.TrimSpace(tpl)
	if name == "" {
		name = "task-{task_number}"
	}
	replacements := map[string]string{
		"task_id":       task.ID,
		"task_number":   strconv.Itoa(task.TaskNumber),
		"root_id":       task.RootID,
		"template_name": task.TaskTemplateName,
		"task_name":     task.Name,
	}
	for key, value := range replacements {
		name = strings.ReplaceAll(name, "{"+key+"}", value)
	}
	name = strings.TrimSpace(name)
	if name == "" || name == "task-0" {
		name = filepath.Base(task.ID)
	}
	return name
}

func normalizeTaskWorktreeBranch(mode, branch string) (string, string) {
	mode = strings.TrimSpace(mode)
	branch = strings.TrimSpace(branch)
	if mode != "existing" {
		return "new", ""
	}
	if branch == "" {
		return "new", ""
	}
	return "existing", branch
}
