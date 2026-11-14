package kanban

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// 任务执行引擎标记：一个 Task 同时最多一个活动执行引擎。
//
// 为什么单独一张表而不是加到 tasks 的列上：tasks 的列由 task_store_write/scan 逐列
// 读写，加列要同时改这两处 + 迁移 + 所有存量扫描路径；而「有没有迁移过」只影响少数
// 入口（legacy 推进前的一次查询）。独立表零侵入，存量任务没有行 = 天然 legacy，
// 不需要任何回填。
const (
	// EngineConversation 是默认执行引擎：留言交给 Router，由它决定跑 workflow 还是
	// 直接在主会话里回一轮（见 ActivityService.RunDirectActivity）。
	//
	// 落库值仍是 "legacy"：存量库与前端 `TaskExecutionEngine.engine` 都按这个字符串
	// 读，改名要动数据迁移，不划算。别被字面量误导 —— 它现在的含义是「对话/Router」，
	// 不是已经被删除的阶段引擎。
	EngineConversation = "legacy"
	EngineWorkflow     = "workflow" // 迁移后的 WorkflowRun 引擎
)

// ErrTaskAlreadyWorkflow 表示任务已经迁移到 WorkflowRun，legacy 引擎不再推进它。
var ErrTaskAlreadyWorkflow = errors.New("task already migrated to workflow run")

// TaskEngine 是一个任务的执行引擎绑定。
type TaskEngine struct {
	TaskID        string    `json:"task_id"`
	Engine        string    `json:"engine"`
	WorkflowRunID string    `json:"workflow_run_id,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// migrateTaskEngines 建执行引擎绑定表。幂等；挂在 migrate() 的显式接线里，
// 不在运行时路径上懒建表。
func (s *TaskStore) migrateTaskEngines() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS task_execution_engines (
	task_id TEXT PRIMARY KEY,
	engine TEXT NOT NULL DEFAULT 'legacy',
	workflow_run_id TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL
);
`)
	return err
}

// taskEngineOf 读任务绑定的引擎；没有行即 legacy（存量零改动）。
func taskEngineOf(ctx context.Context, db *sql.DB, taskID string) (TaskEngine, error) {
	var e TaskEngine
	var ts string
	err := db.QueryRowContext(ctx,
		`SELECT task_id, engine, workflow_run_id, updated_at FROM task_execution_engines WHERE task_id=?`, taskID).
		Scan(&e.TaskID, &e.Engine, &e.WorkflowRunID, &ts)
	if errors.Is(err, sql.ErrNoRows) {
		return TaskEngine{TaskID: taskID, Engine: EngineConversation}, nil
	}
	if err != nil {
		return TaskEngine{}, err
	}
	e.UpdatedAt = parseOutboxTime(ts)
	return e, nil
}

// TaskEngineForStore exposes the engine binding for a caller that already routed to the task node.
func (s *TaskStore) TaskEngine(ctx context.Context, taskID string) (TaskEngine, error) {
	return taskEngineOf(ctx, s.db, taskID)
}

