package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

// fakeRunnerID 给每个 fakeRunner 一个进程内唯一的短 id，用在它建的临时目录名里。
//
// 为什么需要：所有 fakeRunner 建在同一个 os.TempDir() 下，而各自的 dirSeq 都从 1
// 起数 —— 两个不同用例的第 1 次建树会拿到完全一样的路径。撞名之后 A 用例的
// t.Cleanup 会删掉 B 用例正在用的目录，那种失败看起来跟 worktree 逻辑毫无关系。
// 带 PID + 单调计数是最低成本的错开方式（不需要 crypto/rand 那种重家伙）。
var fakeRunnerSeq atomic.Int64

func fakeRunnerID() string {
	return fmt.Sprintf("%d-%d", os.Getpid(), fakeRunnerSeq.Add(1))
}

// 残留的同名目录不能被「继承」：os.MkdirAll 撞到已存在的目录会**静默成功**，于是
// 判据「目录存在 → worktree 可用」拿到一个别的（可能是上次被 kill 的 run 留下的）
// 目录，测试照样绿 —— 那是假通过。独占创建撞名即失败，不悄悄复用。
func mkdirExclusive(path string) error {
	return os.Mkdir(path, 0o755)
}

type fakeRunner struct {
	mu                   sync.Mutex
	execs                []AgentStageExecution
	prompts              []string
	runErr               error
	result               StageResult
	worktreeErr          error
	worktreeBranchMode   string
	worktreeBranch       string
	worktreeName         string
	worktreeCreateCalled bool
	// worktreeInfo 非零时 CreateTaskWorktree 原样返回它（不真建目录），
	// 用来模拟「建树返回成功但产出不可用」—— 判据不能只信 err == nil。
	worktreeInfo WorktreeInfo
	// createdDirs 记下 fake 真建出来的目录，由 newTestService 注册的 Cleanup 删掉。
	// 建在 os.TempDir() 下而不是 t.TempDir()：worktree 名字（task-N）跨用例会撞车，
	// t.TempDir() 每用例一个反而让「同名不同目录」这种真实情况测不到。序号见 dirSeq。
	createdDirs []string
	dirSeq      int
	// gate 非 nil 时 RunAgentStage 记完 exec 后阻塞到 gate 关闭，用来把执行体钉在阶段内部，
	// 稳定复现并发执行（见 TestRunTaskExecutesStageOnceWhenKickedTwice）。
	gate chan struct{}
	// taskUpdates 记 TaskUpdated 的每次调用：广播是「状态变了界面才知道」的唯一通道，
	// 而它以前只由部分路径发出（见 TestStatusChangeAlwaysBroadcasts）。与 execs 共用 mu。
	taskUpdates []string
}

// recordTaskUpdate 记一次广播（任务 id），供测试断言「该播的播了、不该播的没播」。
func (r *fakeRunner) recordTaskUpdate(taskID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.taskUpdates = append(r.taskUpdates, taskID)
}

// updateCount 返回目前收到过几次广播。
func (r *fakeRunner) updateCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.taskUpdates)
}

// result 零值 = StageOutcomeDone：不显式设置的老用例照旧判完成，
// 只有验证「没回报就不许算完成」的新用例才需要构造它。

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
	// worktreeInfo 非零时原样返回，用来喂「建树成功但产出不可用」的形状
	// （空路径 / 指向文件）。默认零值走下面的真建目录。
	if r.worktreeInfo != (WorktreeInfo{}) {
		return r.worktreeInfo, nil
	}
	// 真跑一遍建目录：gitview.AddWorktree 建的目录是真实存在的，而任务侧现在按
	// 「目录还在不在」判 worktree 可用（原先只看字段非空）。fake 只回一个不存在的
	// 路径的话，测的是 fake 的失真而不是被测逻辑。
	//
	// 目录名带**进程内全局**序号：光用 worktree 名（task-1）在同一台机器上跨用例/跨包会撞，
	// 撞了就是「别的用例留下的目录让本用例的判据为真」—— 那种通过是假的。
	// 用包级计数器而不是每个 runner 自己的 dirSeq：每个 fakeRunner 都从 1 起数，
	// 而它们建在同一个 os.TempDir() 下，于是**两个不同用例的第 1 次建树会撞名**。
	// 撞名的后果不是报错而是更难查的静默串味：A 用例的 cleanup 删掉 B 用例正在用的目录。
	// 跨进程残留（上次 run 被 kill）同样靠它错开：进程内计数器从 1 起，但上次的
	// 目录名带的是上次的序号加随机尾巴。
	// worktreeName 仍记真名（renderWorktreeName 的断言要看它）。
	r.mu.Lock()
	r.dirSeq++
	seq := r.dirSeq
	r.mu.Unlock()
	path := filepath.Join(os.TempDir(), fmt.Sprintf("wtf-%s-%d-%s", fakeRunnerID(), seq, name))
	if err := mkdirExclusive(path); err != nil {
		return WorktreeInfo{}, fmt.Errorf("建假 worktree 目录 %s: %w", path, err)
	}
	r.mu.Lock()
	r.createdDirs = append(r.createdDirs, path)
	r.mu.Unlock()
	return WorktreeInfo{RootID: "wt-root", Path: path}, nil
}

func (r *fakeRunner) EnsureAgentSession(ctx context.Context, exec AgentStageExecution) (string, error) {
	return "session-" + exec.Run.ID, nil
}

func (r *fakeRunner) RunAgentStage(ctx context.Context, exec AgentStageExecution) (StageResult, error) {
	r.mu.Lock()
	r.execs = append(r.execs, exec)
	r.prompts = append(r.prompts, exec.Prompt)
	result := r.result
	r.mu.Unlock()
	if r.gate != nil {
		select {
		case <-r.gate:
		case <-ctx.Done():
			return StageResult{}, ctx.Err()
		}
	}
	if r.runErr != nil {
		return StageResult{}, r.runErr
	}
	return result, nil
}

func (r *fakeRunner) TaskUpdated(rootID string, detail TaskDetail) {
	r.recordTaskUpdate(detail.Task.ID)
}

