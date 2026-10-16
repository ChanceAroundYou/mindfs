package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

// AddStageInput 追加一段 prompt（下一段要执行的内容）。
// 任务处于等待用户时，追加即自动推进；其他状态排入流水尾。
type AddStageInput struct {
	RootID string
	TaskID string
	Stage  StageTemplate
	// ByRequest 表示这次追加是**人/系统显式发起的动作**（收尾段），不是用户随手补的
	// 一句评论。两条路的区别只在「能不能离开当前段」：
	//   - 置位 → canLeaveStageOnRequest：agent 段停在 waiting_user（agent 自己没回报
	//     完成）也能被这次显式动作推过去，与「下一段」按钮（moveRelative）同一条规则；
	//   - 不置位 → canAdvanceFromStage：评论不能替代完成本段，agent 段没走完就拒绝。
	//
	// 收尾必须置位：用户点「收尾」是一个明确动作（「这活我收了，去 commit + merge」），
	// 而任务停在「等待你」时当前段正是 agent 段的 waiting_user —— 走评论那条规则必然
	// 被拒，用户拿到的是一个他无从下手的报错（2026-10-08 实测 task-9）。
	ByRequest bool
}

// UpdateStageInput 修改任务流水里某一段的定义。
type UpdateStageInput struct {
	RootID string
	TaskID string
	Index  int
	Stage  *StageTemplate // 全量替换该段定义
}

// RemoveStageInput 删除任务流水里尚未执行的一段。
type RemoveStageInput struct {
	RootID string
	TaskID string
	Index  int
}

// RenameTask 设置任务名。
func (s *Service) RenameTask(ctx context.Context, rootID, taskID, name string) (TaskDetail, error) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return TaskDetail{}, err
	}
	task, err := store.GetTask(ctx, strings.TrimSpace(taskID))
	if err != nil {
		return TaskDetail{}, err
	}
	// 任务名永不携带本任务自己的后缀：这里剥一次，「任务名 ↔ 会话名」往返怎么走都干净。
	task.Name = TrimTaskSessionNameSuffix(name, task.TaskNumber)
	task.UpdatedAt = time.Now().UTC()
	if err := store.UpdateTask(ctx, task); err != nil {
		return TaskDetail{}, err
	}
	detail, err := store.GetDetail(ctx, task.ID)
	if err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(task.RootID, detail)
	}
	return detail, err
}

// TaskNameFromSession：会话改名时同步到绑定的任务（main_session_key 方向；
// 与 HTTP 层「任务→会话」同步共同构成任务名 ↔ 会话名双向绑定）。
// 返回 (detail, true) 表示任务名确实更新；未找到任务或名字未变返回 (_, false)。
func (s *Service) TaskNameFromSession(ctx context.Context, rootID, sessionKey, name string) (TaskDetail, bool) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return TaskDetail{}, false
	}
	taskID, err := store.TaskIDForMainSession(ctx, sessionKey)
	if err != nil || taskID == "" {
		return TaskDetail{}, false
	}
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		return TaskDetail{}, false
	}
	// 会话名带着 " / #编号"，比变更前先剥掉，否则「原样重命名一个带后缀的会话」
	// 会被判成变更，白跑一趟 RenameTask + broadcastTaskUpdated。
	base := TrimTaskSessionNameSuffix(name, task.TaskNumber)
	if task.Name == base {
		return TaskDetail{}, false
	}
	detail, err := s.RenameTask(ctx, rootID, taskID, base)
	if err != nil {
		return TaskDetail{}, false
	}
	return detail, true
}

