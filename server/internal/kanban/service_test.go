package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mindfs/server/internal/fs"
)

type testRoots struct {
	root fs.RootInfo
}

func (r testRoots) GetRoot(rootID string) (fs.RootInfo, error) {
	return r.root, nil
}

func (r testRoots) ListRoots() []fs.RootInfo {
	return []fs.RootInfo{r.root}
}

type fakeRunner struct {
	mu                   sync.Mutex
	execs                []AgentStageExecution
	prompts              []string
	runErr               error
	worktreeErr          error
	worktreeBranchMode   string
	worktreeBranch       string
	worktreeName         string
	worktreeCreateCalled bool
	// gate 非 nil 时 RunAgentStage 记完 exec 后阻塞到 gate 关闭，用来把执行体钉在阶段内部，
	// 稳定复现并发执行（见 TestRunTaskExecutesStageOnceWhenKickedTwice）。
	gate chan struct{}
}

func (r *fakeRunner) CreateTaskWorktree(ctx context.Context, rootID, name, branchMode, branch string) (WorktreeInfo, error) {
	r.mu.Lock()
	r.worktreeBranchMode = branchMode
	r.worktreeBranch = branch
	r.worktreeName = name
	r.worktreeCreateCalled = true
	r.mu.Unlock()
	if r.worktreeErr != nil {
		return WorktreeInfo{}, r.worktreeErr
	}
	return WorktreeInfo{RootID: "wt-root", Path: filepath.Join(os.TempDir(), name)}, nil
}

func (r *fakeRunner) EnsureAgentSession(ctx context.Context, exec AgentStageExecution) (string, error) {
	return "session-" + exec.Run.ID, nil
}

func (r *fakeRunner) RunAgentStage(ctx context.Context, exec AgentStageExecution) error {
	r.mu.Lock()
	r.execs = append(r.execs, exec)
	r.prompts = append(r.prompts, exec.Prompt)
	r.mu.Unlock()
	if r.gate != nil {
		select {
		case <-r.gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return r.runErr
}

func (r *fakeRunner) TaskUpdated(rootID string, detail TaskDetail) {}

// newTestService 是常见测试组合：干净的任务库 + fakeRunner。
func newTestService(t *testing.T, runner *fakeRunner) (*Service, fs.RootInfo) {
	t.Helper()
	root := fs.NewRootInfo("root", "root", t.TempDir())
	svc := NewService(NewTemplateStoreAt(t.TempDir()), testRoots{root: root})
	if runner != nil {
		svc.SetRunner(runner)
	}
	return svc, root
}

// agentStage 构造一个常见的 agent 段定义。
func agentStage(name, prompt string) StageTemplate {
	return StageTemplate{
		Name:               name,
		Role:               RoleAgent,
		Agent:              "codex",
		Model:              "gpt-5",
		PromptTemplate:     prompt,
		SessionReusePolicy: SessionReuseTaskMain,
	}
}

func userStage(name string) StageTemplate {
	return StageTemplate{Name: name, Role: RoleUser}
}

// waitForCondition 轮询直到 cond 为真或超时。
func waitForCondition(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTaskTemplateStoreSeedsBundledTemplatesWhenUserFileMissing(t *testing.T) {
	dir := t.TempDir()
	bundledPath := filepath.Join(t.TempDir(), taskTemplateFile)
	bundled := []TaskTemplate{{
		ID:             "tmpl_default",
		Name:           "Default task",
		MaxConcurrency: 2,
		Stages: []TaskTemplateStage{{
			ID:       "stage_default",
			Position: 0,
			Snapshot: StageTemplate{
				ID:   "stage_user",
				Name: "Describe",
				Role: RoleUser,
			},
		}},
	}}
	data, err := json.MarshalIndent(bundled, "", "  ")
	if err != nil {
		t.Fatalf("marshal bundled templates: %v", err)
	}
	if err := os.WriteFile(bundledPath, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write bundled templates: %v", err)
	}

	previous := bundledTaskTemplatePaths
	bundledTaskTemplatePaths = func() []string { return []string{bundledPath} }
	defer func() { bundledTaskTemplatePaths = previous }()

	store := NewTemplateStoreAt(dir)
	items, err := store.ListTaskTemplates()
	if err != nil {
		t.Fatalf("ListTaskTemplates: %v", err)
	}
	if len(items) != 1 || items[0].ID != "tmpl_default" {
		t.Fatalf("templates = %#v, want bundled default", items)
	}
	if _, err := os.Stat(filepath.Join(dir, taskTemplateFile)); err != nil {
		t.Fatalf("user task template file not seeded: %v", err)
	}
}

func TestTaskTemplateStoreDoesNotSeedWhenUserFileExists(t *testing.T) {
	cases := []struct {
		name     string
		contents string
		wantFile string
	}{
		{name: "empty array", contents: "[]\n", wantFile: "[]"},
		{name: "empty file", contents: "", wantFile: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, taskTemplateFile), []byte(tc.contents), 0o644); err != nil {
				t.Fatalf("write user templates: %v", err)
			}
			bundledPath := filepath.Join(t.TempDir(), taskTemplateFile)
			if err := os.WriteFile(bundledPath, []byte(`[{"id":"tmpl_default","name":"Default task","max_concurrency":1,"stages":[{"id":"stage_default","position":0,"snapshot":{"id":"stage_user","name":"Describe","role":"user"}}]}]`), 0o644); err != nil {
				t.Fatalf("write bundled templates: %v", err)
			}

			previous := bundledTaskTemplatePaths
			bundledTaskTemplatePaths = func() []string { return []string{bundledPath} }
			defer func() { bundledTaskTemplatePaths = previous }()

			store := NewTemplateStoreAt(dir)
			items, err := store.ListTaskTemplates()
			if err != nil {
				t.Fatalf("ListTaskTemplates: %v", err)
			}
			if len(items) != 0 {
				t.Fatalf("templates = %#v, want no seed", items)
			}
			data, err := os.ReadFile(filepath.Join(dir, taskTemplateFile))
			if err != nil {
				t.Fatalf("read user templates: %v", err)
			}
			if got := strings.TrimSpace(string(data)); got != tc.wantFile {
				t.Fatalf("user file = %q, want %q", got, tc.wantFile)
			}
		})
	}
}

