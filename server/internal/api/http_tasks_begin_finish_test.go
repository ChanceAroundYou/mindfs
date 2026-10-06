package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"mindfs/server/internal/fs"
	"mindfs/server/internal/kanban"
)

// 收尾按钮的四分支分流（2026-10-05 重做）。
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
	// edit 让 worktree 分支有独立提交 —— 没有它分支是 main 的祖先，会被当成「已合入」
	// 直接清场，走不到 stage_added 分支。
	handler, svc, root, taskID := newBeginFinishHandler(t, "from worktree\n")
	ctx := context.Background()

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

	// 第一次点击：追加收尾段。
	rec := postBeginFinish(t, handler, root.ID, taskID)
	if rec.Code != http.StatusOK {
		t.Fatalf("click 1: status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if action := beginFinishAction(t, rec); action != "stage_added" {
		t.Fatalf("click 1: action = %q, want stage_added", action)
	}
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
	}
	detail, err = svc.GetTask(ctx, root.ID, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if len(detail.Task.Stages) != stagesAfterFirst {
		t.Fatalf("stages = %d, want %d — repeated clicks must not stack finish stages", len(detail.Task.Stages), stagesAfterFirst)
	}
}
