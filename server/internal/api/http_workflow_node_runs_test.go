package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"testing"

	"mindfs/server/internal/kanban"
)

// 一个任务跑过多轮时，运行视图必须能看到每一轮（任务 #87 第 1 条）。
// 引擎只绑定最新一条非终态 Run，所以端点不能只回那一条：它要按 Run 分组返回
// 全部轮次（旧的在前），平铺的 node_runs 形状继续保留。
func TestWorkflowNodeRunsListReturnsEveryRound(t *testing.T) {
	ctx := context.Background()
	h, svc, rootID := workflowsHandler(t)
	doReq(t, h, http.MethodPost, "/api/workflows", `{"workflow_id":"wf2","author":"agent","graph":`+wfGraphJSON+`}`)
	doReq(t, h, http.MethodPost, "/api/workflows/wf2/publish", `{"confirmed_revision":"r1","published_by":"user"}`)
	detail, err := svc.CreateTask(ctx, kanban.CreateTaskInput{RootID: rootID, Input: ""})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	svc.SetWorkflowNodeRunner(&fakeWorkflowRunner{sessionKey: "session-round-1"})
	rec := doReq(t, h, http.MethodPost, "/api/tasks/"+detail.Task.ID+"/migrate-workflow", `{"root_id":"`+rootID+`","version":"wf2:v1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("migrate status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := svc.WaitWorkflowRunIdle(ctx, rootID, detail.Task.ID); err != nil {
		t.Fatalf("wait idle: %v", err)
	}
	store, err := svc.TaskStoreForRoot(rootID)
	if err != nil {
		t.Fatalf("TaskStoreForRoot: %v", err)
	}
	engine, err := store.TaskEngine(ctx, detail.Task.ID)
	if err != nil || engine.WorkflowRunID == "" {
		t.Fatalf("engine=%+v err=%v", engine, err)
	}

	// 第二轮：再落一条 Run + 节点行（生产里是又一条留言被 Router 派给了工作流）。
	round2 := &kanban.WorkflowRun{
		ID:       engine.WorkflowRunID + "-round2",
		TaskID:   detail.Task.ID,
		Revision: 1,
		Graph: kanban.WorkflowDefinition{Nodes: []kanban.WorkflowNode{
			{ID: "a", Name: "a", Kind: kanban.NodeKindAgent},
		}},
	}
	if err := store.CreateWorkflowRunAndSetEngine(ctx, round2); err != nil {
		t.Fatalf("create round2 run: %v", err)
	}
	if _, claimed, err := store.ClaimWorkflowNodeRun(ctx, kanban.NodeRun{
		ID: "nr-round-2", RunID: round2.ID, NodeID: "a", Attempt: 1,
		Status: kanban.NodeRunQueued, SessionKey: "session-round-2",
	}); err != nil || !claimed {
		t.Fatalf("claim round2 node run: err=%v claimed=%v", err, claimed)
	}

	nodesPath := "/api/tasks/" + detail.Task.ID + "/workflow-run/nodes?root_id=" + rootID
	rec = doReq(t, h, http.MethodGet, nodesPath, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("node runs status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		NodeRuns []kanban.NodeRun `json:"node_runs"`
		Runs     []struct {
			ID       string           `json:"id"`
			NodeRuns []kanban.NodeRun `json:"node_runs"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if len(out.Runs) != 2 {
		t.Fatalf("runs = %d, want 2 (every round): %s", len(out.Runs), rec.Body.String())
	}
	if out.Runs[0].ID != engine.WorkflowRunID || out.Runs[1].ID != round2.ID {
		t.Fatalf("runs order = %q,%q want oldest first", out.Runs[0].ID, out.Runs[1].ID)
	}
	if len(out.Runs[1].NodeRuns) != 1 || out.Runs[1].NodeRuns[0].SessionKey != "session-round-2" {
		t.Fatalf("round2 node runs = %+v", out.Runs[1].NodeRuns)
	}
	if len(out.NodeRuns) != 2 {
		t.Fatalf("flat node_runs = %d, want 2 (shape kept)", len(out.NodeRuns))
	}
}

func TestWorkflowNodeRunRetryEndpointCreatesLinkedAttempt(t *testing.T) {
	h, svc, rootID := workflowsHandler(t)
	doReq(t, h, http.MethodPost, "/api/workflows", `{"workflow_id":"wf2","author":"agent","graph":`+wfGraphJSON+`}`)
	doReq(t, h, http.MethodPost, "/api/workflows/wf2/publish", `{"confirmed_revision":"r1","published_by":"user"}`)
	detail, err := svc.CreateTask(context.Background(), kanban.CreateTaskInput{RootID: rootID, Input: ""}) // empty to avoid triggering background activity goroutine
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	runner := &fakeWorkflowRunner{sessionKey: "session-retry", errs: []error{errors.New("first attempt failed")}}
	svc.SetWorkflowNodeRunner(runner)
	rec := doReq(t, h, http.MethodPost, "/api/tasks/"+detail.Task.ID+"/migrate-workflow", `{"root_id":"`+rootID+`","version":"wf2:v1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("migrate status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := svc.WaitWorkflowRunIdle(context.Background(), rootID, detail.Task.ID); err != nil {
		t.Fatalf("wait idle: %v", err)
	}
	nodesPath := "/api/tasks/" + detail.Task.ID + "/workflow-run/nodes?root_id=" + rootID
	rec = doReq(t, h, http.MethodGet, nodesPath, "")
	var before struct {
		NodeRuns []kanban.NodeRun `json:"node_runs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &before); err != nil || len(before.NodeRuns) != 1 || before.NodeRuns[0].Status != kanban.NodeRunFail {
		t.Fatalf("pre-retry nodes=%s err=%v", rec.Body.String(), err)
	}
	original := before.NodeRuns[0]

	retryPath := "/api/tasks/" + detail.Task.ID + "/workflow-run/nodes/" + url.PathEscape(original.ID) + "/retry"
	rec = doReq(t, h, http.MethodPost, retryPath, `{"root_id":"`+rootID+`","reason":"user_retry"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("retry status=%d body=%s", rec.Code, rec.Body.String())
	}
	var retried struct {
		NodeRun kanban.NodeRun `json:"node_run"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &retried); err != nil || retried.NodeRun.Attempt != 2 || retried.NodeRun.PrevAttemptID != original.ID {
		t.Fatalf("retried=%s err=%v", rec.Body.String(), err)
	}
	if err := svc.WaitWorkflowRunIdle(context.Background(), rootID, detail.Task.ID); err != nil {
		t.Fatalf("wait retry: %v", err)
	}
	rec = doReq(t, h, http.MethodGet, nodesPath, "")
	var after struct {
		NodeRuns []kanban.NodeRun `json:"node_runs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &after); err != nil {
		t.Fatalf("decode nodes: %v", err)
	}
	byAttempt := map[int]kanban.NodeRun{}
	for _, nr := range after.NodeRuns {
		byAttempt[nr.Attempt] = nr
	}
	if byAttempt[1].Status != kanban.NodeRunFail || byAttempt[2].Status != kanban.NodeRunSuccess || byAttempt[2].PrevAttemptID != original.ID {
		t.Fatalf("attempts after retry = %+v", after.NodeRuns)
	}
	// A terminal success row is not retryable; the API must say conflict, not
	// silently create attempt 3.
	rec = doReq(t, h, http.MethodPost, "/api/tasks/"+detail.Task.ID+"/workflow-run/nodes/"+url.PathEscape(byAttempt[2].ID)+"/retry", `{"root_id":"`+rootID+`"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("retry success = %d want 409 body=%s", rec.Code, rec.Body.String())
	}
}

// D7: a migrated task's Complete/Fail must go through the workflow runtime, not
// only rewrite the task row. The run is frozen, and a gate that was waiting can
// no longer resurrect it through the approve endpoint.