func TestTemplateStoreJSONAndFirstStageValidation(t *testing.T) {
	dir := t.TempDir()
	store := NewTemplateStoreAt(dir)
	stage, err := store.SaveStageTemplate(StageTemplate{
		Name:           "Describe",
		Role:           RoleUser,
		PromptTemplate: "Describe the task",
	})
	if err != nil {
		t.Fatalf("SaveStageTemplate: %v", err)
	}
	if stage.ID == "" {
		t.Fatalf("stage ID empty")
	}
	if _, err := os.Stat(filepath.Join(dir, stageTemplateFile)); err != nil {
		t.Fatalf("stage template file missing: %v", err)
	}
	_, err = store.SaveTaskTemplate(TaskTemplate{
		Name: "Bad",
		Stages: []TaskTemplateStage{{
			Position: 0,
			Snapshot: StageTemplate{
				Name: "Agent",
				Role: RoleAgent,
			},
		}},
	})
	if err == nil {
		t.Fatalf("SaveTaskTemplate accepted non-user first stage")
	}
	tmpl, err := store.SaveTaskTemplate(TaskTemplate{
		Name: "Good",
		Stages: []TaskTemplateStage{{
			Position: 0,
			Snapshot: stage,
		}},
	})
	if err != nil {
		t.Fatalf("SaveTaskTemplate: %v", err)
	}
	if tmpl.MaxConcurrency != 1 {
		t.Fatalf("MaxConcurrency = %d, want 1", tmpl.MaxConcurrency)
	}
}

func TestCreateTaskCopiesTaskStagesAndWaitsForUser(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Name:   "修登录按钮",
		Stages: []StageTemplate{
			userStage("Describe"),
			agentStage("Fix", "Fix this:\n{previous_input}"),
		},
		Input: "broken button",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if detail.Task.Status != StatusWaitingUser {
		t.Fatalf("task status=%s, want waiting_user（无调度器：新建任务恒等用户输入）", detail.Task.Status)
	}
	if detail.Task.Name != "修登录按钮" {
		t.Fatalf("task name=%q, want 修登录按钮", detail.Task.Name)
	}
	if len(detail.Task.Stages) != 2 || detail.Task.Stages[1].Name != "Fix" {
		t.Fatalf("task stages not stored on task: %#v", detail.Task.Stages)
	}
	if len(detail.StageRuns) != 1 || detail.StageRuns[0].Input != "broken button" {
		t.Fatalf("stage input not stored in run: %#v", detail.StageRuns)
	}
}