// DetachFromSession：会话被**删除**时（会话→任务方向，与 TaskNameFromSession 互为镜像）
// 清空任务指向它的所有引用，并在 aux_session_error 上留痕，让任务面板显示
// 「关联会话已删除」而不是跳进一个不存在的 key。
//
// 归档不走这里：归档的会话仍能打开，链接必须保留。
// 返回 (detail, true) 表示确实改动了任务；没有任务绑这些会话时返回 (_, false)。
func (s *Service) DetachFromSession(ctx context.Context, rootID string, sessionKeys []string) (TaskDetail, bool) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return TaskDetail{}, false
	}
	var last TaskDetail
	changed := false
	// 逐个 key 反查：删除是按子树级联的，key 数量不定，
	// TaskIDForMainSession 一次只认一个 key。
	for _, sessionKey := range sessionKeys {
		key := strings.TrimSpace(sessionKey)
		if key == "" {
			continue
		}
		taskID, err := store.TaskIDForMainSession(ctx, key)
		if err != nil || taskID == "" {
			continue
		}
		if err := store.ClearSessionRefs(ctx, taskID, key); err != nil {
			log.Printf("[kanban] detach session refs failed task=%s session=%s: %v", taskID, key, err)
			continue
		}
		msg := deletedSessionNotice(key)
		detail, err := s.UpdateTaskAuxFlags(ctx, rootID, taskID, TaskAuxFlagsPatch{SessionError: &msg}, "session_deleted")
		if err != nil {
			continue
		}
		last = detail
		changed = true
	}
	return last, changed
}

// deletedSessionNotice 生成 aux_session_error 的留痕文案。
// 形状是 {"message":..., "data":[...]}，前端 parseTaskSessionErrorMessage 已按此解析。
func deletedSessionNotice(sessionKey string) string {
	payload := map[string]any{
		"message": fmt.Sprintf("关联会话已删除（%s），任务已解绑。", sessionKey),
		"data":    []string{sessionKey},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Sprintf("关联会话已删除（%s），任务已解绑。", sessionKey)
	}
	return string(raw)
}

// AddStage 追加下一段 prompt。任务等待用户时追加即推进并执行；其他状态排入流水尾。
func (s *Service) AddStage(ctx context.Context, in AddStageInput) (TaskDetail, error) {
	store, err := s.taskStore(in.RootID)
	if err != nil {
		return TaskDetail{}, err
	}
	task, err := s.ensureServiceTask(ctx, in.RootID, store, strings.TrimSpace(in.TaskID))
	if err != nil {
		return TaskDetail{}, err
	}
	stage := normalizeStageTemplate(in.Stage)
	if task.Status == StatusWaitingUser && strings.TrimSpace(stage.PromptTemplate) != "" {
		// 准入检查放在**最前面**：下面已经要把当前段批成 approved、要把新段推进上去。
		// 收尾期间放进来做完这些再返回，留下的是「段已 approved、没有执行体」——
		// 和「永远 running」是同一种卡死，只是换了张脸。
		release, admErr := s.beginRunAdmission(in.RootID, task.ID)
		if admErr != nil {
			return store.GetDetail(ctx, task.ID)
		}
		defer release()
		stage.Name = defaultStageName(task, len(task.Stages))
		task.Stages = append(task.Stages, stage)
		if latest, runErr := store.LatestStageRun(ctx, task.ID, task.CurrentStageIndex); runErr == nil {
			// 同 moveRelative：agent 段没走完时，追加一句评论不能当成「这段已完成」。
			// user 段的 waiting_user 是在等输入，补评论正是答案，照常批准。
			//
			// ByRequest 是另一条线：发起者不是「一句评论」而是人/系统的明确动作（收尾段），
			// 规则换成 canLeaveStageOnRequest —— agent 段的 waiting_user 正该由人来判
			// 「就这样，往下走」。见 AddStageInput.ByRequest。
			canLeave := canAdvanceFromStage
			if in.ByRequest {
				canLeave = canLeaveStageOnRequest
			}
			if !canLeave(task.Stages[task.CurrentStageIndex].Role, latest.Status) {
				return TaskDetail{}, fmt.Errorf(
					"current stage is %s: 这一段没走完，追加评论不能替代完成本段，重跑本段或改任务后再试",
					latest.Status,
				)
			}
			if latest.Status != StageStatusSuccess {
				_ = store.UpdateStageRunStatus(ctx, latest.ID, StageStatusApproved)
			}
		}
		detail, err := s.moveTo(ctx, store, task, len(task.Stages)-1, "user_approved", StageStatusApproved, "comment")
		if err != nil {
			return TaskDetail{}, err
		}
		if runErr := s.RunTask(detail.Task.RootID, detail.Task.ID); runErr != nil {
			return TaskDetail{}, runErr
		}
		return detail, nil
	}
	stage.Name = defaultStageName(task, len(task.Stages))
	task.Stages = append(task.Stages, stage)
	task.UpdatedAt = time.Now().UTC()
	if err := store.UpdateTask(ctx, task); err != nil {
		return TaskDetail{}, err
	}
	detail, err := store.GetDetail(ctx, task.ID)
	if err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(task.RootID, detail)
	}
	return detail, err
}

