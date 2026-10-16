package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/server/internal/fs"
	"mindfs/server/internal/kanban"
)

// silentRunner 让 agent 段「跑完了但没回报完成」→ 段停在 waiting_user。
//
// 默认的 fixedRunner 返回 StageResult{}，而 StageOutcomeDone 正好是零值 —— 于是段被记成
// success，把「停在 waiting_user」这条最难走的路整个绕开了。要测它必须显式给 Silent。
type silentRunner struct{ fixedRunner }

// ④ 收尾要越过「停在 waiting_user 的 agent 段」，而不是报错 —— 用户实测的那个拦路虎。
//
// 2026-10-08 task-9（日程管理 / 组件库）的真实形状，三件事叠在一起：
//  1. 分支已经合进主干（merge 在 21:41）；
//  2. 但 worktree 里还有 12 个文件没提交（23:30–00:41 做的），根本没进主干；
//  3. 当前段是 **agent 段的 waiting_user**（agent 干完了但没输出完成标记）。
//
// 修复前用户拿到的是 409 +「合并已成功，但 worktree 里还有没提交的东西」+ 一长串清单，
// 而他点的收尾本该走 ④：追加收尾段交给 agent，让它 commit + merge。
func TestBeginFinishRouteHandsAWaitingAgentStageToTheAgentInsteadOfErroring(t *testing.T) {
	handler, svc, root, taskID := newBeginFinishHandlerWithRunner(t, "from worktree\n", func(mainDir string) kanban.Runner {
		return silentRunner{fixedRunner: fixedRunner{t: t, mainDir: mainDir}}
	})
	// 前提：任务真的停在 agent 段的 waiting_user，而不是 fixture 悄悄记成 success。
	before, err := svc.GetTask(context.Background(), root.ID, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if before.Task.CurrentStageIndex != 1 || before.Task.Stages[1].Role != "agent" {
		t.Fatalf("precondition: current stage = %d (%q), want the agent stage",
			before.Task.CurrentStageIndex, before.Task.Stages[1].Role)
	}
	agentRunStatus := ""
	for _, run := range before.StageRuns {
		if run.StageIndex == 1 {
			agentRunStatus = run.Status
		}
	}
	if agentRunStatus != kanban.StageStatusWaitingUser {
		t.Fatalf("precondition: agent run status = %q, want waiting_user", agentRunStatus)
	}

	// ① 分支已合进主干 ② worktree 里还有没提交的活。
	gitForAPITest(t, root.RootPath, "merge", "-q", "--no-ff", "task-1")
	leaveUncommittedWorktreeChange(t, svc, root.ID, taskID)

	rec := postBeginFinish(t, handler, root.ID, taskID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — 收尾是人点的动作，不该被「评论」那条规则拒掉; body=%s",
			rec.Code, rec.Body.String())
	}
	if action := beginFinishAction(t, rec); action != "stage_added" {
		t.Fatalf("action = %q, want stage_added", action)
	}
	var payload struct {
		Plan struct {
			Mechanical bool     `json:"mechanical"`
			Reason     string   `json:"reason"`
			Files      []string `json:"files"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// 判定必须说「有活要交出去」，而不是「只剩清场」—— 分支合过不等于树里没活。
	if payload.Plan.Mechanical {
		t.Fatal("plan.mechanical = true, want a merged branch with uncommitted work to go through the agent")
	}
	if len(payload.Plan.Files) != 1 || payload.Plan.Files[0] != "wip.txt" {
		t.Fatalf("plan.files = %v, want [wip.txt]", payload.Plan.Files)
	}
	// 收尾段真的追加上去了，而且 worktree 还在（agent 要接着在那儿干活）。
	after, err := svc.GetTask(context.Background(), root.ID, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	finishes := 0
	for _, stage := range after.Task.Stages {
		if strings.TrimSpace(stage.Kind) == kanban.StageKindWorktreeFinish {
			finishes++
		}
	}
	if finishes != 1 {
		t.Fatalf("finish stages = %d, want exactly 1", finishes)
	}
	if _, statErr := os.Stat(mustWorktreePath(t, svc, root.ID, taskID)); statErr != nil {
		t.Fatalf("the worktree must survive — the agent needs it: %v", statErr)
	}
}

// failingRunner 让 agent 段直接失败 → run 记成 fail、任务停在 waiting_user。
//
// （见 service.go 的 agent_stage.session_error 分支：它把 run 写成 StageStatusFail，
// 任务留在 waiting_user —— 不是终态，所以不会触发 reviveTerminalTask。）
type failingRunner struct{ fixedRunner }

// 两条路都断时，主 error 必须是 **agent 那条**，而且**不许把机械清场的文件清单提到顶层**。
//
// 修复前：响应里塞的是机械清场的报告（task-9 那次是 user_changes = 12 个文件），前端按
// 「哪个清单非空」挑弹窗，于是渲染成「合并已成功，但 worktree 里还有没提交的东西」+
// 一长串清单 —— 而真正的拦路虎在 agent 那条。**报错了对象比不报还糟**：用户会照着错的
// 原因去动手（那 12 个文件根本不用他碰）。
//
// 两条路都断的局面：主 checkout 里一个已跟踪文件有未提交改动（机械那条被挡，且**带文件
// 清单**），当前 agent 段的 run 是 fail（agent 那条也拒 —— fail 时评论与收尾段都不许
// 越过它）。
func TestBeginFinishRouteReportsTheAgentPathFailureWhenBothPathsAreBlocked(t *testing.T) {
	handler, svc, root, taskID := newBeginFinishHandlerWithRunner(t, "from worktree\n", func(mainDir string) kanban.Runner {
		return failingRunner{fixedRunner: fixedRunner{t: t, mainDir: mainDir}}
	})
	// 前提：当前 agent 段的 run 记成 fail —— 否则测不到「agent 那条也断」。
	before, err := svc.GetTask(context.Background(), root.ID, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	agentRunStatus := ""
	for _, run := range before.StageRuns {
		if run.StageIndex == 1 {
			agentRunStatus = run.Status
		}
	}
	if agentRunStatus != kanban.StageStatusFail {
		t.Fatalf("precondition: agent run status = %q, want fail", agentRunStatus)
	}

	// 主 checkout：已跟踪文件 + 未提交改动 → 机械清场被挡，而且 report 里**有**文件清单。
	scratch := filepath.Join(root.RootPath, "scratch.txt")
	if err := os.WriteFile(scratch, []byte("committed\n"), 0o644); err != nil {
		t.Fatalf("seed scratch: %v", err)
	}
	gitForAPITest(t, root.RootPath, "add", "scratch.txt")
	gitForAPITest(t, root.RootPath, "commit", "-qm", "seed scratch")
	if err := os.WriteFile(scratch, []byte("mine, uncommitted\n"), 0o644); err != nil {
		t.Fatalf("modify scratch: %v", err)
	}

	rec := postBeginFinish(t, handler, root.ID, taskID)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (两条路都断了，只能让人来决策); body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Error         string   `json:"error"`
		ConflictFiles []string `json:"conflict_files"`
		DirtyFiles    []string `json:"dirty_files"`
		UserChanges   []string `json:"user_changes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	// 主 error 是 agent 那条 —— 「任务此刻为什么不答应」的直接答案。
	// 机械那份（「主 checkout 有未提交改动…」）只是前因，当主 error 会让用户照着错的原因
	// 去动手：scratch.txt 根本不是这次收尾的拦路虎。
	if !strings.Contains(payload.Error, "current stage is") {
		t.Fatalf("error = %q, want the agent-path reason (当前段为什么不答应)", payload.Error)
	}
	// 顶层一个清单都不许有：前端按「哪个清单非空」挑弹窗，提上来就会渲染成
	// 「合并已成功，但 worktree 里还有没提交的东西」+ 一长串文件 —— 报错了对象。
	if len(payload.ConflictFiles)+len(payload.DirtyFiles)+len(payload.UserChanges) != 0 {
		t.Fatalf("the mechanical file lists leaked to the top level: %+v", payload)
	}
}

// newBeginFinishHandlerWithoutAgent 造一个**没有 agent 段**的任务。
//
// 只有 user 段、也不推进 —— 用来测「机械清场没做成、又没有 agent 可继承」这条
// 两条路都断的兜底。TaskHasAgentStage 会返回 false，BeginFinishWorktree 会拒绝。
//
// edit 非空时往 worktree 里放一个真提交：分支和 main 同一个提交时合并会被
// 「已经是祖先」短路，脏检查根本轮不到，那就测不到「被脏 checkout 挡住」了。
func newBeginFinishHandlerWithoutAgent(t *testing.T, edit string) (http.Handler, *kanban.Service, fs.RootInfo, string) {
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