// 模板是预设：创建时拷贝快照，之后改/删预设与在途任务完全无关。
func TestPresetSnapshotIndependentOfTask(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	tmpl, err := svc.SaveTaskTemplate(ctx, TaskTemplate{
		Name: "Bug fix",
		Stages: []TaskTemplateStage{{
			Position: 0,
			Snapshot: userStage("Describe"),
		}},
	})
	if err != nil {
		t.Fatalf("SaveTaskTemplate: %v", err)
	}
	detail, err := svc.CreateTask(ctx, CreateTaskInput{RootID: root.ID, TaskTemplateID: tmpl.ID, Input: "one"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if detail.Task.Name != "Bug fix" || detail.Task.TaskTemplateName != "Bug fix" {
		t.Fatalf("task name=%q/%q, want Bug fix", detail.Task.Name, detail.Task.TaskTemplateName)
	}

	// 在途任务依然在：改预设名、删预设都应成功（解耦），任务身上的快照不变。
	tmpl.Name = "Renamed"
	if _, err := svc.SaveTaskTemplate(ctx, tmpl); err != nil {
		t.Fatalf("SaveTaskTemplate renamed returned error (template should be freely editable): %v", err)
	}
	if err := svc.DeleteTaskTemplate(ctx, tmpl.ID); err != nil {
		t.Fatalf("DeleteTaskTemplate with in-flight task returned error: %v", err)
	}
	got, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Task.Name != "Bug fix" || got.Task.TaskTemplateName != "Bug fix" {
		t.Fatalf("task snapshot changed after preset edits: %q/%q", got.Task.Name, got.Task.TaskTemplateName)
	}
	if len(got.Task.Stages) != 1 || got.Task.Stages[0].Name != "Describe" {
		t.Fatalf("task stages not snapshotted at creation: %#v", got.Task.Stages)
	}
}

// 兼容：老任务（存预设时代，无任务流水）推进时从预设惰性回填一次并落盘。
func TestLegacyTaskStagesBackfilledFromTemplate(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	tmpl, err := svc.SaveTaskTemplate(ctx, TaskTemplate{
		Name: "Legacy",
		Stages: []TaskTemplateStage{{
			Position: 0,
			Snapshot: userStage("Old Describe"),
		}},
	})
	if err != nil {
		t.Fatalf("SaveTaskTemplate: %v", err)
	}
	// 模拟老数据：直接用 store 插一个没有任务流水、只有预设 ID 的任务。
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	now := time.Now().UTC()
	legacy := Task{
		ID:             "task_legacy",
		RootID:         root.ID,
		TaskTemplateID: tmpl.ID,
		Stages:         nil,
		CurrentStageIndex: 0,
		Status:         StatusWaitingUser,
		Labels:         []string{},
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if _, err := store.CreateTask(ctx, legacy, StageRun{
		ID:         "run_legacy",
		TaskID:     "task_legacy",
		StageIndex: 0,
		Role:       RoleUser,
		Status:     StageStatusWaitingUser,
		Input:      "first",
		CreatedAt:  now,
		UpdatedAt:  now,
	}, TaskEvent{ID: "event_legacy", TaskID: "task_legacy", Type: "task_created", CreatedAt: now}); err != nil {
		t.Fatalf("store.CreateTask legacy: %v", err)
	}

	before, err := svc.GetTask(ctx, root.ID, "task_legacy")
	if err != nil {
		t.Fatalf("GetTask before move: %v", err)
	}
	if len(before.Task.Stages) != 0 {
		t.Fatalf("legacy task unexpectedly has stages: %#v", before.Task.Stages)
	}
	if _, err := svc.RerunStage(ctx, MoveInput{RootID: root.ID, TaskID: "task_legacy", StageIndex: 0}); err != nil {
		t.Fatalf("RerunStage on legacy task: %v", err)
	}
	after, err := svc.GetTask(ctx, root.ID, "task_legacy")
	if err != nil {
		t.Fatalf("GetTask after move: %v", err)
	}
	if len(after.Task.Stages) != 1 || after.Task.Stages[0].Name != "Old Describe" {
		t.Fatalf("legacy task stages not backfilled from preset: %#v", after.Task.Stages)
	}
	if after.Task.Status != StatusWaitingUser {
		t.Fatalf("legacy task status=%s, want waiting_user after rerun", after.Task.Status)
	}
}

func TestNextFinishesFinalStageWaitingUser(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Describe"),
			agentStage("Do it", "Do this:\n{previous_input}"),
		},
		Input: "hello",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	waitForCondition(t, func() bool {
		detail, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && detail.Task.Status == StatusWaitingUser && detail.Task.CurrentStageIndex == 1
	})
	if detail.Task.Status != StatusWaitingUser || detail.Task.CurrentStageIndex != 1 {
		t.Fatalf("status = %s stage = %d, want waiting_user @1", detail.Task.Status, detail.Task.CurrentStageIndex)
	}
	// 最后一阶段等待用户时 Next 应完成而不是报 stage out of range。
	detail, err = svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID, Reason: "done"})
	if err != nil {
		t.Fatalf("Next at final stage: %v", err)
	}
	if detail.Task.Status != StatusSuccess {
		t.Fatalf("status = %s, want success", detail.Task.Status)
	}
}

