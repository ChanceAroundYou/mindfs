package kanban

import (
	"context"
	"encoding/json"
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