// UpdateStage 修改任务某一段的定义（prompt / agent / model / effort 等）。
func (s *Service) UpdateStage(ctx context.Context, in UpdateStageInput) (TaskDetail, error) {
	store, err := s.taskStore(in.RootID)
	if err != nil {
		return TaskDetail{}, err
	}
	task, err := s.ensureServiceTask(ctx, in.RootID, store, strings.TrimSpace(in.TaskID))
	if err != nil {
		return TaskDetail{}, err
	}
	idx := in.Index
	if idx < 0 || idx >= len(task.Stages) {
		return TaskDetail{}, errors.New("stage_index out of range")
	}
	now := time.Now().UTC()
	if in.Stage != nil {
		updated := normalizeStageTemplate(*in.Stage)
		updated.ID = task.Stages[idx].ID
		updated.CreatedAt = task.Stages[idx].CreatedAt
		task.Stages[idx] = updated
	}
	task.Stages[idx].UpdatedAt = now
	task.UpdatedAt = now
	if err := store.UpdateTask(ctx, task); err != nil {
		return TaskDetail{}, err
	}
	detail, err := store.GetDetail(ctx, task.ID)
	if err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(task.RootID, detail)
	}
	return detail, err
}

// RemoveStage 删除任务流水里尚未执行的一段。
// index 0 是任务输入段，不可删；当前指针所在段不可删（执行体正对着它）；
// 产生过 StageRun 的段也不可删（要改走 UpdateStage）。
func (s *Service) RemoveStage(ctx context.Context, in RemoveStageInput) (TaskDetail, error) {
	store, err := s.taskStore(in.RootID)
	if err != nil {
		return TaskDetail{}, err
	}
	task, err := s.ensureServiceTask(ctx, in.RootID, store, strings.TrimSpace(in.TaskID))
	if err != nil {
		return TaskDetail{}, err
	}
	idx := in.Index
	if idx <= 0 || idx >= len(task.Stages) {
		return TaskDetail{}, errors.New("stage_index out of range")
	}
	if idx == task.CurrentStageIndex {
		return TaskDetail{}, errors.New("current stage cannot be removed")
	}
	runs, err := store.ListStageRuns(ctx, task.ID)
	if err != nil {
		return TaskDetail{}, err
	}
	for _, run := range runs {
		if run.StageIndex == idx && run.Status != StageStatusPending {
			return TaskDetail{}, errors.New("stage already executed")
		}
	}

	task.Stages = append(task.Stages[:idx], task.Stages[idx+1:]...)
	if idx < task.CurrentStageIndex {
		task.CurrentStageIndex--
	}
	if task.CurrentStageIndex >= len(task.Stages) {
		task.CurrentStageIndex = len(task.Stages) - 1
	}
	if task.CurrentStageIndex < 0 {
		task.CurrentStageIndex = 0
	}
	now := time.Now().UTC()
	task.AuxFlags.SessionError = ""
	task.UpdatedAt = now
	if err := store.UpdateTask(ctx, task); err != nil {
		return TaskDetail{}, err
	}
	_ = store.AddEvent(ctx, TaskEvent{
		ID:        newID("event"),
		TaskID:    task.ID,
		Type:      "stage_removed",
		Payload:   eventPayload(map[string]any{"stage_index": idx}),
		CreatedAt: now,
	})
	detail, err := store.GetDetail(ctx, task.ID)
	if err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(task.RootID, detail)
	}
	return detail, err
}