func TestNextRequiresCurrentUserInputWhenTargetReferencesIt(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Describe"),
			agentStage("Fix", "Fix this:\n{previous_input}"),
		},
		Input: "",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err == nil {
		t.Fatalf("Next with empty input succeeded, want error")
	}
	detail, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if detail.Task.CurrentStageIndex != 0 || detail.Task.Status != StatusWaitingUser {
		t.Fatalf("task stage/status = %d/%s, want 0/%s", detail.Task.CurrentStageIndex, detail.Task.Status, StatusWaitingUser)
	}
	runner := svc.Runner.(*fakeRunner)
	runner.mu.Lock()
	execCount := len(runner.execs)
	runner.mu.Unlock()
	if execCount != 0 {
		t.Fatalf("agent exec count = %d, want 0", execCount)
	}
}

func TestNextAllowsEmptyInputWhenTargetDoesNotReferenceIt(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Gate"),
			agentStage("Static", "Run the static check."),
		},
		Input: "",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	detail, err = svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID})
	if err != nil {
		t.Fatalf("Next with empty input: %v", err)
	}
	if detail.Task.CurrentStageIndex != 1 {
		t.Fatalf("current stage = %d, want 1", detail.Task.CurrentStageIndex)
	}
}

func TestNextRequiresCurrentInputFromAgentStageWhenTargetReferencesIt(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Describe"),
			agentStage("Fix", "Fix this:\n{previous_input}"),
			agentStage("Review", "Review this:\n{previous_input}"),
		},
		Input: "first",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	detail, err = svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID})
	if err != nil {
		t.Fatalf("Next to agent: %v", err)
	}
	waitForCondition(t, func() bool {
		detail, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && detail.Task.CurrentStageIndex == 1 && detail.Task.Status == StatusWaitingUser
	})
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err == nil {
		t.Fatalf("Next from empty agent input succeeded, want error")
	}
	detail, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask after failed next: %v", err)
	}
	if detail.Task.CurrentStageIndex != 1 {
		t.Fatalf("current stage = %d, want 1", detail.Task.CurrentStageIndex)
	}
}

func TestTaskCreateWorktreeIsTaskScoped(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:             root.ID,
		Stages:             []StageTemplate{userStage("Describe")},
		Input:              "one",
		CreateWorktree:     true,
		WorktreeBranchMode: "existing",
		WorktreeBranch:     "feature/a",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if !detail.Task.CreateWorktree {
		t.Fatalf("task CreateWorktree=false, want true")
	}
	if detail.Task.WorktreeBranchMode != "existing" || detail.Task.WorktreeBranch != "feature/a" {
		t.Fatalf("task branch = %q/%q, want existing/feature/a", detail.Task.WorktreeBranchMode, detail.Task.WorktreeBranch)
	}
	disabled := false
	updated, err := svc.UpdateFirstInput(ctx, UpdateTaskInput{RootID: root.ID, TaskID: detail.Task.ID, Input: "two", CreateWorktree: &disabled})
	if err != nil {
		t.Fatalf("UpdateFirstInput: %v", err)
	}
	if updated.Task.CreateWorktree {
		t.Fatalf("updated task CreateWorktree=true, want false")
	}
}

// Next/Resume/RunNow 等处都会触发 RunTask：守卫防止同一个 agent 阶段被并发跑两次。
// 用一个会阻塞的 runner 把第一个执行体钉在阶段内部，再补一次 RunTask —— 没有守卫时 exec 会变成 2。
func TestRunTaskExecutesStageOnceWhenKickedTwice(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	gate := make(chan struct{})
	runner := &fakeRunner{gate: gate}
	svc.SetRunner(runner)

	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Describe"),
			agentStage("Fix", "Fix this:\n{previous_input}"),
		},
		Input: "broken save button",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	// 等第一个执行体真正进入 agent 阶段（gate 会把它留在里面）。
	waitForCondition(t, func() bool {
		runner.mu.Lock()
		defer runner.mu.Unlock()
		return len(runner.execs) > 0
	})
	runner.mu.Lock()
	entered := len(runner.execs) > 0
	runner.mu.Unlock()
	if !entered {
		close(gate)
		t.Fatalf("agent stage never started")
	}
	// 第二个执行体：守卫生效时应被挡下。
	svc.RunTask(root.ID, detail.Task.ID)
	time.Sleep(50 * time.Millisecond)
	close(gate)

	waitForCondition(t, func() bool {
		got, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && got.Task.Status == StatusWaitingUser
	})
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.execs) != 1 {
		t.Fatalf("runner exec count=%d, want 1（同一阶段被执行了多次）", len(runner.execs))
	}
}

