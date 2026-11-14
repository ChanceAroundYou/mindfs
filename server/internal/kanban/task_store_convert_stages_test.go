package kanban

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"mindfs/server/internal/fs"
)

// seedLegacyStageTask 直接写冻结列，模拟迁移前的历史任务。
// 生产写入路径已经完全不再碰这些列，所以这里也不经过 insertTask。
func seedLegacyStageTask(t *testing.T, store *TaskStore, taskID string, status string, stages []legacyStageTemplate, runs []legacyStageRun) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	stagesJSON, err := json.Marshal(stages)
	if err != nil {
		t.Fatalf("marshal stages: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO tasks (id, task_number, root_id, task_template_id, task_template_name, template_snapshot_json, create_worktree, worktree_branch_mode, worktree_branch, current_stage_index, status, main_session_key, worktree_root_id, worktree_path, worktree_built, aux_ask_user_waiting, aux_has_plan, aux_has_todos, aux_has_task, aux_session_error, labels_json, created_at, updated_at, completed_at, name, task_stages_json, task_type) VALUES (?, 1, ?, '', '', '', 0, '', '', 0, ?, '', '', '', 0, 0, 0, 0, 0, '', '[]', ?, ?, ?, '历史任务', ?, 'stage')`,
		taskID, store.root.ID, status, now.Format(time.RFC3339Nano), now.Add(2*time.Hour).Format(time.RFC3339Nano), now.Add(2*time.Hour).Format(time.RFC3339Nano), string(stagesJSON)); err != nil {
		t.Fatalf("insert legacy task: %v", err)
	}
	for _, run := range runs {
		created := run.CreatedAt
		if created.IsZero() {
			created = now
		}
		updated := run.UpdatedAt
		if updated.IsZero() {
			updated = now.Add(time.Hour)
		}
		if _, err := store.db.ExecContext(ctx, `INSERT INTO stage_runs (id, task_id, stage_index, stage_name, role, status, session_key, input, rendered_prompt, started_at, finished_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			run.ID, taskID, run.StageIndex, run.StageName, run.Role, run.Status, run.SessionKey, run.Input, run.RenderedPrompt, run.StartedAt, run.FinishedAt, created.Format(time.RFC3339Nano), updated.Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("insert legacy stage run: %v", err)
		}
	}
}

func TestConvertStagesToConversationBuildsActivitiesAndRun(t *testing.T) {
	ctx := context.Background()
	root := fs.NewRootInfo("convert-root", "convert-root", t.TempDir())
	store, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	defer store.Close()

	stages := []legacyStageTemplate{
		{Name: "任务输入", Role: RoleUser, PromptTemplate: "实现登录"},
		{Name: "方案", Role: RoleAgent, Agent: "claude", Model: "sonnet", PromptTemplate: "给出方案"},
		{Name: "实现", Role: RoleAgent, Agent: "claude", Model: "sonnet", PromptTemplate: "实现"},
	}
	runs := []legacyStageRun{
		{ID: "sr-0", StageIndex: 0, StageName: "任务输入", Role: RoleUser, Status: legacyStageStatusSuccess, Input: "实现登录"},
		{ID: "sr-1", StageIndex: 1, StageName: "方案", Role: RoleAgent, Status: legacyStageStatusSuccess, SessionKey: "sess-1"},
		{ID: "sr-2", StageIndex: 2, StageName: "实现", Role: RoleAgent, Status: legacyStageStatusSuccess, SessionKey: "sess-1"},
	}
	seedLegacyStageTask(t, store, "task-history-success", StatusSuccess, stages, runs)

	stats, err := ConvertStagesToConversation(ctx, store.db, StageConversionOptions{SkipLogFile: true})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if stats.TasksConverted != 1 || stats.ActivitiesCreated != 2 || stats.RunsCreated != 1 || stats.NodeRunsCreated != 2 {
		t.Fatalf("stats = %+v", stats)
	}

	task, err := store.GetTask(ctx, "task-history-success")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	var taskType, stagesJSON string
	if err := store.db.QueryRow(`SELECT task_type, task_stages_json FROM tasks WHERE id=?`, task.ID).Scan(&taskType, &stagesJSON); err != nil {
		t.Fatalf("read frozen columns: %v", err)
	}
	if taskType != "conversation" || stagesJSON != "[]" {
		t.Fatalf("task type/stages = %q/%q, want conversation/[]", taskType, stagesJSON)
	}
	// 迁移是后台整形：updated_at 是「最后一次用户可见变更」，不能让所有历史任务
	// 在同一秒跳到列表顶部。seed 里写的是 created+2h。
	wantUpdated := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	if !task.UpdatedAt.Equal(wantUpdated) {
		t.Fatalf("updated_at = %s, want preserved %s", task.UpdatedAt, wantUpdated)
	}
	activities, err := NewActivityStore(store.db).List(ctx, task.ID)
	if err != nil {
		t.Fatalf("List activities: %v", err)
	}
	if len(activities) != 2 {
		t.Fatalf("activities = %d, want 2", len(activities))
	}
	if activities[0].Type != ActivityTypeUserMessage || activities[0].Body != "实现登录" {
		t.Fatalf("user activity = %+v", activities[0])
	}
	if activities[1].Type != ActivityTypeAgentReply || activities[1].Status != ActivityStatusCompleted {
		t.Fatalf("agent activity = %+v", activities[1])
	}
	if activities[1].RunID == "" || activities[1].SessionKey != "sess-1" {
		t.Fatalf("agent run/session = %q/%q", activities[1].RunID, activities[1].SessionKey)
	}

	engine, err := store.TaskEngine(ctx, task.ID)
	if err != nil {
		t.Fatalf("TaskEngine: %v", err)
	}
	if engine.Engine != EngineWorkflow || engine.WorkflowRunID == "" {
		t.Fatalf("engine = %+v", engine)
	}
	run, err := store.WorkflowRunForTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("WorkflowRunForTask: %v", err)
	}
	if run.TriggeredBy != WorkflowTriggeredByLegacyImport || run.ActivityID != activities[0].ID {
		t.Fatalf("run provenance = %q/%q", run.TriggeredBy, run.ActivityID)
	}
	if ProjectStatusFromRun(run) != StatusSuccess {
		t.Fatalf("projected status = %q, want success", ProjectStatusFromRun(run))
	}
	nodeRuns, err := store.ListWorkflowNodeRuns(ctx, run.ID)
	if err != nil {
		t.Fatalf("ListWorkflowNodeRuns: %v", err)
	}
	if len(nodeRuns) != 2 || nodeRuns[0].Status != NodeRunSuccess || nodeRuns[1].Status != NodeRunSuccess {
		t.Fatalf("node runs = %+v", nodeRuns)
	}

	var backedUp int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM tasks_backup_legacy_stages WHERE id=?`, task.ID).Scan(&backedUp); err != nil {
		t.Fatalf("tasks backup: %v", err)
	}
	if backedUp != 1 {
		t.Fatalf("tasks backup rows = %d, want 1", backedUp)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM stage_runs_backup_legacy WHERE task_id=?`, task.ID).Scan(&backedUp); err != nil {
		t.Fatalf("stage runs backup: %v", err)
	}
	if backedUp != 3 {
		t.Fatalf("stage runs backup rows = %d, want 3", backedUp)
	}
	// 原始 stage_runs 保留为只读审计证据，不删。
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM stage_runs WHERE task_id=?`, task.ID).Scan(&backedUp); err != nil {
		t.Fatalf("stage runs: %v", err)
	}
	if backedUp != 3 {
		t.Fatalf("stage runs rows = %d, want 3", backedUp)
	}

	// 再跑一次必须幂等（task 已是 conversation）。
	again, err := ConvertStagesToConversation(ctx, store.db, StageConversionOptions{SkipLogFile: true})
	if err != nil {
		t.Fatalf("second convert: %v", err)
	}
	if again.TasksConverted != 0 {
		t.Fatalf("second convert converted = %d, want 0", again.TasksConverted)
	}
}

func TestConvertStagesToConversationLeavesNewConversationTasksAlone(t *testing.T) {
	ctx := context.Background()
	root := fs.NewRootInfo("convert-new-root", "convert-new-root", t.TempDir())
	store, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	defer store.Close()
	if _, err := store.CreateTask(ctx, Task{ID: "task-new", RootID: root.ID, Status: StatusPending},
		TaskEvent{ID: "evt-new", TaskID: "task-new", Type: "created"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	var beforeType string
	if err := store.db.QueryRow(`SELECT task_type FROM tasks WHERE id='task-new'`).Scan(&beforeType); err != nil {
		t.Fatalf("read task type: %v", err)
	}
	if beforeType != "conversation" {
		t.Fatalf("new task type = %q, want conversation", beforeType)
	}
	stats, err := ConvertStagesToConversation(ctx, store.db, StageConversionOptions{SkipLogFile: true})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if stats.TasksConverted != 0 {
		t.Fatalf("converted = %d, want 0", stats.TasksConverted)
	}
	var afterType, afterStages string
	if err := store.db.QueryRow(`SELECT task_type, task_stages_json FROM tasks WHERE id='task-new'`).Scan(&afterType, &afterStages); err != nil {
		t.Fatalf("read task after: %v", err)
	}
	if afterType != "conversation" || afterStages != "" {
		t.Fatalf("new task = %q/%q, want conversation with no frozen stages untouched", afterType, afterStages)
	}
}

func TestLegacyImportRunDoesNotBlockNewRunOrRecovery(t *testing.T) {
	ctx := context.Background()
	root := fs.NewRootInfo("convert-legacy-block", "convert-legacy-block", t.TempDir())
	store, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	defer store.Close()

	stages := []legacyStageTemplate{
		{Name: "任务输入", Role: RoleUser},
		{Name: "方案", Role: RoleAgent, PromptTemplate: "给出方案"},
	}
	runs := []legacyStageRun{
		{ID: "sr-wait-0", StageIndex: 0, StageName: "任务输入", Role: RoleUser, Status: legacyStageStatusSuccess, Input: "继续"},
		{ID: "sr-wait-1", StageIndex: 1, StageName: "方案", Role: RoleAgent, Status: legacyStageStatusRunning, SessionKey: "sess-running"},
	}
	seedLegacyStageTask(t, store, "task-history-running", StatusWaitingUser, stages, runs)
	if _, err := ConvertStagesToConversation(ctx, store.db, StageConversionOptions{SkipLogFile: true}); err != nil {
		t.Fatalf("convert: %v", err)
	}
	legacyRun, err := store.WorkflowRunForTask(ctx, "task-history-running")
	if err != nil {
		t.Fatalf("legacy run: %v", err)
	}
	if legacyRun.TriggeredBy != WorkflowTriggeredByLegacyImport {
		t.Fatalf("triggered_by = %q", legacyRun.TriggeredBy)
	}

	// 1) 启动对账跳过 legacy_import：运行中的节点不得被改成 unknown/queued。
	if _, err := (&Service{}).recoverWorkflowRunsOnStartup(ctx, root.ID, store, nil); err != nil {
		t.Fatalf("recover: %v", err)
	}
	node, err := store.WorkflowNodeRunByKey(ctx, legacyRun.ID, "stage-1", "", 0, 1)
	if err != nil {
		t.Fatalf("legacy node run: %v", err)
	}
	if node.Status != NodeRunRunning {
		t.Fatalf("legacy node status = %q, want running untouched", node.Status)
	}

	// 2) 用户新留言可以建新 Run，不被非终态的 legacy_import Run 挡住。
	replacement := &WorkflowRun{ID: "run-new-after-legacy", TaskID: "task-history-running", Revision: 1,
		Graph:       WorkflowDefinition{Nodes: []WorkflowNode{{ID: "n", Kind: NodeKindAgent}}},
		TriggeredBy: WorkflowTriggeredByUser}
	if err := store.CreateWorkflowRunAndSetEngine(ctx, replacement); err != nil {
		t.Fatalf("new run after legacy import: %v", err)
	}
}
