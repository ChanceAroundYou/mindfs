package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/server/internal/fs"
	"mindfs/server/internal/kanban"
)

// 这里跑的是真路由 + 真 handler + 真 git 仓库，不用 mock：收尾的失败分类
// （400 / 409）正是这个文件存在的理由，而 mock 掉 git 就等于把要验的东西
// 一起 mock 掉了 —— 「哪个退出码代表冲突」全靠 git 自己说话。

// singleRootProvider 照实查：rootID 对不上就报 not found。
// 无条件返回那一个 root 会把「root 找不到」这条路径也变成成功 ——
// 而那正是 TestFinishWorktreeRouteRejectsUnknownRootAs400 要钉的东西。
type singleRootProvider struct{ root fs.RootInfo }

func (p singleRootProvider) GetRoot(rootID string) (fs.RootInfo, error) {
	if rootID != p.root.ID {
		return fs.RootInfo{}, errors.New("root not found")
	}
	return p.root, nil
}
func (p singleRootProvider) ListRoots() []fs.RootInfo { return []fs.RootInfo{p.root} }

// fixedRunner 只实现「建树」这一件事：真跑 git worktree add，让 service
// 自己去落归属（归属只有一个写入出口，测试不去代劳那条路径）。
// EnsureAgentSession / RunAgentStage 不会被调用 —— 收尾前任务还没起过 agent。
type fixedRunner struct {
	t       *testing.T
	mainDir string
}

func (r fixedRunner) CreateTaskWorktree(_ context.Context, rootID, name, branchMode, branch string) (kanban.WorktreeInfo, error) {
	r.t.Helper()
	// 和 usecase.CreateGitWorktree 同一个规则：new 模式且没给分支名时用目录名当分支名。
	// 少了这一步就会拿空字符串去 `git worktree add -b` ——
	// 而 service 的 normalizeTaskWorktreeBranch 在 new 模式下**故意**返回空分支，
	// 补名是这一层的职责，不是 service 的。
	if branchMode == "new" && strings.TrimSpace(branch) == "" {
		branch = name
	}
	if strings.TrimSpace(branch) == "" {
		return kanban.WorktreeInfo{}, errors.New("branch required")
	}
	path := filepath.Join(r.mainDir, ".worktree", name)
	// `git worktree add` 的两种形态不一样：-b <branch> <path>（建新分支）vs
	// <path> <branch>（挂已有分支）。把 branch 两种情况都塞进去 git 会把
	// 第二个当成 ref 名，报 "invalid reference"。
	var args []string
	if branchMode != "existing" {
		args = []string{"worktree", "add", "-q", "-b", branch, path}
	} else {
		args = []string{"worktree", "add", "-q", path, branch}
	}
	gitForAPITest(r.t, r.mainDir, args...)
	return kanban.WorktreeInfo{RootID: rootID, Path: path}, nil
}

func (fixedRunner) EnsureAgentSession(context.Context, kanban.AgentStageExecution) (string, error) {
	return "", nil
}
func (fixedRunner) RunAgentStage(context.Context, kanban.AgentStageExecution) (kanban.StageResult, error) {
	return kanban.StageResult{}, nil
}
func (fixedRunner) TaskUpdated(string, kanban.TaskDetail) {}