func TestTaskWorktreeNameUsesTaskNumber(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Describe"),
			agentStage("Fix", "Fix this:\n{previous_input}"),
		},
		Input:              "broken save button",
		CreateWorktree:     true,
		WorktreeBranchMode: "existing",
		WorktreeBranch:     "feature/a",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if detail.Task.TaskNumber != 1 {
		t.Fatalf("task number=%d, want 1", detail.Task.TaskNumber)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	waitForCondition(t, func() bool {
		runner := svc.Runner.(*fakeRunner)
		runner.mu.Lock()
		defer runner.mu.Unlock()
		return runner.worktreeCreateCalled && len(runner.execs) > 0
	})
	runner := svc.Runner.(*fakeRunner)
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if !runner.worktreeCreateCalled {
		t.Fatalf("worktree was not created")
	}
	if runner.worktreeName != "task-1" {
		t.Fatalf("worktree name=%q, want task-1", runner.worktreeName)
	}
	if runner.worktreeBranchMode != "existing" || runner.worktreeBranch != "feature/a" {
		t.Fatalf("worktree branch=%q/%q, want existing/feature/a", runner.worktreeBranchMode, runner.worktreeBranch)
	}
	if len(runner.execs) != 1 {
		t.Fatalf("runner exec count=%d, want 1", len(runner.execs))
	}
	if runner.execs[0].RuntimeRootPath != filepath.Join(os.TempDir(), "task-1") {
		t.Fatalf("runtime root path=%q, want task worktree path", runner.execs[0].RuntimeRootPath)
	}
}

func TestTaskWorktreeCreateErrorStoredOnTask(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{worktreeErr: errors.New("git worktree add failed")})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:         root.ID,
		Stages:         []StageTemplate{userStage("Describe"), agentStage("Fix", "Fix this:\n{previous_input}")},
		Input:          "broken save button",
		CreateWorktree: true,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err == nil {
		t.Fatalf("Next succeeded, want worktree error")
	}
	waitForCondition(t, func() bool {
		detail, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && detail.Task.AuxFlags.SessionError != ""
	})
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if detail.Task.Status != StatusWaitingUser {
		t.Fatalf("task status=%s, want waiting_user", detail.Task.Status)
	}
	if detail.Task.CurrentStageIndex != 0 {
		t.Fatalf("current stage=%d, want 0", detail.Task.CurrentStageIndex)
	}
	if detail.Task.AuxFlags.SessionError != "git worktree add failed" {
		t.Fatalf("session error=%q, want git worktree add failed", detail.Task.AuxFlags.SessionError)
	}
}

func TestUpdateCurrentInputKeepsPreviousStageInput(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Describe"),
			agentStage("Fix", "Fix this:\n{previous_input}"),
		},
		Input: "first input",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	detail, err = svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID})
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	for _, run := range detail.StageRuns {
		if run.StageIndex == 1 && run.Input != "" {
			t.Fatalf("new current stage input=%q, want empty", run.Input)
		}
	}
	updated, err := svc.UpdateCurrentInput(ctx, UpdateTaskInput{RootID: root.ID, TaskID: detail.Task.ID, Input: "current input"})
	if err != nil {
		t.Fatalf("UpdateCurrentInput: %v", err)
	}
	firstRun := StageRun{}
	currentRun := StageRun{}
	for _, run := range updated.StageRuns {
		if run.StageIndex == 0 {
			firstRun = run
		}
		if run.StageIndex == 1 {
			currentRun = run
		}
	}
	if firstRun.Input != "first input" {
		t.Fatalf("first stage input=%q, want first input", firstRun.Input)
	}
	if currentRun.Input != "current input" {
		t.Fatalf("current stage input=%q, want current input", currentRun.Input)
	}
}

