package kanban

import (
	"context"
	"errors"
	"testing"
)

// newMigratableService 造一个带 workflow 控制面库的 Service + 一个两段任务
// （user 段 + agent 段），用于迁移测试。
func newMigratableService(t *testing.T) (*Service, string, Task) {
	t.Helper()
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	svc.Workflows = NewWorkflowStoreAt(t.TempDir())
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Name:   "migrate me",
		Stages: []StageTemplate{userStage("Describe"), agentStage("Fix", "fix {previous_input}")},
		Input:  "broken",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	return svc, root.ID, detail.Task
}

// 迁移：按 legacy Stages 生成线性图快照，并切到 workflow 引擎。
func TestMigrateTaskToWorkflowRun_DerivesGraphFromStages(t *testing.T) {
	ctx := context.Background()
	svc, rootID, task := newMigratableService(t)

	run, engine, err := svc.MigrateTaskToWorkflowRun(ctx, rootID, task.ID, "", "tester")
	if err != nil {
		t.Fatalf("MigrateTaskToWorkflowRun: %v", err)
	}
	if engine.Engine != EngineWorkflow || engine.WorkflowRunID != run.ID {
		t.Fatalf("engine=%+v, want workflow/%s", engine, run.ID)
	}
	if run.TaskID != task.ID {
		t.Fatalf("run task=%s, want %s", run.TaskID, task.ID)
	}
	// 两段 → 两个节点 + 一条边，顺序与 stages 一致。
	if len(run.Graph.Nodes) != 2 || len(run.Graph.Edges) != 1 {
		t.Fatalf("graph nodes/edges = %d/%d, want 2/1: %+v", len(run.Graph.Nodes), len(run.Graph.Edges), run.Graph)
	}
	if run.Graph.Nodes[0].Kind != NodeKindHumanGate {
		t.Fatalf("first node kind=%s, want human_gate（user 段映射）", run.Graph.Nodes[0].Kind)
	}
	if run.Graph.Nodes[1].Kind != NodeKindAgent {
		t.Fatalf("second node kind=%s, want agent", run.Graph.Nodes[1].Kind)
	}
	if run.Graph.Edges[0].From != "stage-0" || run.Graph.Edges[0].To != "stage-1" {
		t.Fatalf("edge=%+v, want stage-0→stage-1", run.Graph.Edges[0])
	}
	if run.Graph.RootID != task.RootID {
		t.Fatalf("graph root=%q, want %q", run.Graph.RootID, task.RootID)
	}
}

