package kanban

import (
	"context"
	"errors"
	"testing"
)

// newMigratableService 造一个带 workflow 控制面库的 Service + 一个空白任务。
// 任务没有阶段，迁移只能按显式发布的 workflow version 建 Run。
func newMigratableService(t *testing.T) (*Service, string, Task) {
	t.Helper()
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	svc.Workflows = NewWorkflowStoreAt(t.TempDir())
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Name:   "migrate me",
		Input:  "", // empty to avoid triggering background activity goroutine
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	return svc, root.ID, detail.Task
}

func publishTestVersion(t *testing.T, svc *Service, graph WorkflowDefinition) string {
	t.Helper()
	ctx := context.Background()
	draft, err := svc.Workflows.CreateDraft(ctx, "wf-migrate", "", "tester", graph)
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	ver, err := svc.Workflows.PublishDraft(ctx, "wf-migrate", draft.ResolvedRevision(), "tester")
	if err != nil {
		t.Fatalf("PublishDraft: %v", err)
	}
	return ver.Version
}

func TestMigrateTaskToWorkflowRunBindsPublishedVersion(t *testing.T) {
	ctx := context.Background()
	svc, rootID, task := newMigratableService(t)
	graph := WorkflowDefinition{
		Nodes: []WorkflowNode{
			wfNode("gate", NodeKindHumanGate),
			wfNode("build", NodeKindAgent),
		},
		Edges: []WorkflowEdge{{From: "gate", To: "build"}},
	}
	versionID := publishTestVersion(t, svc, graph)

	run, engine, err := svc.MigrateTaskToWorkflowRun(ctx, rootID, task.ID, versionID, "tester")
	if err != nil {
		t.Fatalf("MigrateTaskToWorkflowRun: %v", err)
	}
	if engine.Engine != EngineWorkflow || engine.WorkflowRunID != run.ID {
		t.Fatalf("engine=%+v, want workflow/%s", engine, run.ID)
	}
	if run.TaskID != task.ID {
		t.Fatalf("run task=%s, want %s", run.TaskID, task.ID)
	}
	if run.Version != versionID {
		t.Fatalf("run version=%q, want published %q", run.Version, versionID)
	}
	if len(run.Graph.Nodes) != 2 || len(run.Graph.Edges) != 1 {
		t.Fatalf("graph nodes/edges = %d/%d, want inline snapshot of published version",
			len(run.Graph.Nodes), len(run.Graph.Edges))
	}
	if run.Graph.Nodes[0].ID != "gate" || run.Graph.Nodes[1].ID != "build" {
		t.Fatalf("graph = %+v, want published node ids", run.Graph.Nodes)
	}
	if run.Graph.RootID != task.RootID {
		t.Fatalf("graph root=%q, want %q", run.Graph.RootID, task.RootID)
	}
	store, err := svc.taskStore(rootID)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := store.WorkflowRunForTask(ctx, task.ID)
	if err != nil || persisted.ID != run.ID {
		t.Fatalf("task-node run=%+v err=%v, want persisted run %q", persisted, err, run.ID)
	}
}

func TestMigrateTaskToWorkflowRunRequiresVersion(t *testing.T) {
	ctx := context.Background()
	svc, rootID, task := newMigratableService(t)
	if _, _, err := svc.MigrateTaskToWorkflowRun(ctx, rootID, task.ID, "", "tester"); err == nil {
		t.Fatal("迁移必须指定 workflow version：任务已无阶段可推导图")
	}
}

// 迁移后引擎绑定必须让位：再次迁移幂等拒绝，且不改任何状态。
func TestMigrateTaskToWorkflowRunRejectsSecondMigration(t *testing.T) {
	ctx := context.Background()
	svc, rootID, task := newMigratableService(t)
	versionID := publishTestVersion(t, svc, wfDef(wfNode("build", NodeKindAgent)))
	if _, _, err := svc.MigrateTaskToWorkflowRun(ctx, rootID, task.ID, versionID, "tester"); err != nil {
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
	if _, _, err := svc.MigrateTaskToWorkflowRun(ctx, rootID, task.ID, versionID, "tester"); !errors.Is(err, ErrTaskAlreadyWorkflow) {
		t.Fatalf("second migrate err=%v, want ErrTaskAlreadyWorkflow", err)
	}
	after, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if after.Status != before.Status {
		t.Fatalf("重复迁移改了状态：before=%s after=%s", before.Status, after.Status)
	}
}

func TestMigrateTaskToWorkflowRunRejectsTerminalTask(t *testing.T) {
	ctx := context.Background()
	svc, rootID, task := newMigratableService(t)
	versionID := publishTestVersion(t, svc, wfDef(wfNode("build", NodeKindAgent)))
	store, err := svc.taskStore(rootID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	if err := store.UpdateTaskStatus(ctx, task.ID, StatusSuccess, true); err != nil {
		t.Fatalf("set terminal: %v", err)
	}
	if _, _, err := svc.MigrateTaskToWorkflowRun(ctx, rootID, task.ID, versionID, "tester"); err == nil {
		t.Fatal("终态任务不该迁移出无人认领的 Run")
	}
}