func TestCompleteFinalWaitingTask(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{userStage("Describe")},
		Input:  "done",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if detail.Task.Status != StatusWaitingUser {
		t.Fatalf("task status=%s, want waiting_user", detail.Task.Status)
	}
	completed, err := svc.Complete(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID, Reason: "approved"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if completed.Task.Status != StatusSuccess {
		t.Fatalf("task status=%s, want success", completed.Task.Status)
	}
	if completed.Task.CompletedAt == "" {
		t.Fatalf("completed_at empty")
	}
}

func TestTaskNumbersIncrement(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	first, err := svc.CreateTask(ctx, CreateTaskInput{RootID: root.ID, Stages: []StageTemplate{userStage("Describe")}, Input: "one"})
	if err != nil {
		t.Fatalf("CreateTask first: %v", err)
	}
	second, err := svc.CreateTask(ctx, CreateTaskInput{RootID: root.ID, Stages: []StageTemplate{userStage("Describe")}, Input: "two"})
	if err != nil {
		t.Fatalf("CreateTask second: %v", err)
	}
	if first.Task.TaskNumber != 1 || second.Task.TaskNumber != 2 {
		t.Fatalf("task numbers = %d/%d, want 1/2", first.Task.TaskNumber, second.Task.TaskNumber)
	}
	numbered, err := svc.ListTaskDetails(ctx, root.ID, ListTasksOptions{TaskNumber: 2})
	if err != nil {
		t.Fatalf("ListTaskDetails by task number: %v", err)
	}
	if len(numbered) != 1 || numbered[0].Task.ID != second.Task.ID {
		t.Fatalf("task number lookup = %#v, want second task", numbered)
	}
}

func TestBuildAgentPromptAppendsOnlyConfiguredContext(t *testing.T) {
	values := map[string]string{
		"previous_input":     "fix this",
		"task_initial_input": "first input",
		"task_number":        "12",
	}
	prompt := BuildAgentPrompt("Do: {previous_input}", values, TaskControlPromptContext{})
	if prompt != "Do: fix this" {
		t.Fatalf("prompt = %q", prompt)
	}
	withTaskNumber := BuildAgentPrompt("Do: {task_initial_input} #{task_number}", values, TaskControlPromptContext{})
	if withTaskNumber != "Do: first input #12" {
		t.Fatalf("task placeholders not replaced: %q", withTaskNumber)
	}
	legacy := BuildAgentPrompt("Root: {root_id}", values, TaskControlPromptContext{})
	if legacy != "Root: {root_id}" {
		t.Fatalf("legacy placeholder was replaced: %q", legacy)
	}
	withControl := BuildAgentPrompt("Do: {previous_input}", values, TaskControlPromptContext{
		RootID:            "root",
		TaskNumber:        12,
		CurrentStageIndex: "1",
		CurrentStageName:  "Agent",
		Enabled:           true,
	})
	if !containsAll(withControl, []string{"Task control context:", "task_number: 12", "mindfs root -task 12", "mindfs root -task 12 -next"}) {
		t.Fatalf("control prompt missing context: %q", withControl)
	}
}

func TestSchedulerRunsAgentStageAndStoresSessionKey(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Describe"),
			agentStage("Fix", "Fix #{task_number}:\n{previous_input}"),
		},
		Input: "broken save button",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	detail, err = svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID, Reason: "ready for agent"})
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	waitForCondition(t, func() bool {
		detail, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && detail.Task.CurrentStageIndex == 1 && detail.Task.Status == StatusWaitingUser
	})
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if detail.Task.CurrentStageIndex != 1 || detail.Task.Status != StatusWaitingUser {
		t.Fatalf("task stage/status = %d/%s, want 1/waiting_user", detail.Task.CurrentStageIndex, detail.Task.Status)
	}
	if detail.Task.MainSessionKey == "" {
		t.Fatalf("main session key not stored")
	}
	agentRun := detail.StageRuns[len(detail.StageRuns)-1]
	if agentRun.SessionKey == "" {
		t.Fatalf("agent run session key empty: %#v", agentRun)
	}
	if !strings.Contains(agentRun.RenderedPrompt, "broken save button") {
		t.Fatalf("rendered prompt missing input: %q", agentRun.RenderedPrompt)
	}
	if !strings.Contains(agentRun.RenderedPrompt, "Fix #1") {
		t.Fatalf("rendered prompt missing task number: %q", agentRun.RenderedPrompt)
	}
	runner := svc.Runner.(*fakeRunner)
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.execs) != 1 {
		t.Fatalf("runner exec count=%d, want 1", len(runner.execs))
	}
	if runner.execs[0].Run.SessionKey != agentRun.SessionKey {
		t.Fatalf("runner session=%q, run session=%q", runner.execs[0].Run.SessionKey, agentRun.SessionKey)
	}
}

func TestAgentStageSessionErrorWaitsForUser(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{runErr: errors.New("agent unavailable")})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Describe"),
			agentStage("Fix", "Fix this:\n{previous_input}"),
		},
		Input: "broken",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	detail, err = svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID})
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	waitForCondition(t, func() bool {
		detail, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && detail.Task.AuxFlags.SessionError != ""
	})
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if detail.Task.Status != StatusWaitingUser {
		t.Fatalf("task status = %s, want waiting_user", detail.Task.Status)
	}
	run := detail.StageRuns[len(detail.StageRuns)-1]
	if run.Status != StageStatusFail || run.FinishedAt == "" {
		t.Fatalf("stage status/finished_at = %s/%q, want fail/non-empty", run.Status, run.FinishedAt)
	}
	if detail.Task.AuxFlags.SessionError != "agent unavailable" {
		t.Fatalf("session error = %q, want agent unavailable", detail.Task.AuxFlags.SessionError)
	}
	time.Sleep(50 * time.Millisecond)
	runner := svc.Runner.(*fakeRunner)
	runner.mu.Lock()
	execCount := len(runner.execs)
	runner.mu.Unlock()
	if execCount != 1 {
		t.Fatalf("agent exec count = %d, want 1", execCount)
	}
}

