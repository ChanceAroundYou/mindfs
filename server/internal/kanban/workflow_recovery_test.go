package kanban

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWorkflowRuntimeCompleteAndFailFreezeOpenWork(t *testing.T) {
	for _, tc := range []struct {
		name       string
		call       func(*Service, MoveInput) (TaskDetail, error)
		wantStatus string
		wantEvent  string
	}{
		{
			name:       "complete",
			call:       func(s *Service, in MoveInput) (TaskDetail, error) { return s.Complete(context.Background(), in) },
			wantStatus: StatusSuccess,
			wantEvent:  WorkflowEventRunCompleted,
		},
		{
			name:       "fail",
			call:       func(s *Service, in MoveInput) (TaskDetail, error) { return s.Fail(context.Background(), in) },
			wantStatus: StatusFail,
			wantEvent:  WorkflowEventRunFailed,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			svc, root, store, task, run := newRuntimeHarness(t, []StageTemplate{userStage("输入")}, agentGraph("a", "b"), "run-"+tc.name)
			runner := newRuntimeRunner(0)
			runner.release = make(chan struct{})
			svc.SetWorkflowNodeRunner(runner)
			canceller := &runtimeCanceller{}
			svc.SetWorkflowNodeCanceller(canceller)

			if err := svc.WakeWorkflowRun(ctx, root.ID, task.ID, "test"); err != nil {
				t.Fatalf("Wake: %v", err)
			}
			waitRuntimeStarted(t, runner)
			if _, _, err := store.PostComment(ctx, PostCommentInput{
				TaskID: task.ID, CommentID: "c-" + tc.name, Author: "user", Body: "排队中的留言",
			}); err != nil {
				t.Fatalf("PostComment: %v", err)
			}

			detail, err := tc.call(svc, MoveInput{RootID: root.ID, TaskID: task.ID, Reason: "operator_" + tc.name})
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if detail.Task.Status != tc.wantStatus {
				t.Fatalf("task status = %q, want %q", detail.Task.Status, tc.wantStatus)
			}
			calls := canceller.snapshot()
			if len(calls) != 1 || calls[0].SessionKey == "" {
				t.Fatalf("canceller calls = %+v, want one bound session interruption", calls)
			}
			nr, err := store.WorkflowNodeRunByKey(ctx, run.ID, "a", "", 0, 1)
			if err != nil {
				t.Fatalf("node a: %v", err)
			}
			if nr.Status != NodeRunCancelled {
				t.Fatalf("node a status = %q, want cancelled (evidence must not be rewritten)", nr.Status)
			}
			if _, err := store.WorkflowNodeRunByKey(ctx, run.ID, "b", "", 0, 1); err == nil {
				t.Fatal("node b was claimed after operator finish")
			}
			blocked, err := (&OutboxService{Store: store}).GetRequest(ctx, "c-"+tc.name)
			if err != nil {
				t.Fatalf("GetRequest: %v", err)
			}
			if blocked.Status != OutboxBlocked {
				t.Fatalf("comment status = %q, want blocked", blocked.Status)
			}
			events, err := store.ListWorkflowRunEvents(ctx, run.ID)
			if err != nil {
				t.Fatalf("ListWorkflowRunEvents: %v", err)
			}
			found := false
			for _, ev := range events {
				if ev.Type == tc.wantEvent {
					found = true
				}
			}
			if !found {
				t.Fatalf("run events = %+v, want %s", events, tc.wantEvent)
			}

			close(runner.release)
			waitRuntimeIdle(t, svc, root.ID, task.ID)
			if _, err := store.WorkflowNodeRunByKey(ctx, run.ID, "b", "", 0, 1); err == nil {
				t.Fatal("node b appeared after the driver drained")
			}
			final, err := store.GetTask(ctx, task.ID)
			if err != nil {
				t.Fatalf("GetTask: %v", err)
			}
			if final.Status != tc.wantStatus {
				t.Fatalf("final task status = %q, want %q", final.Status, tc.wantStatus)
			}
		})
	}
}