func gitForAPITest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// newFinishTaskHandler 建一个带真 kanban service 的 handler，root 指向一个
// 「主 checkout + 一个已建好的 worktree」的真仓库。
func newFinishTaskHandler(t *testing.T, edit string) (http.Handler, fs.RootInfo, string) {
	t.Helper()
	mainDir := t.TempDir()
	gitForAPITest(t, mainDir, "init", "-q")
	gitForAPITest(t, mainDir, "symbolic-ref", "HEAD", "refs/heads/main")
	if err := os.WriteFile(filepath.Join(mainDir, "note.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base: %v", err)
	}
	gitForAPITest(t, mainDir, "add", "note.txt")
	gitForAPITest(t, mainDir, "commit", "-qm", "initial")

	root := fs.NewRootInfo("root", "root", mainDir)
	svc := kanban.NewService(kanban.NewTemplateStoreAt(t.TempDir()), singleRootProvider{root: root})
	// 树建在 mainDir/.worktree/<name> 下，名字由 service 的 renderWorktreeName 决定，
	// 所以 runner 按同一个拼法算路径（task-{number}）。
	svc.SetRunner(fixedRunner{t: t, mainDir: mainDir})

	detail, err := svc.CreateTask(context.Background(), kanban.CreateTaskInput{
		RootID: root.ID,
		// 首段必须是 user 段（createTask 的硬约束），第二段才是 agent 段。
		Stages: []kanban.StageTemplate{
			{Name: "Describe", Role: "user"},
			{Name: "Fix", Role: "agent"},
		},
		Input:              "fix it",
		CreateWorktree:     true,
		WorktreeBranchMode: "new",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	_ = detail

	// 建树：走 service 自己的入口（RebuildTaskWorktree），这样归属由 service 落库。
	if _, err := svc.RebuildTaskWorktree(context.Background(), kanban.MoveInput{
		RootID: root.ID, TaskID: mustTaskID(t, svc, root.ID),
	}); err != nil {
		t.Fatalf("RebuildTaskWorktree: %v", err)
	}

	taskID := mustTaskID(t, svc, root.ID)
	if edit != "" {
		wt := mustWorktreePath(t, svc, root.ID, taskID)
		if err := os.WriteFile(filepath.Join(wt, "note.txt"), []byte(edit), 0o644); err != nil {
			t.Fatalf("edit in worktree: %v", err)
		}
		gitForAPITest(t, wt, "add", "note.txt")
		gitForAPITest(t, wt, "commit", "-qm", "work")
	}
	return (&HTTPHandler{AppContext: &AppContext{Kanban: svc}}).Routes(), root, taskID
}

func mustTaskID(t *testing.T, svc *kanban.Service, rootID string) string {
	t.Helper()
	details, err := svc.ListTaskDetails(context.Background(), rootID, kanban.ListTasksOptions{})
	if err != nil {
		t.Fatalf("ListTaskDetails: %v", err)
	}
	if len(details) != 1 {
		t.Fatalf("tasks = %d, want 1", len(details))
	}
	return details[0].Task.ID
}

func mustWorktreePath(t *testing.T, svc *kanban.Service, rootID, taskID string) string {
	t.Helper()
	detail, err := svc.GetTask(context.Background(), rootID, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if strings.TrimSpace(detail.Task.WorktreePath) == "" {
		t.Fatal("worktree path is empty — the tree was never created")
	}
	return detail.Task.WorktreePath
}

func postFinish(t *testing.T, handler http.Handler, rootID, taskID string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"root_id": rootID, "delete_branch": true})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+taskID+"/finish-worktree", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestFinishWorktreeRouteMergesAndReturnsOK(t *testing.T) {
	handler, root, taskID := newFinishTaskHandler(t, "from worktree\n")

	rec := postFinish(t, handler, root.ID, taskID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// 合并必须真的落到主 checkout：路由通了不等于活干了。
	out, err := exec.Command("git", "-C", root.RootPath, "show", "main:note.txt").Output()
	if err != nil {
		t.Fatalf("read merged file: %v", err)
	}
	if !strings.Contains(string(out), "from worktree") {
		t.Fatalf("main did not receive the worktree content:\n%s", out)
	}
}

// 冲突必须是 409 且带文件清单：前端靠这个分流、把可点的文件列出来。
// 回 400/500 都会让「去人工解冲突」这句话和那份清单一起消失。
func TestFinishWorktreeRouteReportsConflictAs409WithFiles(t *testing.T) {
	handler, root, taskID := newFinishTaskHandler(t, "worktree wins\n")
	// 主 checkout 改同一行 —— 必冲突。
	if err := os.WriteFile(filepath.Join(root.RootPath, "note.txt"), []byte("main wins\n"), 0o644); err != nil {
		t.Fatalf("edit main: %v", err)
	}
	gitForAPITest(t, root.RootPath, "add", "note.txt")
	gitForAPITest(t, root.RootPath, "commit", "-qm", "main edit")

	rec := postFinish(t, handler, root.ID, taskID)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Error         string   `json:"error"`
		ConflictFiles []string `json:"conflict_files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.ConflictFiles) != 1 || payload.ConflictFiles[0] != "note.txt" {
		t.Fatalf("conflict_files = %v, want [note.txt]", payload.ConflictFiles)
	}
}

// 请求本身不成立（root 找不到）要回 400，不是 500：同文件里 next/run-now
// 都是 400。回 500 会让前端按「服务端炸了」处理，还顺带吐一个全零的 task 出去。
func TestFinishWorktreeRouteRejectsUnknownRootAs400(t *testing.T) {
	handler, _, taskID := newFinishTaskHandler(t, "work\n")

	rec := postFinish(t, handler, "no-such-root", taskID)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestFinishWorktreeRouteRequiresRootAndTaskID(t *testing.T) {
	handler, root, taskID := newFinishTaskHandler(t, "work\n")

	rec := postFinish(t, handler, "", taskID)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing root_id: status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	rec = postFinish(t, handler, root.ID, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing task id: status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}
