package kanban

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type scanner interface{ Scan(dest ...any) error }

// scanTask 读 taskSelectColumns 这一整行。
//
// 其中 template_snapshot_json / current_stage_index / task_stages_json / task_type
// 是**冻结列**：阶段概念已经不存在（2026-11 决策），新代码不再读写它们，但列还在
// 老库里、也还在 taskSelectColumns 里，所以扫描时收进丢弃变量，保持列数与顺序一致。
// 历史阶段任务的内容由启动时的一次性转换（task_store_convert_stages.go）读取。
func scanTask(row scanner) (Task, error) {
	var task Task
	var createWorktree, askUserWaiting, hasPlan, hasTodos, hasTask int
	var worktreeBuilt int
	var sessionError string
	var labels string
	var created, updated string
	var frozenSnapshot, frozenStageIndex, frozenStages, frozenTaskType string
	if err := row.Scan(&task.ID, &task.TaskNumber, &task.RootID, &task.TaskTemplateID, &task.TaskTemplateName, &frozenSnapshot, &createWorktree, &task.WorktreeBranchMode, &task.WorktreeBranch, &frozenStageIndex, &task.Status, &task.MainSessionKey, &task.ResultSummary, &task.WorktreeRootID, &task.WorktreePath, &worktreeBuilt, &askUserWaiting, &hasPlan, &hasTodos, &hasTask, &sessionError, &labels, &created, &updated, &task.CompletedAt, &task.Name, &frozenStages, &frozenTaskType, &task.ArchivedAt); err != nil {
		return Task{}, err
	}
	task.CreateWorktree = createWorktree != 0
	// 存量回填之外还有一层兜底：老库里 worktree_built 可能还是 0，但路径非空说明
	// 树确实在。按「有路径就算建过」算，界面才不会把一棵活着的树说成没建过。
	task.WorktreeBuilt = worktreeBuilt != 0 || strings.TrimSpace(task.WorktreePath) != ""
	task.AuxFlags = TaskAuxFlags{
		AskUserWaiting: askUserWaiting != 0,
		HasPlan:        hasPlan != 0,
		HasTodos:       hasTodos != 0,
		HasTask:        hasTask != 0,
		SessionError:   strings.TrimSpace(sessionError),
	}
	_ = json.Unmarshal([]byte(labels), &task.Labels)
	if task.Labels == nil {
		task.Labels = []string{}
	}
	task.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	task.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return task, nil
}

func scanTaskEvent(row scanner) (TaskEvent, error) {
	var event TaskEvent
	var stageRunID string
	var created string
	if err := row.Scan(&event.ID, &event.TaskID, &stageRunID, &event.Type, &event.Payload, &created); err != nil {
		return TaskEvent{}, err
	}
	event.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return event, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func isTerminalStatus(status string) bool {
	switch status {
	case StatusSuccess, StatusFail, StatusCancelled:
		return true
	default:
		return false
	}
}

func isNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