func TestAutoAdvanceAgentStageFailureWaitsForUserAtCurrentStage(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{runErr: errors.New("agent unavailable")})
	implement := agentStage("Implement", "Implement {previous_input}")
	implement.AutoAdvance = true
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Describe"),
			implement,
			userStage("Review"),
		},
		Input: "change",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	waitForCondition(t, func() bool {
		detail, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && detail.Task.Status == StatusWaitingUser && detail.Task.AuxFlags.SessionError != ""
	})
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if detail.Task.CurrentStageIndex != 1 || detail.Task.Status != StatusWaitingUser {
		t.Fatalf("task stage/status = %d/%s, want 1/waiting_user", detail.Task.CurrentStageIndex, detail.Task.Status)
	}
	if len(detail.StageRuns) != 2 {
		t.Fatalf("stage run count = %d, want 2 (no auto-advanced run)", len(detail.StageRuns))
	}
	latest := detail.StageRuns[len(detail.StageRuns)-1]
	if latest.Status != StageStatusFail {
		t.Fatalf("stage status = %s, want fail", latest.Status)
	}
}

func TestNextAdvancesFailedCurrentStageAfterUserReview(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{runErr: errors.New("agent unavailable")})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Describe"),
			agentStage("Fix", "Fix this:\n{previous_input}"),
			agentStage("Review", "Review."),
		},
		Input: "broken",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	detail, err = svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID})
	if err != nil {
		t.Fatalf("Next to agent: %v", err)
	}
	waitForCondition(t, func() bool {
		detail, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && detail.Task.CurrentStageIndex == 1 && detail.Task.AuxFlags.SessionError != ""
	})
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next from failed stage: %v", err)
	}
	waitForCondition(t, func() bool {
		detail, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && detail.Task.CurrentStageIndex == 2
	})
	if err != nil {
		t.Fatalf("GetTask after next: %v", err)
	}
	if detail.Task.CurrentStageIndex != 2 {
		t.Fatalf("current stage = %d, want 2", detail.Task.CurrentStageIndex)
	}
}

func TestAgentStageAllowsBlankModel(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	blank := agentStage("Fix", "Fix this:\n{previous_input}")
	blank.Model = ""
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{userStage("Describe"), blank},
		Input:  "use defaults",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	waitForCondition(t, func() bool {
		runner := svc.Runner.(*fakeRunner)
		runner.mu.Lock()
		defer runner.mu.Unlock()
		return len(runner.execs) > 0
	})
	runner := svc.Runner.(*fakeRunner)
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.execs) > 0 && runner.execs[0].Stage.Model != "" {
		t.Fatalf("execution model = %q, want empty", runner.execs[0].Stage.Model)
	}
}

func TestRenameTask(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{RootID: root.ID, Stages: []StageTemplate{userStage("Describe")}, Name: "旧名", Input: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	renamed, err := svc.RenameTask(ctx, root.ID, detail.Task.ID, "  修登录按钮  ")
	if err != nil {
		t.Fatalf("RenameTask: %v", err)
	}
	if renamed.Task.Name != "修登录按钮" {
		t.Fatalf("task name=%q, want trimmed new name", renamed.Task.Name)
	}
	got, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Task.Name != "修登录按钮" {
		t.Fatalf("rename not persisted: %q", got.Task.Name)
	}
}

// 等待用户时追加 comment：当前段标记 approved、新增段默认命令名并立即进入执行。
func TestAddStageApprovesAndRunsWhenWaiting(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{userStage("Describe")},
		Input:  "第一版",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	detail, err = svc.AddStage(ctx, AddStageInput{
		RootID: root.ID,
		TaskID: detail.Task.ID,
		Stage:  agentStage("", "再走一遍流程：\n{previous_input}"),
	})
	if err != nil {
		t.Fatalf("AddStage: %v", err)
	}
	if len(detail.Task.Stages) != 2 {
		t.Fatalf("stages len=%d, want 2", len(detail.Task.Stages))
	}
	if detail.Task.Stages[1].Name != "阶段 2" {
		t.Fatalf("appended stage name=%q, want 阶段 2（缺省命名）", detail.Task.Stages[1].Name)
	}
	waitForCondition(t, func() bool {
		got, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && got.Task.Status == StatusWaitingUser && got.Task.CurrentStageIndex == 1
	})
	got, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	runner := svc.Runner.(*fakeRunner)
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.execs) != 1 {
		t.Fatalf("runner exec count=%d, want 1", len(runner.execs))
	}
	if !strings.Contains(runner.prompts[0], "再走一遍流程") || !strings.Contains(runner.prompts[0], "第一版") {
		t.Fatalf("appended prompt not rendered with previous input: %q", runner.prompts[0])
	}
	if got.Task.Status != StatusWaitingUser {
		t.Fatalf("task status=%s, want waiting_user", got.Task.Status)
	}
}

