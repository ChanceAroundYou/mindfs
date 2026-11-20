package kanban

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/server/internal/fs"
)

type completionFixture struct {
	svc   *Service
	store *TaskStore
	acts  *ActivityStore
	root  fs.RootInfo
	task  Task
	run   *WorkflowRun
}

// newCompletionFixture 建一个带单一 agent 节点 build 的 Run。contract 为 nil
// 时 Run 没有完成契约（fail-closed 用例）。
func newCompletionFixture(t *testing.T, contract *CompletionContract) *completionFixture {
	t.Helper()
	ctx := context.Background()
	root := fs.NewRootInfo("root-completion", "completion", t.TempDir())
	svc := NewService(testRoots{root: root})
	store, err := svc.taskStore(root.ID)
	must(t, err, "taskStore")
	t.Cleanup(func() { _ = store.Close() })
	task := Task{ID: "task-c", RootID: root.ID, Status: StatusPending}
	if _, err := store.CreateTask(ctx, task, TaskEvent{ID: "ev-c", TaskID: task.ID, Type: "created"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	graph := wfDef(wfNode("build", NodeKindAgent))
	graph.Completion = contract
	run := &WorkflowRun{ID: "run-c", TaskID: task.ID, Version: "wf1:v1", Revision: 1, Graph: graph}
	must(t, store.CreateWorkflowRunAndSetEngine(ctx, run), "CreateWorkflowRunAndSetEngine")
	reloaded, err := store.WorkflowRunByID(ctx, run.ID)
	must(t, err, "WorkflowRunByID")
	return &completionFixture{svc: svc, store: store, acts: NewActivityStore(store.db), root: root, task: task, run: reloaded}
}

// addAttempt 追加一条 NodeRun，attempt 自动取该节点已有序号 +1。
func (f *completionFixture) addAttempt(t *testing.T, nodeID, status string, output map[string]any, worktree string) {
	t.Helper()
	ctx := context.Background()
	rows, err := f.store.ListWorkflowNodeRuns(ctx, f.run.ID)
	must(t, err, "ListWorkflowNodeRuns")
	attempt := 1
	for _, row := range rows {
		if row.NodeID == nodeID && row.Attempt >= attempt {
			attempt = row.Attempt + 1
		}
	}
	must(t, f.store.UpsertWorkflowNodeRun(ctx, NodeRun{
		ID:           fmt.Sprintf("%s-%s-%d", f.run.ID, nodeID, attempt),
		RunID:        f.run.ID,
		NodeID:       nodeID,
		Attempt:      attempt,
		Status:       status,
		Output:       output,
		WorktreePath: worktree,
	}), "UpsertWorkflowNodeRun")
}

func (f *completionFixture) evaluate(t *testing.T) CompletionReport {
	t.Helper()
	report, err := f.svc.EvaluateCompletion(context.Background(), f.store, f.task, f.run)
	if err != nil {
		t.Fatalf("EvaluateCompletion: %v", err)
	}
	return report
}

func completionItem(t *testing.T, report CompletionReport, id string) CompletionItemStatus {
	t.Helper()
	for _, item := range report.Items {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("report has no item %q: %+v", id, report.Items)
	return CompletionItemStatus{}
}

func nodeDoneContract(keys ...string) *CompletionContract {
	return &CompletionContract{Requirements: []CompletionRequirement{{
		ID:     "build-done",
		Label:  "构建节点完成",
		Kind:   CompletionKindNodeDone,
		NodeID: "build",
		Keys:   keys,
	}}}
}

func TestEvaluateCompletionNodeDone(t *testing.T) {
	ctx := context.Background()

	t.Run("latest attempt done with all keys passes", func(t *testing.T) {
		f := newCompletionFixture(t, nodeDoneContract("files", "summary"))
		f.addAttempt(t, "build", NodeRunSuccess, map[string]any{
			"outcome": "done", "files": "3 个文件", "summary": "改了 a.go 与 b.go",
		}, "")
		report := f.evaluate(t)
		if !report.Contract || !report.Passed {
			t.Fatalf("report = %+v, want contract pass", report)
		}
		if len(report.Items) != 1 || !report.Items[0].Pass {
			t.Fatalf("items = %+v, want single passing item", report.Items)
		}
		if report.Hash == "" {
			t.Fatal("contract report must carry a hash")
		}
		_ = ctx
	})

	t.Run("missing key fails and names it", func(t *testing.T) {
		f := newCompletionFixture(t, nodeDoneContract("files", "summary"))
		f.addAttempt(t, "build", NodeRunSuccess, map[string]any{
			"outcome": "done", "summary": "改了 a.go",
		}, "")
		report := f.evaluate(t)
		if report.Passed {
			t.Fatalf("report = %+v, want fail on missing key", report)
		}
		item := completionItem(t, report, "build-done")
		if item.Pass || !strings.Contains(item.Detail, "files") {
			t.Fatalf("item = %+v, want detail naming missing key files", item)
		}
	})

	t.Run("blocked outcome fails even with every key", func(t *testing.T) {
		f := newCompletionFixture(t, nodeDoneContract("files"))
		f.addAttempt(t, "build", NodeRunSuccess, map[string]any{
			"outcome": "blocked", "files": "0",
		}, "")
		if report := f.evaluate(t); report.Passed {
			t.Fatalf("report = %+v, blocked outcome must not pass", report)
		}
	})
}

func TestEvaluateCompletionOnlyLatestAttempt(t *testing.T) {
	t.Run("older success does not count once the latest fails", func(t *testing.T) {
		f := newCompletionFixture(t, nodeDoneContract("files"))
		f.addAttempt(t, "build", NodeRunSuccess, map[string]any{"outcome": "done", "files": "3"}, "")
		f.addAttempt(t, "build", NodeRunFail, map[string]any{"outcome": "blocked"}, "")
		report := f.evaluate(t)
		if report.Passed {
			t.Fatalf("report = %+v, a failed latest attempt must not pass", report)
		}
	})

	t.Run("latest success passes despite an older failure", func(t *testing.T) {
		f := newCompletionFixture(t, nodeDoneContract("files"))
		f.addAttempt(t, "build", NodeRunFail, map[string]any{"outcome": "blocked"}, "")
		f.addAttempt(t, "build", NodeRunSuccess, map[string]any{"outcome": "done", "files": "3"}, "")
		report := f.evaluate(t)
		if !report.Passed {
			t.Fatalf("report = %+v, latest success must pass", report)
		}
	})
}

func TestEvaluateCompletionNoContractFailsClosed(t *testing.T) {
	f := newCompletionFixture(t, nil)
	f.addAttempt(t, "build", NodeRunSuccess, map[string]any{"outcome": "done"}, "")
	report := f.evaluate(t)
	if report.Contract || report.Passed {
		t.Fatalf("report = %+v, want Contract=false Passed=false", report)
	}
	if len(report.Items) == 0 || report.Items[0].ID != "contract" || report.Items[0].Pass {
		t.Fatalf("items = %+v, want a failing contract item first", report.Items)
	}
	if report.Items[0].Detail == "" {
		t.Fatal("missing contract must explain itself")
	}
}

func TestEvaluateCompletionFileExistsGuards(t *testing.T) {
	f := newCompletionFixture(t, &CompletionContract{Requirements: []CompletionRequirement{
		{ID: "ok", Kind: CompletionKindFileExists, Path: "ok.txt"},
		{ID: "escape-up", Kind: CompletionKindFileExists, Path: "../etc/passwd"},
		{ID: "escape-abs", Kind: CompletionKindFileExists, Path: "/etc/passwd"},
		{ID: "escape-link", Kind: CompletionKindFileExists, Path: "escape.txt"},
		// 中间目录是符号链接时，Lstat 只看最后一段会放行整条路径。
		{ID: "escape-dir", Kind: CompletionKindFileExists, Path: "dir-link/result.txt"},
		{ID: "missing", Kind: CompletionKindFileExists, Path: "nope.txt"},
	}})
	must(t, os.WriteFile(filepath.Join(f.root.RootPath, "ok.txt"), []byte("x"), 0o600), "write ok.txt")
	outside := filepath.Join(t.TempDir(), "outside.txt")
	must(t, os.WriteFile(outside, []byte("x"), 0o600), "write outside.txt")
	must(t, os.Symlink(outside, filepath.Join(f.root.RootPath, "escape.txt")), "symlink escape.txt")
	outsideDir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(outsideDir, "result.txt"), []byte("x"), 0o600), "write outside result.txt")
	must(t, os.Symlink(outsideDir, filepath.Join(f.root.RootPath, "dir-link")), "symlink dir-link")
	t.Cleanup(func() { _ = os.Remove(filepath.Join(f.root.RootPath, "dir-link")) })

	report := f.evaluate(t)
	if report.Passed {
		t.Fatalf("report = %+v, guards must fail", report)
	}
	if item := completionItem(t, report, "ok"); !item.Pass {
		t.Fatalf("ok item = %+v, want pass", item)
	}
	for _, id := range []string{"escape-up", "escape-abs", "escape-link", "escape-dir", "missing"} {
		item := completionItem(t, report, id)
		if item.Pass {
			t.Fatalf("%s item = %+v, want fail", id, item)
		}
		if strings.TrimSpace(item.Detail) == "" {
			t.Fatalf("%s item has no detail", id)
		}
	}
}

func TestEvaluateCompletionFileExistsUsesNodeWorktree(t *testing.T) {
	f := newCompletionFixture(t, &CompletionContract{Requirements: []CompletionRequirement{{
		ID: "node-out", Kind: CompletionKindFileExists, NodeID: "build", Path: "out.txt",
	}}})
	worktree := t.TempDir()
	must(t, os.WriteFile(filepath.Join(worktree, "out.txt"), []byte("x"), 0o600), "write out.txt")
	f.addAttempt(t, "build", NodeRunSuccess, map[string]any{"outcome": "done"}, worktree)

	report := f.evaluate(t)
	if !report.Passed {
		t.Fatalf("report = %+v, node worktree file must be found", report)
	}
}

func newDirectCompletionFixture(t *testing.T) *completionFixture {
	t.Helper()
	ctx := context.Background()
	root := fs.NewRootInfo("root-direct", "direct", t.TempDir())
	svc := NewService(testRoots{root: root})
	store, err := svc.taskStore(root.ID)
	must(t, err, "taskStore")
	t.Cleanup(func() { _ = store.Close() })
	task := Task{ID: "task-d", RootID: root.ID, Status: StatusWaitingUser}
	if _, err := store.CreateTask(ctx, task, TaskEvent{ID: "ev-d", TaskID: task.ID, Type: "created"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	return &completionFixture{svc: svc, store: store, acts: NewActivityStore(store.db), root: root, task: task}
}

func (f *completionFixture) addActivity(t *testing.T, activity TaskActivity) {
	t.Helper()
	activity.TaskID = f.task.ID
	activity.RootID = f.root.ID
	must(t, f.acts.Insert(context.Background(), &activity), "Insert activity %s", activity.ID)
}

func TestEvaluateDirectCompletion(t *testing.T) {
	ctx := context.Background()

	t.Run("settled reply with no live reply and no open run passes", func(t *testing.T) {
		f := newDirectCompletionFixture(t)
		f.addActivity(t, TaskActivity{ID: "msg-1", Type: ActivityTypeUserMessage, Body: "做一下"})
		f.addActivity(t, TaskActivity{ID: "reply-1", Type: ActivityTypeAgentReply, Status: ActivityStatusCompleted, ReplyToID: "msg-1"})
		report, err := evaluateDirectCompletion(ctx, f.store, f.task)
		if err != nil {
			t.Fatalf("evaluateDirectCompletion: %v", err)
		}
		if report.Contract || !report.Passed {
			t.Fatalf("report = %+v, want direct pass without contract", report)
		}
		if len(report.Items) != 3 {
			t.Fatalf("items = %+v, want three machine items", report.Items)
		}
	})

	t.Run("live reply fails no_live_reply and reply_settled", func(t *testing.T) {
		f := newDirectCompletionFixture(t)
		f.addActivity(t, TaskActivity{ID: "msg-1", Type: ActivityTypeUserMessage, Body: "做一下"})
		f.addActivity(t, TaskActivity{ID: "reply-1", Type: ActivityTypeAgentReply, Status: ActivityStatusPending, ReplyToID: "msg-1"})
		report, err := evaluateDirectCompletion(ctx, f.store, f.task)
		if err != nil {
			t.Fatalf("evaluateDirectCompletion: %v", err)
		}
		if report.Passed {
			t.Fatalf("report = %+v, a pending reply must block completion", report)
		}
		if item := completionItem(t, report, "no_live_reply"); item.Pass {
			t.Fatalf("no_live_reply = %+v, want fail", item)
		}
		if item := completionItem(t, report, "reply_settled"); item.Pass {
			t.Fatalf("reply_settled = %+v, want fail while the latest reply is pending", item)
		}
	})

	t.Run("a later message without an answer fails reply_settled", func(t *testing.T) {
		// 复审 Important 4：只看到「有条旧回复已完成」不算答复了最新留言。
		f := newDirectCompletionFixture(t)
		f.addActivity(t, TaskActivity{ID: "msg-1", Type: ActivityTypeUserMessage, Body: "做一下"})
		f.addActivity(t, TaskActivity{ID: "reply-1", Type: ActivityTypeAgentReply, Status: ActivityStatusCompleted, ReplyToID: "msg-1"})
		f.addActivity(t, TaskActivity{ID: "msg-2", Type: ActivityTypeUserMessage, Body: "那这个呢"})
		report, err := evaluateDirectCompletion(ctx, f.store, f.task)
		if err != nil {
			t.Fatalf("evaluateDirectCompletion: %v", err)
		}
		if report.Passed {
			t.Fatalf("report = %+v, an unanswered latest message must block completion", report)
		}
		if item := completionItem(t, report, "reply_settled"); item.Pass {
			t.Fatalf("reply_settled = %+v, want fail: 最新留言还没有回复", item)
		}
	})

	t.Run("a reply to the newest message still passes", func(t *testing.T) {
		f := newDirectCompletionFixture(t)
		f.addActivity(t, TaskActivity{ID: "msg-1", Type: ActivityTypeUserMessage, Body: "做一下"})
		f.addActivity(t, TaskActivity{ID: "reply-1", Type: ActivityTypeAgentReply, Status: ActivityStatusCompleted, ReplyToID: "msg-1"})
		f.addActivity(t, TaskActivity{ID: "msg-2", Type: ActivityTypeUserMessage, Body: "那这个呢"})
		f.addActivity(t, TaskActivity{ID: "reply-2", Type: ActivityTypeAgentReply, Status: ActivityStatusCompleted, ReplyToID: "msg-2"})
		report, err := evaluateDirectCompletion(ctx, f.store, f.task)
		if err != nil {
			t.Fatalf("evaluateDirectCompletion: %v", err)
		}
		if !report.Passed {
			t.Fatalf("report = %+v, want pass", report)
		}
	})

	t.Run("open run fails no_open_run", func(t *testing.T) {
		f := newDirectCompletionFixture(t)
		f.addActivity(t, TaskActivity{ID: "msg-1", Type: ActivityTypeUserMessage, Body: "做一下"})
		f.addActivity(t, TaskActivity{ID: "reply-1", Type: ActivityTypeAgentReply, Status: ActivityStatusCompleted, ReplyToID: "msg-1"})
		graph := wfDef(wfNode("build", NodeKindAgent))
		run := &WorkflowRun{ID: "run-open", TaskID: f.task.ID, Version: "wf1:v1", Revision: 1, Graph: graph}
		must(t, f.store.CreateWorkflowRunAndSetEngine(ctx, run), "CreateWorkflowRunAndSetEngine")
		must(t, f.store.UpsertWorkflowNodeRun(ctx, NodeRun{ID: "nr-open", RunID: run.ID, NodeID: "build", Attempt: 1, Status: NodeRunRunning}), "UpsertWorkflowNodeRun")

		report, err := evaluateDirectCompletion(ctx, f.store, f.task)
		if err != nil {
			t.Fatalf("evaluateDirectCompletion: %v", err)
		}
		if report.Passed {
			t.Fatalf("report = %+v, an open run must block completion", report)
		}
		if item := completionItem(t, report, "no_open_run"); item.Pass {
			t.Fatalf("no_open_run = %+v, want fail", item)
		}
	})
}

func TestCompletionContractImmutable(t *testing.T) {
	ctx := context.Background()
	f := newCompletionFixture(t, nodeDoneContract("files"))

	changed := cloneGraph(f.run.Graph)
	changed.Completion = nodeDoneContract("files", "summary")
	err := f.store.EditWorkflowRunGraph(ctx, f.run.ID, f.run.Revision, changed)
	if err == nil || !strings.Contains(err.Error(), "不能在中途修改") {
		t.Fatalf("EditWorkflowRunGraph changing completion = %v, want 不能在中途修改", err)
	}

	grown := cloneGraph(f.run.Graph)
	grown.Nodes = append(grown.Nodes, wfNode("verify", NodeKindAgent))
	if err := f.store.EditWorkflowRunGraph(ctx, f.run.ID, f.run.Revision, grown); err != nil {
		t.Fatalf("EditWorkflowRunGraph without touching completion: %v", err)
	}

	reloaded, err := f.store.WorkflowRunByID(ctx, f.run.ID)
	must(t, err, "WorkflowRunByID")
	reloaded.Graph.Completion = nodeDoneContract()
	err = f.store.SaveWorkflowRun(ctx, reloaded)
	if err == nil || !strings.Contains(err.Error(), "不能在中途修改") {
		t.Fatalf("SaveWorkflowRun changing completion = %v, want 不能在中途修改", err)
	}
}
