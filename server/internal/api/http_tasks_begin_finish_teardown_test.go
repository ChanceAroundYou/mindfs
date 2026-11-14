package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ②.5 机械清场没做成 → **落回 agent**，不报错。
//
// 用户实测（2026-10-08）：主 checkout 有未提交改动时，收尾不该回 409 让人自己
// 去提交 —— agent 能 commit、能解冲突、能判断哪些改动该留。机械清场是加速，不是门槛。
func TestBeginFinishRouteFallsBackToTheAgentWhenTheTeardownIsBlocked(t *testing.T) {
	handler, svc, root, taskID := newBeginFinishHandler(t, "from worktree\n")
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
	if action := beginFinishAction(t, rec); action != "finish_turn_started" {
		t.Fatalf("action = %q, want finish_turn_started", action)
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
	// 机械清场没做成，就**不该**动到 worktree —— 那是 agent 要接着干的地方。
	if _, statErr := os.Stat(mustWorktreePath(t, svc, root.ID, taskID)); statErr != nil {
		t.Fatalf("the worktree must survive the failed teardown: %v", statErr)
	}
}

// 两条路都断：机械清场做不了，收尾回合也起不来（没有执行端口）→ 409 如实报错。
func TestBeginFinishRouteReportsTheFailureWhenTheTurnCannotStart(t *testing.T) {
	handler, svc, root, taskID := newBeginFinishHandler(t, "from worktree\n")
	svc.SetDirectExecutor(nil)
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
		Error string `json:"error"`
		Plan  struct {
			Mechanical bool     `json:"mechanical"`
			Files      []string `json:"files"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(payload.Error, "收尾执行端口未配置") {
		t.Fatalf("error = %q, want the missing-executor reason", payload.Error)
	}
	if payload.Plan.Mechanical {
		t.Fatal("plan.mechanical = true, want the fallback plan")
	}
	if len(payload.Plan.Files) != 1 || payload.Plan.Files[0] != "scratch.txt" {
		t.Fatalf("plan.files = %v, want [scratch.txt]", payload.Plan.Files)
	}
}

// ② 分支已合进主干、主 checkout 里又有别人留下的未跟踪文件 → 照样当场清场。
//
// 2026-10-08 用户实测（task-37 + 两个 e2e 探针脚本）：活早就合完了，收尾却因为
// 未跟踪文件报「主 checkout 有未提交改动」。没有东西要合时，脏不脏完全无关。
func TestBeginFinishRouteTearsDownEvenWhenTheMainCheckoutHasUntrackedFiles(t *testing.T) {
	handler, svc, root, taskID := newBeginFinishHandler(t, "from worktree\n")
	gitForAPITest(t, root.RootPath, "merge", "-q", "--no-ff", "task-1")
	worktreePath := mustWorktreePath(t, svc, root.ID, taskID)
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
