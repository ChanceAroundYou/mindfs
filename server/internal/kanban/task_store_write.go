package kanban

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// setWorktreeRefsKeepBuilt 清归属但保留 worktree_built。
//
// 之所以不直接复用 setWorktreeRefs：那个方法的 built 参数是给**建树**用的，
// 清这一路必须原样保留旧值，否则清一次就把「建过」这个事实抹了。
func (s *TaskStore) setWorktreeRefsKeepBuilt(ctx context.Context, taskID, rootID, path string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET worktree_root_id = ?, worktree_path = ?, updated_at = ? WHERE id = ?`,
		rootID, path, s.now().UTC().Format(time.RFC3339Nano), taskID)
	return err
}

func (s *TaskStore) UpdateTaskAndStageRun(ctx context.Context, task Task, run StageRun, event TaskEvent) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := updateTaskCore(ctx, tx, task); err != nil {
		return err
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	_, err = tx.ExecContext(ctx, `UPDATE stage_runs SET status = ?, session_key = ?, input = ?, rendered_prompt = ?, started_at = ?, finished_at = ?, updated_at = ? WHERE id = ?`,
		run.Status, run.SessionKey, run.Input, run.RenderedPrompt, run.StartedAt, run.FinishedAt, now, run.ID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(event.ID) != "" {
		if err := insertTaskEvent(ctx, tx, event); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func insertTask(ctx context.Context, tx *sql.Tx, task Task) error {
	labels, _ := json.Marshal(task.Labels)
	stages, _ := json.Marshal(task.Stages)
	_, err := tx.ExecContext(ctx, `INSERT INTO tasks (id, task_number, root_id, task_template_id, task_template_name, template_snapshot_json, create_worktree, worktree_branch_mode, worktree_branch, current_stage_index, status, main_session_key, worktree_root_id, worktree_path, worktree_built, aux_ask_user_waiting, aux_has_plan, aux_has_todos, aux_has_task, aux_session_error, labels_json, created_at, updated_at, completed_at, name, task_stages_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		task.ID, task.TaskNumber, task.RootID, task.TaskTemplateID, task.TaskTemplateName, "", boolInt(task.CreateWorktree), task.WorktreeBranchMode, task.WorktreeBranch, task.CurrentStageIndex, task.Status, task.MainSessionKey, task.WorktreeRootID, task.WorktreePath, boolInt(task.WorktreeBuilt), boolInt(task.AuxFlags.AskUserWaiting), boolInt(task.AuxFlags.HasPlan), boolInt(task.AuxFlags.HasTodos), boolInt(task.AuxFlags.HasTask), strings.TrimSpace(task.AuxFlags.SessionError), string(labels), task.CreatedAt.UTC().Format(time.RFC3339Nano), task.UpdatedAt.UTC().Format(time.RFC3339Nano), task.CompletedAt, task.Name, string(stages))
	return err
}

func updateTaskCore(ctx context.Context, tx *sql.Tx, task Task) error {
	labels, _ := json.Marshal(task.Labels)
	stages, _ := json.Marshal(task.Stages)
	// 刻意**不含** worktree_root_id / worktree_path：这两个列只有一个写入点
	// （Service.ensureTaskWorktree，见 setWorktreeRefs）。留在整行 UPDATE 里，
	// 任何拿着旧快照的后台写入（executeTask 是异步的，跑完一整段才写回）都会把
	// 已清空的归属又写回去 —— 症状是「清完 worktree，路径又回来了」，且只在后台
	// 流程还没结束时出现。归属是「谁建的树」这种一次性事实，不该跟着每次状态更新漂。
	_, err := tx.ExecContext(ctx, `UPDATE tasks SET create_worktree = ?, worktree_branch_mode = ?, worktree_branch = ?, current_stage_index = ?, status = ?, main_session_key = ?, aux_ask_user_waiting = ?, aux_has_plan = ?, aux_has_todos = ?, aux_has_task = ?, aux_session_error = ?, labels_json = ?, updated_at = ?, completed_at = ?, name = ?, task_stages_json = ? WHERE id = ?`,
		boolInt(task.CreateWorktree), task.WorktreeBranchMode, task.WorktreeBranch, task.CurrentStageIndex, task.Status, task.MainSessionKey, boolInt(task.AuxFlags.AskUserWaiting), boolInt(task.AuxFlags.HasPlan), boolInt(task.AuxFlags.HasTodos), boolInt(task.AuxFlags.HasTask), strings.TrimSpace(task.AuxFlags.SessionError), string(labels), task.UpdatedAt.UTC().Format(time.RFC3339Nano), task.CompletedAt, task.Name, string(stages), task.ID)
	return err
}

// setWorktreeRefs 写/清任务的 worktree 归属。建树与清归属共用一个出口，
// 这样「这两个列归谁管」在 schema 旁边就能看全，不用去数整行 UPDATE 里的字段。
//
// built=true 表示「这棵树确实建出来了」，与 path 是否为空无关：清归属只清路径，
// 不清这个事实 —— 前端要靠它区分「还没建」（首段是 user 段）和「建过、记录丢了」
// （目录可能还在被人用）。2026-10-01 加，后两者在界面上被混成同一个「已收尾」。
func (s *TaskStore) setWorktreeRefs(ctx context.Context, taskID, rootID, path string, built bool) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET worktree_root_id = ?, worktree_path = ?, worktree_built = ?, updated_at = ? WHERE id = ?`,
		rootID, path, boolInt(built), s.now().UTC().Format(time.RFC3339Nano), taskID)
	return err
}

// SetWorktreeRefsAndTask 在**一个事务**里写归属 + 更新任务其余字段。
//
// 建树时两者必须一起生效：只有归属没有状态更新会留下「路径可用但 session_error
// 还挂着」的中间态，而分开两次写中间撞上读就是那种状态。分开两个方法是为了让
// ClearWorktreeRefs（只清归属、不碰别的）也能走同一个出口。
func (s *TaskStore) SetWorktreeRefsAndTask(ctx context.Context, task Task) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := updateTaskCore(ctx, tx, task); err != nil {
		return err
	}
	// 建树这一路：path 非空即视为已建出来。
	if _, err := tx.ExecContext(ctx,
		`UPDATE tasks SET worktree_root_id = ?, worktree_path = ?, worktree_built = ? WHERE id = ?`,
		task.WorktreeRootID, task.WorktreePath, boolInt(task.WorktreeBuilt || strings.TrimSpace(task.WorktreePath) != ""), task.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func insertStageRun(ctx context.Context, tx *sql.Tx, run StageRun) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO stage_runs (id, task_id, stage_index, stage_name, role, status, session_key, input, rendered_prompt, started_at, finished_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.TaskID, run.StageIndex, run.StageName, run.Role, run.Status, run.SessionKey, run.Input, run.RenderedPrompt, run.StartedAt, run.FinishedAt, run.CreatedAt.UTC().Format(time.RFC3339Nano), run.UpdatedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func insertTaskEvent(ctx context.Context, tx *sql.Tx, event TaskEvent) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO task_events (id, task_id, stage_run_id, type, payload_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`, event.ID, event.TaskID, event.StageRunID, event.Type, event.Payload, event.CreatedAt.UTC().Format(time.RFC3339Nano))
	return err
}
