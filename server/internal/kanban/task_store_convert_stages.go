package kanban

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 历史阶段任务 → 对话 + WorkflowRun 的一次性转换。
//
// 转换是**单向**的：转换后任务不再有 stages，legacy 引擎也不会再推进它。
// 旧行先整行复制到 *_backup_legacy* 表，再改 tasks。回滚只能从备份恢复，
// 不支持把 conversation 任务反向拆回 stage_runs。
//
// 关键设计：
//   - Run 带 TriggeredBy=legacy_import。启动对账会跳过它（这些节点早已结束，
//     重放会把历史任务重新跑一遍）；创建新 Run 时也允许替换它（否则一个
//     waiting_user 的历史任务会永久挡住用户的新留言）。
//   - ID 全部确定（act-legacy-*, run-legacy-*, nr-legacy-*），重复执行是幂等的；
//     已经有 act-legacy-user-<taskID> 的任务直接跳过。
//   - 图快照内联：即使控制面没有对应 workflow 版本，运行视图也能完整渲染。
//   - stage_runs 原始行不删，作为只读审计证据保留；tasks.task_stages_json 清空。

const (
	legacyUserActivityPrefix  = "act-legacy-user-"
	legacyReplyActivityPrefix = "act-legacy-reply-"
	legacyRunPrefix           = "run-legacy-"
	legacyNodeRunPrefix       = "nr-legacy-"

	legacyImportLogName = "stages-to-conversation.migration.log"
)

// 历史阶段数据只在这一次性迁移里被读取。类型刻意留成包内私有：线上模型
// （Task / Runner / API / 前端）已经没有阶段概念，这里只是旧表列的读取视图。
// 迁移跑完之后，除备份表外不再有任何代码路径认识这些形状。
type legacyStageTemplate struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Role        string `json:"role"`
	AutoAdvance bool   `json:"auto_advance"`
	Agent       string `json:"agent,omitempty"`
	Model       string `json:"model,omitempty"`
	// SessionReusePolicy / Kind 不参与转换，保留是为了 JSON 反序列化兼容。
	SessionReusePolicy string `json:"session_reuse_policy,omitempty"`
	PromptTemplate     string `json:"prompt_template,omitempty"`
	Kind               string `json:"kind,omitempty"`
}