// 运行中追加：只排入流水尾，不立即执行；当前段结束后等待用户，验收再推进。
func TestAddStageQueuesTailWhileRunning(t *testing.T) {
	ctx := context.Background()
	gate := make(chan struct{})
	svc, root := newTestService(t, nil)
	runner := &fakeRunner{gate: gate}
	svc.SetRunner(runner)
	fix := agentStage("Fix", "Fix this:\n{previous_input}")
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{userStage("Describe"), fix},
		Input:  "第一版",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	waitForCondition(t, func() bool {
		runner.mu.Lock()
		defer runner.mu.Unlock()
		return len(runner.execs) > 0
	})

	// 任务 running：追加段应只入流水尾。
	updated, err := svc.AddStage(ctx, AddStageInput{
		RootID: root.ID,
		TaskID: detail.Task.ID,
		Stage:  agentStage("Follow-up", "Follow up."),
	})
	if err != nil {
		t.Fatalf("AddStage while running: %v", err)
	}
	if len(updated.Task.Stages) != 3 {
		t.Fatalf("stages len=%d, want 3", len(updated.Task.Stages))
	}
	if updated.Task.Status != StatusRunning {
		t.Fatalf("task status=%s, want running（追加不打断运行）", updated.Task.Status)
	}
	runner.mu.Lock()
	execBefore := len(runner.execs)
	runner.mu.Unlock()
	time.Sleep(50 * time.Millisecond)
	runner.mu.Lock()
	execAfter := len(runner.execs)
	runner.mu.Unlock()
	if execAfter != execBefore {
		t.Fatalf("appended stage started executing while running (exec %d->%d)", execBefore, execAfter)
	}
	close(gate)

	// Fix 段结束、非 auto_advance → 等待用户；此时追加过的段还没跑。
	waitForCondition(t, func() bool {
		got, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && got.Task.Status == StatusWaitingUser && got.Task.CurrentStageIndex == 1
	})
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID, Reason: "continue"}); err != nil {
		t.Fatalf("Next after approval: %v", err)
	}
	// Next 里那次 RunTask 可能被并发守卫吞掉（第一个执行体还没完全退出）；兜底再踢一次。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runner.mu.Lock()
		execCount := len(runner.execs)
		runner.mu.Unlock()
		if execCount >= 2 {
			break
		}
		svc.RunTask(root.ID, detail.Task.ID)
		time.Sleep(10 * time.Millisecond)
	}
	waitForCondition(t, func() bool {
		got, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && got.Task.CurrentStageIndex == 2
	})
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.execs) != 2 {
		t.Fatalf("runner exec count=%d, want 2（排队段未在验收后执行）", len(runner.execs))
	}
	if !strings.Contains(runner.prompts[1], "Follow up") {
		t.Fatalf("second exec prompt=%q, want appended stage prompt", runner.prompts[1])
	}
}

func TestUpdateStageEditsFutureStagePrompt(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Describe"),
			agentStage("Fix", "v1 {previous_input}"),
		},
		Input: "input",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	newStage := agentStage("Fix improved", "v2 {previous_input}")
	if _, err := svc.UpdateStage(ctx, UpdateStageInput{RootID: root.ID, TaskID: detail.Task.ID, Index: 1, Stage: &newStage}); err != nil {
		t.Fatalf("UpdateStage: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	waitForCondition(t, func() bool {
		runner := svc.Runner.(*fakeRunner)
		runner.mu.Lock()
		defer runner.mu.Unlock()
		return len(runner.execs) > 0
	})
	runner := svc.Runner.(*fakeRunner)
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if !strings.Contains(runner.prompts[0], "v2") {
		t.Fatalf("edited prompt not used: %q", runner.prompts[0])
	}
	got, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Task.Stages[1].Name != "Fix improved" {
		t.Fatalf("stage name not updated: %q", got.Task.Stages[1].Name)
	}
}

// 重跑：把指针移回某段并再次执行（任务不在运行中时）。
func TestRerunStageReexecutesStage(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{userStage("Describe"), agentStage("Fix", "Fix {previous_input}")},
		Input:  "input",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	waitForCondition(t, func() bool {
		got, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && got.Task.Status == StatusWaitingUser && got.Task.CurrentStageIndex == 1
	})
	if _, err := svc.RerunStage(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID, StageIndex: 1}); err != nil {
		t.Fatalf("RerunStage: %v", err)
	}
	waitForCondition(t, func() bool {
		runner := svc.Runner.(*fakeRunner)
		runner.mu.Lock()
		defer runner.mu.Unlock()
		return len(runner.execs) == 2
	})
	// 重跑再次失败后应回到等待用户。
	got, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Task.Status != StatusWaitingUser || got.Task.CurrentStageIndex != 1 {
		t.Fatalf("after rerun status/stage = %s/%d, want waiting_user/1", got.Task.Status, got.Task.CurrentStageIndex)
	}
}

func containsAll(value string, parts []string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}

func TestMain(m *testing.M) {
	time.Local = time.UTC
	os.Exit(m.Run())
}