// Step 4: startup reconciliation matrix. queued-not-sent is safe to resume;
// a bound session with complete evidence resumes that session; an ambiguous
// external side effect becomes unknown/needs-user and never auto-replays.
func TestWorkflowRuntimeStartupReconciliationMatrix(t *testing.T) {
	t.Run("queued_not_sent_resumes", func(t *testing.T) {
		ctx := context.Background()
		svc, root, store, task, run := newRuntimeHarness(t, []StageTemplate{userStage("输入")}, agentGraph("build"), "run-start-queued")
		runner := newRuntimeRunner(-1)
		svc.SetWorkflowNodeRunner(runner)
		nr := workflowAttemptNodeRun(run, run.Graph.Nodes[0])
		if _, won, err := store.ClaimWorkflowNodeRun(ctx, nr); err != nil || !won {
			t.Fatalf("claim queued row: won=%v err=%v", won, err)
		}

		plan, err := svc.RecoverOnStartupForRoot(ctx, root.ID, fakeSessionProbe{found: true, resumable: true})
		if err != nil {
			t.Fatalf("RecoverOnStartupForRoot: %v", err)
		}
		if len(plan.ResumeNodeRunIDs) != 1 || plan.ResumeNodeRunIDs[0] != nr.ID {
			t.Fatalf("resume node runs = %v, want [%s]", plan.ResumeNodeRunIDs, nr.ID)
		}
		waitRuntimeIdle(t, svc, root.ID, task.ID)
		if runner.calls() != 1 {
			t.Fatalf("runner calls = %d, want 1 (queued-not-sent row is safe to begin)", runner.calls())
		}
	})

	t.Run("bound_session_with_evidence_resumes_same_session", func(t *testing.T) {
		ctx := context.Background()
		svc, root, store, task, run := newRuntimeHarness(t, []StageTemplate{userStage("输入")}, agentGraph("build"), "run-start-resume")
		runner := newRuntimeRunner(-1)
		svc.SetWorkflowNodeRunner(runner)
		nr := workflowAttemptNodeRun(run, run.Graph.Nodes[0])
		claimed, won, err := store.ClaimWorkflowNodeRun(ctx, nr)
		if err != nil || !won {
			t.Fatalf("claim: won=%v err=%v", won, err)
		}
		if err := store.BeginWorkflowNodeRun(ctx, claimed, time.Now().UTC()); err != nil {
			t.Fatalf("BeginWorkflowNodeRun: %v", err)
		}
		if err := store.BindWorkflowNodeSession(ctx, claimed, "sess-known"); err != nil {
			t.Fatalf("BindWorkflowNodeSession: %v", err)
		}

		plan, err := svc.RecoverOnStartupForRoot(ctx, root.ID, fakeSessionProbe{found: true, resumable: true})
		if err != nil {
			t.Fatalf("RecoverOnStartupForRoot: %v", err)
		}
		if len(plan.ResumeNodeRunIDs) != 1 || plan.NeedsUser {
			t.Fatalf("plan = %+v, want resume without needs-user", plan)
		}
		waitRuntimeIdle(t, svc, root.ID, task.ID)
		sessions := runner.sessionSnapshot()
		if len(sessions) != 1 || sessions[0] != "sess-known" {
			t.Fatalf("resumed sessions = %v, want [sess-known]", sessions)
		}
	})

	t.Run("ambiguous_side_effect_needs_user_without_replay", func(t *testing.T) {
		ctx := context.Background()
		svc, root, store, task, run := newRuntimeHarness(t, []StageTemplate{userStage("输入")}, agentGraph("build"), "run-start-unknown")
		runner := newRuntimeRunner(-1)
		svc.SetWorkflowNodeRunner(runner)
		nr := workflowAttemptNodeRun(run, run.Graph.Nodes[0])
		claimed, won, err := store.ClaimWorkflowNodeRun(ctx, nr)
		if err != nil || !won {
			t.Fatalf("claim: won=%v err=%v", won, err)
		}
		if err := store.BeginWorkflowNodeRun(ctx, claimed, time.Now().UTC()); err != nil {
			t.Fatalf("BeginWorkflowNodeRun: %v", err)
		}
		if err := store.BindWorkflowNodeSession(ctx, claimed, "sess-ambiguous"); err != nil {
			t.Fatalf("BindWorkflowNodeSession: %v", err)
		}

		plan, err := svc.RecoverOnStartupForRoot(ctx, root.ID, fakeSessionProbe{found: true, resumable: false})
		if err != nil {
			t.Fatalf("RecoverOnStartupForRoot: %v", err)
		}
		if !plan.NeedsUser {
			t.Fatal("ambiguous external side effect must need user")
		}
		waitRuntimeIdle(t, svc, root.ID, task.ID)
		if runner.calls() != 0 {
			t.Fatalf("runner calls = %d, want 0 (unknown is never auto-replayed)", runner.calls())
		}
		final, err := store.WorkflowNodeRunByKey(ctx, run.ID, "build", "", 0, 1)
		if err != nil {
			t.Fatalf("WorkflowNodeRunByKey: %v", err)
		}
		if final.Status != NodeRunUnknown {
			t.Fatalf("node status = %q, want unknown", final.Status)
		}
		after, err := store.GetTask(ctx, task.ID)
		if err != nil {
			t.Fatalf("GetTask: %v", err)
		}
		if after.Status != StatusWaitingUser {
			t.Fatalf("task status = %q, want waiting_user", after.Status)
		}
	})

	t.Run("paused_task_is_reconciled_but_not_woken_or_overwritten", func(t *testing.T) {
		ctx := context.Background()
		svc, root, store, task, run := newRuntimeHarness(t, []StageTemplate{userStage("输入")}, agentGraph("build"), "run-start-paused")
		runner := newRuntimeRunner(-1)
		svc.SetWorkflowNodeRunner(runner)
		nr := workflowAttemptNodeRun(run, run.Graph.Nodes[0])
		if _, won, err := store.ClaimWorkflowNodeRun(ctx, nr); err != nil || !won {
			t.Fatalf("claim queued row: won=%v err=%v", won, err)
		}
		if err := store.UpdateTaskStatus(ctx, task.ID, StatusPaused, false); err != nil {
			t.Fatalf("UpdateTaskStatus: %v", err)
		}

		plan, err := svc.RecoverOnStartupForRoot(ctx, root.ID, fakeSessionProbe{found: true, resumable: true})
		if err != nil {
			t.Fatalf("RecoverOnStartupForRoot: %v", err)
		}
		if len(plan.ResumeNodeRunIDs) != 1 || plan.ResumeNodeRunIDs[0] != nr.ID {
			t.Fatalf("resume node runs = %v, want [%s]", plan.ResumeNodeRunIDs, nr.ID)
		}
		if plan.NeedsUser {
			t.Fatalf("paused queued row must not be reported as unknown: %+v", plan)
		}
		waitRuntimeIdle(t, svc, root.ID, task.ID)
		if runner.calls() != 0 {
			t.Fatalf("runner calls = %d, want 0 (paused tasks are never woken)", runner.calls())
		}
		after, err := store.GetTask(ctx, task.ID)
		if err != nil || after.Status != StatusPaused {
			t.Fatalf("task status = %#v err=%v, want paused", after, err)
		}
		row, err := store.WorkflowNodeRunByKey(ctx, run.ID, "build", "", 0, 1)
		if err != nil || row.Status != NodeRunQueued {
			t.Fatalf("queued row = %#v err=%v, want requeued and untouched", row, err)
		}
	})

}

