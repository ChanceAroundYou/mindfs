package kanban

import (
	"context"
	"testing"
	"time"

	"mindfs/server/internal/fs"
)

func seedLegacyStageTask(t *testing.T, store *TaskStore, taskID string, status string, stages []StageTemplate, runs []StageRun) {
	t.Helper()
	ctx := context.Background()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	now := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	task := Task{
		ID: taskID, TaskNumber: 1, RootID: store.root.ID, Name: "历史任务",
		TaskType: TaskTypeStage, Stages: stages, Status: status,
		Labels: []string{}, CreatedAt: now, UpdatedAt: now.Add(2 * time.Hour),
		CompletedAt: now.Add(2 * time.Hour).Format(time.RFC3339Nano),
	}
	if err := insertTask(ctx, tx, task); err != nil {
		t.Fatalf("insertTask: %v", err)
	}
	for _, run := range runs {
		run.TaskID = taskID
		if run.CreatedAt.IsZero() {
			run.CreatedAt = now
		}
		if run.UpdatedAt.IsZero() {
			run.UpdatedAt = now.Add(time.Hour)
		}
		if err := insertStageRun(ctx, tx, run); err != nil {
			t.Fatalf("insertStageRun: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
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

	stages := []StageTemplate{
		{Name: "任务输入", Role: RoleUser, PromptTemplate: "实现登录"},
		{Name: "方案", Role: RoleAgent, Agent: "claude", Model: "sonnet", PromptTemplate: "给出方案"},
		{Name: "实现", Role: RoleAgent, Agent: "claude", Model: "sonnet", PromptTemplate: "实现"},
	}
	runs := []StageRun{
		{ID: "sr-0", StageIndex: 0, StageName: "任务输入", Role: RoleUser, Status: StageStatusSuccess, Input: "实现登录"},
		{ID: "sr-1", StageIndex: 1, StageName: "方案", Role: RoleAgent, Status: StageStatusSuccess, SessionKey: "sess-1"},
		{ID: "sr-2", StageIndex: 2, StageName: "实现", Role: RoleAgent, Status: StageStatusSuccess, SessionKey: "sess-1"},
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
	if task.TaskType != TaskTypeConversation || len(task.Stages) != 0 {
		t.Fatalf("task type/stages = %q/%d, want conversation/0", task.TaskType, len(task.Stages))
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
	stages := []StageTemplate{{Name: "input", Role: RoleUser}, {Name: "work", Role: RoleAgent, PromptTemplate: "{previous_input}"}}
	if _, err := store.CreateTask(ctx, Task{ID: "task-new", RootID: root.ID, Status: StatusPending, Stages: stages},
		StageRun{ID: "sr-new", TaskID: "task-new", StageIndex: 0, StageName: "input", Role: RoleUser, Status: StageStatusPending},
		TaskEvent{ID: "evt-new", TaskID: "task-new", Type: "created"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	before, err := store.GetTask(ctx, "task-new")
	if err != nil {
		t.Fatalf("GetTask before: %v", err)
	}
	if before.TaskType != TaskTypeConversation {
		t.Fatalf("new task type = %q, want conversation", before.TaskType)
	}
	stats, err := ConvertStagesToConversation(ctx, store.db, StageConversionOptions{SkipLogFile: true})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if stats.TasksConverted != 0 {
		t.Fatalf("converted = %d, want 0", stats.TasksConverted)
	}
	after, err := store.GetTask(ctx, "task-new")
	if err != nil {
		t.Fatalf("GetTask after: %v", err)
	}
	if len(after.Stages) != len(stages) {
		t.Fatalf("new task stages = %d, want %d untouched", len(after.Stages), len(stages))
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

	stages := []StageTemplate{
		{Name: "任务输入", Role: RoleUser},
		{Name: "方案", Role: RoleAgent, PromptTemplate: "给出方案"},
	}
	runs := []StageRun{
		{ID: "sr-wait-0", StageIndex: 0, StageName: "任务输入", Role: RoleUser, Status: StageStatusSuccess, Input: "继续"},
		{ID: "sr-wait-1", StageIndex: 1, StageName: "方案", Role: RoleAgent, Status: StageStatusRunning, SessionKey: "sess-running"},
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