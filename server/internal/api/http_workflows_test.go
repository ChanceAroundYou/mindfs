package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"mindfs/server/internal/fs"
	"mindfs/server/internal/kanban"
)

func workflowsHandler(t *testing.T) (http.Handler, *kanban.Service, string) {
	t.Helper()
	root := fs.NewRootInfo("root", "root", t.TempDir())
	svc := kanban.NewService(singleRootProviderT{root: root})
	svc.SetWriterGate(kanban.NewWriterGate())
	// 生产 app 层总会注入共享 gate；写节点缺 gate 时按设计 fail closed。
	// 测试组合不注入就会把「没有 gate」当成被测执行行为。
	svc.SetWriterGate(kanban.NewWriterGate())
	svc.Workflows = kanban.NewWorkflowStoreAt(t.TempDir())
	return (&HTTPHandler{AppContext: &AppContext{Kanban: svc}}).Routes(), svc, root.ID
}

const wfGraphJSON = `{"nodes":[{"id":"a","name":"a","kind":"agent","params":{"prompt":"do it"}}]}`

// fakeWorkflowRunner lets an API test drive a migrated run through the real
// per-task driver without starting an agent CLI.
type fakeWorkflowRunner struct {
	sessionKey string
	errs       []error
	calls      int
}

func (f *fakeWorkflowRunner) EnsureWorkflowNodeSession(context.Context, kanban.WorkflowNodeExecution) (string, error) {
	return f.sessionKey, nil
}

func (f *fakeWorkflowRunner) RunWorkflowNode(context.Context, kanban.WorkflowNodeExecution) (kanban.WorkflowNodeResult, error) {
	f.calls++
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		return kanban.WorkflowNodeResult{}, err
	}
	return kanban.WorkflowNodeResult{Outcome: kanban.WorkflowNodeDone, SessionKey: f.sessionKey, Output: map[string]any{"summary": "ok"}}, nil
}

