package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"mindfs/server/internal/fs"
	"mindfs/server/internal/kanban"
)

// 收尾按钮的分流（2026-10-05 重做，2026-10-07 加「机械优先」）。
//
// 收尾是「agent + 机械清场」两个阶段的整合，分流把两者编成一条幂等的流水：
//
//	① 执行体在动            → session_running（正常等待，不是错）
//	② 机械清场能直接做完    → teardown（**机械优先**：不等 agent）
//	③ 已有收尾段            → nudged（不追加第二段）
//	④ 有 agent 段可继承     → stage_added（agent 先 commit + merge）
//	④.5 没有 agent 段        → teardown（没有 agent 可继承）
//
// 以前这个路由无条件调 BeginFinishWorktree，于是「agent 已经把活合完了、只差机械
// 清场」的任务会撞上「该任务已在收尾流程中」被 409 顶回来 —— 界面上表现为收尾一直
// 转、点不动、也结束不了。现在按真实状态分流，且每条分支都幂等。

// newBeginFinishHandler 与 newFinishTaskHandler 同一套真 git 仓库，但把 service
// 也交出来 —— 分流判据要改任务状态（绑会话、追加收尾段），拿不到 service 就只能
// 从路由外面猜。
func newBeginFinishHandler(t *testing.T, edit string) (http.Handler, *kanban.Service, fs.RootInfo, string) {
	t.Helper()
	mainDir := t.TempDir()
	gitForAPITest(t, mainDir, "init", "-q")
	gitForAPITest(t, mainDir, "symbolic-ref", "HEAD", "refs/heads/main")
	if err := os.WriteFile(mainDir+"/note.txt", []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base: %v", err)
	}
	gitForAPITest(t, mainDir, "add", "note.txt")
	gitForAPITest(t, mainDir, "commit", "-qm", "initial")

	root := fs.NewRootInfo("root", "root", mainDir)
	svc := kanban.NewService(kanban.NewTemplateStoreAt(t.TempDir()), singleRootProvider{root: root})
	svc.SetRunner(fixedRunner{t: t, mainDir: mainDir})

	if _, err := svc.CreateTask(context.Background(), kanban.CreateTaskInput{
		RootID: root.ID,
		Stages: []kanban.StageTemplate{
			{Name: "Describe", Role: "user"},
			{Name: "Fix", Role: "agent", Agent: "claude"},
		},
		Input:              "fix it",
		CreateWorktree:     true,
		WorktreeBranchMode: "new",
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	taskID := mustTaskID(t, svc, root.ID)
	if _, err := svc.RebuildTaskWorktree(context.Background(), kanban.MoveInput{
		RootID: root.ID, TaskID: taskID,
	}); err != nil {
		t.Fatalf("RebuildTaskWorktree: %v", err)
	}
	// 推进到 agent 段并等它跑完（fixedRunner 返回空结果 → waiting_user）。
	// BeginFinishWorktree 要求 waiting_user，pending 态会被拒。
	if _, err := svc.Next(context.Background(), kanban.MoveInput{RootID: root.ID, TaskID: taskID}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	waitForStatus(t, svc, root.ID, taskID, kanban.StatusWaitingUser)
	if edit != "" {
		wt := mustWorktreePath(t, svc, root.ID, taskID)
		if err := os.WriteFile(wt+"/note.txt", []byte(edit), 0o644); err != nil {
			t.Fatalf("edit in worktree: %v", err)
		}
		gitForAPITest(t, wt, "add", "note.txt")
		gitForAPITest(t, wt, "commit", "-qm", "work")
	}
	return (&HTTPHandler{AppContext: &AppContext{Kanban: svc}}).Routes(), svc, root, taskID
}

// waitForStatus 轮询直到任务到达指定状态。RunTask 是异步的，Next 返回时任务还在
// running —— 不等它跑完，后面所有断言都会撞上一个还在动的任务。
func waitForStatus(t *testing.T, svc *kanban.Service, rootID, taskID, status string) {
	t.Helper()
	for i := 0; i < 200; i++ {
		detail, err := svc.GetTask(context.Background(), rootID, taskID)
		if err != nil {
			t.Fatalf("GetTask: %v", err)
		}
		if detail.Task.Status == status {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("task did not reach status %s (still %s)", status, func() string {
		detail, _ := svc.GetTask(context.Background(), rootID, taskID)
		return detail.Task.Status
	}())
}

// waitForNotBusy 等任务不再有执行体在动。
//
// 必须等：RunTask / RerunStage 都是异步的，段还 running 时再点收尾会命中 ①
// 拿到 session_running —— 那是**正确**行为（收尾要等 agent 停），但会让「分流到
// 哪一条」的断言测不到。
func waitForNotBusy(t *testing.T, svc *kanban.Service, rootID, taskID string) {
	t.Helper()
	for i := 0; i < 400; i++ {
		if !svc.TaskBusy(context.Background(), rootID, taskID) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("task stayed busy — the stage never settled")
}

// leaveUncommittedWorktreeChange 在 worktree 里留一个没提交的文件。
//
// 这是「必须让 agent 先干点什么」的标准起点：worktree 有没提交的活时，机械清场
// 不能替用户决定哪些是成品、该怎么提交，只能交给 agent。
func leaveUncommittedWorktreeChange(t *testing.T, svc *kanban.Service, rootID, taskID string) {
	t.Helper()
	wt := mustWorktreePath(t, svc, rootID, taskID)
	if err := os.WriteFile(wt+"/wip.txt", []byte("wip\n"), 0o644); err != nil {
		t.Fatalf("write wip in worktree: %v", err)
	}
}

// runTestGitOutput 跑一条 git 命令并返回 stdout —— 断言「主干上到底合了什么」时
// 需要看输出，而 gitForAPITest 只关心成败。
func runTestGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return string(out)
}

func postBeginFinish(t *testing.T, handler http.Handler, rootID, taskID string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"root_id": rootID})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+taskID+"/begin-finish", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func beginFinishAction(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var payload struct {
		Action string `json:"action"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	if payload.Error != "" {
		t.Fatalf("unexpected error: %s", payload.Error)
	}
	return payload.Action
}

// ② 分支已合进主干 → 当场机械清场。
//
// 这是「agent 那半已经做完了」的判据：活已经在主干里，再让 agent 跑一遍收尾段只会
// 追加一次没意义的提交。清场是同步的，回来时目录已经拆了。
func TestBeginFinishRouteTearsDownWhenTheBranchIsAlreadyMerged(t *testing.T) {
	handler, svc, root, taskID := newBeginFinishHandler(t, "from worktree\n")
	ctx := context.Background()
	// 真合一次 —— 判据必须跟着真 git 走。
	gitForAPITest(t, root.RootPath, "merge", "-q", "--no-ff", "task-1")
	// 清场前抓到路径，清场后目录就没了。
	worktreePath := mustWorktreePath(t, svc, root.ID, taskID)

	rec := postBeginFinish(t, handler, root.ID, taskID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if action := beginFinishAction(t, rec); action != "teardown" {
		t.Fatalf("action = %q, want teardown", action)
	}
	// 清场必须真的落地：目录拆了、归属清了。
	if _, statErr := os.Stat(worktreePath); !os.IsNotExist(statErr) {
		t.Fatalf("the worktree must be gone, stat err = %v", statErr)
	}
	detail, err := svc.GetTask(ctx, root.ID, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if strings.TrimSpace(detail.Task.WorktreePath) != "" {
		t.Fatalf("worktree_path = %q, want cleared", detail.Task.WorktreePath)
	}
}

// ③ 已有收尾段但还没跑成 → 重跑那段（nudge），不追加第二段。
func TestBeginFinishRouteNudgesAnExistingFinishStage(t *testing.T) {
	// edit 让 worktree 分支有独立提交 —— 没有它分支是 main 的祖先，会被当成「已合入」
	// 直接清场，走不到 nudge 分支。
	handler, svc, root, taskID := newBeginFinishHandler(t, "from worktree\n")
	ctx := context.Background()
	if _, err := svc.BeginFinishWorktree(ctx, kanban.BeginFinishInput{RootID: root.ID, TaskID: taskID}); err != nil {
		t.Fatalf("BeginFinishWorktree: %v", err)
	}
	// 等收尾段跑停：还在跑时点击会命中 ①（正确但要等），测不到 ③。
	waitForNotBusy(t, svc, root.ID, taskID)
	// 留一份没提交的活，否则 ② 的机械优先会直接把场清了，走不到 ③。
	leaveUncommittedWorktreeChange(t, svc, root.ID, taskID)
	before, err := svc.GetTask(ctx, root.ID, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	stagesBefore := len(before.Task.Stages)

	rec := postBeginFinish(t, handler, root.ID, taskID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if action := beginFinishAction(t, rec); action != "nudged" {
		t.Fatalf("action = %q, want nudged", action)
	}
	after, err := svc.GetTask(ctx, root.ID, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if len(after.Task.Stages) != stagesBefore {
		t.Fatalf("stages = %d, want %d — a second finish stage must not be stacked", len(after.Task.Stages), stagesBefore)
	}
}

// ④ 头一次收尾 → 追加收尾段。
func TestBeginFinishRouteAppendsAFinishStageTheFirstTime(t *testing.T) {
	// edit 让 worktree 分支有独立提交 —— 没有它分支是 main 的祖先，会被当成「已合入」。
	handler, svc, root, taskID := newBeginFinishHandler(t, "from worktree\n")
	ctx := context.Background()
	leaveUncommittedWorktreeChange(t, svc, root.ID, taskID)

	rec := postBeginFinish(t, handler, root.ID, taskID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if action := beginFinishAction(t, rec); action != "stage_added" {
		t.Fatalf("action = %q, want stage_added", action)
	}
	detail, err := svc.GetTask(ctx, root.ID, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	last := detail.Task.Stages[len(detail.Task.Stages)-1]
	if !kanban.IsFinishStage(last) {
		t.Fatalf("last stage kind = %q, want the finish stage", last.Kind)
	}
}

// 幂等是这次重做的核心要求：连点三次，结果必须和点一次一样。
func TestBeginFinishRouteIsIdempotent(t *testing.T) {
	handler, svc, root, taskID := newBeginFinishHandler(t, "from worktree\n")
	ctx := context.Background()
	// 没提交的活 → 走 agent 路径（机械优先会退让），这是幂等最要紧的那条路：
	// 反复点不能堆出一串收尾段。
	leaveUncommittedWorktreeChange(t, svc, root.ID, taskID)

	// 第一次点击：追加收尾段。
	rec := postBeginFinish(t, handler, root.ID, taskID)
	if rec.Code != http.StatusOK {
		t.Fatalf("click 1: status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if action := beginFinishAction(t, rec); action != "stage_added" {
		t.Fatalf("click 1: action = %q, want stage_added", action)
	}
	// 等收尾段跑停：还在跑时下一次点击会命中 ①（正确但要等），测不到 ③。
	waitForNotBusy(t, svc, root.ID, taskID)
	detail, err := svc.GetTask(ctx, root.ID, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	stagesAfterFirst := len(detail.Task.Stages)

	// 第二、三次点击：重跑同一段，不追加新段。
	for i := 2; i <= 3; i++ {
		rec := postBeginFinish(t, handler, root.ID, taskID)
		if rec.Code != http.StatusOK {
			t.Fatalf("click %d: status = %d, want 200; body=%s", i, rec.Code, rec.Body.String())
		}
		if action := beginFinishAction(t, rec); action != "nudged" {
			t.Fatalf("click %d: action = %q, want nudged", i, action)
		}
		waitForNotBusy(t, svc, root.ID, taskID)
	}
	detail, err = svc.GetTask(ctx, root.ID, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if len(detail.Task.Stages) != stagesAfterFirst {
		t.Fatalf("stages = %d, want %d — repeated clicks must not stack finish stages", len(detail.Task.Stages), stagesAfterFirst)
	}
}

// ① 段在跑时点击 → 200 session_running（正常等待，不是错）。
//
// 以前只看会话探针，段在跑时会漏过去、让请求撞进收尾准入变成 409 —— 界面上表现为
// 「收尾一直转、点不动」。
func TestBeginFinishRouteWaitsWhileAnAgentStageIsRunning(t *testing.T) {
	handler, svc, root, taskID := newBeginFinishHandler(t, "from worktree\n")
	// 让下一次 agent 段调用卡住不返回，制造「段正在跑」。
	release := make(chan struct{})
	defer close(release)
	svc.SetRunner(&blockingRunner{fixedRunner: fixedRunner{t: t, mainDir: root.RootPath}, entered: make(chan struct{}, 1), release: release})
	// 重跑 **agent 段**（下标 1）—— 重跑 user 段只会回到 waiting_user，不会起 agent。
	if _, err := svc.RerunStage(context.Background(), kanban.MoveInput{RootID: root.ID, TaskID: taskID, StageIndex: 1}); err != nil {
		t.Fatalf("RerunStage: %v", err)
	}
	// 等段真的跑起来。
	select {
	case <-svc.Runner.(*blockingRunner).entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the stage never started")
	}

	rec := postBeginFinish(t, handler, root.ID, taskID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (a running agent is a wait, not an error); body=%s", rec.Code, rec.Body.String())
	}
	if action := beginFinishAction(t, rec); action != "session_running" {
		t.Fatalf("action = %q, want session_running", action)
	}
}

// ② worktree 干净 + 合并无冲突 → **不等 agent**，当场机械清场。
//
// 这是「机械优先」的核心：以前只要有 agent 段就一定先跑一遍收尾段，于是
// 「agent 早把活合完了、只差机械清场」的任务要白等一轮、还多一个空提交。
func TestBeginFinishRouteGoesMechanicalWhenTheWorktreeIsClean(t *testing.T) {
	handler, svc, root, taskID := newBeginFinishHandler(t, "from worktree\n")
	worktreePath := mustWorktreePath(t, svc, root.ID, taskID)
	// 分支有独立提交（edit 过），所以不会命中「已合入」那条 —— 走的正是机械合并。
	stagesBefore, err := svc.GetTask(context.Background(), root.ID, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	countBefore := len(stagesBefore.Task.Stages)

	rec := postBeginFinish(t, handler, root.ID, taskID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if action := beginFinishAction(t, rec); action != "teardown" {
		t.Fatalf("action = %q, want teardown (a clean worktree must skip the agent)", action)
	}
	// 真的合了，且目录真的没了。
	merged := runTestGitOutput(t, root.RootPath, "log", "--oneline", "-1")
	if strings.TrimSpace(merged) == "" {
		t.Fatal("the main checkout has no merge commit")
	}
	if _, statErr := os.Stat(worktreePath); !os.IsNotExist(statErr) {
		t.Fatalf("the worktree must be gone, stat err = %v", statErr)
	}
	// 没有追加收尾段 —— 机械路径不该给流水加段。
	after, err := svc.GetTask(context.Background(), root.ID, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if len(after.Task.Stages) != countBefore {
		t.Fatalf("stages = %d, want %d — the mechanical path must not append a finish stage",
			len(after.Task.Stages), countBefore)
	}
}

// 幂等是这次重做的核心要求：机械路径连点两次，第二次必须仍然 200，且**不再合一次**。
//
// 关键断言是「主干上只有一条 Merge branch 'task-1'」：第二次点击时任务已经被
// ClearWorktreeRefs 清了 worktree_path，如果下层对空路径不幂等、或者编排层又合了一遍，
// 主干上就会多出一条重复的合并提交。
func TestBeginFinishRouteMechanicalPathIsIdempotent(t *testing.T) {
	handler, svc, root, taskID := newBeginFinishHandler(t, "from worktree\n")

	first := postBeginFinish(t, handler, root.ID, taskID)
	if first.Code != http.StatusOK {
		t.Fatalf("click 1: status = %d, want 200; body=%s", first.Code, first.Body.String())
	}
	if action := beginFinishAction(t, first); action != "teardown" {
		t.Fatalf("click 1: action = %q, want teardown", action)
	}

	second := postBeginFinish(t, handler, root.ID, taskID)
	if second.Code != http.StatusOK {
		t.Fatalf("click 2: status = %d, want 200 (a repeat click must not error); body=%s", second.Code, second.Body.String())
	}
	if action := beginFinishAction(t, second); action != "teardown" {
		t.Fatalf("click 2: action = %q, want teardown", action)
	}
	// 第二次必须**什么都没做**，且如实说一句。
	var payload struct {
		Report struct {
			Note string `json:"note"`
		} `json:"report"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if strings.TrimSpace(payload.Report.Note) == "" {
		t.Fatal("the second click must report a note saying there was nothing left to do")
	}

	// 主干上只能有一条合并提交。
	log := runTestGitOutput(t, root.RootPath, "log", "--oneline")
	if n := strings.Count(log, "Merge branch 'task-1'"); n != 1 {
		t.Fatalf("main has %d merges of task-1, want exactly 1:\n%s", n, log)
	}
	_ = svc
}

// ② 合并会冲突 → 退给 agent，且把冲突文件报给前端。
func TestBeginFinishRouteDefersToAgentWhenTheMergeWouldConflict(t *testing.T) {
	handler, svc, root, taskID := newBeginFinishHandler(t, "from worktree\n")
	// 主 checkout 改同一行 → 机械合并会冲突。
	if err := os.WriteFile(root.RootPath+"/note.txt", []byte("from main\n"), 0o644); err != nil {
		t.Fatalf("edit main: %v", err)
	}
	gitForAPITest(t, root.RootPath, "add", "note.txt")
	gitForAPITest(t, root.RootPath, "commit", "-qm", "main edit")
	_ = svc

	rec := postBeginFinish(t, handler, root.ID, taskID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if action := beginFinishAction(t, rec); action != "stage_added" {
		t.Fatalf("action = %q, want stage_added (a conflict must go through the agent)", action)
	}
	var payload struct {
		Plan struct {
			Mechanical bool     `json:"mechanical"`
			Files      []string `json:"files"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Plan.Mechanical {
		t.Fatal("plan.mechanical = true, want false for a conflicting merge")
	}
	if len(payload.Plan.Files) != 1 || payload.Plan.Files[0] != "note.txt" {
		t.Fatalf("plan.files = %v, want [note.txt]", payload.Plan.Files)
	}
}

// ② worktree 里有没提交的活 → 退给 agent（机械清场不能替用户决定提交什么）。
func TestBeginFinishRouteDefersToAgentWhenTheWorktreeHasUncommittedWork(t *testing.T) {
	handler, svc, root, taskID := newBeginFinishHandler(t, "from worktree\n")
	leaveUncommittedWorktreeChange(t, svc, root.ID, taskID)

	rec := postBeginFinish(t, handler, root.ID, taskID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if action := beginFinishAction(t, rec); action != "stage_added" {
		t.Fatalf("action = %q, want stage_added", action)
	}
	var payload struct {
		Plan struct {
			Mechanical bool     `json:"mechanical"`
			Files      []string `json:"files"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Plan.Mechanical {
		t.Fatal("plan.mechanical = true, want false for uncommitted work")
	}
	if len(payload.Plan.Files) != 1 || payload.Plan.Files[0] != "wip.txt" {
		t.Fatalf("plan.files = %v, want [wip.txt]", payload.Plan.Files)
	}
}

// blockingRunner 让 agent 段调用卡住，制造「段正在跑」的窗口。
type blockingRunner struct {
	fixedRunner
	entered chan struct{}
	release chan struct{}
}

func (r *blockingRunner) RunAgentStage(ctx context.Context, exec kanban.AgentStageExecution) (kanban.StageResult, error) {
	select {
	case r.entered <- struct{}{}:
	default:
	}
	<-r.release
	return r.fixedRunner.RunAgentStage(ctx, exec)
}