// setTaskEngine 写绑定（幂等：同 task 覆盖）。只能由迁移在「WorkflowRun 已落库之后」调用，
// 否则会出现引擎切过去却没有 Run 可跑的空窗。
func setTaskEngine(ctx context.Context, db *sql.DB, taskID, engine, runID string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO task_execution_engines(task_id, engine, workflow_run_id, updated_at) VALUES(?,?,?,?)
		 ON CONFLICT(task_id) DO UPDATE SET engine=excluded.engine, workflow_run_id=excluded.workflow_run_id, updated_at=excluded.updated_at`,
		taskID, engine, runID, nowUTC().Format(time.RFC3339Nano))
	return err
}

// MigrateTaskToWorkflowRun 把一个存量任务显式迁移到 WorkflowRun 引擎。
//
// 语义（spec「新旧执行模型互斥」）：
//   - 只有显式调用才迁移，绝不批量转换存量任务；
//   - 迁移生成一份独立、内联的图快照，来源是**显式指定的已发布版本**；任务身上没有
//     阶段（2026-11 已彻底删除），所以 version 必填，不存在「按 Stages 逐段映射」的
//     兼容分支。之后该任务不再由对话/直连引擎推进，也不双写两份运行模型；
//   - 图快照先落库、引擎标记后落库：中途失败只是多一份没人引用的 Run，任务仍是
//     原引擎，不会出现「引擎切了但没有可跑的 Run」；
//   - 已迁移的任务再次调用返回 ErrTaskAlreadyWorkflow（幂等拒绝，不改任何状态）；
//   - 终态任务拒绝迁移（没有可推进的活，迁移只会制造无人认领的 Run）。
func (s *Service) MigrateTaskToWorkflowRun(ctx context.Context, rootID, taskID, versionID, author string) (*WorkflowRun, TaskEngine, error) {
	if s == nil {
		return nil, TaskEngine{}, errors.New("kanban service not configured")
	}
	store, task, err := s.loadForMove(ctx, rootID, taskID)
	if err != nil {
		return nil, TaskEngine{}, err
	}
	if isTerminalStatus(task.Status) {
		return nil, TaskEngine{}, fmt.Errorf("task is terminal (%s): 终态任务无需迁移", task.Status)
	}
	cur, err := taskEngineOf(ctx, store.db, task.ID)
	if err != nil {
		return nil, TaskEngine{}, err
	}
	if cur.Engine == EngineWorkflow {
		return nil, cur, ErrTaskAlreadyWorkflow
	}

	graph, resolvedVersion, err := s.migrationGraph(ctx, versionID)
	if err != nil {
		return nil, TaskEngine{}, err
	}
	// 版本图可能是在别的 root（或空 root）下编辑的；Run 跑在这张任务的库里，
	// 图必须跟着任务走，否则节点解析出的工作区与 Run 存放位置不一致。
	graph.RootID = task.RootID
	integrationBranch, err := s.snapshotRunIntegrationBranch(ctx, task, graph)
	if err != nil {
		// D11：没有可用 target 就不建 Run；运行期不允许隐式回落 "main"。
		return nil, TaskEngine{}, err
	}
	run := &WorkflowRun{ID: newRunID(), TaskID: task.ID, Version: resolvedVersion, Revision: 1, Graph: cloneGraph(graph),
		IntegrationBranch: integrationBranch,
		NodeStates:        map[string]string{}, NodeOutputs: map[string]map[string]any{}, SelectedRoutes: map[string]string{},
		LoopState: map[string]*LoopState{}, LoopExitExpr: map[string]string{}}
	// Store snapshot and task-engine binding atomically in this task node's SQLite database.
	if err := store.CreateWorkflowRunAndSetEngine(ctx, run); err != nil {
		return nil, TaskEngine{}, err
	}

	engine, err := taskEngineOf(ctx, store.db, taskID)
	if err != nil {
		return nil, TaskEngine{}, err
	}
	_ = store.AddEvent(ctx, TaskEvent{
		ID:        newID("evt"),
		TaskID:    task.ID,
		Type:      "task_migrated_to_workflow",
		Payload:   fmt.Sprintf(`{"run_id":%q,"version":%q,"author":%q}`, run.ID, versionID, strings.TrimSpace(author)),
		CreatedAt: time.Now().UTC(),
	})
	// Migration is an approved trigger (D1): persist first, then wake the
	// per-task driver. A wake failure leaves the run persisted and actionable by
	// the next RunNow/resume instead of rolling back the migration.
	_ = s.WakeWorkflowRun(ctx, rootID, task.ID, "migration")
	return run, engine, nil
}

// migrationGraph 取迁移用的不可变版本图。
//
// 任务身上没有阶段了（2026-11 决策），所以这里唯一合法的来源是显式 version：
// 以前「不带 version 就按 Stages 逐段映射」的兼容分支随阶段概念一起删除。
func (s *Service) migrationGraph(ctx context.Context, versionID string) (WorkflowDefinition, string, error) {
	versionID = strings.TrimSpace(versionID)
	if versionID == "" {
		return WorkflowDefinition{}, "", errors.New("version required: 任务已无阶段，迁移必须指定 workflow version")
	}
	if s.Workflows == nil {
		return WorkflowDefinition{}, "", errors.New("workflow control-plane store unavailable")
	}
	ver, err := s.Workflows.GetVersion(ctx, versionID)
	if err != nil {
		return WorkflowDefinition{}, "", fmt.Errorf("workflow version %q: %w", versionID, err)
	}
	return ver.Graph, ver.Version, nil
}

// taskRunsOnWorkflow 报任务是否已经切到 WorkflowRun 引擎（legacy 推进前必须查）。
func (s *Service) taskRunsOnWorkflow(ctx context.Context, store *TaskStore, taskID string) bool {
	e, err := taskEngineOf(ctx, store.db, taskID)
	if err != nil {
		return false
	}
	return e.Engine == EngineWorkflow
}

// TaskEngineFor 读任务当前的执行引擎绑定（供 UI 判断是否展示运行视图）。
func (s *Service) TaskEngineFor(ctx context.Context, rootID, taskID string) (TaskEngine, error) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return TaskEngine{}, err
	}
	if _, _, err := s.loadForMove(ctx, rootID, taskID); err != nil {
		return TaskEngine{}, err
	}
	return taskEngineOf(ctx, store.db, taskID)
}

// TaskStoreForRoot returns the task-node store after normal root routing. Callers must supply
// rootID from the task's explicit data-plane scope; this is not a control-plane store.
func (s *Service) TaskStoreForRoot(rootID string) (*TaskStore, error) {
	return s.taskStore(rootID)
}
