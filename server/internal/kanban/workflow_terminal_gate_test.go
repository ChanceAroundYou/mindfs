package kanban

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// 末端 human_gate 是 2026-11-19 用户裁定：审批只能出现在执行中间。
//
// 规则分两层：
//   - 定义写入（草稿 / typed patch / dry-run）直接拒绝「gate 没有出边」；
//   - 已发布的旧版本（如 workflow-new-feature:v1 末端 verify）不能因此无法建
//     Run，运行期按「通过」收尾，不把用户永远停在审批上。

func terminalGateGraph() WorkflowDefinition {
	return WorkflowDefinition{Nodes: []WorkflowNode{
		{ID: "verify", Kind: NodeKindHumanGate, Params: map[string]any{"prompt": "验收"}},
	}}
}

func TestAuthoringValidationRejectsTerminalHumanGate(t *testing.T) {
	graph := terminalGateGraph()
	// 已发布旧版本仍可建 Run：基础校验不能收紧。
	if err := ValidateWorkflowDefinition(graph); err != nil {
		t.Fatalf("base validator rejected a legacy published graph: %v", err)
	}
	err := ValidateWorkflowDefinitionForAuthoring(graph)
	if err == nil || !strings.Contains(err.Error(), "verify") {
		t.Fatalf("authoring validator err = %v, want rejection naming the gate", err)
	}

	withTail := WorkflowDefinition{
		Nodes: []WorkflowNode{
			{ID: "verify", Kind: NodeKindHumanGate, Params: map[string]any{"prompt": "验收"}},
			{ID: "ship", Kind: NodeKindNotify, Params: map[string]any{"message": "已发布"}},
		},
		Edges: []WorkflowEdge{{From: "verify", To: "ship"}},
	}
	if err := ValidateWorkflowDefinitionForAuthoring(withTail); err != nil {
		t.Fatalf("gate with a downstream node rejected: %v", err)
	}
}

func TestCreateDraftRejectsTerminalHumanGate(t *testing.T) {
	ctx := context.Background()
	store := NewWorkflowStoreAt(t.TempDir())
	if _, err := store.CreateDraft(ctx, "wf-terminal", "", "tester", terminalGateGraph()); err == nil {
		t.Fatal("CreateDraft accepted a terminal human_gate")
	}
}

func TestAdvanceWorkflowRunAutoPassesTerminalHumanGate(t *testing.T) {
	ctx := context.Background()
	store, svc, run, _ := newWorkflowCommentFixture(t, "wf-auto-gate-task", "wf-auto-gate-run", terminalGateGraph())
	if err := svc.AdvanceWorkflowRun(ctx, store, run.TaskID); err != nil {
		t.Fatalf("AdvanceWorkflowRun: %v", err)
	}
	gate, err := store.WorkflowNodeRunByKey(ctx, run.ID, "verify", "", 0, 1)
	if err != nil {
		t.Fatalf("gate node run: %v", err)
	}
	if gate.Status != NodeRunSuccess {
		t.Fatalf("terminal gate status = %q, want success", gate.Status)
	}
	if approved, _ := gate.Output["auto_approved"].(bool); !approved {
		t.Fatalf("gate output = %#v, want auto_approved marker", gate.Output)
	}
	// 不再物化等待卡：用户根本看不到一张没有下游可批的审批。
	if _, err := NewActivityStore(store.db).Get(ctx, "wf:"+gate.ID+":human_waiting"); !errors.Is(err, ErrActivityNotFound) {
		t.Fatalf("waiting activity err = %v, want none", err)
	}
	reloaded, err := store.WorkflowRunByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("reload run: %v", err)
	}
	if reloaded.NodeStates["verify"] != NodeRunSuccess {
		t.Fatalf("run projection = %#v, want verify success", reloaded.NodeStates)
	}
}

