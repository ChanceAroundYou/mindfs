package kanban

import (
	"context"
	"errors"
	"strings"
	"testing"

	"mindfs/server/internal/fs"
)

type scriptedWorkflowNodeRunner struct {
	ensureCalls int
	runCalls    int
	execs       []WorkflowNodeExecution
	outcomes    []WorkflowNodeResult
	errs        []error
	// onRun 在返回结果前执行，用来模拟 agent 真实改动/提交工作区。
	onRun func(WorkflowNodeExecution) error
}

func (r *scriptedWorkflowNodeRunner) EnsureWorkflowNodeSession(_ context.Context, _ WorkflowNodeExecution) (string, error) {
	r.ensureCalls++
	return "wf-attempt-session", nil
}

func (r *scriptedWorkflowNodeRunner) RunWorkflowNode(_ context.Context, exec WorkflowNodeExecution) (WorkflowNodeResult, error) {
	r.execs = append(r.execs, exec)
	if r.onRun != nil {
		if err := r.onRun(exec); err != nil {
			return WorkflowNodeResult{}, err
		}
	}
	i := r.runCalls
	r.runCalls++
	if i < len(r.errs) && r.errs[i] != nil {
		return WorkflowNodeResult{}, r.errs[i]
	}
	if i < len(r.outcomes) {
		return r.outcomes[i], nil
	}
	return WorkflowNodeResult{Outcome: WorkflowNodeDone}, nil
}