// Step 5: explicit retry creates a new attempt linked to the original NodeRun,
// leaves the original evidence and comment identity untouched.
func TestWorkflowRuntimeExplicitRetryLinksNewAttemptWithoutMutatingEvidence(t *testing.T) {
	ctx := context.Background()
	svc, root, store, task, run := newRuntimeHarness(t, []StageTemplate{userStage("输入")}, agentGraph("build"), "run-retry")
	runner := newRuntimeRunner(-1)
	runner.errs = []error{errors.New("first attempt failed")}
	svc.SetWorkflowNodeRunner(runner)

	if err := svc.WakeWorkflowRun(ctx, root.ID, task.ID, "test"); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	waitRuntimeIdle(t, svc, root.ID, task.ID)
	orig, err := store.WorkflowNodeRunByKey(ctx, run.ID, "build", "", 0, 1)
	if err != nil {
		t.Fatalf("original node run: %v", err)
	}
	if orig.Status != NodeRunFail {
		t.Fatalf("original status = %q, want fail", orig.Status)
	}
	originalOutput := orig.Output["error"]
	if _, err := (&OutboxService{Store: store}).RecordComment(ctx, ContinueRequestRecord{
		ID: "cr-retry", TaskID: task.ID, CommentID: "c-orig", RootID: root.ID,
		NodeRunID: orig.ID, SessionKey: orig.SessionKey, Message: "原始留言", Author: "user",
	}); err != nil {
		t.Fatalf("RecordComment: %v", err)
	}

	retried, err := svc.CreateWorkflowNodeAttempt(ctx, root.ID, task.ID, orig.ID, "user_retry")
	if err != nil {
		t.Fatalf("CreateWorkflowNodeAttempt: %v", err)
	}
	if retried.ID == orig.ID || retried.Attempt != 2 || retried.PrevAttemptID != orig.ID {
		t.Fatalf("retry row = %+v, want attempt 2 linked to %s", retried, orig.ID)
	}
	waitRuntimeIdle(t, svc, root.ID, task.ID)

	original, err := store.WorkflowNodeRunByKey(ctx, run.ID, "build", "", 0, 1)
	if err != nil {
		t.Fatalf("reload original: %v", err)
	}
	if original.Status != NodeRunFail || original.Output["error"] != originalOutput {
		t.Fatalf("original evidence changed: status=%q output=%v", original.Status, original.Output)
	}
	final, err := store.WorkflowNodeRunByKey(ctx, run.ID, "build", "", 0, 2)
	if err != nil {
		t.Fatalf("retry node run: %v", err)
	}
	if final.Status != NodeRunSuccess || final.PrevAttemptID != orig.ID {
		t.Fatalf("retry final = %+v, want success linked to original", final)
	}
	comment, err := (&OutboxService{Store: store}).GetRequest(ctx, "cr-retry")
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if comment.CommentID != "c-orig" || comment.NodeRunID != orig.ID || comment.Message != "原始留言" {
		t.Fatalf("comment identity changed: %+v", comment)
	}
	if runner.calls() != 2 {
		t.Fatalf("runner calls = %d, want 2 (fail + retry)", runner.calls())
	}
}