// 旧版本已经停在 waiting_user 的末端 gate（#88 的 verify）必须在 driver 下一次
// 推进时被兼容收尾，而不是继续挂着审批卡。
func TestAdvanceWorkflowRunHealsParkedTerminalHumanGate(t *testing.T) {
	ctx := context.Background()
	store, svc, run, _ := newWorkflowCommentFixture(t, "wf-parked-gate-task", "wf-parked-gate-run", terminalGateGraph())
	node := terminalGateGraph().Nodes[0]
	loading, err := store.WorkflowRunByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("reload run: %v", err)
	}
	nr := workflowAttemptNodeRun(loading, node)
	nr.Status = NodeRunWaitingUser
	nr.Output = map[string]any{"prompt": "验收"}
	payload, err := marshalWorkflowJSON(map[string]any{
		"prompt": "验收", "node_id": node.ID, "node_run_id": nr.ID,
	})
	if err != nil {
		t.Fatalf("marshal gate payload: %v", err)
	}
	act := &TaskActivity{
		ID: "wf:" + nr.ID + ":human_waiting", Type: ActivityTypeHumanGate,
		Author: "system", Role: "system", Body: "等待你确认：验收",
		Status: ActivityStatusWaitingUser, PayloadJSON: payload,
	}
	if _, _, err := store.CommitWorkflowNodeTransition(ctx, loading, WorkflowNodeTransition{
		NodeRun: nr, EventType: WorkflowEventHumanWaiting, Activity: act,
	}); err != nil {
		t.Fatalf("park gate: %v", err)
	}

	if err := svc.AdvanceWorkflowRun(ctx, store, run.TaskID); err != nil {
		t.Fatalf("AdvanceWorkflowRun: %v", err)
	}
	healed, err := store.WorkflowNodeRunByKey(ctx, run.ID, "verify", "", 0, 1)
	if err != nil || healed.Status != NodeRunSuccess {
		t.Fatalf("healed gate = %#v err=%v, want success", healed, err)
	}
	card, err := NewActivityStore(store.db).Get(ctx, "wf:"+healed.ID+":human_waiting")
	if err != nil {
		t.Fatalf("gate activity: %v", err)
	}
	if card.Status != ActivityStatusCompleted {
		t.Fatalf("gate activity status = %q, want completed", card.Status)
	}
	if !strings.Contains(card.PayloadJSON, `"auto_approved":true`) {
		t.Fatalf("gate payload = %s, want auto_approved marker", card.PayloadJSON)
	}
}

func TestHumanGateApproveCarriesNoteAsUserMessage(t *testing.T) {
	ctx := context.Background()
	store, svc, run, root := newWorkflowCommentFixture(t, "wf-gate-note-task", "wf-gate-note-run", humanGateGraph())
	if err := svc.AdvanceWorkflowRun(ctx, store, run.TaskID); err != nil {
		t.Fatalf("AdvanceWorkflowRun: %v", err)
	}
	if _, err := svc.ResolveWorkflowHumanGate(ctx, root.ID, run.TaskID, "gate", "approve", "", ""); err != nil {
		t.Fatalf("approve without note: %v", err)
	}
	if got := userMessages(t, store, run.TaskID); len(got) != 0 {
		t.Fatalf("empty note created %d user messages, want 0", len(got))
	}

	store2, svc2, run2, root2 := newWorkflowCommentFixture(t, "wf-gate-note-task-2", "wf-gate-note-run-2", humanGateGraph())
	if err := svc2.AdvanceWorkflowRun(ctx, store2, run2.TaskID); err != nil {
		t.Fatalf("AdvanceWorkflowRun: %v", err)
	}
	note := "  顺便把 README 更新一下  "
	if _, err := svc2.ResolveWorkflowHumanGate(ctx, root2.ID, run2.TaskID, "gate", "approve", "", note); err != nil {
		t.Fatalf("approve with note: %v", err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := svc2.WaitWorkflowRunIdle(waitCtx, root2.ID, run2.TaskID); err != nil {
		t.Fatalf("WaitWorkflowRunIdle: %v", err)
	}
	notes := userMessages(t, store2, run2.TaskID)
	if len(notes) != 1 || notes[0].Body != "顺便把 README 更新一下" {
		t.Fatalf("note rows = %#v, want one trimmed user message", notes)
	}
	if notes[0].Status == ActivityStatusQueued {
		t.Fatal("note stayed queued after the approved round settled: it never reached the next round")
	}

	store3, svc3, run3, root3 := newWorkflowCommentFixture(t, "wf-gate-note-task-3", "wf-gate-note-run-3", humanGateGraph())
	if err := svc3.AdvanceWorkflowRun(ctx, store3, run3.TaskID); err != nil {
		t.Fatalf("AdvanceWorkflowRun: %v", err)
	}
	if _, err := svc3.ResolveWorkflowHumanGate(ctx, root3.ID, run3.TaskID, "gate", "reject", "", "不要了"); err != nil {
		t.Fatalf("reject with note: %v", err)
	}
	if got := userMessages(t, store3, run3.TaskID); len(got) != 0 {
		t.Fatalf("reject created %d note messages, want 0", len(got))
	}
}

func userMessages(t *testing.T, store *TaskStore, taskID string) []*TaskActivity {
	t.Helper()
	activities, err := NewActivityStore(store.db).List(context.Background(), taskID)
	if err != nil {
		t.Fatalf("list activities: %v", err)
	}
	var out []*TaskActivity
	for _, act := range activities {
		if act.Type == ActivityTypeUserMessage && act.Role == RoleUser {
			out = append(out, act)
		}
	}
	return out
}
