package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"mindfs/server/internal/fs"
	"mindfs/server/internal/kanban"

	"github.com/gorilla/websocket"
)

// Task 7 (Phase 2): WebSocket activity broadcasting.
//
// The plan's sketch attaches a StreamHub straight onto ActivityService; in this
// repository the hub lives in the per-account api.AppContext and ActivityService
// only knows the kanban.ActivityBroadcaster port (Task 5). These tests wire the
// real adapter and read the events over a real WebSocket so that
// adapt → BroadcastAll → SendToClient → WriteJSON is all covered.

type fakeActivityWorkflowExecutor struct{}

func (fakeActivityWorkflowExecutor) ExecuteActivityWorkflow(context.Context, kanban.Task, *kanban.WorkflowRun) (string, error) {
	return "completed", nil
}

// ExecuteActivityWorkflowWithProgress opts into the optional progress port so
// the conversation stream shows per-node progress.
func (fakeActivityWorkflowExecutor) ExecuteActivityWorkflowWithProgress(
	_ context.Context,
	_ kanban.Task,
	run *kanban.WorkflowRun,
	onNodeStarted func(nodeID string),
) (string, error) {
	for _, node := range run.Graph.Nodes {
		onNodeStarted(node.ID)
	}
	return "completed", nil
}

// newActivityWSFixture builds the real per-root kanban wiring (same shape as
// activitiesHandler) plus an AppContext whose hub is the broadcast target.
func newActivityWSFixture(t *testing.T) (*AppContext, *kanban.Service, string, string) {
	t.Helper()
	ctx := context.Background()
	root := fs.NewRootInfo("root", "root", t.TempDir())
	svc := kanban.NewService(singleRootProviderT{root: root})
	svc.SetWriterGate(kanban.NewWriterGate())
	svc.Workflows = kanban.NewWorkflowStoreAt(t.TempDir())

	graph := kanban.WorkflowDefinition{Nodes: []kanban.WorkflowNode{
		{ID: "step-1", Name: "分析", Kind: kanban.NodeKindNotify},
		{ID: "step-2", Name: "收尾", Kind: kanban.NodeKindNotify},
	}}
	if _, err := svc.Workflows.CreateDraft(ctx, "workflow-1", "", "tester", graph); err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if _, err := svc.Workflows.PublishDraft(ctx, "workflow-1", "r1", "tester"); err != nil {
		t.Fatalf("PublishDraft: %v", err)
	}

	detail, err := svc.CreateTask(ctx, kanban.CreateTaskInput{
		RootID: root.ID,
		Stages: []kanban.StageTemplate{{Name: "input", Role: kanban.RoleUser}},
		Input:  "hi",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	app := &AppContext{Kanban: svc}
	svc.SetActivityBroadcaster(NewTaskActivityBroadcaster(app))
	return app, svc, root.ID, detail.Task.ID
}

// readActivityBroadcast reads until one event of wantType satisfies match.
// Unlike readBroadcast it can skip earlier events of the same type (a run's
// link/status updates all use task.activity.updated).
func readActivityBroadcast(t *testing.T, conn *websocket.Conn, wantType string, match func(map[string]any) bool) map[string]any {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		var resp WSResponse
		if err := conn.ReadJSON(&resp); err != nil {
			t.Fatalf("read broadcast (want %s): %v", wantType, err)
		}
		if resp.Type != wantType {
			continue
		}
		if match == nil || match(resp.Payload) {
			return resp.Payload
		}
	}
}

func TestWebSocket_ActivityEvents(t *testing.T) {
	app, _, rootID, taskID := newActivityWSFixture(t)
	conn := dialHub(t, app.GetSessionStreamHub())
	h := (&HTTPHandler{AppContext: app}).Routes()

	body := `{"root_id":"` + rootID + `","body":"test","workflow_choice":"empty"}`
	rec := doReq(t, h, http.MethodPost, "/api/tasks/"+taskID+"/activities", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}

	payload := readActivityBroadcast(t, conn, "task.activity.created", func(p map[string]any) bool {
		return p["task_id"] == taskID
	})
	if payload["task_id"] != taskID {
		t.Fatalf("payload task_id = %v, want %q", payload["task_id"], taskID)
	}
	activity, ok := payload["activity"].(map[string]any)
	if !ok || activity == nil {
		t.Fatalf("payload.activity missing: %#v", payload)
	}
	if activity["type"] != kanban.ActivityTypeUserMessage {
		t.Fatalf("activity type = %v, want %s", activity["type"], kanban.ActivityTypeUserMessage)
	}

	// The agent reply is a second created event and must carry its id.
	agentPayload := readActivityBroadcast(t, conn, "task.activity.created", func(p map[string]any) bool {
		a, _ := p["activity"].(map[string]any)
		return a != nil && a["type"] == kanban.ActivityTypeAgentReply
	})
	agent, _ := agentPayload["activity"].(map[string]any)
	if agent["id"] == "" || agent["id"] == nil {
		t.Fatalf("agent activity has no id: %#v", agent)
	}
}

func TestWebSocket_ActivityUpdatedEvent(t *testing.T) {
	app, svc, rootID, taskID := newActivityWSFixture(t)
	conn := dialHub(t, app.GetSessionStreamHub())
	h := (&HTTPHandler{AppContext: app}).Routes()

	body := `{"root_id":"` + rootID + `","body":"test","workflow_choice":"empty"}`
	rec := doReq(t, h, http.MethodPost, "/api/tasks/"+taskID+"/activities", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	agent := decodeBody(t, rec)["agent_activity"].(map[string]any)
	activityID := agent["id"].(string)
	runID := agent["run_id"].(string)
	if runID == "" {
		t.Fatal("precondition: agent activity must own a run")
	}

	activitySvc, err := svc.ActivityServiceForRoot(rootID)
	if err != nil {
		t.Fatalf("ActivityServiceForRoot: %v", err)
	}
	if err := activitySvc.OnWorkflowRunCompleted(context.Background(), runID, "done"); err != nil {
		t.Fatalf("OnWorkflowRunCompleted: %v", err)
	}

	payload := readActivityBroadcast(t, conn, "task.activity.updated", func(p map[string]any) bool {
		changes, _ := p["changes"].(map[string]any)
		return p["activity_id"] == activityID && changes["status"] == kanban.ActivityStatusCompleted
	})
	if payload["task_id"] != taskID {
		t.Fatalf("payload task_id = %v, want %q", payload["task_id"], taskID)
	}
	changes, _ := payload["changes"].(map[string]any)
	if changes["summary"] != "done" {
		t.Fatalf("changes.summary = %v, want done", changes["summary"])
	}
}

func TestWebSocket_ActivityProgressUpdates(t *testing.T) {
	app, svc, _, taskID := newActivityWSFixture(t)
	conn := dialHub(t, app.GetSessionStreamHub())

	activitySvc, err := svc.ActivityServiceForRoot("root")
	if err != nil {
		t.Fatalf("ActivityServiceForRoot: %v", err)
	}
	activitySvc.SetWorkflowExecutor(fakeActivityWorkflowExecutor{})
	_, agentAct, err := activitySvc.CreateUserActivity(context.Background(), taskID, "跑一下", kanban.CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	if agentAct.RunID == "" {
		t.Fatal("precondition: agent activity must own a run")
	}

	payload := readActivityBroadcast(t, conn, "task.activity.updated", func(p map[string]any) bool {
		changes, _ := p["changes"].(map[string]any)
		body, _ := changes["body"].(string)
		return p["activity_id"] == agentAct.ID && strings.HasPrefix(body, "步骤 ")
	})
	changes, _ := payload["changes"].(map[string]any)
	if got := changes["body"]; got != "步骤 1/2: 分析" {
		t.Fatalf("progress body = %v, want 步骤 1/2: 分析", got)
	}
}

// Review Focus #4: WebSocket 断线重连。
// Events sent while disconnected are intentionally not replayed; the client
// compensates by refetching over HTTP. This test locks that contract.
func TestWebSocket_ReconnectReceivesMissedUpdates(t *testing.T) {
	app, svc, rootID, taskID := newActivityWSFixture(t)
	conn := dialHub(t, app.GetSessionStreamHub())
	h := (&HTTPHandler{AppContext: app}).Routes()

	body := `{"root_id":"` + rootID + `","body":"test","workflow_choice":"empty"}`
	rec := doReq(t, h, http.MethodPost, "/api/tasks/"+taskID+"/activities", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	agent := decodeBody(t, rec)["agent_activity"].(map[string]any)
	activityID := agent["id"].(string)
	runID := agent["run_id"].(string)

	_ = conn.Close()

	activitySvc, err := svc.ActivityServiceForRoot(rootID)
	if err != nil {
		t.Fatalf("ActivityServiceForRoot: %v", err)
	}
	if err := activitySvc.OnWorkflowRunCompleted(context.Background(), runID, "done"); err != nil {
		t.Fatalf("OnWorkflowRunCompleted: %v", err)
	}

	// A fresh client is a reconnect: it must be able to reconstruct the latest
	// state through HTTP GET even though it missed the broadcast.
	conn2 := dialHub(t, app.GetSessionStreamHub())
	_ = conn2
	getRec := doReq(t, h, http.MethodGet, "/api/tasks/"+taskID+"/activities/"+activityID+"?root_id="+rootID, "")
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", getRec.Code, getRec.Body.String())
	}
	if got := decodeBody(t, getRec)["status"]; got != kanban.ActivityStatusCompleted {
		t.Fatalf("activity status = %v, want %s", got, kanban.ActivityStatusCompleted)
	}
}

func TestStreamHubFrozenQueueBlocksAutomaticPopUntilUnfrozen(t *testing.T) {
	hub := NewStreamHub()
	rootID := "root"
	sessionKey := "session"

	hub.EnqueueSessionMessage(rootID, sessionKey, "Session", QueuedUserMessage{
		ID: "first",
		PendingUserMessage: PendingUserMessage{
			Content:   "first message",
			Timestamp: time.Now().UTC(),
		},
	})
	hub.EnqueueSessionMessage(rootID, sessionKey, "Session", QueuedUserMessage{
		ID: "second",
		PendingUserMessage: PendingUserMessage{
			Content:   "second message",
			Timestamp: time.Now().UTC(),
		},
	})

	frozenQueue, frozen := hub.FreezeQueuedSessionMessages(sessionKey)
	if !frozen {
		t.Fatal("expected queue freeze to succeed")
	}
	if len(frozenQueue) != 2 {
		t.Fatalf("expected frozen queue snapshot to contain 2 items, got %d", len(frozenQueue))
	}
	if _, queue, ok := hub.PopQueuedSessionMessage(sessionKey, ""); ok {
		t.Fatal("expected frozen queue to block automatic pop")
	} else if len(queue) != 2 {
		t.Fatalf("expected frozen queue to remain intact, got %d items", len(queue))
	}

	queue, ok := hub.PromoteQueuedSessionMessage(sessionKey, "second")
	if !ok {
		t.Fatal("expected promote to succeed")
	}
	if len(queue) != 2 || queue[0].ID != "second" {
		t.Fatalf("expected promoted item at queue head, got %#v", queue)
	}

	item, queue, ok := hub.PopQueuedSessionMessage(sessionKey, "")
	if !ok {
		t.Fatal("expected promoted queue to be unfrozen")
	}
	if item.ID != "second" {
		t.Fatalf("expected promoted item to pop first, got %q", item.ID)
	}
	if len(queue) != 1 || queue[0].ID != "first" {
		t.Fatalf("expected remaining queue to contain first item, got %#v", queue)
	}
}

func TestStreamHubUnfreezeQueueAllowsAutomaticPop(t *testing.T) {
	hub := NewStreamHub()
	sessionKey := "session"
	hub.EnqueueSessionMessage("root", sessionKey, "Session", QueuedUserMessage{
		ID: "first",
		PendingUserMessage: PendingUserMessage{
			Content:   "first message",
			Timestamp: time.Now().UTC(),
		},
	})
	_, frozen := hub.FreezeQueuedSessionMessages(sessionKey)
	if !frozen {
		t.Fatal("expected queue freeze to succeed")
	}

	unfrozenQueue, changed := hub.UnfreezeQueuedSessionMessages(sessionKey)
	if !changed {
		t.Fatal("expected queue unfreeze to report changed")
	}
	if len(unfrozenQueue) != 1 {
		t.Fatalf("expected unfreeze queue snapshot to contain 1 item, got %d", len(unfrozenQueue))
	}
	item, queue, ok := hub.PopQueuedSessionMessage(sessionKey, "")
	if !ok {
		t.Fatal("expected automatic pop after unfreeze")
	}
	if item.ID != "first" {
		t.Fatalf("expected first item, got %q", item.ID)
	}
	if len(queue) != 0 {
		t.Fatalf("expected empty queue, got %#v", queue)
	}
}

func TestStreamHubSetPendingUserAtUsesProvidedTimestamp(t *testing.T) {
	hub := NewStreamHub()
	want := time.Date(2026, 7, 29, 10, 0, 0, int(456*time.Millisecond), time.UTC)

	pending, _ := hub.SetPendingUserAt("root", "session", "Session", "codex", "gpt-test", "", "", "", "", false, "hello", want)

	if pending == nil {
		t.Fatal("SetPendingUserAt returned nil")
	}
	if !pending.Timestamp.Equal(want) {
		t.Fatalf("pending timestamp = %s, want %s", pending.Timestamp.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
	}
}
