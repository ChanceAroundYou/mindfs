package kanban

import (
	"context"
	"errors"
	"testing"

	"mindfs/server/internal/fs"
)

func TestTaskStoreWorkflowRunPersistsOnTaskNodeAndListsNodeRuns(t *testing.T) {
	ctx := context.Background()
	root := fs.NewRootInfo("node-a-root", "node-a-root", t.TempDir())
	store, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	defer store.Close()

	task, err := store.CreateTask(ctx, Task{ID: "task-a", RootID: root.ID, Status: StatusPending, Stages: []StageTemplate{userStage("input")}},
		StageRun{ID: "legacy-a", TaskID: "task-a", StageIndex: 0, StageName: "input", Role: RoleUser, Status: StageStatusPending},
		TaskEvent{ID: "event-a", TaskID: "task-a", Type: "created"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	_ = task

	run := &WorkflowRun{ID: "run-a", TaskID: "task-a", Version: "wf:v1", Revision: 1,
		Graph: WorkflowDefinition{Nodes: []WorkflowNode{{ID: "n1", Name: "Build", Kind: NodeKindAgent}}}}
	if err := store.CreateWorkflowRunAndSetEngine(ctx, run); err != nil {
		t.Fatalf("CreateWorkflowRunAndSetEngine: %v", err)
	}
	nodeRun := NodeRun{ID: "nr-a1", RunID: run.ID, NodeID: "n1", Attempt: 1, SessionKey: "session-a", Status: NodeRunRunning}
	if err := store.UpsertWorkflowNodeRun(ctx, nodeRun); err != nil {
		t.Fatalf("UpsertWorkflowNodeRun: %v", err)
	}

	// Re-open the exact same task-node DB: run snapshot and node records survive process restart.
	reopened, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("reopen TaskStore: %v", err)
	}
	defer reopened.Close()
	gotRun, err := reopened.WorkflowRunForTask(ctx, "task-a")
	if err != nil {
		t.Fatalf("WorkflowRunForTask: %v", err)
	}
	if gotRun.ID != "run-a" || gotRun.Graph.Nodes[0].ID != "n1" {
		t.Fatalf("reopened run=%+v, want run-a with inline graph", gotRun)
	}
	gotNodes, err := reopened.ListWorkflowNodeRuns(ctx, "run-a")
	if err != nil {
		t.Fatalf("ListWorkflowNodeRuns: %v", err)
	}
	if len(gotNodes) != 1 || gotNodes[0].ID != "nr-a1" || gotNodes[0].SessionKey != "session-a" {
		t.Fatalf("node runs=%+v, want nr-a1/session-a", gotNodes)
	}
}

func TestWorkflowRunAndNodeRunAreIsolatedByTaskNodeDatabase(t *testing.T) {
	ctx := context.Background()
	rootA := fs.NewRootInfo("root-a", "root-a", t.TempDir())
	rootB := fs.NewRootInfo("root-b", "root-b", t.TempDir())
	storeA, err := NewTaskStore(rootA)
	if err != nil {
		t.Fatalf("NewTaskStore A: %v", err)
	}
	defer storeA.Close()
	storeB, err := NewTaskStore(rootB)
	if err != nil {
		t.Fatalf("NewTaskStore B: %v", err)
	}
	defer storeB.Close()

	for _, tc := range []struct {
		store *TaskStore
		root  fs.RootInfo
		task  string
		run   string
	}{
		{storeA, rootA, "task-a", "run-a"},
		{storeB, rootB, "task-b", "run-b"},
	} {
		if _, err := tc.store.CreateTask(ctx, Task{ID: tc.task, RootID: tc.root.ID, Status: StatusPending, Stages: []StageTemplate{userStage("input")}},
			StageRun{ID: "stage-" + tc.task, TaskID: tc.task, StageIndex: 0, StageName: "input", Role: RoleUser, Status: StageStatusPending},
			TaskEvent{ID: "event-" + tc.task, TaskID: tc.task, Type: "created"}); err != nil {
			t.Fatalf("CreateTask %s: %v", tc.task, err)
		}
		if err := tc.store.CreateWorkflowRunAndSetEngine(ctx, &WorkflowRun{ID: tc.run, TaskID: tc.task, Revision: 1,
			Graph: WorkflowDefinition{Nodes: []WorkflowNode{{ID: "node", Kind: NodeKindAgent}}}}); err != nil {
			t.Fatalf("CreateWorkflowRun %s: %v", tc.run, err)
		}
		if err := tc.store.UpsertWorkflowNodeRun(ctx, NodeRun{ID: "nr-" + tc.task, RunID: tc.run, NodeID: "node", Attempt: 1, Status: NodeRunSuccess}); err != nil {
			t.Fatalf("Upsert node run %s: %v", tc.run, err)
		}
	}

	if _, err := storeA.WorkflowRunForTask(ctx, "task-b"); !errors.Is(err, ErrWorkflowRunNotFound) {
		t.Fatalf("node A must not read node B task run; err=%v", err)
	}
	if _, err := storeB.WorkflowRunForTask(ctx, "task-a"); !errors.Is(err, ErrWorkflowRunNotFound) {
		t.Fatalf("node B must not read node A task run; err=%v", err)
	}
	if nodes, err := storeA.ListWorkflowNodeRuns(ctx, "run-b"); err != nil || len(nodes) != 0 {
		t.Fatalf("node A must not expose node B NodeRuns: rows=%+v err=%v", nodes, err)
	}
}

func TestWorkflowRunSaveUsesRevisionCASInTaskDatabase(t *testing.T) {
	ctx := context.Background()
	root := fs.NewRootInfo("cas-root", "cas-root", t.TempDir())
	store, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	defer store.Close()
	if _, err := store.CreateTask(ctx, Task{ID: "cas-task", RootID: root.ID, Status: StatusPending, Stages: []StageTemplate{userStage("input")}},
		StageRun{ID: "cas-stage", TaskID: "cas-task", StageIndex: 0, StageName: "input", Role: RoleUser, Status: StageStatusPending},
		TaskEvent{ID: "cas-event", TaskID: "cas-task", Type: "created"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := store.CreateWorkflowRunAndSetEngine(ctx, &WorkflowRun{ID: "cas-run", TaskID: "cas-task", Revision: 1,
		Graph: WorkflowDefinition{Nodes: []WorkflowNode{{ID: "n", Kind: NodeKindAgent}}}}); err != nil {
		t.Fatalf("CreateWorkflowRun: %v", err)
	}
	a, err := store.WorkflowRunForTask(ctx, "cas-task")
	if err != nil {
		t.Fatalf("read run A: %v", err)
	}
	b, err := store.WorkflowRunForTask(ctx, "cas-task")
	if err != nil {
		t.Fatalf("read run B: %v", err)
	}
	a.NodeStates["n"] = NodeRunRunning
	if err := store.SaveWorkflowRun(ctx, a); err != nil {
		t.Fatalf("first save: %v", err)
	}
	b.NodeStates["n"] = NodeRunSuccess
	if err := store.SaveWorkflowRun(ctx, b); !errors.Is(err, ErrRunConflict) {
		t.Fatalf("stale save err=%v, want ErrRunConflict", err)
	}
}


func TestTaskStoreRunLookupByIDAndTaskOwnership(t *testing.T) {
 ctx:=context.Background(); root:=fs.NewRootInfo("lookup-root","lookup-root",t.TempDir()); store,err:=NewTaskStore(root); if err!=nil{t.Fatal(err)}; defer store.Close()
 if _,err:=store.CreateTask(ctx,Task{ID:"lookup-task",RootID:root.ID,Status:StatusPending,Stages:[]StageTemplate{userStage("u")}},StageRun{ID:"lookup-stage",TaskID:"lookup-task",StageIndex:0,StageName:"u",Role:RoleUser,Status:StageStatusPending},TaskEvent{ID:"lookup-event",TaskID:"lookup-task",Type:"created"});err!=nil{t.Fatal(err)}
 run:=&WorkflowRun{ID:"lookup-run",TaskID:"lookup-task",Revision:1,Graph:WorkflowDefinition{Nodes:[]WorkflowNode{{ID:"n",Kind:NodeKindAgent}}}}
 if err:=store.CreateWorkflowRunAndSetEngine(ctx,run);err!=nil{t.Fatal(err)}
 got,err:=store.WorkflowRunByID(ctx,"lookup-run");if err!=nil{t.Fatal(err)};if got.TaskID!="lookup-task"{t.Fatalf("task=%s",got.TaskID)}
 if _,err:=store.WorkflowRunForTask(ctx,"other-task");!errors.Is(err,ErrWorkflowRunNotFound){t.Fatalf("other task lookup err=%v",err)}
}