type legacyStageRun struct {
	ID             string
	TaskID         string
	StageIndex     int
	StageName      string
	Role           string
	Status         string
	SessionKey     string
	Input          string
	RenderedPrompt string
	StartedAt      string
	FinishedAt     string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// legacyTaskRow 是迁移读取的任务行：Task 的标准字段 + 两个冻结的旧列。
type legacyTaskRow struct {
	Task
	Stages   []legacyStageTemplate
	TaskType string
}

const (
	legacyTaskTypeStage        = "stage"
	legacyTaskTypeConversation = "conversation"

	legacyStageStatusPending     = "pending"
	legacyStageStatusRunning     = "running"
	legacyStageStatusWaitingUser = "waiting_user"
	legacyStageStatusSuccess     = "success"
	legacyStageStatusFail        = "fail"
	legacyStageStatusCancelled   = "cancelled"
	legacyStageStatusApproved    = "approved"
	legacyStageStatusRejected    = "rejected"
)

// StageConversionOptions 控制转换行为。生产的 migrate() 用零值；独立脚本用它
// 做 --dry-run 与显式日志输出。
type StageConversionOptions struct {
	DryRun  bool
	Now     time.Time
	LogPath string
	// SkipLogFile 在单测/临时库里不落盘，避免每个 t.TempDir 都留一个日志。
	SkipLogFile bool
	Logf        func(format string, args ...any)
}

// StageConversionStats 是可核对的转换结果。脚本把这些数字直接打给用户。
type StageConversionStats struct {
	TasksSeen            int `json:"tasks_seen"`
	TasksConverted       int `json:"tasks_converted"`
	TasksSkipped         int `json:"tasks_skipped"`
	AlreadyConverted     int `json:"already_converted"`
	ActivitiesCreated    int `json:"activities_created"`
	RunsCreated          int `json:"runs_created"`
	NodeRunsCreated      int `json:"node_runs_created"`
	TasksBackedUp        int `json:"tasks_backed_up"`
	StageRunsBackedUp    int `json:"stage_runs_backed_up"`
	TasksWithoutStages   int `json:"tasks_without_stages"`
	TasksWithRunningNode int `json:"tasks_with_running_node"`
}

// ConvertStagesToConversation 扫描 tasks，把 task_type='stage' 的任务转成
// conversation + 内联 WorkflowRun。db 必须是任务节点的 SQLite（每项目一份）。
func ConvertStagesToConversation(ctx context.Context, db *sql.DB, opts StageConversionOptions) (StageConversionStats, error) {
	stats := StageConversionStats{}
	if db == nil {
		return stats, errors.New("task database required")
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	}
	if opts.Logf == nil {
		opts.Logf = func(string, ...any) {}
	}
	hasTaskType, err := tableHasColumn(db, "tasks", "task_type")
	if err != nil {
		return stats, err
	}
	if !hasTaskType && !opts.DryRun {
		if _, err := db.Exec(`ALTER TABLE tasks ADD COLUMN task_type TEXT NOT NULL DEFAULT '` + legacyTaskTypeStage + `'`); err != nil &&
			!strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return stats, err
		}
		hasTaskType = true
	}

	query := `SELECT ` + legacyTaskSelectColumns + ` FROM tasks ORDER BY created_at ASC, id ASC`
	if !hasTaskType {
		// dry-run 在未 ALTER 的库上也能预演：把缺失列当成空串，下面按 stage 处理。
		query = `SELECT ` + strings.Replace(legacyTaskSelectColumns, ", task_type", ", ''", 1) + ` FROM tasks ORDER BY created_at ASC, id ASC`
	}
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return stats, err
	}
	var tasks []legacyTaskRow
	for rows.Next() {
		task, scanErr := scanLegacyTaskRow(rows)
		if scanErr != nil {
			rows.Close()
			return stats, scanErr
		}
		tasks = append(tasks, task)
	}
	if err := rows.Close(); err != nil {
		return stats, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return stats, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS tasks_backup_legacy_stages AS SELECT * FROM tasks WHERE 0`); err != nil {
		return stats, err
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS stage_runs_backup_legacy AS SELECT * FROM stage_runs WHERE 0`); err != nil {
		return stats, err
	}

	for _, task := range tasks {
		stats.TasksSeen++
		taskType := strings.TrimSpace(task.TaskType)
		if taskType == legacyTaskTypeConversation {
			stats.TasksSkipped++
			continue
		}
		if taskType != "" && taskType != legacyTaskTypeStage {
			stats.TasksSkipped++
			continue
		}
		already, err := activityExistsTx(ctx, tx, task.ID, legacyUserActivityPrefix+task.ID)
		if err != nil {
			return stats, err
		}
		if already {
			stats.AlreadyConverted++
			stats.TasksSkipped++
			continue
		}
		engine, _, err := taskEngineTx(ctx, tx, task.ID)
		if err != nil {
			return stats, err
		}
		if engine == EngineWorkflow {
			// 已经显式迁移过（或上一次转换在提交前留下了绑定）——不覆盖。
			stats.TasksSkipped++
			continue
		}
		runs, err := listStageRunsTx(ctx, tx, task.ID)
		if err != nil {
			return stats, err
		}
		if len(task.Stages) == 0 && len(runs) == 0 {
			// 没有可映射的流水：只改模型标记，不造空 Run。
			stats.TasksWithoutStages++
			if err := markTaskConversationTx(ctx, tx, task.ID); err != nil {
				return stats, err
			}
			stats.TasksConverted++
			continue
		}
		conv, err := buildLegacyConversion(task, runs)
		if err != nil {
			return stats, fmt.Errorf("task %s: %w", task.ID, err)
		}
		if err := backupLegacyRowsTx(ctx, tx, task.ID); err != nil {
			return stats, err
		}
		stats.TasksBackedUp++
		stats.StageRunsBackedUp += len(runs)

		if err := insertLegacyActivitiesTx(ctx, tx, conv); err != nil {
			return stats, err
		}
		stats.ActivitiesCreated += 2

		if conv.Run != nil {
			if err := insertLegacyRunTx(ctx, tx, conv); err != nil {
				return stats, err
			}
			stats.RunsCreated++
			stats.NodeRunsCreated += len(conv.NodeRuns)
			if conv.HasRunningNode {
				stats.TasksWithRunningNode++
			}
		}
		if err := upsertLegacyEngineTx(ctx, tx, task.ID, conv.Run, opts.Now); err != nil {
			return stats, err
		}
		if err := markTaskConversationTx(ctx, tx, task.ID); err != nil {
			return stats, err
		}
		stats.TasksConverted++
	}

	if opts.DryRun {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			return stats, err
		}
		stats.log(opts, db, true)
		return stats, nil
	}
	if err := tx.Commit(); err != nil {
		return stats, err
	}
	stats.log(opts, db, false)
	return stats, nil
}