// RerunStage 重新执行某段（任务不在运行中时）：把指针移回此段并触发执行。
func (s *Service) RerunStage(ctx context.Context, in MoveInput) (TaskDetail, error) {
	store, task, err := s.loadForMove(ctx, in.RootID, in.TaskID)
	if err != nil {
		return TaskDetail{}, err
	}
	if running, runErr := store.LatestStageRun(ctx, task.ID, task.CurrentStageIndex); runErr == nil && running.Status == StageStatusRunning && task.Status == StatusRunning {
		return TaskDetail{}, errors.New("task is running")
	}
	if in.StageIndex < 0 || in.StageIndex >= len(task.Stages) {
		return TaskDetail{}, errors.New("stage_index out of range")
	}
	release, admErr := s.beginRunAdmission(in.RootID, task.ID)
	if admErr != nil {
		return store.GetDetail(ctx, task.ID)
	}
	defer release()
	detail, err := s.moveTo(ctx, store, task, in.StageIndex, "stage_rerun", "", in.Reason)
	if err != nil {
		return TaskDetail{}, err
	}
	if !isTerminalStatus(detail.Task.Status) {
		if runErr := s.RunTask(detail.Task.RootID, detail.Task.ID); runErr != nil {
			return TaskDetail{}, runErr
		}
	}
	return detail, err
}

func (s *Service) UpdateCurrentInput(ctx context.Context, in UpdateTaskInput) (TaskDetail, error) {
	store, err := s.taskStore(in.RootID)
	if err != nil {
		return TaskDetail{}, err
	}
	task, err := store.GetTask(ctx, strings.TrimSpace(in.TaskID))
	if err != nil {
		return TaskDetail{}, err
	}
	run, err := store.LatestStageRun(ctx, task.ID, task.CurrentStageIndex)
	if err != nil {
		return TaskDetail{}, err
	}
	input := strings.TrimSpace(in.Input)
	payload := map[string]any{"input": input}
	if in.CreateWorktree != nil {
		if task.CurrentStageIndex != 0 {
			return TaskDetail{}, errors.New("create_worktree can only be changed in first stage")
		}
		// 建过树就不许再改 create_worktree —— 已经跑在那个目录里了。判据用
		// 「路径字段非空」而不是「目录还在」：目录被删是失效状态，该走的是重建入口，
		// 不该让用户借此把任务改成一个和既有历史矛盾的状态。
		if strings.TrimSpace(task.WorktreePath) != "" {
			return TaskDetail{}, errors.New("create_worktree cannot be changed after worktree is created")
		}
		now := time.Now().UTC()
		task.CreateWorktree = *in.CreateWorktree
		task.WorktreeBranchMode, task.WorktreeBranch = normalizeTaskWorktreeBranch(in.WorktreeBranchMode, in.WorktreeBranch)
		task.AuxFlags.SessionError = ""
		task.UpdatedAt = now
		run.Input = input
		event := TaskEvent{
			ID:         newID("event"),
			TaskID:     task.ID,
			StageRunID: run.ID,
			Type:       "stage_input_updated",
			Payload: eventPayload(map[string]any{
				"input":                input,
				"create_worktree":      *in.CreateWorktree,
				"worktree_branch_mode": task.WorktreeBranchMode,
				"worktree_branch":      task.WorktreeBranch,
			}),
			CreatedAt: now,
		}
		if err := store.UpdateTaskAndStageRun(ctx, task, run, event); err != nil {
			return TaskDetail{}, err
		}
		detail, err := store.GetDetail(ctx, task.ID)
		if err == nil && s.Runner != nil {
			s.Runner.TaskUpdated(task.RootID, detail)
		}
		return detail, err
	}
	if err := store.UpdateStageRunInput(ctx, run.ID, input); err != nil {
		return TaskDetail{}, err
	}
	if strings.TrimSpace(task.AuxFlags.SessionError) != "" {
		empty := ""
		_ = store.UpdateTaskAuxFlags(ctx, task.ID, TaskAuxFlagsPatch{SessionError: &empty})
	}
	_ = store.AddEvent(ctx, TaskEvent{
		ID:         newID("event"),
		TaskID:     task.ID,
		StageRunID: run.ID,
		Type:       "stage_input_updated",
		Payload:    eventPayload(payload),
		CreatedAt:  time.Now().UTC(),
	})
	detail, err := store.GetDetail(ctx, task.ID)
	if err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(task.RootID, detail)
	}
	return detail, err
}

func (s *Service) UpdateFirstInput(ctx context.Context, in UpdateTaskInput) (TaskDetail, error) {
	return s.UpdateCurrentInput(ctx, in)
}
