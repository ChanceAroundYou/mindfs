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
	EngineLegacy   = "legacy"   // 既有 Stages/StageRun 引擎（默认）
	EngineWorkflow = "workflow" // 迁移后的 WorkflowRun 引擎
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
		return TaskEngine{TaskID: taskID, Engine: EngineLegacy}, nil
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
//   - 迁移生成一份独立、内联的图快照（legacy 的 Stages 逐段映射成线性 Agent/HumanGate
//     节点），之后该任务不再由 legacy 引擎推进，也不双写两份运行模型；
//   - 图快照先落库、引擎标记后落库：中途失败只是多一份没人引用的 Run，任务仍是
//     legacy，不会出现「引擎切了但没有可跑的 Run」；
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

	if strings.TrimSpace(versionID) != "" && s.Workflows == nil {
		return nil, TaskEngine{}, errors.New("workflow control-plane store unavailable: version lookup required")
	}
	graph, resolvedVersion, err := s.migrationGraph(ctx, store, task, versionID)
	if err != nil {
		return nil, TaskEngine{}, err
	}
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

// migrationGraph 生成迁移用的内联图快照。
//
// 给了 versionID 就用该不可变版本的图（真正「按工作流迁移」）；没给就按 legacy 的
// Stages 逐段映射成一条线性链 —— 段是 Agent 就 Agent 节点，是 user 就 HumanGate 节点，
// 这样迁移后的图与任务原本的顺序语义一致，而不是凭空造一个空图。
func (s *Service) migrationGraph(ctx context.Context, store *TaskStore, task Task, versionID string) (WorkflowDefinition, string, error) {
	if strings.TrimSpace(versionID) != "" {
		if s.Workflows == nil {
			return WorkflowDefinition{}, "", errors.New("workflow control-plane store unavailable")
		}
		ver, err := s.Workflows.GetVersion(ctx, versionID)
		if err != nil {
			return WorkflowDefinition{}, "", fmt.Errorf("workflow version %q: %w", versionID, err)
		}
		return ver.Graph, ver.Version, nil
	}
	if len(task.Stages) == 0 {
		return WorkflowDefinition{}, "", errors.New("task has no stages to derive a workflow graph from")
	}
	nodes := make([]WorkflowNode, 0, len(task.Stages))
	edges := make([]WorkflowEdge, 0, len(task.Stages)-1)
	prev := ""
	for i, st := range task.Stages {
		id := fmt.Sprintf("stage-%d", i)
		node := WorkflowNode{ID: id, Name: st.Name}
		if st.Role == RoleUser {
			node.Kind = NodeKindHumanGate
			node.Params = map[string]any{"prompt": strings.TrimSpace(st.PromptTemplate)}
		} else {
			node.Kind = NodeKindAgent
			node.Params = map[string]any{
				"agent":            st.Agent,
				"model":            st.Model,
				"prompt":           st.PromptTemplate,
				"writes_workspace": true,
			}
		}
		nodes = append(nodes, node)
		if prev != "" {
			edges = append(edges, WorkflowEdge{From: prev, To: id})
		}
		prev = id
	}
	return WorkflowDefinition{Nodes: nodes, Edges: edges, RootID: task.RootID}, "", nil
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