func (s StageConversionStats) log(opts StageConversionOptions, db *sql.DB, dryRun bool) {
	if s.TasksConverted == 0 && s.AlreadyConverted == 0 && s.TasksSkipped == 0 {
		return
	}
	prefix := "convert"
	if dryRun {
		prefix = "dry-run"
	}
	line := fmt.Sprintf("%s at %s: seen=%d converted=%d skipped=%d already=%d activities=%d runs=%d node_runs=%d backed_up_tasks=%d backed_up_stage_runs=%d no_stages=%d running_nodes=%d",
		prefix, time.Now().UTC().Format(time.RFC3339), s.TasksSeen, s.TasksConverted, s.TasksSkipped,
		s.AlreadyConverted, s.ActivitiesCreated, s.RunsCreated, s.NodeRunsCreated,
		s.TasksBackedUp, s.StageRunsBackedUp, s.TasksWithoutStages, s.TasksWithRunningNode)
	opts.Logf("%s", line)
	if opts.SkipLogFile {
		return
	}
	path := strings.TrimSpace(opts.LogPath)
	if path == "" {
		path = defaultConversionLogPath(db)
	}
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		opts.Logf("migration log skipped: %v", err)
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		opts.Logf("migration log skipped: %v", err)
		return
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		opts.Logf("migration log write skipped: %v", err)
	}
}

func defaultConversionLogPath(db *sql.DB) string {
	var seq int
	var name, file string
	if err := db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &file); err != nil {
		return ""
	}
	if strings.TrimSpace(file) == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(file), legacyImportLogName)
}