// newTestService 是常见测试组合：干净的任务库 + fakeRunner。
func newTestService(t *testing.T, runner *fakeRunner) (*Service, fs.RootInfo) {
	t.Helper()
	root := fs.NewRootInfo("root", "root", t.TempDir())
	svc := NewService(NewTemplateStoreAt(t.TempDir()), testRoots{root: root})
	if runner != nil {
		svc.SetRunner(runner)
		t.Cleanup(func() {
			runner.mu.Lock()
			dirs := append([]string(nil), runner.createdDirs...)
			runner.mu.Unlock()
			for _, dir := range dirs {
				_ = os.RemoveAll(dir)
			}
		})
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

// waitForCondition 轮询直到 cond 为真或超时；超时视为失败（静默返回会把真超时
// 转化成下游莫名其妙的断言失败，反而更难定位）。
func waitForCondition(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("waitForCondition: timeout")
}

func TestTaskTemplateStoreSeedsBundledTemplatesWhenUserFileMissing(t *testing.T) {
	dir := t.TempDir()
	bundledPath := filepath.Join(t.TempDir(), taskTemplateFile)
	bundled := []TaskTemplate{{
		ID:   "tmpl_default",
		Name: "Default task",
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
			if err := os.WriteFile(bundledPath, []byte(`[{"id":"tmpl_default","name":"Default task","stages":[{"id":"stage_default","position":0,"snapshot":{"id":"stage_user","name":"Describe","role":"user"}}]}]`), 0o644); err != nil {
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
	_, err = store.SaveTaskTemplate(TaskTemplate{
		Name: "Good",
		Stages: []TaskTemplateStage{{
			Position: 0,
			Snapshot: stage,
		}},
	})
	if err != nil {
		t.Fatalf("SaveTaskTemplate: %v", err)
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
	// 新建任务是未开始态：要有用户输入才开跑（用户点「开始」→ RunNow）。
	if detail.Task.Status != StatusPending {
		t.Fatalf("task status=%s, want pending（新建任务未开始）", detail.Task.Status)
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

// 首段勾了「立即执行」：创建后不用手动点「立即执行」就自己跑起来。
func TestCreateTaskStartsImmediatelyWhenFirstStageSaysSo(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	first := userStage("Describe")
	first.StartImmediately = true
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{first, agentStage("Fix", "Fix this:\n{previous_input}")},
		Input:  "broken button",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	waitForCondition(t, func() bool {
		d, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && d.Task.Status == StatusRunning
	})
	detail, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if detail.Task.CurrentStageIndex != 1 {
		t.Fatalf("current stage=%d, want 1（首段勾了立即执行就该直接进 agent 段）", detail.Task.CurrentStageIndex)
	}
	// 开跑这一次就把 user 段批准掉了，不该留在待审核。
	for _, run := range detail.StageRuns {
		if run.StageIndex == 0 && run.Status == StageStatusWaitingUser {
			t.Fatalf("stage 0 still waiting_user after start immediately")
		}
	}
}

// 没勾「立即执行」：安静停在「未开始」，等用户手动点「立即执行」。
func TestCreateTaskStaysPendingWithoutStartImmediately(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{userStage("Describe"), agentStage("Fix", "Fix this:\n{previous_input}")},
		Input:  "broken button",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if detail.Task.Status != StatusPending || detail.Task.CurrentStageIndex != 0 {
		t.Fatalf("task stage/status = %d/%s, want 0/%s（没勾就该停在未开始）",
			detail.Task.CurrentStageIndex, detail.Task.Status, StatusPending)
	}
}

// user 段的 AutoAdvance 是死字段（引擎对 user 段一律推进，不读它）：
// 它为 true 也不能触发开跑，否则模板里一个改不动的值就能决定任务跑不跑。
func TestCreateTaskIgnoresUserStageAutoAdvance(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	first := userStage("Describe")
	first.AutoAdvance = true
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{first, agentStage("Fix", "Fix this:\n{previous_input}")},
		Input:  "broken button",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if detail.Task.Status != StatusPending || detail.Task.CurrentStageIndex != 0 {
		t.Fatalf("task stage/status = %d/%s, want 0/%s（user 段的 auto_advance 管不了开跑）",
			detail.Task.CurrentStageIndex, detail.Task.Status, StatusPending)
	}
}

// 面板上把模板勾上的那颗取消掉：前端会把首段的 start_immediately 显式发回 false，
// 任务必须尊重它、停在「未开始」（不能因为模板是 true 就自己跑起来）。
func TestCreateTaskRespectsExplicitStartImmediatelyOffOverTemplate(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	tmpl, err := svc.SaveTaskTemplate(ctx, TaskTemplate{
		Name: "Autostart",
		Stages: []TaskTemplateStage{
			{Position: 0, Snapshot: StageTemplate{Name: "Describe", Role: RoleUser, StartImmediately: true}},
			{Position: 1, Snapshot: agentStage("Fix", "Fix this:\n{previous_input}")},
		},
	})
	if err != nil {
		t.Fatalf("SaveTaskTemplate: %v", err)
	}
	// 前端 applyStageOverride 的输出形状：拍平的 stages，首段 start_immediately=false。
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:         root.ID,
		TaskTemplateID: tmpl.ID,
		Input:          "broken button",
		Stages: []StageTemplate{
			{Name: "Describe", Role: RoleUser, StartImmediately: false},
			agentStage("Fix", "Fix this:\n{previous_input}"),
		},
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if detail.Task.Status != StatusPending || detail.Task.CurrentStageIndex != 0 {
		t.Fatalf("task stage/status = %d/%s, want 0/%s（面板显式取消就该停在未开始）",
			detail.Task.CurrentStageIndex, detail.Task.Status, StatusPending)
	}
}

// 模板首段勾了「立即执行」且客户端没发 stages（老客户端）：后端照模板的开跑。
func TestCreateTaskFollowsTemplateStartImmediatelyWhenNoStagesSent(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	tmpl, err := svc.SaveTaskTemplate(ctx, TaskTemplate{
		Name: "Autostart",
		Stages: []TaskTemplateStage{
			{Position: 0, Snapshot: StageTemplate{Name: "Describe", Role: RoleUser, StartImmediately: true}},
			{Position: 1, Snapshot: agentStage("Fix", "Fix this:\n{previous_input}")},
		},
	})
	if err != nil {
		t.Fatalf("SaveTaskTemplate: %v", err)
	}
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:         root.ID,
		TaskTemplateID: tmpl.ID,
		Input:          "broken button",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	waitForCondition(t, func() bool {
		d, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && d.Task.Status == StatusRunning
	})
	detail, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if detail.Task.CurrentStageIndex != 1 {
		t.Fatalf("current stage=%d, want 1（没发 stages 就该照模板立即执行）", detail.Task.CurrentStageIndex)
	}
}

// 勾了立即执行但没给输入：停在「未开始」，创建请求不能因此报错
// （目标段引用 {previous_input}，RunNow 本会因「input required」失败）。
func TestCreateTaskStartImmediatelyRequiresNonEmptyInput(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	first := userStage("Describe")
	first.StartImmediately = true
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{first, agentStage("Fix", "Fix this:\n{previous_input}")},
	})
	if err != nil {
		t.Fatalf("CreateTask with empty input returned error: %v", err)
	}
	if detail.Task.Status != StatusPending || detail.Task.CurrentStageIndex != 0 {
		t.Fatalf("task stage/status = %d/%s, want 0/%s（没有输入不该立即执行）",
			detail.Task.CurrentStageIndex, detail.Task.Status, StatusPending)
	}
}

// 存模板时把 user 段的 auto_advance 归一成 true：引擎对 user 段一律推进，
// 面板上那颗开关是灰的、改不了，存成 false 只会让 JSON 里躺着个假值。
// 存量模板（AutoAdvance 缺失 = false）下次一存就自愈。
func TestSaveTaskTemplateNormalizesUserStageAutoAdvance(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t, nil)
	tmpl, err := svc.SaveTaskTemplate(ctx, TaskTemplate{
		Name: "Legacy",
		Stages: []TaskTemplateStage{
			{Position: 0, Snapshot: StageTemplate{Name: "Describe", Role: RoleUser, AutoAdvance: false, StartImmediately: true}},
			{Position: 1, Snapshot: agentStage("Fix", "Fix this:\n{previous_input}")},
		},
	})
	if err != nil {
		t.Fatalf("SaveTaskTemplate: %v", err)
	}
	if !tmpl.Stages[0].Snapshot.AutoAdvance {
		t.Fatalf("user stage auto_advance=%v, want true（user 段一律推进，存 false 是假值）",
			tmpl.Stages[0].Snapshot.AutoAdvance)
	}
	// 首段的「立即执行」不该被这次归一化碰到。
	if !tmpl.Stages[0].Snapshot.StartImmediately {
		t.Fatalf("user stage start_immediately=%v, want true", tmpl.Stages[0].Snapshot.StartImmediately)
	}
	// agent 段的 auto_advance 仍由用户自己管。
	agent := agentStage("Fix", "Fix this:\n{previous_input}")
	agent.AutoAdvance = false
	tmpl.Stages[1].Snapshot = agent
	saved, err := svc.SaveTaskTemplate(ctx, tmpl)
	if err != nil {
		t.Fatalf("SaveTaskTemplate again: %v", err)
	}
	if saved.Stages[1].Snapshot.AutoAdvance {
		t.Fatalf("agent stage auto_advance was forced true, want false")
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
		ID:                "task_legacy",
		RootID:            root.ID,
		TaskTemplateID:    tmpl.ID,
		Stages:            nil,
		CurrentStageIndex: 0,
		Status:            StatusWaitingUser,
		Labels:            []string{},
		CreatedAt:         now,
		UpdatedAt:         now,
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
	// 校验失败不改写状态：仍停在未开始态的第 0 段。
	if detail.Task.CurrentStageIndex != 0 || detail.Task.Status != StatusPending {
		t.Fatalf("task stage/status = %d/%s, want 0/%s", detail.Task.CurrentStageIndex, detail.Task.Status, StatusPending)
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
	// agent 的 cwd 必须就是建出来的那个 worktree 目录。比对 fake 记下的真实路径，
	// 不写死字面量：目录名带序号（见 CreateTaskWorktree），写死就成了测常量。
	if len(runner.createdDirs) != 1 {
		t.Fatalf("created dirs=%v, want exactly 1", runner.createdDirs)
	}
	if runner.execs[0].RuntimeRootPath != runner.createdDirs[0] {
		t.Fatalf("runtime root path=%q, want task worktree path %q",
			runner.execs[0].RuntimeRootPath, runner.createdDirs[0])
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
	if detail.Task.Status != StatusPending {
		t.Fatalf("task status=%s, want pending（新建任务未开始）", detail.Task.Status)
	}
	// 「开始」才真正进执行体；worktree 创建失败在那里被记下。
	if _, err := svc.RunNow(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	waitForCondition(t, func() bool {
		detail, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && detail.Task.AuxFlags.SessionError != ""
	})
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if detail.Task.CurrentStageIndex != 0 {
		t.Fatalf("current stage=%d, want 0", detail.Task.CurrentStageIndex)
	}
	if detail.Task.AuxFlags.SessionError != "git worktree add failed" {
		t.Fatalf("session error=%q, want git worktree add failed", detail.Task.AuxFlags.SessionError)
	}
}

// 待开始态点「开始」必须直接进执行中：先过掉 user 段再跑 agent 段。
// 曾经 RunNow 对 pending 只调 RunTask 不推进阶段，任务卡在待审核，
// 用户得再点一次「执行」才真的跑起来。
func TestRunNowFromPendingStartsAgentStage(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{userStage("Describe"), agentStage("Fix", "Fix this:\n{previous_input}")},
		Input:  "broken save button",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if detail.Task.Status != StatusPending || detail.Task.CurrentStageIndex != 0 {
		t.Fatalf("new task stage/status = %d/%s, want 0/%s", detail.Task.CurrentStageIndex, detail.Task.Status, StatusPending)
	}

	if _, err := svc.RunNow(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	waitForCondition(t, func() bool {
		d, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && d.Task.Status == StatusRunning
	})
	detail, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if detail.Task.CurrentStageIndex != 1 {
		t.Fatalf("current stage=%d, want 1（「开始」应直接进入 agent 段）", detail.Task.CurrentStageIndex)
	}
	// user 段被「开始」这一次动作批准掉，不该留在待审核。
	for _, run := range detail.StageRuns {
		if run.StageIndex == 0 && run.Status == StageStatusWaitingUser {
			t.Fatalf("stage 0 still waiting_user after one RunNow")
		}
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
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{userStage("Describe")},
		Input:  "done",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if detail.Task.Status != StatusPending {
		t.Fatalf("task status=%s, want pending（新建任务未开始）", detail.Task.Status)
	}
	// Complete 只接受等待用户态；未开始态先「开始」，由执行体把单段 user 任务推到等待用户。
	if _, err := svc.RunNow(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	waitForCondition(t, func() bool {
		got, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && got.Task.Status == StatusWaitingUser
	})
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

// 任务状态不跟会话/worktree 绑死：会话被删、worktree 丢失后任务会一直卡在 running，
// 此时必须还能被人工完成，否则这些任务永远动不了。
func TestCompleteStuckRunningTaskAfterSessionLost(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
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
	// 任务已经进入 agent 段（running）后，会话/worktree 丢失：执行体不再回来。
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	if err := store.UpdateTaskStatus(ctx, detail.Task.ID, StatusRunning, false); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}
	stuck, err := store.GetTask(ctx, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if stuck.Status != StatusRunning {
		t.Fatalf("precondition status=%s, want running", stuck.Status)
	}

	completed, err := svc.Complete(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID, Reason: "session gone"})
	if err != nil {
		t.Fatalf("Complete on a stuck running task must succeed, got: %v", err)
	}
	if completed.Task.Status != StatusSuccess {
		t.Fatalf("task status=%s, want success", completed.Task.Status)
	}
	if completed.Task.CompletedAt == "" {
		t.Fatalf("completed_at empty")
	}
}

// 终态任务不能被状态操作复活：Pause/Resume 走 setTaskStatus，终态时必须原样返回。
func TestTerminalTaskCannotBeResurrected(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		kill  func(*Service, string, string) error
		after string
	}{
		{"success+resume", func(s *Service, r, id string) error {
			_, err := s.Resume(ctx, MoveInput{RootID: r, TaskID: id})
			return err
		}, StatusSuccess},
		{"success+pause", func(s *Service, r, id string) error {
			_, err := s.Pause(ctx, MoveInput{RootID: r, TaskID: id})
			return err
		}, StatusSuccess},
		{"cancelled+pause", func(s *Service, r, id string) error {
			_, err := s.Pause(ctx, MoveInput{RootID: r, TaskID: id})
			return err
		}, StatusCancelled},
		{"cancelled+resume", func(s *Service, r, id string) error {
			_, err := s.Resume(ctx, MoveInput{RootID: r, TaskID: id})
			return err
		}, StatusCancelled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, root := newTestService(t, nil)
			d, err := svc.CreateTask(ctx, CreateTaskInput{RootID: root.ID, Stages: []StageTemplate{userStage("Describe")}, Input: "x"})
			if err != nil {
				t.Fatalf("CreateTask: %v", err)
			}
			// 从非终态进入终态：cancelled 用 Cancel，success 用 Complete。
			// 不能拿 success 再 Cancel——终态已不可改写，那是本测试要守的行为本身。
			if tc.after == StatusCancelled {
				if _, err := svc.Cancel(ctx, MoveInput{RootID: root.ID, TaskID: d.Task.ID}); err != nil {
					t.Fatalf("Cancel: %v", err)
				}
			} else if _, err := svc.Complete(ctx, MoveInput{RootID: root.ID, TaskID: d.Task.ID}); err != nil {
				t.Fatalf("Complete: %v", err)
			}
			if err := tc.kill(svc, root.ID, d.Task.ID); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got, err := svc.GetTask(ctx, root.ID, d.Task.ID)
			if err != nil {
				t.Fatalf("GetTask: %v", err)
			}
			if got.Task.Status != tc.after {
				t.Fatalf("terminal task was resurrected: status=%s, want %s", got.Task.Status, tc.after)
			}
		})
	}
}

// 暂停/恢复往返：暂停后状态保持，恢复后继续跑（非终态才允许）。
func TestPauseResumeRoundTrip(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	d, err := svc.CreateTask(ctx, CreateTaskInput{RootID: root.ID,
		Stages: []StageTemplate{userStage("A"), agentStage("B", "do {previous_input}")}, Input: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	paused, err := svc.Pause(ctx, MoveInput{RootID: root.ID, TaskID: d.Task.ID})
	if err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if paused.Task.Status != StatusPaused {
		t.Fatalf("status=%s, want paused", paused.Task.Status)
	}
	resumed, err := svc.Resume(ctx, MoveInput{RootID: root.ID, TaskID: d.Task.ID})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if resumed.Task.Status != StatusRunning {
		t.Fatalf("status=%s, want running", resumed.Task.Status)
	}
}

// 状态变更必须发 task.updated，否则别的客户端看不到：
// setTaskStatus（Pause/Resume/Cancel/Fail）与 moveTo（换段）以前都只写库、
// 由 HTTP handler 事后补一次，于是 KickPending 等非 HTTP 入口推不出去。
func TestStatusChangeAlwaysBroadcasts(t *testing.T) {
	ctx := context.Background()
	runner := &fakeRunner{}
	svc, root := newTestService(t, runner)
	d, err := svc.CreateTask(ctx, CreateTaskInput{RootID: root.ID,
		Stages: []StageTemplate{userStage("A"), agentStage("B", "do {previous_input}")}, Input: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	// Pause 走 setTaskStatus
	if _, err := svc.Pause(ctx, MoveInput{RootID: root.ID, TaskID: d.Task.ID, Reason: "r"}); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if got := runner.updateCount(); got != 1 {
		t.Fatalf("broadcasts after Pause = %d, want 1 (setTaskStatus must broadcast)", got)
	}
	// Cancel 也走 setTaskStatus：任务变终态，卡片要从板上消失
	if _, err := svc.Cancel(ctx, MoveInput{RootID: root.ID, TaskID: d.Task.ID}); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if got := runner.updateCount(); got != 2 {
		t.Fatalf("broadcasts after Cancel = %d, want 2", got)
	}
	// 已是终态再 Cancel：状态没变，就不该再推一条一模一样的（否则每个客户端白重渲染一次）
	if _, err := svc.Cancel(ctx, MoveInput{RootID: root.ID, TaskID: d.Task.ID}); err != nil {
		t.Fatalf("Cancel on terminal task: %v", err)
	}
	if got := runner.updateCount(); got != 2 {
		t.Fatalf("broadcasts after redundant Cancel = %d, want 2 (unchanged status must stay silent)", got)
	}
}

// moveTo 换段指针后必须播：卡片上的「当前在第几段」就挂在这条上。
func TestAdvanceBroadcastsNewStage(t *testing.T) {
	ctx := context.Background()
	runner := &fakeRunner{}
	svc, root := newTestService(t, runner)
	d, err := svc.CreateTask(ctx, CreateTaskInput{RootID: root.ID,
		Stages: []StageTemplate{userStage("A"), agentStage("B", "do {previous_input}")}, Input: "x"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	before := runner.updateCount()
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: d.Task.ID}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	if got := runner.updateCount(); got <= before {
		t.Fatalf("broadcasts after Next = %d, want > %d (moveTo must broadcast)", got, before)
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

// 完成契约与匹配判据必须同源：契约里让 agent 输出的那个标记，得是匹配时认的那个。
// 直接从契约文本里把标记抠出来再喂给 matcher —— 手抄一份字面量的话，
// 契约里段号写错这类错位就测不出来。
func TestStageExitContractMatchesTheMarkerItAsksFor(t *testing.T) {
	markerRe := regexp.MustCompile(`\[` + stageDoneTag + `:\d+\]`)
	for _, stageIndex := range []int{0, 1, 7} {
		contract := BuildStageExitContract(stageIndex)
		marker := markerRe.FindString(contract)
		if marker == "" {
			t.Fatalf("stage %d: contract asks for no done marker: %q", stageIndex, contract)
		}
		if want := "[" + stageDoneTag + ":" + strconv.Itoa(stageIndex) + "]"; marker != want {
			t.Fatalf("stage %d: contract asks for %q, want %q", stageIndex, marker, want)
		}
		if got := matchStageOutcome("干完了\n"+marker, stageIndex); got.Outcome != StageOutcomeDone {
			t.Fatalf("stage %d: matcher rejects the marker the contract asked for: %+v", stageIndex, got)
		}
		blockedMarker := "[" + stageBlockedTag + ":" + strconv.Itoa(stageIndex) + " 缺凭证]"
		if !strings.Contains(contract, blockedMarker[:len(blockedMarker)-len(" 缺凭证]")]) {
			t.Fatalf("stage %d: contract asks for no blocked marker: %q", stageIndex, contract)
		}
		got := matchStageOutcome(blockedMarker, stageIndex)
		if got.Outcome != StageOutcomeBlocked || got.Reason != "缺凭证" {
			t.Fatalf("stage %d: blocked = %+v, want Blocked/缺凭证", stageIndex, got)
		}
	}
}

func TestMatchStageOutcome(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		stageIndex int
		want       StageOutcome
		wantReason string
	}{
		{"done", "改完了，看过测试\n[MINDFS-STAGE-DONE:1]", 1, StageOutcomeDone, ""},
		{"done on first stage", "[MINDFS-STAGE-DONE:0]", 0, StageOutcomeDone, ""},
		{"blocked with reason", "跑不动，缺数据库迁移文件\n[MINDFS-STAGE-BLOCKED:1 缺数据库迁移文件]", 1, StageOutcomeBlocked, "缺数据库迁移文件"},
		{"blocked without reason", "[MINDFS-STAGE-BLOCKED:2]", 2, StageOutcomeBlocked, ""},
		// 段号不吻合 = 上一段的残留标记，不许拿来当本段完成。
		{"stale marker from previous stage", "上轮标记\n[MINDFS-STAGE-DONE:1]", 2, StageOutcomeSilent, ""},
		{"stale blocked marker from previous stage", "[MINDFS-STAGE-BLOCKED:0 旧的]", 1, StageOutcomeSilent, ""},
		{"silent", "我先看看代码", 1, StageOutcomeSilent, ""},
		{"empty", "", 1, StageOutcomeSilent, ""},
		// 先说做完了又补一句「其实有地方要确认」：按做完算（marker 是显式契约，
		// 比正文里的语气可信）。
		{"done wins over blocked", "[MINDFS-STAGE-DONE:1]\n[MINDFS-STAGE-BLOCKED:1 顺带一提]", 1, StageOutcomeDone, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchStageOutcome(tt.text, tt.stageIndex)
			if got.Outcome != tt.want || got.Reason != tt.wantReason {
				t.Fatalf("matchStageOutcome(%q, %d) = %+v, want outcome %v reason %q", tt.text, tt.stageIndex, got, tt.want, tt.wantReason)
			}
		})
	}
}

// agent 没显式回报完成，本段就不许算成功——否则 AutoAdvance 会把下一段植进来，
// 前一段的活没干完，下一段已经在错误前提上开跑（本次要消灭的阶段错乱）。
func TestAgentStageWithoutDoneMarkerStopsAtCurrentStage(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{result: StageResult{Outcome: StageOutcomeSilent}})
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
		return err == nil && detail.Task.Status == StatusWaitingUser
	})
	// 停在第 1 段，下一段没被植进来。
	if detail.Task.CurrentStageIndex != 1 {
		t.Fatalf("current stage = %d, want 1（没回报完成不许推进）", detail.Task.CurrentStageIndex)
	}
	if len(detail.StageRuns) != 2 {
		t.Fatalf("stage run count = %d, want 2（下一段不许开跑）", len(detail.StageRuns))
	}
	latest := detail.StageRuns[len(detail.StageRuns)-1]
	if latest.Status != StageStatusWaitingUser {
		t.Fatalf("stage status = %s, want waiting_user", latest.Status)
	}
	if !strings.Contains(detail.Task.AuxFlags.SessionError, "未回报完成") {
		t.Fatalf("session error = %q, want it to say the stage never reported done", detail.Task.AuxFlags.SessionError)
	}
	// 人工补一句评论也不许顶掉没走完的 agent 段。
	if _, err := svc.AddStage(ctx, AddStageInput{
		RootID: root.ID,
		TaskID: detail.Task.ID,
		Stage:  StageTemplate{PromptTemplate: "补一句"},
	}); err == nil {
		t.Fatalf("AddStage on an unfinished agent stage must be rejected, got nil error")
	}
}

// agent 自己说受阻：原因原样摆到卡面上等人处理，同样不许推进。
func TestAgentStageBlockedStopsWithAgentReason(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{
		result: StageResult{Outcome: StageOutcomeBlocked, Reason: "缺数据库迁移文件"},
	})
	implement := agentStage("Implement", "Implement {previous_input}")
	implement.AutoAdvance = true
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{userStage("Describe"), implement, userStage("Review")},
		Input:  "change",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	waitForCondition(t, func() bool {
		detail, err = svc.GetTask(ctx, detail.Task.RootID, detail.Task.ID)
		return err == nil && detail.Task.Status == StatusWaitingUser
	})
	if detail.Task.CurrentStageIndex != 1 || len(detail.StageRuns) != 2 {
		t.Fatalf("blocked stage advanced: index=%d runs=%d", detail.Task.CurrentStageIndex, len(detail.StageRuns))
	}
	if detail.Task.AuxFlags.SessionError != "缺数据库迁移文件" {
		t.Fatalf("session error = %q, want the agent's own reason", detail.Task.AuxFlags.SessionError)
	}
}

// 回报完成后照旧推进（第一段之外的行为不许被新契约改掉）。
func TestAgentStageWithDoneMarkerAutoAdvances(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{result: StageResult{Outcome: StageOutcomeDone}})
	implement := agentStage("Implement", "Implement {previous_input}")
	implement.AutoAdvance = true
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{userStage("Describe"), implement, userStage("Review")},
		Input:  "change",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	waitForCondition(t, func() bool {
		detail, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && detail.Task.CurrentStageIndex == 2 && detail.Task.Status == StatusWaitingUser
	})
	// 契约必须真的随 prompt 送出去了，否则 agent 不知道要输出什么。
	runner := svc.Runner.(*fakeRunner)
	runner.mu.Lock()
	prompts := append([]string(nil), runner.prompts...)
	runner.mu.Unlock()
	if len(prompts) == 0 {
		t.Fatalf("agent stage ran without a prompt")
	}
	if !strings.Contains(prompts[len(prompts)-1], "[MINDFS-STAGE-DONE:1]") {
		t.Fatalf("stage exit contract missing from the rendered prompt: %q", prompts[len(prompts)-1])
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

// 失败段不许被一句「下一段」推过去。这条曾经是反着写的
// （TestNextAdvancesFailedCurrentStageAfterUserReview），当时把
// 「失败段可以靠下一次 Next 强推」当成期望行为，实际就是阶段错乱的来源：
// 前一段的活没干完，下一段已经在错误前提上开跑。
// 现在失败段只能靠 RerunStage 重跑或改任务离开，Next 必须被拒。
func TestNextRejectsFailedAgentStageUntilRerun(t *testing.T) {
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
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next to agent: %v", err)
	}
	waitForCondition(t, func() bool {
		detail, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && detail.Task.CurrentStageIndex == 1 && detail.Task.AuxFlags.SessionError != ""
	})
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err == nil {
		t.Fatalf("Next from a failed stage must be rejected, got nil error")
	}
	detail, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if detail.Task.CurrentStageIndex != 1 {
		t.Fatalf("current stage = %d, want 1（失败段不许被推走）", detail.Task.CurrentStageIndex)
	}
	latest := detail.StageRuns[len(detail.StageRuns)-1]
	if latest.Status != StageStatusFail {
		t.Fatalf("stage status = %s, want fail（不许被改成 approved）", latest.Status)
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
	// 带本任务编号后缀的名字进来要剥掉：任务名不背后缀，后缀只归会话名。
	suffixed, err := svc.RenameTask(ctx, root.ID, detail.Task.ID, "修登录按钮 / #1")
	if err != nil {
		t.Fatalf("RenameTask(suffixed): %v", err)
	}
	if suffixed.Task.Name != "修登录按钮" {
		t.Fatalf("task name=%q, want suffix stripped", suffixed.Task.Name)
	}
}

// 会话名 ↔ 任务名双向绑定（kanban 侧）：main_session_key 反查任务后改名；
// 名字相同时不动，避免会话改名触发的回流造成事件抖动。
func TestTaskNameFromSession(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{userStage("Describe")},
		Name:   "初名",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	task, err := store.GetTask(ctx, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	task.MainSessionKey = "sess-1"
	if err := store.UpdateTask(ctx, task); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if _, changed := svc.TaskNameFromSession(ctx, root.ID, "sess-1", "初名"); changed {
		t.Fatal("same name should be no-op")
	}
	got, changed := svc.TaskNameFromSession(ctx, root.ID, "sess-1", "新名")
	if !changed {
		t.Fatal("expected task renamed")
	}
	if got.Task.Name != "新名" {
		t.Fatalf("task name=%q", got.Task.Name)
	}
	if _, changed := svc.TaskNameFromSession(ctx, root.ID, "sess-other", "再新"); changed {
		t.Fatal("unbound session must not touch any task")
	}
}

// 会话名带着 " / #编号" 回流时，剥成 base 再比：原样重命名一个带后缀的会话
// 必须判成 no-op，否则每次都会白跑一遍 RenameTask + 广播。
func TestTaskNameFromSessionStripsNumberSuffix(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{userStage("Describe")},
		Name:   "初名",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	task, err := store.GetTask(ctx, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	task.MainSessionKey = "sess-1"
	if err := store.UpdateTask(ctx, task); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	number := task.TaskNumber

	// 会话现名「初名 / #N」，任务现名「初名」：剥完相等 → 不动。
	if _, changed := svc.TaskNameFromSession(ctx, root.ID, "sess-1", TaskSessionName("初名", number)); changed {
		t.Fatalf("renaming to the same suffixed name must be a no-op (task_number=%d)", number)
	}

	got, changed := svc.TaskNameFromSession(ctx, root.ID, "sess-1", TaskSessionName("新名", number))
	if !changed {
		t.Fatal("expected task renamed")
	}
	if got.Task.Name != "新名" {
		t.Fatalf("task name=%q, want %q (suffix must not land on the task)", got.Task.Name, "新名")
	}
}

// 删会话时清空任务链接：会话没了，任务不能再指向它，且要在 aux_session_error 留痕。
func TestDetachFromSessionClearsTaskLink(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{userStage("Describe")},
		Name:   "任务",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	task, err := store.GetTask(ctx, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	task.MainSessionKey = "sess-dead"
	if err := store.UpdateTask(ctx, task); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	// 让阶段运行也指向这个会话：任务面板的会话列表同时收 stage_run.session_key
	runs, err := store.ListStageRuns(ctx, task.ID)
	if err != nil || len(runs) == 0 {
		t.Fatalf("ListStageRuns: %v runs=%d", err, len(runs))
	}
	runs[0].SessionKey = "sess-dead"
	if err := store.UpdateStageRunExecution(ctx, runs[0]); err != nil {
		t.Fatalf("UpdateStageRunExecution: %v", err)
	}

	got, changed := svc.DetachFromSession(ctx, root.ID, []string{"sess-dead"})
	if !changed {
		t.Fatal("expected task updated")
	}
	if got.Task.MainSessionKey != "" {
		t.Fatalf("main_session_key should be cleared, got %q", got.Task.MainSessionKey)
	}
	if strings.TrimSpace(got.Task.AuxFlags.SessionError) == "" {
		t.Fatal("expected session_error trace left on the task")
	}
	// 留痕要能被前端解析：形状是 {"message":..., "data":[...]}
	var notice struct {
		Message string   `json:"message"`
		Data    []string `json:"data"`
	}
	if err := json.Unmarshal([]byte(got.Task.AuxFlags.SessionError), &notice); err != nil {
		t.Fatalf("session_error is not the expected JSON shape: %v (%q)", err, got.Task.AuxFlags.SessionError)
	}
	if strings.TrimSpace(notice.Message) == "" {
		t.Fatalf("session_error.message empty: %q", got.Task.AuxFlags.SessionError)
	}
	runs, err = store.ListStageRuns(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListStageRuns: %v", err)
	}
	if runs[0].SessionKey != "" {
		t.Fatalf("stage run session_key should be cleared, got %q", runs[0].SessionKey)
	}
}

func TestDetachFromSessionIgnoresUnboundAndForeignSessions(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{userStage("Describe")},
		Name:   "任务",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	task, err := store.GetTask(ctx, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	task.MainSessionKey = "sess-alive"
	if err := store.UpdateTask(ctx, task); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if _, changed := svc.DetachFromSession(ctx, root.ID, []string{"sess-unbound", ""}); changed {
		t.Fatal("unbound session must not touch any task")
	}
	after, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if after.MainSessionKey != "sess-alive" {
		t.Fatalf("main_session_key must survive unrelated detach, got %q", after.MainSessionKey)
	}
}

// ClearSessionRefs 只清确实指向该会话的引用：主会话被改成别的 key 后不该被误清。
func TestClearSessionRefsKeepsRepointedTask(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{userStage("Describe")},
		Name:   "任务",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	task, err := store.GetTask(ctx, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	task.MainSessionKey = "sess-new"
	if err := store.UpdateTask(ctx, task); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if err := store.ClearSessionRefs(ctx, task.ID, "sess-old"); err != nil {
		t.Fatalf("ClearSessionRefs: %v", err)
	}
	after, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if after.MainSessionKey != "sess-new" {
		t.Fatalf("repointed task must keep its key, got %q", after.MainSessionKey)
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
	// 新建任务是未开始态 → 先「开始」，让首段被执行体推进到等待用户。
	if _, err := svc.RunNow(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	waitForCondition(t, func() bool {
		got, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && got.Task.Status == StatusWaitingUser
	})
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

// 删除未执行段：只允许删尚未产生 StageRun、且不在指针上的段；index 0 与当前段不可删。
func TestRemoveStageDeletesUnexecutedStage(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Describe"),
			agentStage("Fix", "Fix {previous_input}"),
			agentStage("Extra", "Extra {previous_input}"),
		},
		Input: "input",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	taskID := detail.Task.ID

	// index 0 是任务输入段，不可删。
	if _, err := svc.RemoveStage(ctx, RemoveStageInput{RootID: root.ID, TaskID: taskID, Index: 0}); err == nil {
		t.Fatal("RemoveStage(index 0) succeeded, want rejection")
	}
	// 越界。
	if _, err := svc.RemoveStage(ctx, RemoveStageInput{RootID: root.ID, TaskID: taskID, Index: 3}); err == nil {
		t.Fatal("RemoveStage(out of range) succeeded, want rejection")
	}
	// 新建任务的指针在 0，index 1/2 都还没跑过 → 指针之外的尾部可删。
	got, err := svc.RemoveStage(ctx, RemoveStageInput{RootID: root.ID, TaskID: taskID, Index: 2})
	if err != nil {
		t.Fatalf("RemoveStage(untouched tail): %v", err)
	}
	if len(got.Task.Stages) != 2 {
		t.Fatalf("stages len=%d, want 2", len(got.Task.Stages))
	}
	if got.Task.CurrentStageIndex != 0 {
		t.Fatalf("current_stage_index=%d, want 0（指针在删除点之前，不动）", got.Task.CurrentStageIndex)
	}

	// 跑到 index 1 → 等待用户，该段已产生 success 的 StageRun。
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: taskID}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	waitForCondition(t, func() bool {
		got, err := svc.GetTask(ctx, root.ID, taskID)
		return err == nil && got.Task.Status == StatusWaitingUser && got.Task.CurrentStageIndex == 1
	})
	// 已执行段不可删。
	if _, err := svc.RemoveStage(ctx, RemoveStageInput{RootID: root.ID, TaskID: taskID, Index: 1}); err == nil {
		t.Fatal("RemoveStage(executed stage) succeeded, want rejection")
	}

	// 尾段（index 1）现在是当前段：即便被 `Next` 判成末段收尾，也不能删当前指针段。
	if _, err := svc.RemoveStage(ctx, RemoveStageInput{RootID: root.ID, TaskID: taskID, Index: 1}); err == nil {
		t.Fatal("RemoveStage(current stage) succeeded, want rejection")
	}

	// 追加一段空 prompt 的段（UI「+ 新增阶段」就是这种）→ 只入流水尾，不推进。
	// 然后删掉它：指针仍停在 1。
	added, err := svc.AddStage(ctx, AddStageInput{
		RootID: root.ID,
		TaskID: taskID,
		Stage:  agentStage("Follow-up", ""),
	})
	if err != nil {
		t.Fatalf("AddStage: %v", err)
	}
	if len(added.Task.Stages) != 3 {
		t.Fatalf("stages len=%d, want 3", len(added.Task.Stages))
	}
	if added.Task.CurrentStageIndex != 1 {
		t.Fatalf("current_stage_index=%d, want 1（空 prompt 追加不推进）", added.Task.CurrentStageIndex)
	}
	got, err = svc.RemoveStage(ctx, RemoveStageInput{RootID: root.ID, TaskID: taskID, Index: 2})
	if err != nil {
		t.Fatalf("RemoveStage(appended tail): %v", err)
	}
	if len(got.Task.Stages) != 2 {
		t.Fatalf("stages len=%d, want 2", len(got.Task.Stages))
	}
	if got.Task.CurrentStageIndex != 1 {
		t.Fatalf("current_stage_index=%d, want 1", got.Task.CurrentStageIndex)
	}
}

// 删掉指针之前的段时，指针要跟着回移，不能指到别的段上。
func TestRemoveStageBeforeCurrentShiftsPointer(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Describe"),
			agentStage("Fix", "Fix {previous_input}"),
			userStage("Review"),
			agentStage("Polish", "Polish {previous_input}"),
		},
		Input: "input",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	taskID := detail.Task.ID

	// 走到 index 2（user 段，等待用户）。RerunStage 顶替了 Jump（d1f0e6e 删了后者）。
	if _, err := svc.RerunStage(ctx, MoveInput{RootID: root.ID, TaskID: taskID, StageIndex: 2}); err != nil {
		t.Fatalf("RerunStage: %v", err)
	}
	before, err := svc.GetTask(ctx, root.ID, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if before.Task.CurrentStageIndex != 2 {
		t.Fatalf("current_stage_index=%d, want 2", before.Task.CurrentStageIndex)
	}

	// index 1 在指针之前且未执行 → 可删，指针 2 回移到 1。
	got, err := svc.RemoveStage(ctx, RemoveStageInput{RootID: root.ID, TaskID: taskID, Index: 1})
	if err != nil {
		t.Fatalf("RemoveStage(before current): %v", err)
	}
	if len(got.Task.Stages) != 3 {
		t.Fatalf("stages len=%d, want 3", len(got.Task.Stages))
	}
	if got.Task.CurrentStageIndex != 1 {
		t.Fatalf("current_stage_index=%d, want 1（指针随删除回移）", got.Task.CurrentStageIndex)
	}
	if got.Task.Stages[1].Name != "Review" {
		t.Fatalf("stages[1].name=%q, want Review", got.Task.Stages[1].Name)
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
	// 等 goroutine 把任务状态落定（RerunStage 失败 → waiting_user），再断言确实执行了第二段。
	// 不能等 len(runner.execs)==2：那在 goroutine 头几条语句就满足，同步 GetTask 会跑赢
	// 剩余的 SQLite 落库尾巴，读到中间态（-count=10 下约 9/10 复现）。
	waitForCondition(t, func() bool {
		got, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		runner := svc.Runner.(*fakeRunner)
		return err == nil && got.Task.Status == StatusWaitingUser &&
			func() bool {
				runner.mu.Lock()
				defer runner.mu.Unlock()
				return len(runner.execs) == 2
			}()
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

// 会话名的 " / #编号" 只有一处派生（TaskSessionName），建会话和两条改名路径都走它，
// 改名才不会把后缀丢掉。表驱动钉住建会话那侧一直以来的行为。
func TestTaskSessionName(t *testing.T) {
	cases := []struct {
		name       string
		base       string
		taskNumber int
		want       string
	}{
		{name: "名 + 编号", base: "登录页闪退", taskNumber: 8, want: "登录页闪退 / #8"},
		{name: "没名字只留编号", base: "", taskNumber: 7, want: "#7"},
		{name: "legacy 无编号", base: "名", taskNumber: 0, want: "名"},
		{name: "已带本编号后缀不叠加", base: "名 / #8", taskNumber: 8, want: "名 / #8"},
		{name: "尾随空白先去掉", base: "  名  ", taskNumber: 8, want: "名 / #8"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TaskSessionName(tc.base, tc.taskNumber); got != tc.want {
				t.Fatalf("TaskSessionName(%q, %d) = %q, want %q", tc.base, tc.taskNumber, got, tc.want)
			}
		})
	}
}

// 剥离只认「自己的编号」：任务真名叫 foo / #3 而编号是 8 时不能被误伤。
func TestTrimTaskSessionNameSuffix(t *testing.T) {
	cases := []struct {
		name       string
		name_      string
		taskNumber int
		want       string
	}{
		{name: "剥掉本编号后缀", name_: "名 / #8", taskNumber: 8, want: "名"},
		{name: "编号不匹配保持原样", name_: "foo / #3", taskNumber: 8, want: "foo / #3"},
		{name: "不是数字保持原样", name_: "名 / #abc", taskNumber: 8, want: "名 / #abc"},
		{name: "无后缀原样返回", name_: "名", taskNumber: 8, want: "名"},
		{name: "legacy 无编号不动", name_: "名 / #8", taskNumber: 0, want: "名 / #8"},
		{name: "只剥最后一段", name_: "a / #1 / #8", taskNumber: 8, want: "a / #1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TrimTaskSessionNameSuffix(tc.name_, tc.taskNumber); got != tc.want {
				t.Fatalf("TrimTaskSessionNameSuffix(%q, %d) = %q, want %q", tc.name_, tc.taskNumber, got, tc.want)
			}
		})
	}
}

func TestMain(m *testing.M) {
	time.Local = time.UTC
	os.Exit(m.Run())
}

// ── worktree 目录消失后的行为 ─────────────────────────────────────
//
// 背景：WorktreePath 全仓只有 ensureTaskWorktree 一处写入，目录却可能事后被删
// （DELETE /api/git/worktrees、wt-finish.sh cleanup、手工 rm），没人清这个字段。
// 原先拿「字段非空」当「目录存在」，于是失效路径一路流到 agent 启动才报 chdir 错。

// 建树 → 把目录删掉 → 再点执行：必须立刻报「worktree 目录已不存在」，
// 而不是拿失效 cwd 去跑 agent。
func TestRunNowReportsMissingWorktreeInsteadOfUsingStalePath(t *testing.T) {
	ctx := context.Background()
	runner := &fakeRunner{}
	svc, root := newTestService(t, runner)
	// 三段：user → agent → user。留最后一段是必需的 —— 只有两段时 agent 段跑完就停在
	// 末段，RunNow 里的 Next 会在 moveTo 之前先撞上 "target stage out of range"，
	// 压根走不到 worktree 校验，测的就不是「立即执行」这条路了。
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:         root.ID,
		Stages:         []StageTemplate{userStage("Describe"), agentStage("Fix", "Fix this:\n{previous_input}"), userStage("Review")},
		Input:          "broken save button",
		CreateWorktree: true,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	// 先建出 worktree（走到 agent 段才会建）。
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	waitForCondition(t, func() bool {
		runner.mu.Lock()
		defer runner.mu.Unlock()
		return runner.worktreeCreateCalled
	})
	// 同上：先等在跑的那段落地，免得删目录和它的收尾写回抢同一行。
	waitForCondition(t, func() bool {
		d, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && d.Task.Status == StatusWaitingUser
	})
	before, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if !worktreeDirUsable(before.Task.WorktreePath) {
		t.Fatalf("worktree %q should exist right after creation", before.Task.WorktreePath)
	}
	runner.mu.Lock()
	execsBeforeDelete := len(runner.execs)
	runner.mu.Unlock()

	// 目录没了，字段还非空 —— 这正是要修的状态。
	if err := os.RemoveAll(before.Task.WorktreePath); err != nil {
		t.Fatalf("remove worktree: %v", err)
	}

	// 关键：点执行必须在**启动 agent 之前**就报出来。
	// RunNow 自己不能失败：用户点按钮要拿到的是卡片上的那条错误，不是一个 HTTP 500。
	if _, err := svc.RunNow(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	detail, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if !strings.Contains(detail.Task.AuxFlags.SessionError, "worktree 目录已不存在") {
		t.Fatalf("expected a missing-worktree error, got %q", detail.Task.AuxFlags.SessionError)
	}
	if !strings.Contains(detail.Task.AuxFlags.SessionError, before.Task.WorktreePath) {
		t.Fatalf("session error should name the missing path, got %q", detail.Task.AuxFlags.SessionError)
	}
	// agent 不能被启动：它一跑起来就会在失效 cwd 上失败。
	runner.mu.Lock()
	execsAfterDelete := len(runner.execs)
	runner.mu.Unlock()
	if execsAfterDelete != execsBeforeDelete {
		t.Fatalf("agent ran %d extra time(s) with a deleted worktree", execsAfterDelete-execsBeforeDelete)
	}
}

// 重建：目录没了之后点「重建 worktree」要真把树建回来，路径恢复可用。
func TestRebuildTaskWorktreeRecreatesDeletedDirectory(t *testing.T) {
	ctx := context.Background()
	runner := &fakeRunner{}
	svc, root := newTestService(t, runner)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:         root.ID,
		Stages:         []StageTemplate{userStage("Describe"), agentStage("Fix", "Fix this:\n{previous_input}")},
		Input:          "broken save button",
		CreateWorktree: true,
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
		return runner.worktreeCreateCalled
	})
	before, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	// 必须等在跑的 agent 段落地，否则下面删目录会和它收尾时那次 UpdateTask 抢同一行：
	// executeTask 拿的是自己那份 in-memory task，整行写回会把重建好的新路径覆盖成旧路径。
	// 顺带把任务留在 waiting_user（首段 user 段批准后即终止），再点执行时
	// RunNow 走的就是真实生产路径。
	waitForCondition(t, func() bool {
		d, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && d.Task.Status == StatusWaitingUser
	})
	runner.mu.Lock()
	if n := len(runner.execs); n != 1 {
		runner.mu.Unlock()
		t.Fatalf("expected the agent stage to have run once, got %d", n)
	}
	runner.mu.Unlock()

	before, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	oldPath := before.Task.WorktreePath
	if err := os.RemoveAll(oldPath); err != nil {
		t.Fatalf("remove worktree: %v", err)
	}

	detail, err = svc.RebuildTaskWorktree(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID})
	if err != nil {
		t.Fatalf("RebuildTaskWorktree: %v", err)
	}
	if !worktreeDirUsable(detail.Task.WorktreePath) {
		t.Fatalf("rebuilt worktree %q is still not usable", detail.Task.WorktreePath)
	}
	if detail.Task.AuxFlags.SessionError != "" {
		t.Fatalf("rebuild should clear the stale error, got %q", detail.Task.AuxFlags.SessionError)
	}
	// 重建后的路径要落库，不能只在内存里对。
	reloaded, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask after rebuild: %v", err)
	}
	if reloaded.Task.WorktreePath != detail.Task.WorktreePath {
		t.Fatalf("rebuilt path not persisted: %q vs %q", reloaded.Task.WorktreePath, detail.Task.WorktreePath)
	}
}

// 幂等：目录还在时重建是 no-op，不重复建树。
func TestRebuildTaskWorktreeIsNoopWhenDirectoryExists(t *testing.T) {
	ctx := context.Background()
	runner := &fakeRunner{}
	svc, root := newTestService(t, runner)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:         root.ID,
		Stages:         []StageTemplate{userStage("Describe"), agentStage("Fix", "Fix this:\n{previous_input}")},
		Input:          "broken save button",
		CreateWorktree: true,
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
		return runner.worktreeCreateCalled
	})
	runner.mu.Lock()
	before := len(runner.createdDirs)
	runner.mu.Unlock()

	if _, err := svc.RebuildTaskWorktree(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("RebuildTaskWorktree: %v", err)
	}
	runner.mu.Lock()
	after := len(runner.createdDirs)
	runner.mu.Unlock()
	if after != before {
		t.Fatalf("rebuild created another worktree (%d -> %d) while one exists", before, after)
	}
}

// worktree_missing 是派生的：目录没了转 true，重建后转回 false。
//
// 判据是 WorktreeMissingNow() 而不是 WorktreeMissing 字段：后者是 json:"-" 的派生槽位，
// 只在 MarshalJSON 内部被赋值，从库里读出来的 Task 上恒为零值。拿它断言等于什么都没测。
func TestTaskWorktreeMissingIsDerivedFromDirectory(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "afile")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	cases := []struct {
		name string
		task Task
		want bool
	}{
		{"没建过 worktree", Task{CreateWorktree: true}, false},
		{"目录还在", Task{CreateWorktree: true, WorktreePath: dir}, false},
		{"目录已删", Task{CreateWorktree: true, WorktreePath: filepath.Join(dir, "deleted")}, true},
		// 没开 worktree 的任务不该被报成失效（那不是它的 worktree）。
		{"任务本身没开 worktree", Task{CreateWorktree: false, WorktreePath: filepath.Join(dir, "deleted")}, false},
		// 路径存在但不是目录（cwd 必须是目录）同样算失效。
		{"路径是文件", Task{CreateWorktree: true, WorktreePath: filePath}, true},
	}
	for _, tc := range cases {
		if got := tc.task.WorktreeMissingNow(); got != tc.want {
			t.Errorf("%s: WorktreeMissingNow() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// 序列化后仍带着派生的 worktree_missing：前端靠这个字段改徽标。
func TestTaskJSONIncludesDerivedWorktreeMissing(t *testing.T) {
	gone := Task{ID: "t1", CreateWorktree: true, WorktreePath: filepath.Join(t.TempDir(), "deleted")}
	raw, err := json.Marshal(gone)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out["worktree_missing"] != true {
		t.Fatalf("worktree_missing=%v, want true", out["worktree_missing"])
	}
	// 反序列化不能把派生字段当持久化字段读回来（它每次都该重算）。
	var back Task
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal into Task: %v", err)
	}
	live := Task{ID: "t2", CreateWorktree: true, WorktreePath: t.TempDir()}
	rawLive, err := json.Marshal(live)
	if err != nil {
		t.Fatalf("marshal live: %v", err)
	}
	if err := json.Unmarshal(rawLive, &out); err != nil {
		t.Fatalf("unmarshal live: %v", err)
	}
	if out["worktree_missing"] != false {
		t.Fatalf("worktree_missing=%v, want false", out["worktree_missing"])
	}
}

// ClearTaskWorktree：repoint 后任务侧要跟着解绑。
func TestClearTaskWorktreeDetachesTask(t *testing.T) {
	ctx := context.Background()
	runner := &fakeRunner{}
	svc, root := newTestService(t, runner)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:         root.ID,
		Stages:         []StageTemplate{userStage("Describe"), agentStage("Fix", "Fix this:\n{previous_input}")},
		Input:          "broken save button",
		CreateWorktree: true,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	// 等**落库后的**路径，不是 fake 的 worktreeCreateCalled：那个标志在
	// CreateTaskWorktree 入口就置位了，早于 service.go:1028 的 UpdateTask。
	// 按标志等会在两个方向上翻车 —— 抢在写库之前读会看到空路径，之后清完再看
	// 又会被后台那次写入把路径写回来（两种失败都跟 ClearTaskWorktree 无关）。
	var before TaskDetail
	waitForCondition(t, func() bool {
		got, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		if err != nil {
			return false
		}
		before = got
		return strings.TrimSpace(got.Task.WorktreePath) != ""
	})
	if strings.TrimSpace(before.Task.WorktreePath) == "" {
		t.Fatalf("worktree path should be set before clearing")
	}

	if err := svc.ClearTaskWorktree(ctx, root.ID, detail.Task.ID); err != nil {
		t.Fatalf("ClearTaskWorktree: %v", err)
	}
	after, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask after clear: %v", err)
	}
	if after.Task.WorktreePath != "" || after.Task.WorktreeRootID != "" {
		t.Fatalf("worktree refs should be cleared, got path=%q rootID=%q",
			after.Task.WorktreePath, after.Task.WorktreeRootID)
	}
	// 幂等：再清一次不报错。
	if err := svc.ClearTaskWorktree(ctx, root.ID, detail.Task.ID); err != nil {
		t.Fatalf("ClearTaskWorktree should be idempotent, got %v", err)
	}
}

// 建树返回「成功」但产出不可用时，必须当失败，绝不能当成建过了。
//
// err == nil 不等于树建起来了。空路径一旦被当成成功写进库，两个不可逆的后果同时
// 发生：agent 拿到空的 RuntimeRootPath（见 service.go 取的就是 task.WorktreePath），
// 于是在**主 checkout** 里跑、改的是主干；界面那边看到「建过 + 路径为空」，把一棵
// 根本没建出来的活说成「已收尾」。
func TestBuildingAnUnusableWorktreeIsNotRecordedAsBuilt(t *testing.T) {
	runner := &fakeRunner{worktreeInfo: WorktreeInfo{RootID: "wt-root"}} // 成功，但没有 Path
	svc, root := newTestService(t, runner)
	ctx := context.Background()

	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:         root.ID,
		Stages:         []StageTemplate{userStage("Describe"), agentStage("Fix", "Fix:\n{previous_input}")},
		Input:          "something to fix",
		CreateWorktree: true,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	// Next 必须当场拒绝：建树没产出可用目录，推到 agent 段就会在主 checkout 里跑。
	_, err = svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID})
	if err == nil {
		t.Fatal("advancing into an agent stage must fail when the worktree creation produced nothing usable")
	}

	after := mustGetTask(t, svc, root.ID, detail.Task.ID)
	if strings.TrimSpace(after.WorktreePath) != "" {
		t.Fatalf("an unusable path must not be persisted as the task's worktree, got %q", after.WorktreePath)
	}
	if after.WorktreeBuilt {
		t.Fatal("a worktree that was never actually created must not be recorded as built — that flag drives the \"finished\" badge")
	}
	// 更要紧的：agent 绝不能因此在主 checkout 里跑起来。
	runner.mu.Lock()
	execs := append([]AgentStageExecution(nil), runner.execs...)
	runner.mu.Unlock()
	for _, exec := range execs {
		if strings.TrimSpace(exec.RuntimeRootPath) == "" && after.CreateWorktree {
			t.Fatalf("the agent ran with no worktree cwd (task=%s): it would edit the main checkout", exec.Task.ID)
		}
	}
}

// 路径指向文件而不是目录时同样不可用：cwd 必须是目录。
func TestBuildingAWorktreeThatIsAFileIsNotRecordedAsBuilt(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write decoy file: %v", err)
	}
	runner := &fakeRunner{worktreeInfo: WorktreeInfo{RootID: "wt-root", Path: file}}
	svc, root := newTestService(t, runner)
	ctx := context.Background()

	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:         root.ID,
		Stages:         []StageTemplate{userStage("Describe"), agentStage("Fix", "Fix:\n{previous_input}")},
		Input:          "something to fix",
		CreateWorktree: true,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err == nil {
		t.Fatal("a path that is not a directory must not be accepted as a worktree")
	}
	if !runner.worktreeCreateCalled {
		t.Fatal("the fixture must actually have reached worktree creation")
	}

	after := mustGetTask(t, svc, root.ID, detail.Task.ID)
	if after.WorktreeBuilt {
		t.Fatal("a path that is not a directory must not be recorded as a built worktree")
	}
}

// mustGetDetail 取一次任务详情并在出错时立刻失败，省掉每个用例重复的 err 检查。
// 与 worktree_finish_stage_test.go 的 mustGetTask（返回 Task）分开：这里要 stage_runs。
func mustGetDetail(t *testing.T, svc *Service, rootID, taskID string) TaskDetail {
	t.Helper()
	d, err := svc.GetTask(context.Background(), rootID, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	return d
}

// agent 段停在 waiting_user（没输出 [STAGE-DONE:N]）时，用户手动点「立即执行」要能推进。
//
// 2026-10-03 实测（mindfs 任务 26）：定位段把活干完了但没回报完成，于是
//   - 详情面板按 canAdvanceFromCurrentStage 不给按钮，
//   - 看板卡片只查 hasLaterStage，给了个「立即执行」，
//   - 点下去 moveRelative 报错、RunNow 又把错吞掉只回未变的详情。
// 结果任务彻底卡死，用户唯一想做的事（就这样，推进到下一段）没有任何入口能达成。
//
// 自动推进那条防线不在此处：引擎走 moveTo（auto_advanced 事件），根本不经过
// canLeaveStageOnRequest —— TestAgentStageWithoutDoneMarkerStopsAtCurrentStage 守着它。
func TestRunNowAdvancesAgentStageThatStoppedWaitingForUser(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{result: StageResult{Outcome: StageOutcomeSilent}})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Describe"),
			agentStage("Locate", "Locate:\n{previous_input}"),
			agentStage("Fix", "Fix it."),
		},
		Input: "find it",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next to agent: %v", err)
	}
	waitForCondition(t, func() bool {
		d, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && d.Task.Status == StatusWaitingUser && d.Task.CurrentStageIndex == 1
	})

	// 复现形状：RunNow 在 waiting_user 下走 Next；以前 moveRelative 拒绝、错误被吞掉。
	after := mustGetDetail(t, svc, root.ID, detail.Task.ID)
	if after.Task.Status != StatusWaitingUser {
		t.Fatalf("precondition: status = %s, want waiting_user", after.Task.Status)
	}
	if _, err := svc.RunNow(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("RunNow on an unreported agent stage must advance, got %v", err)
	}
	moved := mustGetDetail(t, svc, root.ID, detail.Task.ID)
	if moved.Task.CurrentStageIndex != 2 {
		t.Fatalf("current stage = %d, want 2（用户点了就该推进到下一段）", moved.Task.CurrentStageIndex)
	}
	if len(moved.StageRuns) != 3 {
		t.Fatalf("stage run count = %d, want 3（下一段被植进来了）", len(moved.StageRuns))
	}
}

// 引擎的自动推进仍然不许跳过没回报完成的 agent 段：它走 moveTo，不走
// canLeaveStageOnRequest。这条与上一条是一对 —— 放宽的只有人点的那条路。
func TestAutoAdvanceStillStopsAtUnreportedAgentStage(t *testing.T) {
	ctx := context.Background()
	// auto_advance=true 但 agent 静默：引擎跑完这一段后必须停在原地。
	svc, root := newTestService(t, &fakeRunner{result: StageResult{Outcome: StageOutcomeSilent}})
	implement := agentStage("Implement", "Implement {previous_input}")
	implement.AutoAdvance = true
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{userStage("Describe"), implement, agentStage("Review", "Review.")},
		Input: "change",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	waitForCondition(t, func() bool {
		d, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && d.Task.Status == StatusWaitingUser && d.Task.AuxFlags.SessionError != ""
	})
	d := mustGetDetail(t, svc, root.ID, detail.Task.ID)
	if d.Task.CurrentStageIndex != 1 {
		t.Fatalf("current stage = %d, want 1（引擎不许自己跳过没回报的段）", d.Task.CurrentStageIndex)
	}
	if len(d.StageRuns) != 2 {
		t.Fatalf("stage run count = %d, want 2（下一段没被植进来）", len(d.StageRuns))
	}
}

// fail / cancelled / rejected 仍然拦得住：放宽的只有 waiting_user 一种。
func TestRunNowStillRejectsFailedAgentStage(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{runErr: errors.New("agent unavailable")})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Describe"),
			agentStage("Fix", "Fix:\n{previous_input}"),
			agentStage("Review", "Review."),
		},
		Input: "broken",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next to agent: %v", err)
	}
	waitForCondition(t, func() bool {
		d, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && d.Task.CurrentStageIndex == 1 && d.Task.AuxFlags.SessionError != ""
	})
	// RunNow 的 waiting_user 分支照旧把错误吞掉回详情（前端有那条人话可看），
	// 所以断言的是**指针没动**，而不是 HTTP 层报错。
	if _, err := svc.RunNow(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	d := mustGetDetail(t, svc, root.ID, detail.Task.ID)
	if d.Task.CurrentStageIndex != 1 {
		t.Fatalf("current stage = %d, want 1（失败段仍不许被推走）", d.Task.CurrentStageIndex)
	}
}

// running / paused 下的「立即执行」不再静默回 200：调用方拿得到明确错误，
// 而不是 apply 完一个没变的详情、看着像按钮坏了。
func TestRunNowRejectsRunningTaskWithExplicitError(t *testing.T) {
	ctx := context.Background()
	runner := &fakeRunner{}
	runner.gate = make(chan struct{})
	svc, root := newTestService(t, runner)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Describe"),
			agentStage("Fix", "Fix:\n{previous_input}"),
			agentStage("Review", "Review."),
		},
		Input: "change",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next to agent: %v", err)
	}
	waitForCondition(t, func() bool {
		d, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && d.Task.Status == StatusRunning
	})
	if _, err := svc.RunNow(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err == nil {
		t.Fatal("RunNow on a running task must return an explicit error, not a silent 200")
	}
	close(runner.gate)
}

// 模板库只有主节点一份，但模板可以限定到某个项目（TaskTemplate.RootID）。
// 列表按项目过滤时要给出「全局 + 本项目」的并集：全局模板在哪儿都能用，
// 而**别的**项目的专用模板不该出现在这个项目的下拉框里 —— 那会让菜单变成一张
// 「这张模板根本不适用于本项目」的名单。
func TestListTaskTemplatesForRootScopesByProject(t *testing.T) {
	store := NewTemplateStoreAt(t.TempDir())
	save := func(id, name, rootID string) {
		t.Helper()
		_, err := store.SaveTaskTemplate(TaskTemplate{
			ID:   id,
			Name: name,
			// 第一段必须是 user 段，SaveTaskTemplate 会校验。
			Stages: []TaskTemplateStage{{
				ID:       id + "_s0",
				Position: 0,
				Snapshot: StageTemplate{ID: id + "_u", Name: "输入", Role: RoleUser, PromptTemplate: "do"},
			}},
			RootID: rootID,
		})
		if err != nil {
			t.Fatalf("SaveTaskTemplate(%s): %v", id, err)
		}
	}
	save("tmpl_global", "全局", "")
	save("tmpl_mine", "本项目", "CMAI")
	save("tmpl_theirs", "别人的", "mindfs")

	ids := func(items []TaskTemplate) []string {
		out := make([]string, 0, len(items))
		for _, item := range items {
			out = append(out, item.ID)
		}
		return out
	}

	items, err := store.ListTaskTemplatesForRoot("CMAI")
	if err != nil {
		t.Fatalf("ListTaskTemplatesForRoot(CMAI): %v", err)
	}
	got := ids(items)
	if len(got) != 2 {
		t.Fatalf("templates for CMAI = %v, want global + own", got)
	}
	// 排序按名字（中文按 unicode 码位），所以别依赖顺序，只看集合。
	if !contains(got, "tmpl_global") || !contains(got, "tmpl_mine") {
		t.Fatalf("templates for CMAI = %v, want [tmpl_global tmpl_mine]", got)
	}
	if contains(got, "tmpl_theirs") {
		t.Fatalf("another project's template leaked into CMAI: %v", got)
	}

	// 没带项目 = 只给全局的。建任务面板总是带着项目来，但工作台这种没有项目上下文的
	 // 地方不该看到一堆「只对某个项目成立」的模板。
	globals, err := store.ListTaskTemplatesForRoot("")
	if err != nil {
		t.Fatalf("ListTaskTemplatesForRoot(\"\"): %v", err)
	}
	if ids := ids(globals); len(ids) != 1 || ids[0] != "tmpl_global" {
		t.Fatalf("unscoped templates = %v, want [tmpl_global]", ids)
	}
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// 建任务时前端会把模板的流水随包带上（worker 节点上没有模板库，回查必然失败）。
// 后端这条路径要能只靠 payload 建出任务，模板名也用前端传来的快照。
func TestCreateTaskUsesInlinedStagesAndTemplateName(t *testing.T) {
	registry := fs.NewRootInfo("root", "root", t.TempDir())
	svc := NewService(NewTemplateStoreAt(t.TempDir()), testRoots{root: registry})

	// 本机模板库里**没有**这个模板 —— 模拟 worker 节点。
	detail, err := svc.CreateTask(context.Background(), CreateTaskInput{
		RootID:           registry.ID,
		Stages:           []StageTemplate{{Name: "输入", Role: RoleUser, PromptTemplate: "do {previous_input}"}, agentStage("Fix", "Fix:\n{previous_input}")},
		TaskTemplateID:   "tmpl_only_on_primary",
		TaskTemplateName: "论文评审",
		Input:            "review",
	})
	if err != nil {
		t.Fatalf("CreateTask with inlined stages: %v", err)
	}
	if len(detail.Task.Stages) != 2 {
		t.Fatalf("stages = %d, want 2 (the payload's, not a template lookup)", len(detail.Task.Stages))
	}
	if detail.Task.TaskTemplateName != "论文评审" {
		t.Fatalf("template name = %q, want the frontend snapshot", detail.Task.TaskTemplateName)
	}
}

// 本机模板库里**有**这个模板时，本机的名字优先：前端那份快照只是兜底
// （用户在模板弹窗里改了名字，前端那份可能过期）。
func TestCreateTaskPrefersLocalTemplateName(t *testing.T) {
	registry := fs.NewRootInfo("root", "root", t.TempDir())
	templates := NewTemplateStoreAt(t.TempDir())
	svc := NewService(templates, testRoots{root: registry})

	if _, err := templates.SaveTaskTemplate(TaskTemplate{
		ID:   "tmpl_saved",
		Name: "本机名字",
		Stages: []TaskTemplateStage{{
			ID:       "tmpl_saved_s0",
			Position: 0,
			Snapshot: StageTemplate{ID: "tmpl_saved_u", Name: "输入", Role: RoleUser, PromptTemplate: "do"},
		}},
	}); err != nil {
		t.Fatalf("SaveTaskTemplate: %v", err)
	}

	detail, err := svc.CreateTask(context.Background(), CreateTaskInput{
		RootID:           registry.ID,
		TaskTemplateID:   "tmpl_saved",
		TaskTemplateName: "过期的前端快照",
		Input:            "go",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if detail.Task.TaskTemplateName != "本机名字" {
		t.Fatalf("template name = %q, want the local store's", detail.Task.TaskTemplateName)
	}
	if detail.Task.Name != "本机名字" {
		t.Fatalf("task name = %q, want the template's", detail.Task.Name)
	}
}
