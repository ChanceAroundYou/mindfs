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

// newBeginFinishHandler 与 newFinishTaskHandler 同一套真 git 仓库，但把 service
// 也交出来 —— 分流判据要改任务状态（绑会话、追加收尾段），拿不到 service 就只能
// 从路由外面猜。
func newBeginFinishHandler(t *testing.T, edit string) (http.Handler, *kanban.Service, fs.RootInfo, string) {
	t.Helper()
	return newBeginFinishHandlerWithRunner(t, edit, func(mainDir string) kanban.Runner {
		return fixedRunner{t: t, mainDir: mainDir}
	})
}

// newBeginFinishHandlerWithRunner 与 newBeginFinishHandler 同一套真 git 仓库，只是换一个
// runner —— 用来造出「agent 段停在 waiting_user」这种由 runner 结局决定的局面。
//
// 必须在这里换而不是建完再换：推进到 agent 段（Next）发生在建仓库的过程中，runner 得在
// 那之前就位。
func newBeginFinishHandlerWithRunner(t *testing.T, edit string, runnerFor func(mainDir string) kanban.Runner) (http.Handler, *kanban.Service, fs.RootInfo, string) {
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
	svc.SetRunner(runnerFor(mainDir))

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
