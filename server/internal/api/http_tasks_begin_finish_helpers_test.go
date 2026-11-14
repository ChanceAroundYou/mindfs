package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"mindfs/server/internal/fs"
	"mindfs/server/internal/kanban"
)

// apiFinishTurn 是收尾回合的假直连执行端口。
//
// gate 非 nil 时卡住，用来制造「正在收尾」的窗口；entered 用来等回合真的跑起来
// （收尾是异步起的，点完直接断言会撞在回合还没开始的那一刻）。
type apiFinishTurn struct {
	mu      sync.Mutex
	reply   string
	entered chan struct{}
	gate    <-chan struct{}
}

func (f *apiFinishTurn) RunDirectActivity(ctx context.Context, task kanban.Task, body string) (string, error) {
	if f.entered != nil {
		select {
		case f.entered <- struct{}{}:
		default:
		}
	}
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reply, nil
}

// newBeginFinishHandler 建一套真 git 仓库 + 真 kanban service：主 checkout、
// 一个已建好的 linked worktree、一张 waiting_user 的对话任务。
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
	svc := kanban.NewService(singleRootProvider{root: root})
	svc.SetWriterGate(kanban.NewWriterGate())
	svc.SetRunner(fixedRunner{t: t, mainDir: mainDir})
	svc.SetDirectExecutor(&apiFinishTurn{reply: "收尾完成\n"})
	if _, err := svc.CreateTask(context.Background(), kanban.CreateTaskInput{
		RootID:             root.ID,
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
	if edit != "" {
		wt := mustWorktreePath(t, svc, root.ID, taskID)
		if err := os.WriteFile(wt+"/note.txt", []byte(edit), 0o644); err != nil {
			t.Fatalf("edit in worktree: %v", err)
		}
		gitForAPITest(t, wt, "add", "note.txt")
		gitForAPITest(t, wt, "commit", "-qm", "work")
	}
	parkWaitingUser(t, svc, root.ID, taskID)
	return (&HTTPHandler{AppContext: &AppContext{Kanban: svc}}).Routes(), svc, root, taskID
}

// parkWaitingUser 把任务停在 waiting_user：对话式任务没有「当前段」可推进，
// 状态就是上一轮跑完的投影；收尾只要求此刻没有执行体在动。
func parkWaitingUser(t *testing.T, svc *kanban.Service, rootID, taskID string) {
	t.Helper()
	store, err := svc.TaskStoreForRoot(rootID)
	if err != nil {
		t.Fatalf("TaskStoreForRoot: %v", err)
	}
	if err := store.UpdateTaskStatus(context.Background(), taskID, kanban.StatusWaitingUser, false); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}
}

// parkRunning 把任务标成 running，模拟「执行体正在动」。
func parkRunning(t *testing.T, svc *kanban.Service, rootID, taskID string) {
	t.Helper()
	store, err := svc.TaskStoreForRoot(rootID)
	if err != nil {
		t.Fatalf("TaskStoreForRoot: %v", err)
	}
	if err := store.UpdateTaskStatus(context.Background(), taskID, kanban.StatusRunning, false); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}
}

// waitForNotFinishing 等收尾回合结束（Finishing 是内存标记，回合一结束就落）。
func waitForNotFinishing(t *testing.T, svc *kanban.Service, rootID, taskID string) {
	t.Helper()
	for i := 0; i < 400; i++ {
		if !svc.Finishing(rootID, taskID) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the finish turn never settled")
}

// leaveUncommittedWorktreeChange 在 worktree 里留一个没提交的文件：
// loadPlan 会把收尾判成「机械做不了」，交给 agent 那一轮。
func leaveUncommittedWorktreeChange(t *testing.T, svc *kanban.Service, rootID, taskID string) {
	t.Helper()
	wt := mustWorktreePath(t, svc, rootID, taskID)
	if err := os.WriteFile(wt+"/wip.txt", []byte("wip\n"), 0o644); err != nil {
		t.Fatalf("write wip in worktree: %v", err)
	}
}

// runTestGitOutput 跑一条 git 命令并返回 stdout。
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
