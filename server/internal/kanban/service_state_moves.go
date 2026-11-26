package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

func (s *Service) UpdateTaskAuxFlags(ctx context.Context, rootID, taskID string, patch TaskAuxFlagsPatch, eventType string) (TaskDetail, error) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return TaskDetail{}, err
	}
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		return TaskDetail{}, err
	}
	if isTerminalStatus(task.Status) && patch.AskUserWaiting != nil && *patch.AskUserWaiting {
		return store.GetDetail(ctx, task.ID)
	}
	if err := store.UpdateTaskAuxFlags(ctx, task.ID, patch); err != nil {
		return TaskDetail{}, err
	}
	if strings.TrimSpace(eventType) != "" {
		_ = store.AddEvent(ctx, TaskEvent{
			ID:        newID("event"),
			TaskID:    task.ID,
			Type:      strings.TrimSpace(eventType),
			Payload:   eventPayload(map[string]any{"aux_flags": patchPayload(patch)}),
			CreatedAt: time.Now().UTC(),
		})
	}
	detail, err := store.GetDetail(ctx, task.ID)
	if err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(rootID, detail)
	}
	return detail, err
}

// NotifyTaskCardChanged 触发一次 task.updated，让看板重拉这张卡。
//
// 提问卡不写任务行时也需要它：router ask 生命周期只广播活动事件，而看板只认
// task.updated —— 卡上的「等你回答」徽标因此要等下一次别的事件才出现/消失。
// 读取失败不报错：下一次整包刷新会兜底。
func (s *Service) NotifyTaskCardChanged(ctx context.Context, rootID, taskID string) {
	if s == nil {
		return
	}
	rootID = strings.TrimSpace(rootID)
	taskID = strings.TrimSpace(taskID)
	if rootID == "" || taskID == "" {
		return
	}
	store, err := s.taskStore(rootID)
	if err != nil {
		return
	}
	// 调用点常在回合收尾路径上，上一层的 ctx 可能已经取消；通知本身很短，
	// 用脱离取消的 ctx + 超时，避免「卡已经落库、看板却没收到」。
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	detail, err := store.GetDetail(ctx, taskID)
	if err != nil || s.Runner == nil {
		return
	}
	s.Runner.TaskUpdated(rootID, detail)
}

func patchPayload(patch TaskAuxFlagsPatch) map[string]any {
	out := map[string]any{}
	if patch.AskUserWaiting != nil {
		out["ask_user_waiting"] = *patch.AskUserWaiting
	}
	if patch.HasPlan != nil {
		out["has_plan"] = *patch.HasPlan
	}
	if patch.HasTodos != nil {
		out["has_todos"] = *patch.HasTodos
	}
	if patch.HasTask != nil {
		out["has_task"] = *patch.HasTask
	}
	if patch.SessionError != nil {
		out["session_error"] = strings.TrimSpace(*patch.SessionError)
	}
	return out
}

// setTaskStatus 改任务状态（Pause/Resume/Cancel/Fail 共用）。
// 终态任务不再接受任何状态改写：已归档的任务不能被 Resume/Pause 复活。
func (s *Service) setTaskStatus(ctx context.Context, rootID, taskID, status, eventType, reason string, terminal bool) (TaskDetail, error) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return TaskDetail{}, err
	}
	if current, getErr := store.GetTask(ctx, taskID); getErr == nil && isTerminalStatus(current.Status) {
		// 状态没变（已经是终态了），刻意不播：推一条与现状相同的 task.updated
		// 只会让每个客户端白做一次重渲染。
		return store.GetDetail(ctx, current.ID)
	}
	if err := store.UpdateTaskStatus(ctx, taskID, status, terminal); err != nil {
		return TaskDetail{}, err
	}
	_ = store.AddEvent(ctx, TaskEvent{
		ID:        newID("event"),
		TaskID:    strings.TrimSpace(taskID),
		Type:      eventType,
		Payload:   eventPayload(map[string]any{"reason": strings.TrimSpace(reason)}),
		CreatedAt: time.Now().UTC(),
	})
	detail, err := store.GetDetail(ctx, taskID)
	// Pause/Resume/Cancel/Fail 的界面反馈全靠这条推送。以前不发、只由 HTTP handler
	// 补，于是任何非 HTTP 调用方推不出去，别的客户端看到的还是暂停前的状态。
	if err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(rootID, detail)
	}
	// 终态是「人不在电脑前也需要知道」的那一类消息；waiting_user 是正常轮次，
	// 每轮都推等于每轮都吵人。已经在终态的任务在上面的守卫处就返回了，
	// 所以这里每个任务每个终态只推一次。
	if terminal && err == nil {
		s.notifyCard(ctx, CardNotice{
			ID:        "notice-" + detail.Task.ID + "-" + status + "-" + strconv.FormatInt(time.Now().UnixNano(), 10),
			Kind:      CardKindNotice,
			RootID:    detail.Task.RootID,
			TaskID:    detail.Task.ID,
			TaskTitle: taskTitle(detail.Task), TaskNumber: detail.Task.TaskNumber,
			Status: status, Summary: strings.TrimSpace(reason),
		})
		s.maybeAutoFinishWorktree(ctx, rootID, taskID)
	}
	return detail, err
}

// recordTaskError 把一条错误写到任务的 aux_flags.session_error 上并广播。
func (s *Service) recordTaskError(ctx context.Context, store *TaskStore, task Task, message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}
	current, err := store.GetTask(ctx, task.ID)
	if err != nil {
		return err
	}
	current.AuxFlags.SessionError = message
	current.UpdatedAt = time.Now().UTC()
	if err := store.UpdateTask(ctx, current); err != nil {
		return err
	}
	_ = store.AddEvent(ctx, TaskEvent{
		ID:        newID("event"),
		TaskID:    current.ID,
		Type:      "task_error",
		Payload:   eventPayload(map[string]any{"message": message}),
		CreatedAt: time.Now().UTC(),
	})
	if detail, err := store.GetDetail(ctx, current.ID); err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(current.RootID, detail)
	}
	return nil
}

func (s *Service) loadForMove(ctx context.Context, rootID, taskID string) (*TaskStore, Task, error) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return nil, Task{}, err
	}
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		return nil, Task{}, err
	}
	return store, task, nil
}

func (s *Service) taskStore(rootID string) (*TaskStore, error) {
	rootID = strings.TrimSpace(rootID)
	if rootID == "" {
		return nil, errors.New("root_id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stores == nil {
		s.stores = map[string]*TaskStore{}
	}
	if store := s.stores[rootID]; store != nil {
		return store, nil
	}
	if s.Roots == nil {
		return nil, errors.New("root provider not configured")
	}
	root, err := s.Roots.GetRoot(rootID)
	if err != nil {
		return nil, err
	}
	store, err := NewTaskStore(root)
	if err != nil {
		return nil, err
	}
	s.stores[rootID] = store
	return store, nil
}

func eventPayload(value map[string]any) string {
	payload, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(payload)
}

// RenderPrompt 替换提示词里的 {key} 占位符。空值刻意保留原样还是抹成空串？
// 这里按「有值才替换」处理：调用方只传真正有值的键，缺键说明这一段没有那个
// 上下文，留在原文里更容易被发现（与 workflow 节点解析的历史行为一致）。
func RenderPrompt(template string, values map[string]string) string {
	out := template
	for key, value := range values {
		out = strings.ReplaceAll(out, "{"+key+"}", value)
	}
	return out
}
