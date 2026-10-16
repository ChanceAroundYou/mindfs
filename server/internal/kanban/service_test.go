package kanban

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

func TestMain(m *testing.M) {
	time.Local = time.UTC
	os.Exit(m.Run())
}

// ── worktree 目录消失后的行为 ─────────────────────────────────────
//
// 背景：WorktreePath 全仓只有 ensureTaskWorktree 一处写入，目录却可能事后被删
// （DELETE /api/git/worktrees、wt-finish.sh cleanup、手工 rm），没人清这个字段。
// 原先拿「字段非空」当「目录存在」，于是失效路径一路流到 agent 启动才报 chdir 错。
