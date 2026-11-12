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
		if _, err := db.Exec(`ALTER TABLE tasks ADD COLUMN task_type TEXT NOT NULL DEFAULT '` + TaskTypeStage + `'`); err != nil &&
			!strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return stats, err
		}
		hasTaskType = true
	}

	query := `SELECT ` + taskSelectColumns + ` FROM tasks ORDER BY created_at ASC, id ASC`
	if !hasTaskType {
		// dry-run 在未 ALTER 的库上也能预演：把缺失列当成空串，下面按 stage 处理。
		query = `SELECT ` + strings.Replace(taskSelectColumns, ", task_type", ", ''", 1) + ` FROM tasks ORDER BY created_at ASC, id ASC`
	}
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return stats, err
	}
	var tasks []Task
	for rows.Next() {
		task, scanErr := scanTask(rows)
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
		if taskType == TaskTypeConversation {
			stats.TasksSkipped++
			continue
		}
		if taskType != "" && taskType != TaskTypeStage {
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
		return EngineLegacy, "", nil
	}
	return engine, runID, err
}

func listStageRunsTx(ctx context.Context, tx *sql.Tx, taskID string) ([]StageRun, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, task_id, stage_index, stage_name, role, status, session_key, input, rendered_prompt, started_at, finished_at, created_at, updated_at FROM stage_runs WHERE task_id=? ORDER BY stage_index ASC, created_at ASC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StageRun
	for rows.Next() {
		run, err := scanStageRun(rows)
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
		TaskTypeConversation, taskID)
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

type legacyConversion struct {
	Task           Task
	Stages         []StageTemplate
	LatestByIndex  map[int]StageRun
	UserActivity   TaskActivity
	ReplyActivity  TaskActivity
	Run            *WorkflowRun
	NodeRuns       []NodeRun
	HasRunningNode bool
}

func buildLegacyConversion(task Task, runs []StageRun) (legacyConversion, error) {
	stages := task.Stages
	if len(stages) == 0 {
		stages = synthesizeStagesFromRuns(runs)
	}
	latest := map[int]StageRun{}
	for _, run := range runs {
		if current, ok := latest[run.StageIndex]; !ok || run.UpdatedAt.After(current.UpdatedAt) || run.CreatedAt.After(current.CreatedAt) {
			latest[run.StageIndex] = run
		}
	}

	userBody := strings.TrimSpace(latest[0].Input)
	if userBody == "" && len(stages) > 0 {
		userBody = strings.TrimSpace(stages[0].PromptTemplate)
	}
	if userBody == "" {
		userBody = strings.TrimSpace(task.Name)
	}
	if userBody == "" {
		userBody = "历史任务（无输入记录）"
	}
	userAt := task.CreatedAt
	if run, ok := latest[0]; ok && !run.CreatedAt.IsZero() {
		userAt = run.CreatedAt
	}
	conv := legacyConversion{
		Task:          task,
		Stages:        stages,
		LatestByIndex: latest,
		UserActivity: TaskActivity{
			ID: legacyUserActivityPrefix + task.ID, TaskID: task.ID, RootID: task.RootID,
			Type: ActivityTypeUserMessage, Author: "user", Role: RoleUser, Body: userBody,
			WorkflowChoice: CommentWorkflowChoiceEmpty, CreatedAt: userAt, UpdatedAt: userAt,
		},
	}

	graph, nodeRuns, hasRunning := legacyGraphFromStages(task, stages, latest)
	replyAt := legacyReplyTime(task, latest, userAt)
	conv.ReplyActivity = TaskActivity{
		ID: legacyReplyActivityPrefix + task.ID, TaskID: task.ID, RootID: task.RootID,
		Type: ActivityTypeAgentReply, Author: "agent", Role: RoleAgent,
		Body: "", WorkflowChoice: CommentWorkflowChoiceEmpty,
		Status: activityStatusFromTask(task.Status), Summary: legacySummary(task, runs),
		SessionKey: legacySessionKey(task, runs), CreatedAt: replyAt, UpdatedAt: replyAt,
	}
	if len(graph.Nodes) > 0 {
		runID := legacyRunPrefix + task.ID
		conv.ReplyActivity.RunID = runID
		conv.Run = &WorkflowRun{
			ID: runID, TaskID: task.ID, Version: legacyWorkflowVersionFor(task.TaskTemplateName),
			Revision: 1, Graph: graph,
			IntegrationBranch: strings.TrimSpace(task.WorktreeBranch),
			TriggeredBy:       WorkflowTriggeredByLegacyImport,
			ActivityID:        conv.UserActivity.ID,
			NodeStates:        map[string]string{},
			NodeOutputs:       map[string]map[string]any{},
			SelectedRoutes:    map[string]string{},
			LoopState:         map[string]*LoopState{},
			LoopExitExpr:      map[string]string{},
			CreatedAt:         userAt,
			UpdatedAt:         replyAt,
		}
		for nodeID, state := range graphNodeStates(graph, latest) {
			conv.Run.NodeStates[nodeID] = state
		}
		for i := range nodeRuns {
			nodeRuns[i].RunID = runID
		}
		conv.NodeRuns = nodeRuns
		conv.HasRunningNode = hasRunning
	}
	return conv, nil
}

func synthesizeStagesFromRuns(runs []StageRun) []StageTemplate {
	byIndex := map[int]StageRun{}
	for _, run := range runs {
		if current, ok := byIndex[run.StageIndex]; !ok || run.UpdatedAt.After(current.UpdatedAt) || run.CreatedAt.After(current.CreatedAt) {
			byIndex[run.StageIndex] = run
		}
	}
	maxIndex := -1
	for index := range byIndex {
		if index > maxIndex {
			maxIndex = index
		}
	}
	stages := make([]StageTemplate, 0, maxIndex+1)
	for i := 0; i <= maxIndex; i++ {
		run := byIndex[i]
		role := run.Role
		if role == "" {
			role = RoleAgent
		}
		stages = append(stages, StageTemplate{
			ID: run.ID, Name: run.StageName, Role: role, PromptTemplate: run.RenderedPrompt,
		})
	}
	return stages
}

func legacyGraphFromStages(task Task, stages []StageTemplate, latest map[int]StageRun) (WorkflowDefinition, []NodeRun, bool) {
	graph := WorkflowDefinition{RootID: task.RootID}
	var nodeRuns []NodeRun
	hasRunning := false
	previous := ""
	for index := 1; index < len(stages); index++ {
		stage := stages[index]
		nodeID := fmt.Sprintf("stage-%d", index)
		node := WorkflowNode{ID: nodeID, Name: legacyStageName(stage, index)}
		run, hasRun := latest[index]
		prompt := strings.TrimSpace(stage.PromptTemplate)
		if hasRun && strings.TrimSpace(run.RenderedPrompt) != "" {
			prompt = strings.TrimSpace(run.RenderedPrompt)
		}
		if hasRun && prompt == "" {
			prompt = strings.TrimSpace(run.Input)
		}
		if stage.Role == RoleUser {
			node.Kind = NodeKindHumanGate
			node.Params = map[string]any{"prompt": prompt}
		} else {
			node.Kind = NodeKindAgent
			node.Params = map[string]any{
				"agent":            strings.TrimSpace(stage.Agent),
				"model":            strings.TrimSpace(stage.Model),
				"prompt":           prompt,
				"writes_workspace": true,
			}
		}
		graph.Nodes = append(graph.Nodes, node)
		if previous != "" {
			graph.Edges = append(graph.Edges, WorkflowEdge{From: previous, To: nodeID})
		}
		previous = nodeID

		status := NodeRunPending
		if hasRun {
			status = legacyNodeStatus(run.Status)
		}
		if status == NodeRunRunning {
			hasRunning = true
		}
		nr := NodeRun{
			ID: legacyNodeRunPrefix + task.ID + "-" + fmt.Sprint(index),
			// RunID 由调用方补；node 级证据保留原始 prompt 与会话。
			NodeID: nodeID, Attempt: 1, Status: status, Agent: stage.Agent, Model: stage.Model,
			Input: map[string]any{"prompt": prompt, "stage_index": index, "stage_name": node.Name},
			Output: map[string]any{},
		}
		if hasRun {
			nr.SessionKey = run.SessionKey
			nr.StartedAt = parseLegacyTime(run.StartedAt)
			nr.FinishedAt = parseLegacyTime(run.FinishedAt)
			if nr.StartedAt.IsZero() && status != NodeRunPending {
				nr.StartedAt = run.CreatedAt
			}
			if nr.FinishedAt.IsZero() && isLegacyTerminalNodeStatus(status) {
				nr.FinishedAt = run.UpdatedAt
			}
		}
		nodeRuns = append(nodeRuns, nr)
	}
	return graph, nodeRuns, hasRunning
}

func graphNodeStates(graph WorkflowDefinition, latest map[int]StageRun) map[string]string {
	states := map[string]string{}
	for index := 1; index < len(graph.Nodes)+1; index++ {
		nodeID := fmt.Sprintf("stage-%d", index)
		if _, ok := graph.node(nodeID); !ok {
			continue
		}
		state := NodeRunPending
		if run, ok := latest[index]; ok {
			state = legacyNodeStatus(run.Status)
		}
		states[nodeID] = state
	}
	return states
}

func legacyStageName(stage StageTemplate, index int) string {
	if name := strings.TrimSpace(stage.Name); name != "" {
		return name
	}
	return fmt.Sprintf("阶段 %d", index+1)
}

func legacyNodeStatus(status string) string {
	switch strings.TrimSpace(status) {
	case StageStatusSuccess, StageStatusApproved:
		return NodeRunSuccess
	case StageStatusFail:
		return NodeRunFail
	case StageStatusCancelled, StageStatusRejected:
		return NodeRunCancelled
	case StageStatusWaitingUser:
		return NodeRunWaitingUser
	case StageStatusRunning:
		return NodeRunRunning
	default:
		return NodeRunPending
	}
}

func isLegacyTerminalNodeStatus(status string) bool {
	switch status {
	case NodeRunSuccess, NodeRunFail, NodeRunCancelled, NodeRunWaitingUser, NodeRunUnknown:
		return true
	default:
		return false
	}
}

func activityStatusFromTask(status string) string {
	switch status {
	case StatusSuccess:
		return ActivityStatusCompleted
	case StatusFail:
		return ActivityStatusFailed
	case StatusCancelled:
		return ActivityStatusCancelled
	case StatusRunning:
		return ActivityStatusRunning
	default:
		return ActivityStatusPending
	}
}

func legacySummary(task Task, runs []StageRun) string {
	total := len(task.Stages)
	if total == 0 {
		total = len(runs)
	}
	completed := 0
	for _, run := range runs {
		if run.Status == StageStatusSuccess || run.Status == StageStatusApproved {
			completed++
		}
	}
	count := fmt.Sprintf("%d/%d 个阶段", completed, total)
	switch task.Status {
	case StatusSuccess:
		if total == 0 {
			return "任务已完成（无详细记录）"
		}
		return "已完成 " + count
	case StatusFail:
		return "任务失败（已完成 " + count + "）"
	case StatusCancelled:
		return "任务已取消（已完成 " + count + "）"
	case StatusRunning:
		return "正在执行（已完成 " + count + "）"
	case StatusWaitingUser:
		return "等待你的输入（已完成 " + count + "）"
	default:
		if total == 0 {
			return "待开始"
		}
		return "待开始（共 " + fmt.Sprint(total) + " 个阶段）"
	}
}

func legacySessionKey(task Task, runs []StageRun) string {
	if key := strings.TrimSpace(task.MainSessionKey); key != "" {
		return key
	}
	for i := len(runs) - 1; i >= 0; i-- {
		if key := strings.TrimSpace(runs[i].SessionKey); key != "" {
			return key
		}
	}
	return ""
}

func legacyReplyTime(task Task, latest map[int]StageRun, fallback time.Time) time.Time {
	if completed := parseLegacyTime(task.CompletedAt); !completed.IsZero() {
		return laterTime(completed, fallback)
	}
	var newest time.Time
	for _, run := range latest {
		if run.UpdatedAt.After(newest) {
			newest = run.UpdatedAt
		}
		if finished := parseLegacyTime(run.FinishedAt); finished.After(newest) {
			newest = finished
		}
	}
	if newest.IsZero() {
		newest = task.UpdatedAt
	}
	if newest.Before(fallback) {
		newest = fallback
	}
	return newest
}

func laterTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func parseLegacyTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t
	}
	return time.Time{}
}

