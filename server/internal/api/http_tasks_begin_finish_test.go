package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"mindfs/server/internal/kanban"
)

// 收尾按钮的分流（对话模型）：
//
//	① 执行体在动         → session_running（正常等待，不是错）
//	② 机械清场能直接做完 → teardown（**机械优先**：不等 agent）
//	③ 其余               → 起一轮收尾回合（finish_turn_started）
//
// 任务没有阶段，所以「重跑收尾段 / 追加收尾段」这两条路不存在；③ 就是普通对话的一轮，
// 收尾状态由 Service.Finishing 的内存标记表达。

// ② 分支已合进主干 → 当场机械清场。
//
// 这是「agent 那半已经做完了」的判据：活已经在主干里，再让 agent 跑一遍收尾回合只会
// 追加一次没意义的提交。清场是同步的，回来时目录已经拆了。
func TestBeginFinishRouteTearsDownWhenTheBranchIsAlreadyMerged(t *testing.T) {
	handler, svc, root, taskID := newBeginFinishHandler(t, "from worktree\n")
	ctx := context.Background()
	gitForAPITest(t, root.RootPath, "merge", "-q", "--no-ff", "task-1")
	worktreePath := mustWorktreePath(t, svc, root.ID, taskID)

	rec := postBeginFinish(t, handler, root.ID, taskID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if action := beginFinishAction(t, rec); action != "teardown" {
		t.Fatalf("action = %q, want teardown", action)
	}
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

// ③ 头一次收尾且机械做不了 → 起一轮收尾回合。
func TestBeginFinishRouteStartsAFinishTurn(t *testing.T) {
	handler, svc, root, taskID := newBeginFinishHandler(t, "from worktree\n")
	ctx := context.Background()
	leaveUncommittedWorktreeChange(t, svc, root.ID, taskID)

	rec := postBeginFinish(t, handler, root.ID, taskID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if action := beginFinishAction(t, rec); action != "finish_turn_started" {
		t.Fatalf("action = %q, want finish_turn_started", action)
	}
	// 收尾回合是异步起的：等它落定，任务回到 waiting_user（本轮没有完成标记）。
	waitForNotFinishing(t, svc, root.ID, taskID)
	detail, err := svc.GetTask(ctx, root.ID, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if detail.Task.Status != kanban.StatusWaitingUser {
		t.Fatalf("status = %q, want waiting_user after the finish turn", detail.Task.Status)
	}
	// 机械清场没跑成 / 没跑，worktree 必须还在 —— 那是 agent 接着干的地方。
	if _, statErr := os.Stat(mustWorktreePath(t, svc, root.ID, taskID)); statErr != nil {
		t.Fatalf("the worktree must survive: %v", statErr)
	}
}

// 正在收尾时再点一次：409，不并发起第二轮。
func TestBeginFinishRouteRefusesASecondClickWhileFinishing(t *testing.T) {
	handler, svc, root, taskID := newBeginFinishHandler(t, "from worktree\n")
	leaveUncommittedWorktreeChange(t, svc, root.ID, taskID)

	gate := make(chan struct{})
	entered := make(chan struct{}, 1)
	svc.SetDirectExecutor(&apiFinishTurn{reply: "处理中", entered: entered, gate: gate})

	first := postBeginFinish(t, handler, root.ID, taskID)
	if first.Code != http.StatusOK {
		t.Fatalf("click 1: status = %d, want 200; body=%s", first.Code, first.Body.String())
	}
	if action := beginFinishAction(t, first); action != "finish_turn_started" {
		t.Fatalf("click 1: action = %q, want finish_turn_started", action)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the finish turn never started")
	}

	second := postBeginFinish(t, handler, root.ID, taskID)
	if second.Code != http.StatusConflict {
		t.Fatalf("click 2: status = %d, want 409; body=%s", second.Code, second.Body.String())
	}
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &payload); err != nil || !strings.Contains(payload.Error, "正在收尾") {
		t.Fatalf("click 2 error = %q (decode err %v), want 正在收尾", payload.Error, err)
	}

	close(gate)
	waitForNotFinishing(t, svc, root.ID, taskID)

	// 回合结束后再点：又能起一轮（锁没有泄漏）。
	third := postBeginFinish(t, handler, root.ID, taskID)
	if third.Code != http.StatusOK {
		t.Fatalf("click 3: status = %d, want 200; body=%s", third.Code, third.Body.String())
	}
	if action := beginFinishAction(t, third); action != "finish_turn_started" {
		t.Fatalf("click 3: action = %q, want finish_turn_started", action)
	}
	waitForNotFinishing(t, svc, root.ID, taskID)
}

// ① 执行体在跑时点击 → 200 session_running（正常等待，不是错）。
func TestBeginFinishRouteWaitsWhileTheTaskIsRunning(t *testing.T) {
	handler, svc, root, taskID := newBeginFinishHandler(t, "from worktree\n")
	parkRunning(t, svc, root.ID, taskID)

	rec := postBeginFinish(t, handler, root.ID, taskID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (a running agent is a wait, not an error); body=%s", rec.Code, rec.Body.String())
	}
	if action := beginFinishAction(t, rec); action != "session_running" {
		t.Fatalf("action = %q, want session_running", action)
	}
}

// ② worktree 干净 + 合并无冲突 → **不等 agent**，当场机械清场。
func TestBeginFinishRouteGoesMechanicalWhenTheWorktreeIsClean(t *testing.T) {
	handler, svc, root, taskID := newBeginFinishHandler(t, "from worktree\n")
	worktreePath := mustWorktreePath(t, svc, root.ID, taskID)

	rec := postBeginFinish(t, handler, root.ID, taskID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if action := beginFinishAction(t, rec); action != "teardown" {
		t.Fatalf("action = %q, want teardown (a clean worktree must skip the agent)", action)
	}
	merged := runTestGitOutput(t, root.RootPath, "log", "--oneline", "-1")
	if strings.TrimSpace(merged) == "" {
		t.Fatal("the main checkout has no merge commit")
	}
	if _, statErr := os.Stat(worktreePath); !os.IsNotExist(statErr) {
		t.Fatalf("the worktree must be gone, stat err = %v", statErr)
	}
}

// 机械路径连点两次：第二次仍 200、不再合一次、如实说明「无事可做」。
func TestBeginFinishRouteMechanicalPathIsIdempotent(t *testing.T) {
	handler, _, root, taskID := newBeginFinishHandler(t, "from worktree\n")

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
	log := runTestGitOutput(t, root.RootPath, "log", "--oneline")
	if n := strings.Count(log, "Merge branch 'task-1'"); n != 1 {
		t.Fatalf("main has %d merges of task-1, want exactly 1:\n%s", n, log)
	}
}

// ② 合并会冲突 → 交给 agent，且把冲突文件报给前端。
func TestBeginFinishRouteDefersToAgentWhenTheMergeWouldConflict(t *testing.T) {
	handler, _, root, taskID := newBeginFinishHandler(t, "from worktree\n")
	if err := os.WriteFile(root.RootPath+"/note.txt", []byte("from main\n"), 0o644); err != nil {
		t.Fatalf("edit main: %v", err)
	}
	gitForAPITest(t, root.RootPath, "add", "note.txt")
	gitForAPITest(t, root.RootPath, "commit", "-qm", "main edit")

	rec := postBeginFinish(t, handler, root.ID, taskID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if action := beginFinishAction(t, rec); action != "finish_turn_started" {
		t.Fatalf("action = %q, want finish_turn_started (a conflict must go through the agent)", action)
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

// ② worktree 里有没提交的活 → 交给 agent（机械清场不能替用户决定提交什么）。
func TestBeginFinishRouteDefersToAgentWhenTheWorktreeHasUncommittedWork(t *testing.T) {
	handler, svc, root, taskID := newBeginFinishHandler(t, "from worktree\n")
	leaveUncommittedWorktreeChange(t, svc, root.ID, taskID)

	rec := postBeginFinish(t, handler, root.ID, taskID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if action := beginFinishAction(t, rec); action != "finish_turn_started" {
		t.Fatalf("action = %q, want finish_turn_started", action)
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