func doReq(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestWorkflowDraftCreateUpdatePublish(t *testing.T) {
	h, _, _ := workflowsHandler(t)
	// create
	rec := doReq(t, h, http.MethodPost, "/api/workflows", `{"workflow_id":"wf1","author":"agent","graph":`+wfGraphJSON+`}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	// update with correct base
	rec = doReq(t, h, http.MethodPut, "/api/workflows/wf1", `{"base_revision":"r1","author":"agent","graph":`+wfGraphJSON+`}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", rec.Code, rec.Body.String())
	}
	// stale base -> 409
	rec = doReq(t, h, http.MethodPut, "/api/workflows/wf1", `{"base_revision":"r1","author":"agent","graph":`+wfGraphJSON+`}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale update = %d want 409", rec.Code)
	}
	// publish with stale confirm -> 409
	rec = doReq(t, h, http.MethodPost, "/api/workflows/wf1/publish", `{"confirmed_revision":"r1","published_by":"user"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale publish = %d want 409 body=%s", rec.Code, rec.Body.String())
	}
	// publish with current -> 200
	rec = doReq(t, h, http.MethodPost, "/api/workflows/wf1/publish", `{"confirmed_revision":"r2","published_by":"user"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("publish status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Version struct {
			Version string `json:"version"`
		} `json:"version"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Version.Version != "wf1:v2" {
		t.Fatalf("bad publish response %s err=%v", rec.Body.String(), err)
	}
	// version immutable & fetchable
	rec = doReq(t, h, http.MethodGet, "/api/workflows/versions/wf1:v2", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get version status=%d", rec.Code)
	}
}

func TestWorkflowDraftLoopInnerGraphRoundTrip(t *testing.T) {
	const loopGraph = `{"nodes":[{"id":"loop","name":"retry","kind":"loop","params":{"max_iterations":3,"exit_expression":"check.output.pass","graph":{"nodes":[{"id":"work","kind":"agent","params":{"agent":"claude"},"position":{"x":10,"y":20}},{"id":"check","kind":"condition","params":{"expression":"work.output.ok","routes":["pass","fail"]},"position":{"x":240,"y":20}}],"edges":[{"from":"work","to":"check"}]}},"position":{"x":60,"y":70}}]}`
	h, _, _ := workflowsHandler(t)
	assertLoop := func(stage, body string) string {
		t.Helper()
		var out struct {
			Draft struct {
				Revision string `json:"revision"`
				Graph    struct {
					Nodes []struct {
						ID     string         `json:"id"`
						Params map[string]any `json:"params"`
					} `json:"nodes"`
				} `json:"graph"`
			} `json:"draft"`
		}
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatalf("%s: bad snapshot %s: %v", stage, body, err)
		}
		if len(out.Draft.Graph.Nodes) != 1 || out.Draft.Graph.Nodes[0].ID != "loop" {
			t.Fatalf("%s: loop node lost: %s", stage, body)
		}
		params := out.Draft.Graph.Nodes[0].Params
		if params["max_iterations"] != float64(3) || params["exit_expression"] != "check.output.pass" {
			t.Fatalf("%s: loop config lost: %v", stage, params)
		}
		inner, ok := params["graph"].(map[string]any)
		if !ok {
			t.Fatalf("%s: inner graph lost: %v", stage, params)
		}
		nodes, _ := inner["nodes"].([]any)
		edges, _ := inner["edges"].([]any)
		if len(nodes) != 2 || len(edges) != 1 {
			t.Fatalf("%s: inner graph shape changed: %v", stage, inner)
		}
		first, _ := nodes[0].(map[string]any)
		pos, _ := first["position"].(map[string]any)
		if pos["x"] != float64(10) || pos["y"] != float64(20) {
			t.Fatalf("%s: inner positions lost: %v", stage, first)
		}
		return out.Draft.Revision
	}
	rec := doReq(t, h, http.MethodPost, "/api/workflows", `{"workflow_id":"wf-loop","author":"user","graph":`+loopGraph+`}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodGet, "/api/workflows/wf-loop/snapshot", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("snapshot status=%d body=%s", rec.Code, rec.Body.String())
	}
	rev := assertLoop("reopen", rec.Body.String())
	rec = doReq(t, h, http.MethodPut, "/api/workflows/wf-loop", `{"base_revision":"`+rev+`","author":"user","graph":`+loopGraph+`}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodGet, "/api/workflows/wf-loop/snapshot", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("reopen status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertLoop("after update", rec.Body.String())
}

func TestWorkflowRunGetRejectsMismatchedRoot(t *testing.T) {
	h, svc, rootID := workflowsHandler(t)
	detail, err := svc.CreateTask(context.Background(), kanban.CreateTaskInput{RootID: rootID, Stages: []kanban.StageTemplate{{Name: "input", Role: kanban.RoleUser}}, Input: ""})
	if err != nil {
		t.Fatal(err)
	}
	// The task belongs to rootID; specifying another root must not reveal its run or fall back to a shared store.
	path := "/api/tasks/" + detail.Task.ID + "/workflow-run?root_id=other-root"
	rec := doReq(t, h, http.MethodGet, path, "")
	if rec.Code == http.StatusOK {
		t.Fatalf("mismatched root unexpectedly read task: %s", rec.Body.String())
	}
}

func TestWorkflowRunCreateAndEditCAS(t *testing.T) {
	h, svc, rootID := workflowsHandler(t)
	doReq(t, h, http.MethodPost, "/api/workflows", `{"workflow_id":"wf2","author":"agent","graph":`+wfGraphJSON+`}`)
	doReq(t, h, http.MethodPost, "/api/workflows/wf2/publish", `{"confirmed_revision":"r1","published_by":"user"}`)
	detail, err := svc.CreateTask(context.Background(), kanban.CreateTaskInput{RootID: rootID, Stages: []kanban.StageTemplate{{Name: "input", Role: kanban.RoleUser}}, Input: ""}) // empty to avoid triggering background activity goroutine
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	// Explicit migration must materialize the run on the task node, not in WorkflowStore.
	svc.SetWorkflowNodeRunner(&fakeWorkflowRunner{sessionKey: "session-a"})
	rec := doReq(t, h, http.MethodPost, "/api/tasks/"+detail.Task.ID+"/migrate-workflow", `{"root_id":"`+rootID+`","version":"wf2:v1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("migrate status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Run struct {
			ID string `json:"id"`
		} `json:"run"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Run.ID == "" {
		t.Fatalf("bad migrate response %s", rec.Body.String())
	}
	// Run GET is task-node scoped; query includes root_id and task_id, so a control-plane
	// WorkflowStore cannot accidentally serve a run from a different data node.
	path := "/api/tasks/" + detail.Task.ID + "/workflow-run?root_id=" + rootID
	rec = doReq(t, h, http.MethodGet, path, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("task-node run get status=%d body=%s", rec.Code, rec.Body.String())
	}
	// Persist one real NodeRun on the task-node DB via the driver, then verify
	// the scoped GET returns it.
	if err := svc.WaitWorkflowRunIdle(context.Background(), rootID, detail.Task.ID); err != nil {
		t.Fatal(err)
	}
	nodesPath := "/api/tasks/" + detail.Task.ID + "/workflow-run/nodes?root_id=" + rootID
	rec = doReq(t, h, http.MethodGet, nodesPath, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("node runs status=%d body=%s", rec.Code, rec.Body.String())
	}
	var nodeOut struct {
		NodeRuns []kanban.NodeRun `json:"node_runs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &nodeOut); err != nil || len(nodeOut.NodeRuns) != 1 || nodeOut.NodeRuns[0].SessionKey != "session-a" {
		t.Fatalf("bad node runs response %s err=%v", rec.Body.String(), err)
	}
	// Stale graph edit on the task-node snapshot must conflict.
	rec = doReq(t, h, http.MethodPut, path, `{"revision":999,"graph":`+wfGraphJSON+`}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("edit stale task-node run = %d want 409 body=%s", rec.Code, rec.Body.String())
	}
	// Read back on the same task data plane.
	rec = doReq(t, h, http.MethodGet, path, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("task-node run reload status=%d body=%s", rec.Code, rec.Body.String())
	}
	var loaded struct {
		Run struct {
			ID    string `json:"id"`
			Graph struct {
				Nodes []struct {
					ID string `json:"id"`
				} `json:"nodes"`
			} `json:"graph"`
		} `json:"run"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &loaded); err != nil || loaded.Run.ID != out.Run.ID || len(loaded.Run.Graph.Nodes) != 1 {
		t.Fatalf("bad task-node run reload %s err=%v", rec.Body.String(), err)
	}
}

func TestWorkflowNodeRunRetryEndpointCreatesLinkedAttempt(t *testing.T) {
	h, svc, rootID := workflowsHandler(t)
	doReq(t, h, http.MethodPost, "/api/workflows", `{"workflow_id":"wf2","author":"agent","graph":`+wfGraphJSON+`}`)
	doReq(t, h, http.MethodPost, "/api/workflows/wf2/publish", `{"confirmed_revision":"r1","published_by":"user"}`)
	detail, err := svc.CreateTask(context.Background(), kanban.CreateTaskInput{RootID: rootID, Stages: []kanban.StageTemplate{{Name: "input", Role: kanban.RoleUser}}, Input: ""}) // empty to avoid triggering background activity goroutine
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
func TestWorkflowTaskCompleteAndFailFreezeRun(t *testing.T) {
	for _, action := range []struct {
		verb   string
		status string
	}{{"complete", kanban.StatusSuccess}, {"fail", kanban.StatusFail}} {
		t.Run(action.verb, func(t *testing.T) {
			h, svc, rootID := workflowsHandler(t)
			detail, err := svc.CreateTask(context.Background(), kanban.CreateTaskInput{
				RootID: rootID,
				Stages: []kanban.StageTemplate{{Name: "approval", Role: kanban.RoleUser, PromptTemplate: "approve?"}},
				Input:  "", // empty to avoid triggering background activity goroutine
			})
			if err != nil {
				t.Fatalf("CreateTask: %v", err)
			}
			rec := doReq(t, h, http.MethodPost, "/api/tasks/"+detail.Task.ID+"/migrate-workflow", `{"root_id":"`+rootID+`"}`)
			if rec.Code != http.StatusOK {
				t.Fatalf("migrate status=%d body=%s", rec.Code, rec.Body.String())
			}
			var migrated struct {
				Run struct {
					Graph struct {
						Nodes []struct {
							ID string `json:"id"`
						} `json:"nodes"`
					} `json:"graph"`
				} `json:"run"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &migrated); err != nil || len(migrated.Run.Graph.Nodes) != 1 {
				t.Fatalf("migrate response %s err=%v", rec.Body.String(), err)
			}
			gateID := migrated.Run.Graph.Nodes[0].ID
			if err := svc.WaitWorkflowRunIdle(context.Background(), rootID, detail.Task.ID); err != nil {
				t.Fatalf("wait idle: %v", err)
			}

			rec = doReq(t, h, http.MethodPost, "/api/tasks/"+detail.Task.ID+"/"+action.verb, `{"root_id":"`+rootID+`","reason":"operator_`+action.verb+`"}`)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s status=%d body=%s", action.verb, rec.Code, rec.Body.String())
			}
			var moved kanban.TaskDetail
			if err := json.Unmarshal(rec.Body.Bytes(), &moved); err != nil || moved.Task.Status != action.status {
				t.Fatalf("%s detail=%s err=%v, want %s", action.verb, rec.Body.String(), err, action.status)
			}
			// The gate was still waiting when the operator finished; approving it
			// afterwards must conflict instead of running the downstream node.
			rec = doReq(t, h, http.MethodPost, "/api/tasks/"+detail.Task.ID+"/human-gate", `{"root_id":"`+rootID+`","node_id":"`+gateID+`","decision":"approve"}`)
			if rec.Code != http.StatusConflict {
				t.Fatalf("gate approve after %s = %d want 409 body=%s", action.verb, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestWorkflowTaskPauseResumeCancelMapToRunLifecycle(t *testing.T) {
	h, svc, rootID := workflowsHandler(t)
	detail, err := svc.CreateTask(context.Background(), kanban.CreateTaskInput{
		RootID: rootID,
		Stages: []kanban.StageTemplate{{Name: "approval", Role: kanban.RoleUser, PromptTemplate: "approve?"}},
		Input:  "", // empty to avoid triggering background activity goroutine
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	rec := doReq(t, h, http.MethodPost, "/api/tasks/"+detail.Task.ID+"/migrate-workflow", `{"root_id":"`+rootID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("migrate status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := svc.WaitWorkflowRunIdle(context.Background(), rootID, detail.Task.ID); err != nil {
		t.Fatalf("wait idle: %v", err)
	}
	before, err := svc.GetTask(context.Background(), rootID, detail.Task.ID)
	if err != nil || before.Task.Status != kanban.StatusWaitingUser {
		t.Fatalf("after migration status=%v err=%v, want waiting_user", before.Task.Status, err)
	}

	move := func(action string) kanban.TaskDetail {
		t.Helper()
		rec := doReq(t, h, http.MethodPost, "/api/tasks/"+detail.Task.ID+"/"+action, `{"root_id":"`+rootID+`","reason":"test"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", action, rec.Code, rec.Body.String())
		}
		var out kanban.TaskDetail
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode %s: %v", action, err)
		}
		return out
	}
	if got := move("pause"); got.Task.Status != kanban.StatusPaused {
		t.Fatalf("pause status=%q", got.Task.Status)
	}
	if got := move("resume"); got.Task.Status != kanban.StatusRunning {
		t.Fatalf("resume status=%q", got.Task.Status)
	}
	if err := svc.WaitWorkflowRunIdle(context.Background(), rootID, detail.Task.ID); err != nil {
		t.Fatalf("wait resume: %v", err)
	}
	if got := move("cancel"); got.Task.Status != kanban.StatusCancelled {
		t.Fatalf("cancel status=%q", got.Task.Status)
	}
	if err := svc.WaitWorkflowRunIdle(context.Background(), rootID, detail.Task.ID); err != nil {
		t.Fatalf("wait cancel: %v", err)
	}
	final, err := svc.GetTask(context.Background(), rootID, detail.Task.ID)
	if err != nil || final.Task.Status != kanban.StatusCancelled {
		t.Fatalf("final status=%v err=%v", final.Task.Status, err)
	}
}