// legacyWorkflowVersionFor 把历史模板名映射到**已发布**的 workflow 版本。
// 2026-11-12 的模板迁移已经把三个内置模板转成 workflow-new-feature /
// workflow-debug / workflow-blank 并发布为 v1，所以这里引用同一批版本，
// 让 provenance 可解析、不再造一批只在历史里存在的 workflow-legacy-*。
// 映射不上的模板（自定义/backlog/空）返回空串：Run 的图快照是内联的，
// 运行视图不依赖版本号，宁可留空也不谎报来源。
func legacyWorkflowVersionFor(templateName string) string {
	name := strings.TrimSpace(templateName)
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(name, "新功能"):
		return "workflow-new-feature:v1"
	case strings.Contains(lower, "debug"), strings.Contains(name, "调试"):
		return "workflow-debug:v1"
	case strings.Contains(name, "空白"):
		return "workflow-blank:v1"
	default:
		return ""
	}
}

func insertLegacyActivitiesTx(ctx context.Context, tx *sql.Tx, conv legacyConversion) error {
	for _, activity := range []TaskActivity{conv.UserActivity, conv.ReplyActivity} {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO task_activities (id, task_id, root_id, type, author, role, body, workflow_choice, workflow_version_id, status, summary, run_id, node_run_id, session_key, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			activity.ID, activity.TaskID, activity.RootID, activity.Type, activity.Author, activity.Role,
			activity.Body, activity.WorkflowChoice, activity.WorkflowVersionID, activity.Status,
			activity.Summary, activity.RunID, activity.NodeRunID, activity.SessionKey,
			activity.CreatedAt.UTC().Format(time.RFC3339Nano), activity.UpdatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return nil
}

func insertLegacyRunTx(ctx context.Context, tx *sql.Tx, conv legacyConversion) error {
	run := conv.Run
	graph, err := json.Marshal(run.Graph)
	if err != nil {
		return err
	}
	states, err := json.Marshal(run.NodeStates)
	if err != nil {
		return err
	}
	outputs, err := json.Marshal(run.NodeOutputs)
	if err != nil {
		return err
	}
	routes, err := json.Marshal(run.SelectedRoutes)
	if err != nil {
		return err
	}
	loops, err := json.Marshal(run.LoopState)
	if err != nil {
		return err
	}
	exits, err := json.Marshal(run.LoopExitExpr)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO workflow_runs (id, task_id, version, revision, graph_json, node_states_json, node_outputs_json, selected_routes_json, loop_state_json, loop_exit_expr_json, created_at, updated_at, integration_branch, triggered_by, activity_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET version=excluded.version, graph_json=excluded.graph_json, node_states_json=excluded.node_states_json, node_outputs_json=excluded.node_outputs_json, selected_routes_json=excluded.selected_routes_json, loop_state_json=excluded.loop_state_json, loop_exit_expr_json=excluded.loop_exit_expr_json, updated_at=excluded.updated_at, integration_branch=excluded.integration_branch, triggered_by=excluded.triggered_by, activity_id=excluded.activity_id`,
		run.ID, run.TaskID, run.Version, run.Revision, string(graph), string(states), string(outputs), string(routes), string(loops), string(exits),
		run.CreatedAt.UTC().Format(time.RFC3339Nano), run.UpdatedAt.UTC().Format(time.RFC3339Nano),
		run.IntegrationBranch, run.TriggeredBy, run.ActivityID); err != nil {
		return err
	}
	for _, nr := range conv.NodeRuns {
		input, err := json.Marshal(nr.Input)
		if err != nil {
			return err
		}
		output, err := json.Marshal(nr.Output)
		if err != nil {
			return err
		}
		started, finished := "", ""
		if !nr.StartedAt.IsZero() {
			started = nr.StartedAt.UTC().Format(time.RFC3339Nano)
		}
		if !nr.FinishedAt.IsZero() {
			finished = nr.FinishedAt.UTC().Format(time.RFC3339Nano)
		}
		created := run.CreatedAt
		if !nr.StartedAt.IsZero() {
			created = nr.StartedAt
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO workflow_node_runs (id, run_id, node_id, loop_path, iteration, attempt, agent, harness, model, session_key, status, input_json, output_json, started_at, finished_at, prev_attempt_id, worktree_id, worktree_path, worktree_branch, created_at, updated_at)
			 VALUES (?, ?, ?, '', 0, ?, ?, '', ?, ?, ?, ?, ?, ?, ?, '', '', '', '', ?, ?)
			 ON CONFLICT(id) DO UPDATE SET status=excluded.status, session_key=excluded.session_key, input_json=excluded.input_json, output_json=excluded.output_json, started_at=excluded.started_at, finished_at=excluded.finished_at, updated_at=excluded.updated_at`,
			nr.ID, nr.RunID, nr.NodeID, nr.Attempt, nr.Agent, nr.Model, nr.SessionKey, nr.Status, string(input), string(output), started, finished,
			created.UTC().Format(time.RFC3339Nano), run.UpdatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return nil
}