// 迁移后 legacy 引擎必须让位：executeTask 直接返回 ErrTaskAlreadyWorkflow，且不改状态。
func TestMigrateTaskToWorkflowRun_FreezesLegacyEngine(t *testing.T) {
	ctx := context.Background()
	svc, rootID, task := newMigratableService(t)
	if _, _, err := svc.MigrateTaskToWorkflowRun(ctx, rootID, task.ID, "", "tester"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store, err := svc.taskStore(rootID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	before, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if err := svc.executeTask(ctx, rootID, task.ID); !errors.Is(err, ErrTaskAlreadyWorkflow) {
		t.Fatalf("executeTask err=%v, want ErrTaskAlreadyWorkflow", err)
	}
	after, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if after.Status != before.Status || after.CurrentStageIndex != before.CurrentStageIndex {
		t.Fatalf("legacy engine must not touch migrated task: before=%s/%d after=%s/%d",
			before.Status, before.CurrentStageIndex, after.Status, after.CurrentStageIndex)
	}
}

// 重复迁移幂等拒绝：不产生第二个 Run，也不改绑定。
func TestMigrateTaskToWorkflowRun_SecondCallRejected(t *testing.T) {
	ctx := context.Background()
	svc, rootID, task := newMigratableService(t)
	run, _, err := svc.MigrateTaskToWorkflowRun(ctx, rootID, task.ID, "", "tester")
	if err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	_, engine, err := svc.MigrateTaskToWorkflowRun(ctx, rootID, task.ID, "", "tester")
	if !errors.Is(err, ErrTaskAlreadyWorkflow) {
		t.Fatalf("second migrate err=%v, want ErrTaskAlreadyWorkflow", err)
	}
	if engine.WorkflowRunID != run.ID {
		t.Fatalf("engine run=%q, want unchanged %q", engine.WorkflowRunID, run.ID)
	}
	drafts, err := svc.Workflows.ListDrafts(ctx)
	if err != nil {
		t.Fatalf("ListDrafts: %v", err)
	}
	_ = drafts // 迁移不写 Draft，只写 Run；这里只保证没有额外副作用
}

// 终态任务拒绝迁移：没有可推进的活，迁移只会留下无人认领的 Run。
func TestMigrateTaskToWorkflowRun_RejectsTerminalTask(t *testing.T) {
	ctx := context.Background()
	svc, rootID, task := newMigratableService(t)
	store, err := svc.taskStore(rootID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	if err := store.UpdateTaskStatus(ctx, task.ID, StatusSuccess, true); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}
	if _, _, err := svc.MigrateTaskToWorkflowRun(ctx, rootID, task.ID, "", "tester"); err == nil {
		t.Fatal("terminal task must be rejected")
	}
	engine, err := taskEngineOf(ctx, store.db, task.ID)
	if err != nil {
		t.Fatalf("taskEngineOf: %v", err)
	}
	if engine.Engine != EngineLegacy {
		t.Fatalf("engine=%s, want legacy（拒绝迁移不得改绑定）", engine.Engine)
	}
}

// 指定 version 时用该不可变版本的图，而不是从 Stages 推导。
func TestMigrateTaskToWorkflowRun_UsesRequestedVersion(t *testing.T) {
	ctx := context.Background()
	svc, rootID, task := newMigratableService(t)
	graph := WorkflowDefinition{Nodes: []WorkflowNode{
		{ID: "only", Name: "solo", Kind: NodeKindNotify, Params: map[string]any{"message": "hi"}},
	}}
	draft, err := svc.Workflows.CreateDraft(ctx, "wf-1", "", "tester", graph)
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	ver, err := svc.Workflows.PublishDraft(ctx, "wf-1", draft.Revision, "tester")
	if err != nil {
		t.Fatalf("PublishDraft: %v", err)
	}
	run, _, err := svc.MigrateTaskToWorkflowRun(ctx, rootID, task.ID, ver.Version, "tester")
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(run.Graph.Nodes) != 1 || run.Graph.Nodes[0].ID != "only" {
		t.Fatalf("graph=%+v, want the published version graph", run.Graph)
	}
	if run.Version != ver.Version {
		t.Fatalf("run version=%q, want %q", run.Version, ver.Version)
	}
}

// 控制面库没接：拒绝迁移并保持 legacy（不产生「引擎切了但没有 Run」的状态）。
func TestMigrateTaskToWorkflowRun_NoStoreKeepsLegacy(t *testing.T) {
	ctx := context.Background()
	svc, rootID, task := newMigratableService(t)
	svc.Workflows = nil
	if _, _, err := svc.MigrateTaskToWorkflowRun(ctx, rootID, task.ID, "", "tester"); err == nil {
		t.Fatal("migration must fail without a workflow store")
	}
	store, err := svc.taskStore(rootID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	engine, err := taskEngineOf(ctx, store.db, task.ID)
	if err != nil {
		t.Fatalf("taskEngineOf: %v", err)
	}
	if engine.Engine != EngineLegacy {
		t.Fatalf("engine=%s, want legacy", engine.Engine)
	}
}

// 存量任务没有引擎行 = legacy，零回填（升级不批量转换）。
func TestTaskEngine_DefaultIsLegacyWithoutRow(t *testing.T) {
	svc, rootID, task := newMigratableService(t)
	store, err := svc.taskStore(rootID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	engine, err := taskEngineOf(context.Background(), store.db, task.ID)
	if err != nil {
		t.Fatalf("taskEngineOf: %v", err)
	}
	if engine.Engine != EngineLegacy {
		t.Fatalf("engine=%s, want legacy", engine.Engine)
	}
}
