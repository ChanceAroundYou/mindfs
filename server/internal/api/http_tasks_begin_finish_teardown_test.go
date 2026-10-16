package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/server/internal/kanban"
)

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

// ②.5 机械清场没做成 → **落回 agent**，不报错。
//
// 2026-10-08 用户实测：重启服务后点 task-37 的收尾，拿到的是
// 「主 checkout 有未提交改动，先提交或暂存后再收尾」+ 409。用户的原话是
// 「不应该报错，而是应该把 comment 提交给 agent，进入 agent 合并流程」。
//
// 机械清场是「两个阶段整合」里的**加速段**，不是门槛：判定说能做、执行时被挡下
// （主 checkout 有改动、撞冲突、worktree 里还有没提交的活）时，结论不是「收尾失败」，
// 而是「让 agent 去做」—— agent 能 commit、能解冲突、能判断哪些改动该留。
func TestBeginFinishRouteFallsBackToTheAgentWhenTheTeardownIsBlocked(t *testing.T) {
	handler, svc, root, taskID := newBeginFinishHandler(t, "from worktree\n")
	// 主 checkout 里一个**已跟踪**文件有未提交改动 —— 合并会顶掉它，机械清场不敢做。
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
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — 机械没成不该报错; body=%s", rec.Code, rec.Body.String())
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
	if payload.Plan.Mechanical {
		t.Fatal("plan.mechanical = true, want the failed teardown handed to the agent")
	}
	if payload.Plan.Reason == "" {
		t.Fatal("plan.reason is empty — 用户得知道为什么又跑起 agent 了")
	}
	if len(payload.Plan.Files) != 1 || payload.Plan.Files[0] != "scratch.txt" {
		t.Fatalf("plan.files = %v, want [scratch.txt]", payload.Plan.Files)
	}
	// 收尾段真的追加上去了，而且**只有一段**（不追加第二段）。
	detail, err := svc.GetTask(context.Background(), root.ID, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	finishes := 0
	for _, stage := range detail.Task.Stages {
		if strings.TrimSpace(stage.Kind) == kanban.StageKindWorktreeFinish {
			finishes++
		}
	}
	if finishes != 1 {
		t.Fatalf("finish stages = %d, want exactly 1", finishes)
	}
	// 机械清场没做成，就**不该**动到 worktree —— 那是 agent 要接着干的地方。
	if _, statErr := os.Stat(mustWorktreePath(t, svc, root.ID, taskID)); statErr != nil {
		t.Fatalf("the worktree must survive the failed teardown: %v", statErr)
	}
}

// 机械清场没做成、而且**没有 agent 可继承**时，才把失败如实回给调用方。
//
// 走到这里说明两条路都断了：机械清场做不了，BeginFinishWorktree 也会拒绝
// （「该任务还没有 agent 阶段，无法自动收尾」）。这时只能让人来决策 —— 所以回
// 409 + 结构化清单，清单**不进句子**。
func TestBeginFinishRouteReportsTheTeardownFailureWhenThereIsNoAgentToFallBackTo(t *testing.T) {
	// worktree 里得有一个真提交：否则分支和 main 同一个提交，合并会被「已经是祖先」
	// 短路掉，脏检查根本轮不到 —— 那就测不到「机械清场被脏 checkout 挡住」了。
	handler, svc, root, taskID := newBeginFinishHandlerWithoutAgent(t, "from worktree\n")
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
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Error      string   `json:"error"`
		DirtyFiles []string `json:"dirty_files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Error == "" {
		t.Fatal("error is empty — 用户得知道为什么两条路都断了")
	}
	if len(payload.DirtyFiles) != 1 || payload.DirtyFiles[0] != "scratch.txt" {
		t.Fatalf("dirty_files = %v, want [scratch.txt]", payload.DirtyFiles)
	}
	// 清单不进句子：句子是给人读的，清单是给 UI 列表渲染的。
	if strings.Contains(payload.Error, "scratch.txt") {
		t.Fatalf("the file list leaked into the sentence: %q", payload.Error)
	}
	// 没有 agent 可继承，就不该凭空造出收尾段。
	detail, err := svc.GetTask(context.Background(), root.ID, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	for _, stage := range detail.Task.Stages {
		if strings.TrimSpace(stage.Kind) == kanban.StageKindWorktreeFinish {
			t.Fatal("a finish stage was appended even though there is no agent to run it")
		}
	}
}

// ② 分支已合进主干、主 checkout 里又有别人留下的未跟踪文件 → 照样当场清场。
//
// 这就是 2026-10-08 用户实测的那个场景（task-37 + 两个 e2e 探针脚本）：活早就合完了，
// 收尾却因为主 checkout 里两个未跟踪文件报「主 checkout 有未提交改动」。
// 没有东西要合的时候，主 checkout 脏不脏完全无关 —— 一步 checkout/merge 都不会发生。
func TestBeginFinishRouteTearsDownEvenWhenTheMainCheckoutHasUntrackedFiles(t *testing.T) {
	handler, svc, root, taskID := newBeginFinishHandler(t, "from worktree\n")
	gitForAPITest(t, root.RootPath, "merge", "-q", "--no-ff", "task-1")
	worktreePath := mustWorktreePath(t, svc, root.ID, taskID)
	// 两个别人留下的散落文件 —— 和用户实测里那两个探针脚本一模一样。
	for _, name := range []string{"e2e-pending-probe.mjs", "e2e-pending-probe2.mjs"} {
		if err := os.WriteFile(filepath.Join(root.RootPath, name), []byte("// probe\n"), 0o600); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

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
	// 别人的文件一个字节都不能少。
	for _, name := range []string{"e2e-pending-probe.mjs", "e2e-pending-probe2.mjs"} {
		data, err := os.ReadFile(filepath.Join(root.RootPath, name))
		if err != nil {
			t.Fatalf("%s must survive: %v", name, err)
		}
		if string(data) != "// probe\n" {
			t.Fatalf("%s was touched: %q", name, data)
		}
	}
}
