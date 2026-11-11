package kanban

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mindfs/server/internal/fs"

	_ "modernc.org/sqlite"
)

// D16：NodeRun 自身直接持久化工作树归属，恢复/合并时不需要再从旁路推断。
// 这里同时钉住「旧库新增列」的迁移路径：只改 CREATE TABLE 不改存量库，升级后
// 第一次扫 NodeRun 就会因为列不存在而失败。
func TestWorkflowNodeRunPersistsWorktreeAssociationAcrossReopen(t *testing.T) {
	ctx := context.Background()
	root := fs.NewRootInfo("persist-root", "persist-root", t.TempDir())
	store, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	if _, err := store.CreateTask(ctx, Task{ID: "task-persist", RootID: root.ID, Status: StatusPending},
		StageRun{ID: "stage-persist", TaskID: "task-persist", StageIndex: 0, StageName: "input", Role: RoleUser, Status: StageStatusPending},
		TaskEvent{ID: "evt-persist", TaskID: "task-persist", Type: "created"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := store.CreateWorkflowRunAndSetEngine(ctx, &WorkflowRun{
		ID: "run-persist", TaskID: "task-persist", Revision: 1,
		Graph: WorkflowDefinition{Nodes: []WorkflowNode{{ID: "build", Kind: NodeKindAgent, Params: map[string]any{"prompt": "x"}}}},
	}); err != nil {
		t.Fatalf("CreateWorkflowRunAndSetEngine: %v", err)
	}
	claimed, won, err := store.ClaimWorkflowNodeRun(ctx, NodeRun{
		ID: "nr-persist", RunID: "run-persist", NodeID: "build", Attempt: 1, Status: NodeRunQueued,
		WorktreeID: "wt-node", WorktreePath: "/tmp/wt-node", WorktreeBranch: "task-9-node",
	})
	if err != nil || !won {
		t.Fatalf("ClaimWorkflowNodeRun won=%v err=%v", won, err)
	}
	if err := store.BeginWorkflowNodeRun(ctx, claimed, time.Now().UTC()); err != nil {
		t.Fatalf("BeginWorkflowNodeRun: %v", err)
	}
	entry := WorktreeEntry{
		TaskID: "task-persist", WorktreeID: "wt-node", Branch: "task-9-node",
		Path: "/tmp/wt-node", NodeRunID: "nr-persist", State: WorktreeStateActive,
	}
	if err := store.BindWorkflowNodeWorktree(ctx, claimed, entry); err != nil {
		t.Fatalf("BindWorkflowNodeWorktree: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("reopen NewTaskStore: %v", err)
	}
	defer reopened.Close()
	got, err := reopened.WorkflowNodeRunByKey(ctx, "run-persist", "build", "", 0, 1)
	if err != nil {
		t.Fatalf("WorkflowNodeRunByKey: %v", err)
	}
	if got.WorktreeID != entry.WorktreeID || got.WorktreePath != entry.Path || got.WorktreeBranch != entry.Branch {
		t.Fatalf("node run worktree = %q/%q/%q, want %q/%q/%q",
			got.WorktreeID, got.WorktreePath, got.WorktreeBranch, entry.WorktreeID, entry.Path, entry.Branch)
	}
}

// 存量任务库的 workflow_node_runs 没有 worktree 列。升级必须用 ALTER 补列，
// 而不是只改新库的建表语句 —— 否则旧节点第一次执行工作流就炸在扫描上。
func TestWorkflowNodeRunWorktreeColumnsMigrateExistingDatabase(t *testing.T) {
	ctx := context.Background()
	root := fs.NewRootInfo("migrate-root", "migrate-root", t.TempDir())
	dbPath, err := taskDBPath(root)
	if err != nil {
		t.Fatalf("taskDBPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("mkdir db dir: %v", err)
	}
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open old db: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, ddl := range []string{
		`CREATE TABLE workflow_runs (
 id TEXT PRIMARY KEY, task_id TEXT NOT NULL, version TEXT NOT NULL DEFAULT '',
 revision INTEGER NOT NULL DEFAULT 1, graph_json TEXT NOT NULL,
 node_states_json TEXT NOT NULL DEFAULT '{}', node_outputs_json TEXT NOT NULL DEFAULT '{}',
 selected_routes_json TEXT NOT NULL DEFAULT '{}', loop_state_json TEXT NOT NULL DEFAULT '{}',
 loop_exit_expr_json TEXT NOT NULL DEFAULT '{}', created_at TEXT NOT NULL, updated_at TEXT NOT NULL
)`,
		`CREATE TABLE workflow_node_runs (
 id TEXT PRIMARY KEY, run_id TEXT NOT NULL, node_id TEXT NOT NULL, loop_path TEXT NOT NULL DEFAULT '',
 iteration INTEGER NOT NULL DEFAULT 0, attempt INTEGER NOT NULL DEFAULT 1,
 agent TEXT NOT NULL DEFAULT '', harness TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '',
 session_key TEXT NOT NULL DEFAULT '', status TEXT NOT NULL, input_json TEXT NOT NULL DEFAULT '{}',
 output_json TEXT NOT NULL DEFAULT '{}', started_at TEXT NOT NULL DEFAULT '', finished_at TEXT NOT NULL DEFAULT '',
 prev_attempt_id TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 UNIQUE(run_id,node_id,loop_path,iteration,attempt)
)`,
		`INSERT INTO workflow_node_runs(id,run_id,node_id,attempt,status,created_at,updated_at)
 VALUES('legacy-nr','legacy-run','build',1,'queued','` + now + `','` + now + `')`,
	} {
		if _, err := raw.Exec(ddl); err != nil {
			t.Fatalf("seed old schema: %v", err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close old db: %v", err)
	}

	store, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore after old schema: %v", err)
	}
	defer store.Close()
	got, err := store.WorkflowNodeRunByKey(ctx, "legacy-run", "build", "", 0, 1)
	if err != nil {
		t.Fatalf("scan legacy row after migration: %v", err)
	}
	if got.Status != NodeRunQueued || got.WorktreeID != "" || got.WorktreePath != "" || got.WorktreeBranch != "" {
		t.Fatalf("legacy row = %#v, want queued with empty worktree identity", got)
	}
}

// D11：集成分支随 Run 快照持久化。运行时只读 snapshot；这里先钉住它真的能
// 写进去并读回来，Task 3 的 merge 才有可信来源。
func TestWorkflowRunPersistsIntegrationBranchSnapshot(t *testing.T) {
	ctx := context.Background()
	root := fs.NewRootInfo("branch-root", "branch-root", t.TempDir())
	store, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	defer store.Close()
	run := &WorkflowRun{
		ID: "run-branch", TaskID: "task-branch", Revision: 1,
		IntegrationBranch: "release/2026-11",
		Graph:             WorkflowDefinition{Nodes: []WorkflowNode{{ID: "build", Kind: NodeKindAgent, Params: map[string]any{"prompt": "x"}}}},
	}
	if err := store.CreateWorkflowRunAndSetEngine(ctx, run); err != nil {
		t.Fatalf("CreateWorkflowRunAndSetEngine: %v", err)
	}
	got, err := store.WorkflowRunByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("WorkflowRunByID: %v", err)
	}
	if got.IntegrationBranch != "release/2026-11" {
		t.Fatalf("integration branch = %q, want release/2026-11", got.IntegrationBranch)
	}
}

// D11：快照取项目 checkout 的当前分支，不是硬编码的 main。
func TestSnapshotRunIntegrationBranchUsesCurrentCheckoutBranch(t *testing.T) {
	ctx := context.Background()
	mainDir := newGitRoot(t)
	gitForTest(t, mainDir, "checkout", "-qb", "release/2026-11")
	root := fs.NewRootInfo("branch-snapshot-root", "branch-snapshot-root", mainDir)
	svc := NewService(nil, testRoots{root: root})
	graph := WorkflowDefinition{Nodes: []WorkflowNode{{
		ID: "build", Kind: NodeKindAgent,
		Params: map[string]any{"prompt": "x", "worktree": "independent"},
	}}}
	got, err := svc.snapshotRunIntegrationBranch(ctx, Task{RootID: root.ID}, graph)
	if err != nil {
		t.Fatalf("snapshotRunIntegrationBranch: %v", err)
	}
	if got != "release/2026-11" {
		t.Fatalf("snapshot = %q, want release/2026-11 (never implicit main)", got)
	}
}

// 独立 worktree 节点必须有 merge target；解析不出来要在 Run 创建前失败。
// 共享主树节点不需要 target，非 git 项目仍可迁移（老行为不变）。
func TestSnapshotRunIntegrationBranchRequiresTargetOnlyForIndependentNodes(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	independent := WorkflowDefinition{Nodes: []WorkflowNode{{
		ID: "build", Kind: NodeKindAgent,
		Params: map[string]any{"prompt": "x", "worktree": "independent"},
	}}}
	if _, err := svc.snapshotRunIntegrationBranch(ctx, Task{RootID: root.ID}, independent); err == nil {
		t.Fatal("independent worktree node without a resolvable branch must fail before the run exists")
	}
	shared := WorkflowDefinition{Nodes: []WorkflowNode{{
		ID: "build", Kind: NodeKindAgent, Params: map[string]any{"prompt": "x"},
	}}}
	if got, err := svc.snapshotRunIntegrationBranch(ctx, Task{RootID: root.ID}, shared); err != nil || got != "" {
		t.Fatalf("shared-only graph = %q err=%v, want empty branch without error", got, err)
	}
}

// ── Task 2：NodeRun 启动/结束接线 Ensure/Close ────────────────────────────
//
// 这里的 fixture 用完整 Service（Roots + app 级 gate + fake CreateTaskWorktree），
// 因为被测行为就是「节点启动前有没有真的拿到可写工作区」；只用 store 测不到。

type workflowWorktreeFixture struct {
	root  fs.RootInfo
	store *TaskStore
	svc   *Service
	run   *WorkflowRun
	node  WorkflowNode
	fake  *fakeRunner
	wf    *scriptedWorkflowNodeRunner
}

func newWorkflowWorktreeFixture(t *testing.T, taskID, runID string, independent bool, integration string) *workflowWorktreeFixture {
	t.Helper()
	ctx := context.Background()
	fake := &fakeRunner{}
	svc, root := newTestService(t, fake)
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	if _, err := store.CreateTask(ctx, Task{ID: taskID, RootID: root.ID, Status: StatusPending, Stages: []StageTemplate{userStage("input")}},
		StageRun{ID: taskID + "-stage", TaskID: taskID, StageIndex: 0, StageName: "input", Role: RoleUser, Status: StageStatusPending},
		TaskEvent{ID: taskID + "-event", TaskID: taskID, Type: "created"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	params := map[string]any{"prompt": "实现登录页"}
	if independent {
		params["worktree"] = "independent"
	}
	node := WorkflowNode{ID: "build", Kind: NodeKindAgent, Params: params}
	run := &WorkflowRun{
		ID: runID, TaskID: taskID, Revision: 1, IntegrationBranch: integration,
		Graph: WorkflowDefinition{Nodes: []WorkflowNode{node}},
	}
	if err := store.CreateWorkflowRunAndSetEngine(ctx, run); err != nil {
		t.Fatalf("CreateWorkflowRunAndSetEngine: %v", err)
	}
	wf := &scriptedWorkflowNodeRunner{}
	svc.SetWorkflowNodeRunner(wf)
	return &workflowWorktreeFixture{root: root, store: store, svc: svc, run: run, node: node, fake: fake, wf: wf}
}

// beginNodeRun 把 NodeRun 推进到 running：worktree 绑定列只允许落在 running 行上。
func (f *workflowWorktreeFixture) beginNodeRun(t *testing.T, id string) NodeRun {
	t.Helper()
	ctx := context.Background()
	claimed, won, err := f.store.ClaimWorkflowNodeRun(ctx, NodeRun{
		ID: id, RunID: f.run.ID, NodeID: f.node.ID, Attempt: 1, Status: NodeRunQueued,
	})
	if err != nil || !won {
		t.Fatalf("ClaimWorkflowNodeRun won=%v err=%v", won, err)
	}
	if err := f.store.BeginWorkflowNodeRun(ctx, claimed, time.Now().UTC()); err != nil {
		t.Fatalf("BeginWorkflowNodeRun: %v", err)
	}
	claimed.Status = NodeRunRunning
	return claimed
}

func (f *workflowWorktreeFixture) attempt(t *testing.T, nodeRunID string) (NodeRun, error) {
	t.Helper()
	return f.svc.RunWorkflowNodeAttempt(context.Background(), f.store, f.run, f.node, NodeRun{
		ID: nodeRunID, RunID: f.run.ID, NodeID: f.node.ID, Attempt: 1, Status: NodeRunQueued,
	})
}

// D16：独立节点启动时建/认领一棵树，归属同时落 registry 与 NodeRun 自身。
// 重复 prepare（重启/重试的第一步）不能建第二棵。
func TestPrepareWorkflowNodeWorktreeClaimsIndependentTreeOnce(t *testing.T) {
	ctx := context.Background()
	f := newWorkflowWorktreeFixture(t, "wt-claim-task", "wt-claim-run", true, "main")
	nr := f.beginNodeRun(t, "nr-wt-claim")

	lease, err := f.svc.PrepareWorkflowNodeWorktree(ctx, f.store, f.run, f.node, nr)
	if err != nil {
		t.Fatalf("PrepareWorkflowNodeWorktree: %v", err)
	}
	if lease.RootPath == "" || !worktreeDirUsable(lease.RootPath) {
		t.Fatalf("lease root %q is not a usable directory", lease.RootPath)
	}
	entry, err := f.store.FindWorktreeEntryByNodeRun(ctx, "wt-claim-task", nr.ID)
	if err != nil {
		t.Fatalf("FindWorktreeEntryByNodeRun: %v", err)
	}
	if entry.NodeRunID != nr.ID || entry.State != WorktreeStateActive || entry.Path != lease.RootPath {
		t.Fatalf("registry entry = %#v, want active owner %s at %s", entry, nr.ID, lease.RootPath)
	}
	stored, err := f.store.WorkflowNodeRunByID(ctx, nr.ID)
	if err != nil {
		t.Fatalf("WorkflowNodeRunByID: %v", err)
	}
	if stored.WorktreeID != entry.WorktreeID || stored.WorktreePath != entry.Path || stored.WorktreeBranch != entry.Branch {
		t.Fatalf("node run worktree = %q/%q/%q, want %q/%q/%q",
			stored.WorktreeID, stored.WorktreePath, stored.WorktreeBranch, entry.WorktreeID, entry.Path, entry.Branch)
	}
	if f.fake.worktreeCreateCount != 1 {
		t.Fatalf("worktree create count = %d, want 1", f.fake.worktreeCreateCount)
	}
	lease.Release()

	again, err := f.svc.PrepareWorkflowNodeWorktree(ctx, f.store, f.run, f.node, lease.NodeRun)
	if err != nil {
		t.Fatalf("second PrepareWorkflowNodeWorktree: %v", err)
	}
	defer again.Release()
	if again.RootPath != lease.RootPath {
		t.Fatalf("second prepare root = %q, want existing %q", again.RootPath, lease.RootPath)
	}
	if f.fake.worktreeCreateCount != 1 {
		t.Fatalf("second prepare created another worktree (count=%d)", f.fake.worktreeCreateCount)
	}
}

// 建树失败时不能调用 Agent：没有可写的物理工作区，跑起来只会写错地方。
func TestWorkflowNodeAttemptCreateWorktreeFailureDoesNotCallAgent(t *testing.T) {
	ctx := context.Background()
	f := newWorkflowWorktreeFixture(t, "wt-fail-task", "wt-fail-run", true, "main")
	f.fake.worktreeErr = errors.New("git worktree add failed")

	got, err := f.attempt(t, "nr-wt-fail")
	if err == nil || !strings.Contains(err.Error(), "git worktree add failed") {
		t.Fatalf("attempt err = %v, want worktree create failure", err)
	}
	if got.Status != NodeRunFail {
		t.Fatalf("node run status = %q, want fail", got.Status)
	}
	if f.wf.ensureCalls != 0 || f.wf.runCalls != 0 {
		t.Fatalf("agent called without a workspace (ensure=%d run=%d)", f.wf.ensureCalls, f.wf.runCalls)
	}
	if _, ferr := f.store.FindWorktreeEntryByNodeRun(ctx, "wt-fail-task", "nr-wt-fail"); ferr == nil {
		t.Fatal("failed create left a registry owner row")
	}
}

// 同一活动分支已被另一个 NodeRun 检出：拒绝，且不能改写既有 owner。
// 这不是执行失败而是需要人处理，NodeRun 落 waiting_user。
func TestWorkflowNodeAttemptBranchConflictWaitsForUserAndKeepsOwner(t *testing.T) {
	ctx := context.Background()
	f := newWorkflowWorktreeFixture(t, "wt-conflict-task", "wt-conflict-run", true, "main")
	task, err := f.store.GetTask(ctx, "wt-conflict-task")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	nrID := "nr-wt-conflict"
	// EnsureNodeWorktree 的 new 分支名 = renderWorktreeName + NodeRun 短 id。
	branch := renderWorktreeName("", task) + "-" + shortID(nrID)
	seedPath := filepath.Join(t.TempDir(), "other")
	if err := os.MkdirAll(seedPath, 0o755); err != nil {
		t.Fatalf("seed dir: %v", err)
	}
	if _, err := f.store.ClaimWorktreeEntry(ctx, WorktreeEntry{
		TaskID: task.ID, WorktreeID: "wt-other", Branch: branch, Path: seedPath,
		RootID: f.root.ID, NodeRunID: "nr-other", State: WorktreeStateActive,
	}); err != nil {
		t.Fatalf("seed other owner: %v", err)
	}

	got, err := f.attempt(t, nrID)
	if !errors.Is(err, ErrWorktreeOwnerConflict) {
		t.Fatalf("attempt err = %v, want ErrWorktreeOwnerConflict", err)
	}
	if got.Status != NodeRunWaitingUser {
		t.Fatalf("node run status = %q, want waiting_user", got.Status)
	}
	if f.wf.runCalls != 0 {
		t.Fatal("agent ran despite branch ownership conflict")
	}
	entry, err := f.store.GetWorktreeEntry(ctx, task.ID, "wt-other")
	if err != nil {
		t.Fatalf("GetWorktreeEntry: %v", err)
	}
	if entry.NodeRunID != "nr-other" || entry.State != WorktreeStateActive {
		t.Fatalf("existing owner overwritten: %#v", entry)
	}
}

// 失败/unknown 保留工作树：unknown 尤其不能删，agent 可能仍在写那棵目录。
// （成功路径由 Task 3 的 merge+close 接管，见 TestWorkflowNodeSuccessMergesToRunSnapshotBranch。）
func TestWorkflowNodeAttemptRetainsIndependentTree(t *testing.T) {
	cases := []struct {
		name       string
		configure  func(f *workflowWorktreeFixture)
		wantStatus string
		wantErr    bool
	}{
		{"failure", func(f *workflowWorktreeFixture) { f.wf.errs = []error{errors.New("transport down")} }, NodeRunFail, true},
		{"unknown", func(f *workflowWorktreeFixture) {
			f.wf.outcomes = []WorkflowNodeResult{{Outcome: WorkflowNodeUnknown}}
		}, NodeRunUnknown, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			f := newWorkflowWorktreeFixture(t, "wt-retain-"+tc.name+"-task", "wt-retain-"+tc.name+"-run", true, "main")
			tc.configure(f)
			nrID := "nr-wt-retain-" + tc.name
			got, err := f.attempt(t, nrID)
			if tc.wantErr && err == nil {
				t.Fatal("attempt succeeded, want error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("attempt err = %v, want nil", err)
			}
			if got.Status != tc.wantStatus {
				t.Fatalf("status = %q, want %q", got.Status, tc.wantStatus)
			}
			entry, ferr := f.store.FindWorktreeEntryByNodeRun(ctx, "wt-retain-"+tc.name+"-task", nrID)
			if ferr != nil {
				t.Fatalf("registry owner gone after %s: %v", tc.name, ferr)
			}
			if entry.State != WorktreeStateActive {
				t.Fatalf("registry state = %q after %s, want active (Task 2 never closes)", entry.State, tc.name)
			}
			if !worktreeDirUsable(entry.Path) {
				t.Fatalf("worktree directory %q removed after %s", entry.Path, tc.name)
			}
			stored, ferr := f.store.WorkflowNodeRunByID(ctx, nrID)
			if ferr != nil {
				t.Fatalf("WorkflowNodeRunByID: %v", ferr)
			}
			if stored.WorktreePath != entry.Path {
				t.Fatalf("node run worktree path = %q, want %q", stored.WorktreePath, entry.Path)
			}
		})
	}
}

// 恢复/重复触发同一 NodeRun：不得建第二棵树，也不得再跑一次 Agent。
func TestWorkflowNodeAttemptRepeatCreatesNoSecondWorktree(t *testing.T) {
	f := newWorkflowWorktreeFixture(t, "wt-once-task", "wt-once-run", true, "main")
	// 用失败终态验证重复触发：失败保留树，第二个 attempt 必须复用同一行。
	f.wf.errs = []error{errors.New("transport down")}
	first, err := f.attempt(t, "nr-wt-once")
	if err == nil {
		t.Fatal("first attempt must surface the transport failure")
	}
	if first.Status != NodeRunFail {
		t.Fatalf("first status = %q, want fail", first.Status)
	}
	second, err := f.attempt(t, "nr-wt-once")
	if err != nil {
		t.Fatalf("second attempt: %v", err)
	}
	if second.ID != first.ID || second.Status != NodeRunFail {
		t.Fatalf("second attempt = %#v, want same terminal row", second)
	}
	if f.fake.worktreeCreateCount != 1 {
		t.Fatalf("worktree create count = %d, want 1", f.fake.worktreeCreateCount)
	}
	if f.wf.runCalls != 1 {
		t.Fatalf("agent run calls = %d, want 1", f.wf.runCalls)
	}
}
