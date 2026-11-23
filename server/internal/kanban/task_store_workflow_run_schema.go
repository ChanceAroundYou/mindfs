package kanban

import (
	"database/sql"
	"errors"
	"strings"
)

// workflow_runs / workflow_node_runs 的建表、补列与历史重建。2026-11-23 从
// task_store_workflow_runs.go 纯移动拆出（该文件越过 500 行硬线）。这里只放
// 「表长什么样、老库怎么补齐」，读写与冻结守卫仍在原文件。
// 规则：新增列要同时改这里的建表 DDL、DropTaskUnique 的重建语句、原文件的
// ALTER 列表与 scan/INSERT 三处，缺一处老库就会被静默丢列。

// migrateWorkflowRuns creates runtime snapshots and NodeRun attempts in the task-node SQLite DB.
// D14：一个任务可以有多个历史 Run，但至多一个非终态 Run。SQLite 不能原地去掉
// UNIQUE(task_id)，所以先按 sqlite_master 的 DDL 判断旧表，再做标准表重建。
func (s *TaskStore) migrateWorkflowRuns() error {
	if err := s.migrateWorkflowRunsDropTaskUnique(); err != nil {
		return err
	}
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS workflow_runs (
 id TEXT PRIMARY KEY, task_id TEXT NOT NULL, version TEXT NOT NULL DEFAULT '',
 revision INTEGER NOT NULL DEFAULT 1, graph_json TEXT NOT NULL,
 node_states_json TEXT NOT NULL DEFAULT '{}', node_outputs_json TEXT NOT NULL DEFAULT '{}',
 selected_routes_json TEXT NOT NULL DEFAULT '{}', loop_state_json TEXT NOT NULL DEFAULT '{}',
 loop_exit_expr_json TEXT NOT NULL DEFAULT '{}', created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 integration_branch TEXT NOT NULL DEFAULT '',
 triggered_by TEXT NOT NULL DEFAULT '', activity_id TEXT NOT NULL DEFAULT '',
 session_key TEXT NOT NULL DEFAULT '', holds_used INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_workflow_runs_task ON workflow_runs(task_id);
CREATE INDEX IF NOT EXISTS idx_workflow_runs_task_created ON workflow_runs(task_id,created_at DESC);
CREATE TABLE IF NOT EXISTS workflow_node_runs (
 id TEXT PRIMARY KEY, run_id TEXT NOT NULL, node_id TEXT NOT NULL, loop_path TEXT NOT NULL DEFAULT '',
 iteration INTEGER NOT NULL DEFAULT 0, attempt INTEGER NOT NULL DEFAULT 1,
 agent TEXT NOT NULL DEFAULT '', harness TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '',
 session_key TEXT NOT NULL DEFAULT '', status TEXT NOT NULL, input_json TEXT NOT NULL DEFAULT '{}',
 output_json TEXT NOT NULL DEFAULT '{}', started_at TEXT NOT NULL DEFAULT '', finished_at TEXT NOT NULL DEFAULT '',
 prev_attempt_id TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 worktree_id TEXT NOT NULL DEFAULT '', worktree_path TEXT NOT NULL DEFAULT '', worktree_branch TEXT NOT NULL DEFAULT '',
 not_before TEXT NOT NULL DEFAULT '', last_output_at TEXT NOT NULL DEFAULT '',
 UNIQUE(run_id,node_id,loop_path,iteration,attempt)
);
CREATE INDEX IF NOT EXISTS idx_workflow_node_runs_run ON workflow_node_runs(run_id,created_at);
`)
	if err != nil {
		return err
	}
	// 存量库补列：CREATE TABLE IF NOT EXISTS 不会给已存在的表加列。顺序固定、
	// 逐列判断，重复启动幂等。列名是常量，不接受外部输入。
	for _, col := range []struct{ table, name, ddl string }{
		{"workflow_runs", "integration_branch", "TEXT NOT NULL DEFAULT ''"},
		{"workflow_runs", "triggered_by", "TEXT NOT NULL DEFAULT ''"},
		{"workflow_runs", "activity_id", "TEXT NOT NULL DEFAULT ''"},
		{"workflow_runs", "session_key", "TEXT NOT NULL DEFAULT ''"},
		{"workflow_runs", "holds_used", "INTEGER NOT NULL DEFAULT 0"},
		{"workflow_node_runs", "worktree_id", "TEXT NOT NULL DEFAULT ''"},
		{"workflow_node_runs", "worktree_path", "TEXT NOT NULL DEFAULT ''"},
		{"workflow_node_runs", "worktree_branch", "TEXT NOT NULL DEFAULT ''"},
		{"workflow_node_runs", "not_before", "TEXT NOT NULL DEFAULT ''"},
		// 输出水位（W3-1，见 turn_watermark.go）：与 task_activities.last_output_at 同源。
		{"workflow_node_runs", "last_output_at", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := s.ensureTableColumn(col.table, col.name, col.ddl); err != nil {
			return err
		}
	}
	return nil
}

// ensureTableColumn adds a column when the table predates it. Table/column names
// are internal constants; never call this with request-derived strings.
func (s *TaskStore) ensureTableColumn(table, column, ddl string) error {
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == column {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if found {
		return nil
	}
	_, err = s.db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` ` + ddl)
	return err
}

// migrateWorkflowRunsDropTaskUnique rebuilds workflow_runs when it still carries
// the original UNIQUE(task_id). Rows are copied verbatim; only the constraint
// changes, so existing runs and their node history survive.
func (s *TaskStore) migrateWorkflowRunsDropTaskUnique() error {
	var ddl string
	err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='workflow_runs'`).Scan(&ddl)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !strings.Contains(ddl, "task_id TEXT NOT NULL UNIQUE") {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statements := []string{
		`ALTER TABLE workflow_runs RENAME TO workflow_runs_task_unique_legacy`,
		`CREATE TABLE workflow_runs (
 id TEXT PRIMARY KEY, task_id TEXT NOT NULL, version TEXT NOT NULL DEFAULT '',
 revision INTEGER NOT NULL DEFAULT 1, graph_json TEXT NOT NULL,
 node_states_json TEXT NOT NULL DEFAULT '{}', node_outputs_json TEXT NOT NULL DEFAULT '{}',
 selected_routes_json TEXT NOT NULL DEFAULT '{}', loop_state_json TEXT NOT NULL DEFAULT '{}',
 loop_exit_expr_json TEXT NOT NULL DEFAULT '{}', created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 integration_branch TEXT NOT NULL DEFAULT '', triggered_by TEXT NOT NULL DEFAULT '', activity_id TEXT NOT NULL DEFAULT '',
 session_key TEXT NOT NULL DEFAULT '', holds_used INTEGER NOT NULL DEFAULT 0
)`,
		`INSERT INTO workflow_runs(id,task_id,version,revision,graph_json,node_states_json,node_outputs_json,selected_routes_json,loop_state_json,loop_exit_expr_json,created_at,updated_at)
 SELECT id,task_id,version,revision,graph_json,node_states_json,node_outputs_json,selected_routes_json,loop_state_json,loop_exit_expr_json,created_at,updated_at FROM workflow_runs_task_unique_legacy`,
		`DROP TABLE workflow_runs_task_unique_legacy`,
		`CREATE INDEX IF NOT EXISTS idx_workflow_runs_task ON workflow_runs(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_workflow_runs_task_created ON workflow_runs(task_id,created_at DESC)`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}