func tableHasColumn(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func activityExistsTx(ctx context.Context, tx *sql.Tx, taskID, activityID string) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM task_activities WHERE task_id=? AND id=? LIMIT 1`, taskID, activityID).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func taskEngineTx(ctx context.Context, tx *sql.Tx, taskID string) (string, string, error) {
	var engine, runID string
	err := tx.QueryRowContext(ctx, `SELECT engine, workflow_run_id FROM task_execution_engines WHERE task_id=?`, taskID).Scan(&engine, &runID)
	if errors.Is(err, sql.ErrNoRows) {
		return EngineConversation, "", nil
	}
	return engine, runID, err
}

// legacyTaskSelectColumns / scanLegacyTaskRow 是迁移专用的读取路径：
// 线上模型不再有 task_stages_json / task_type / current_stage_index，
// 但旧库的行还在，迁移必须能读到它们。
const legacyTaskSelectColumns = "id, task_number, root_id, task_template_id, task_template_name, create_worktree, worktree_branch_mode, worktree_branch, status, main_session_key, worktree_root_id, worktree_path, worktree_built, aux_ask_user_waiting, aux_has_plan, aux_has_todos, aux_has_task, aux_session_error, labels_json, created_at, updated_at, completed_at, name, task_stages_json, task_type"

func scanLegacyTaskRow(row interface{ Scan(dest ...any) error }) (legacyTaskRow, error) {
	var out legacyTaskRow
	var createWorktree, askUserWaiting, hasPlan, hasTodos, hasTask, worktreeBuilt int
	var sessionError, labels, stagesJSON, created, updated string
	if err := row.Scan(
		&out.ID, &out.TaskNumber, &out.RootID, &out.TaskTemplateID, &out.TaskTemplateName,
		&createWorktree, &out.WorktreeBranchMode, &out.WorktreeBranch, &out.Status, &out.MainSessionKey,
		&out.WorktreeRootID, &out.WorktreePath, &worktreeBuilt,
		&askUserWaiting, &hasPlan, &hasTodos, &hasTask, &sessionError,
		&labels, &created, &updated, &out.CompletedAt, &out.Name, &stagesJSON, &out.TaskType,
	); err != nil {
		return legacyTaskRow{}, err
	}
	out.CreateWorktree = createWorktree != 0
	out.WorktreeBuilt = worktreeBuilt != 0 || strings.TrimSpace(out.WorktreePath) != ""
	out.AuxFlags = TaskAuxFlags{
		AskUserWaiting: askUserWaiting != 0,
		HasPlan:        hasPlan != 0,
		HasTodos:       hasTodos != 0,
		HasTask:        hasTask != 0,
		SessionError:   strings.TrimSpace(sessionError),
	}
	_ = json.Unmarshal([]byte(labels), &out.Labels)
	if strings.TrimSpace(stagesJSON) != "" && stagesJSON != "[]" {
		_ = json.Unmarshal([]byte(stagesJSON), &out.Stages)
	}
	out.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	out.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return out, nil
}

func scanLegacyStageRun(row interface{ Scan(dest ...any) error }) (legacyStageRun, error) {
	var run legacyStageRun
	var created, updated string
	if err := row.Scan(&run.ID, &run.TaskID, &run.StageIndex, &run.StageName, &run.Role, &run.Status, &run.SessionKey, &run.Input, &run.RenderedPrompt, &run.StartedAt, &run.FinishedAt, &created, &updated); err != nil {
		return legacyStageRun{}, err
	}
	run.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	run.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return run, nil
}

func listStageRunsTx(ctx context.Context, tx *sql.Tx, taskID string) ([]legacyStageRun, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, task_id, stage_index, stage_name, role, status, session_key, input, rendered_prompt, started_at, finished_at, created_at, updated_at FROM stage_runs WHERE task_id=? ORDER BY stage_index ASC, created_at ASC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []legacyStageRun
	for rows.Next() {
		run, err := scanLegacyStageRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

func backupLegacyRowsTx(ctx context.Context, tx *sql.Tx, taskID string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM tasks_backup_legacy_stages WHERE id=?`, taskID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO tasks_backup_legacy_stages SELECT * FROM tasks WHERE id=?`, taskID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM stage_runs_backup_legacy WHERE task_id=?`, taskID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO stage_runs_backup_legacy SELECT * FROM stage_runs WHERE task_id=?`, taskID)
	return err
}

// markTaskConversationTx 只改模型标记，不动 updated_at：这一列是「最后一次用户
// 可见变更」的投影，迁移是后台数据整形，不该让所有历史任务在同一秒跳到列表顶部。
func markTaskConversationTx(ctx context.Context, tx *sql.Tx, taskID string) error {
	_, err := tx.ExecContext(ctx, `UPDATE tasks SET task_stages_json='[]', task_type=?, current_stage_index=0 WHERE id=?`,
		legacyTaskTypeConversation, taskID)
	return err
}

func upsertLegacyEngineTx(ctx context.Context, tx *sql.Tx, taskID string, run *WorkflowRun, now time.Time) error {
	if run == nil {
		_, err := tx.ExecContext(ctx, `DELETE FROM task_execution_engines WHERE task_id=?`, taskID)
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO task_execution_engines(task_id,engine,workflow_run_id,updated_at) VALUES(?,?,?,?)
	 ON CONFLICT(task_id) DO UPDATE SET engine=excluded.engine, workflow_run_id=excluded.workflow_run_id, updated_at=excluded.updated_at`,
		taskID, EngineWorkflow, run.ID, now.UTC().Format(time.RFC3339Nano))
	return err
}
