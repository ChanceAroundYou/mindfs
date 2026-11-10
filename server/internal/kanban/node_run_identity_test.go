package kanban

import (
	"context"
	"testing"

	"mindfs/server/internal/fs"
)

func TestResolveNodeRunPrefersCanonicalWorkflowIdentity(t *testing.T) {
	ctx := context.Background()
	root := fs.NewRootInfo("resolve-wf-root", "resolve-wf-root", t.TempDir())
	store, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	defer store.Close()
	if _, err := store.CreateTask(ctx, Task{ID: "resolve-wf-task", RootID: root.ID, Status: StatusPending, Stages: []StageTemplate{userStage("input")}},
		StageRun{ID: "resolve-wf-stage", TaskID: "resolve-wf-task", StageIndex: 0, StageName: "input", Role: RoleUser, Status: StageStatusPending},
		TaskEvent{ID: "resolve-wf-event", TaskID: "resolve-wf-task", Type: "created"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	run := &WorkflowRun{ID: "resolve-wf-run", TaskID: "resolve-wf-task", Revision: 1,
		Graph: WorkflowDefinition{Nodes: []WorkflowNode{{ID: "n", Kind: NodeKindAgent}}}}
	if err := store.CreateWorkflowRunAndSetEngine(ctx, run); err != nil {
		t.Fatalf("CreateWorkflowRun: %v", err)
	}
	if _, _, err := store.ClaimWorkflowNodeRun(ctx, NodeRun{ID: "nr-wf", RunID: run.ID, NodeID: "n", Attempt: 1, Status: NodeRunQueued}); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := store.UpsertWorkflowNodeRun(ctx, NodeRun{ID: "nr-wf", RunID: run.ID, NodeID: "n", Attempt: 1, Status: NodeRunRunning, SessionKey: "sess-wf"}); err != nil {
		t.Fatalf("mark running: %v", err)
	}

	rec, source, err := ResolveNodeRun(ctx, store, "nr-wf")
	if err != nil {
		t.Fatalf("ResolveNodeRun: %v", err)
	}
	if source != NodeRunSourceWorkflow {
		t.Fatalf("source = %q, want %q", source, NodeRunSourceWorkflow)
	}
	if rec.ID != "nr-wf" || rec.TaskID != "resolve-wf-task" || rec.RootID != root.ID || rec.SessionKey != "sess-wf" || rec.Status != NodeRunRunning || rec.Attempt != 1 {
		t.Fatalf("resolved record = %#v", rec)
	}
	legacy, err := GetNodeRun(ctx, store, "nr-wf")
	if err != nil {
		t.Fatalf("GetNodeRun: %v", err)
	}
	if legacy != rec {
		t.Fatalf("GetNodeRun = %#v, want canonical %#v", legacy, rec)
	}
}

// WP-03 的 node_runs 影子行没有 run/node 归属，但旧留言与启动恢复仍按 id 读它；
// 新运行时建表后必须继续可读，不能被 canonical 解析吞掉。
func TestResolveNodeRunKeepsLegacyOutboxRowsReadable(t *testing.T) {
	ctx := context.Background()
	root := fs.NewRootInfo("resolve-legacy-root", "resolve-legacy-root", t.TempDir())
	store, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	defer store.Close()
	if _, err := store.CreateTask(ctx, Task{ID: "resolve-legacy-task", RootID: root.ID, Status: StatusPending, Stages: []StageTemplate{userStage("input")}},
		StageRun{ID: "resolve-legacy-stage", TaskID: "resolve-legacy-task", StageIndex: 0, StageName: "input", Role: RoleUser, Status: StageStatusPending},
		TaskEvent{ID: "resolve-legacy-event", TaskID: "resolve-legacy-task", Type: "created"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	outbox := &OutboxService{Store: store}
	req, err := outbox.RecordComment(ctx, ContinueRequestRecord{
		TaskID: "resolve-legacy-task", CommentID: "c-legacy", RootID: root.ID,
		NodeRunID: "nr-legacy", SessionKey: "sess-legacy", Message: "旧留言", Author: "user",
	})
	if err != nil {
		t.Fatalf("RecordComment: %v", err)
	}
	if req.NodeRunID != "nr-legacy" {
		t.Fatalf("request node run = %q", req.NodeRunID)
	}

	rec, source, err := ResolveNodeRun(ctx, store, "nr-legacy")
	if err != nil {
		t.Fatalf("ResolveNodeRun: %v", err)
	}
	if source != NodeRunSourceLegacy {
		t.Fatalf("source = %q, want %q", source, NodeRunSourceLegacy)
	}
	if rec.ID != "nr-legacy" || rec.TaskID != "resolve-legacy-task" || rec.RootID != root.ID || rec.SessionKey != "sess-legacy" || rec.Status != OutboxActive {
		t.Fatalf("legacy record = %#v", rec)
	}
	if got, err := outbox.GetRequest(ctx, req.ID); err != nil || got.Message != "旧留言" || got.NodeRunID != "nr-legacy" {
		t.Fatalf("legacy request no longer readable: %#v err=%v", got, err)
	}
	items, err := outbox.ListForTask(ctx, "resolve-legacy-task")
	if err != nil || len(items) != 1 || items[0].ID != req.ID {
		t.Fatalf("ListForTask = %#v err=%v", items, err)
	}
}

// 同一 id 同时出现在两张表时，canonical 必须胜出：新运行时不能把执行身份
// 解析回没有 run/node 归属的旧影子行。
func TestResolveNodeRunPrefersWorkflowWhenLegacyIDCollides(t *testing.T) {
	ctx := context.Background()
	root := fs.NewRootInfo("resolve-collide-root", "resolve-collide-root", t.TempDir())
	store, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	defer store.Close()
	if _, err := store.CreateTask(ctx, Task{ID: "resolve-collide-task", RootID: root.ID, Status: StatusPending, Stages: []StageTemplate{userStage("input")}},
		StageRun{ID: "resolve-collide-stage", TaskID: "resolve-collide-task", StageIndex: 0, StageName: "input", Role: RoleUser, Status: StageStatusPending},
		TaskEvent{ID: "resolve-collide-event", TaskID: "resolve-collide-task", Type: "created"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := (&OutboxService{Store: store}).RecordComment(ctx, ContinueRequestRecord{
		TaskID: "resolve-collide-task", CommentID: "c-collide", RootID: root.ID,
		NodeRunID: "nr-collide", SessionKey: "sess-legacy", Message: "旧留言", Author: "user",
	}); err != nil {
		t.Fatalf("RecordComment: %v", err)
	}
	run := &WorkflowRun{ID: "resolve-collide-run", TaskID: "resolve-collide-task", Revision: 1,
		Graph: WorkflowDefinition{Nodes: []WorkflowNode{{ID: "n", Kind: NodeKindAgent}}}}
	if err := store.CreateWorkflowRunAndSetEngine(ctx, run); err != nil {
		t.Fatalf("CreateWorkflowRun: %v", err)
	}
	if _, _, err := store.ClaimWorkflowNodeRun(ctx, NodeRun{ID: "nr-collide", RunID: run.ID, NodeID: "n", Attempt: 1, Status: NodeRunQueued}); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := store.UpsertWorkflowNodeRun(ctx, NodeRun{ID: "nr-collide", RunID: run.ID, NodeID: "n", Attempt: 1, Status: NodeRunRunning, SessionKey: "sess-wf"}); err != nil {
		t.Fatalf("mark running: %v", err)
	}

	rec, source, err := ResolveNodeRun(ctx, store, "nr-collide")
	if err != nil {
		t.Fatalf("ResolveNodeRun: %v", err)
	}
	if source != NodeRunSourceWorkflow || rec.SessionKey != "sess-wf" || rec.Status != NodeRunRunning {
		t.Fatalf("resolved = %#v source=%q, want canonical running row", rec, source)
	}
}