func newWorkflowAttemptFixture(t *testing.T, taskID, runID string) (*TaskStore, *Service, *WorkflowRun, WorkflowNode) {
	t.Helper()
	ctx := context.Background()
	root := fs.NewRootInfo("wf-attempt-root", "wf-attempt-root", t.TempDir())
	store, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if _, err := store.CreateTask(ctx, Task{ID: taskID, RootID: root.ID, Status: StatusPending, Stages: []StageTemplate{userStage("input")}},
		StageRun{ID: taskID + "-stage", TaskID: taskID, StageIndex: 0, StageName: "input", Role: RoleUser, Status: StageStatusPending},
		TaskEvent{ID: taskID + "-event", TaskID: taskID, Type: "created"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	node := WorkflowNode{ID: "build", Kind: NodeKindAgent, Params: map[string]any{"prompt": "实现登录页"}}
	run := &WorkflowRun{ID: runID, TaskID: taskID, Revision: 1,
		Graph: WorkflowDefinition{Nodes: []WorkflowNode{node}}}
	if err := store.CreateWorkflowRunAndSetEngine(ctx, run); err != nil {
		t.Fatalf("CreateWorkflowRunAndSetEngine: %v", err)
	}
	svc := NewService(testRoots{root: root})
	// Agent 节点默认写工作区（D10）。没有 app 级共享 gate 时按设计 fail closed，
	// 所以这个 fixture 必须注入一把，否则测到的是「没配 gate」而不是执行逻辑。
	svc.SetWriterGate(NewWriterGate())
	return store, svc, run, node
}

func attemptNodeRun(runID string) NodeRun {
	return NodeRun{ID: "nr-build-1", RunID: runID, NodeID: "build", LoopPath: "", Iteration: 1, Attempt: 1, Status: NodeRunQueued}
}

func TestWorkflowNodeAttemptFailingRunnerProjectsFail(t *testing.T) {
	ctx := context.Background()
	store, svc, run, node := newWorkflowAttemptFixture(t, "wf-fail-task", "wf-fail-run")
	runner := &scriptedWorkflowNodeRunner{errs: []error{errors.New("agent transport down")}}
	svc.SetWorkflowNodeRunner(runner)

	if _, err := svc.RunWorkflowNodeAttempt(ctx, store, run, node, attemptNodeRun(run.ID)); err == nil || !strings.Contains(err.Error(), "agent transport down") {
		t.Fatalf("RunWorkflowNodeAttempt err = %v, want transport failure", err)
	}
	if runner.runCalls != 1 {
		t.Fatalf("runner called %d times, want exactly 1", runner.runCalls)
	}
	final, err := store.WorkflowNodeRunByKey(ctx, run.ID, "build", "", 1, 1)
	if err != nil {
		t.Fatalf("WorkflowNodeRunByKey: %v", err)
	}
	if final.Status != NodeRunFail || final.FinishedAt.IsZero() {
		t.Fatalf("node run = %#v, want fail with finished_at", final)
	}
	if final.SessionKey != "wf-attempt-session" {
		t.Fatalf("node run session = %q, want bound before the turn", final.SessionKey)
	}
	if reason, _ := final.Output["error"].(string); !strings.Contains(reason, "agent transport down") {
		t.Fatalf("node run output = %#v, want retained error", final.Output)
	}
	task, err := store.GetTask(ctx, run.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != StatusFail {
		t.Fatalf("task status = %q, want fail", task.Status)
	}
	acts, err := store.ListActivities(ctx, run.TaskID)
	if err != nil {
		t.Fatalf("ListActivities: %v", err)
	}
	if len(acts) != 1 || acts[0].Type != ActivityTypeAttention {
		t.Fatalf("activities = %#v, want one attention entry", acts)
	}
	if acts[0].RunID != run.ID || acts[0].NodeRunID != final.ID {
		t.Fatalf("activity correlation = run %q node %q, want %q/%q", acts[0].RunID, acts[0].NodeRunID, run.ID, final.ID)
	}
	if !strings.Contains(acts[0].Body, "agent transport down") {
		t.Fatalf("activity body = %q, want failure reason", acts[0].Body)
	}
	events, err := store.ListWorkflowRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatalf("ListWorkflowRunEvents: %v", err)
	}
	var types []string
	for _, ev := range events {
		types = append(types, ev.Type)
	}
	want := []string{WorkflowEventNodeQueued, WorkflowEventNodeStarted, WorkflowEventNodeSessionBound, WorkflowEventNodeFailed}
	if strings.Join(types, ",") != strings.Join(want, ",") {
		t.Fatalf("ledger = %v, want %v", types, want)
	}
}

func TestWorkflowNodeAttemptBlockedAndSilentProjectWaitingUser(t *testing.T) {
	cases := []struct {
		name    string
		outcome WorkflowNodeOutcome
		reason  string
		event   string
	}{
		{"blocked", WorkflowNodeBlocked, "缺权限", WorkflowEventNodeBlocked},
		{"silent", WorkflowNodeSilent, "没有回报完成", WorkflowEventNodeSilent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store, svc, run, node := newWorkflowAttemptFixture(t, "wf-"+tc.name+"-task", "wf-"+tc.name+"-run")
			runner := &scriptedWorkflowNodeRunner{outcomes: []WorkflowNodeResult{{Outcome: tc.outcome, Reason: tc.reason}}}
			svc.SetWorkflowNodeRunner(runner)

			got, err := svc.RunWorkflowNodeAttempt(ctx, store, run, node, attemptNodeRun(run.ID))
			if err != nil {
				t.Fatalf("RunWorkflowNodeAttempt: %v", err)
			}
			if got.Status != NodeRunWaitingUser {
				t.Fatalf("node run status = %q, want waiting_user (never success)", got.Status)
			}
			if got.Output["outcome"] != string(tc.outcome) || got.Output["reason"] != tc.reason {
				t.Fatalf("node output = %#v, want outcome/reason", got.Output)
			}
			task, err := store.GetTask(ctx, run.TaskID)
			if err != nil {
				t.Fatalf("GetTask: %v", err)
			}
			if task.Status != StatusWaitingUser {
				t.Fatalf("task status = %q, want waiting_user", task.Status)
			}
			acts, err := store.ListActivities(ctx, run.TaskID)
			if err != nil {
				t.Fatalf("ListActivities: %v", err)
			}
			if len(acts) != 1 || acts[0].Type != ActivityTypeAttention || !strings.Contains(acts[0].Body, tc.reason) {
				t.Fatalf("activities = %#v, want attention carrying %q", acts, tc.reason)
			}
			events, err := store.ListWorkflowRunEvents(ctx, run.ID)
			if err != nil {
				t.Fatalf("ListWorkflowRunEvents: %v", err)
			}
			last := events[len(events)-1]
			if last.Type != tc.event {
				t.Fatalf("last ledger event = %q, want %q", last.Type, tc.event)
			}
		})
	}
}

func TestWorkflowNodeAttemptDispatchesExactlyOnceAcrossRetries(t *testing.T) {
	ctx := context.Background()
	store, svc, run, node := newWorkflowAttemptFixture(t, "wf-once-task", "wf-once-run")
	runner := &scriptedWorkflowNodeRunner{outcomes: []WorkflowNodeResult{{Outcome: WorkflowNodeDone, Output: map[string]any{"summary": "ok"}}}}
	svc.SetWorkflowNodeRunner(runner)
	nr := attemptNodeRun(run.ID)

	first, err := svc.RunWorkflowNodeAttempt(ctx, store, run, node, nr)
	if err != nil {
		t.Fatalf("first attempt: %v", err)
	}
	if first.Status != NodeRunSuccess {
		t.Fatalf("first attempt status = %q, want success", first.Status)
	}
	// 重试同一个 key 必须落到已有行，不得再调用一次外部回合。
	second, err := svc.RunWorkflowNodeAttempt(ctx, store, run, node, nr)
	if err != nil {
		t.Fatalf("second attempt: %v", err)
	}
	if second.ID != first.ID || second.Status != first.Status {
		t.Fatalf("second attempt = %#v, want persisted row %#v", second, first)
	}
	if runner.runCalls != 1 || runner.ensureCalls != 1 {
		t.Fatalf("runner dispatched run=%d ensure=%d, want exactly 1/1", runner.runCalls, runner.ensureCalls)
	}
}

func TestWorkflowNodeAttemptClaimFailureDoesNotDispatch(t *testing.T) {
	ctx := context.Background()
	store, svc, run, node := newWorkflowAttemptFixture(t, "wf-claim-task", "wf-claim-run")
	runner := &scriptedWorkflowNodeRunner{}
	svc.SetWorkflowNodeRunner(runner)
	// 关掉 DB 让 claim 真实失败：任何外部回合都不允许在 claim 提交前发生。
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := svc.RunWorkflowNodeAttempt(ctx, store, run, node, attemptNodeRun(run.ID)); err == nil {
		t.Fatal("attempt on closed store must fail")
	}
	if runner.ensureCalls != 0 || runner.runCalls != 0 {
		t.Fatalf("runner dispatched after claim failure: ensure=%d run=%d", runner.ensureCalls, runner.runCalls)
	}
}
