package kanban

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// verifierAgent 是只读复核回合的假 agent：记录调用与请求形状，按脚本回一段
// 文本。block 非 nil 时先等它关闭，用来观察「apply 已返回、复核还没回来」。
type verifierAgent struct {
	mu       sync.Mutex
	calls    int
	requests []RouterAgentRequest
	block    chan struct{}
	reply    string
	err      error
}

func (a *verifierAgent) RunRouterTurn(ctx context.Context, req RouterAgentRequest) (RouterAgentResult, error) {
	a.mu.Lock()
	a.calls++
	a.requests = append(a.requests, req)
	block, reply, err := a.block, a.reply, a.err
	a.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return RouterAgentResult{}, ctx.Err()
		}
	}
	if err != nil {
		return RouterAgentResult{}, err
	}
	return RouterAgentResult{SessionKey: req.SessionKey, FinalText: reply}, nil
}

func (a *verifierAgent) CloseRouterSession(ctx context.Context, rootID, sessionKey string) error {
	return nil
}

func (a *verifierAgent) callCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

func (a *verifierAgent) requestAt(i int) RouterAgentRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.requests[i]
}

func insertVerifierDecision(t *testing.T, f *completionFixture, id, summary string) RouterDecision {
	t.Helper()
	decision := RouterDecision{
		ID:          id,
		TaskID:      f.task.ID,
		RootID:      f.root.ID,
		TurnID:      "turn-" + id,
		TriggerKind: RouterTriggerUserMessage,
		Status:      RouterDecisionApplied,
		Actions:     []RouterAction{completionAction(summary)},
	}
	must(t, NewRouterStore(f.store.db).InsertDecision(context.Background(), &decision), "InsertDecision")
	return decision
}

func completionRecord(t *testing.T, f *completionFixture) TaskCompletion {
	t.Helper()
	completion, found, err := f.store.GetTaskCompletion(context.Background(), f.task.ID)
	must(t, err, "GetTaskCompletion")
	if !found {
		t.Fatal("task_completion row missing")
	}
	return completion
}

func waitCompletionStatus(t *testing.T, f *completionFixture, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if completionRecord(t, f).Status == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("completion status = %q, want %q within %s", completionRecord(t, f).Status, want, timeout)
}

// TestVerifierPassCommits：复核判定 pass 就直接收尾，任务落到 success。
func TestVerifierPassCommits(t *testing.T) {
	ctx := context.Background()
	f := newCompletionFixture(t, nil)
	agent := &verifierAgent{reply: `{"verdict":"pass","findings":["工作区里能看到 README.md 已更新"]}`}
	f.svc.SetRouterAgent(agent)
	f.svc.SetRouterConfig(RouterConfig{Mode: RouterModeCoordinate, Agent: "claude", Model: "sonnet"})
	insertVerifierDecision(t, f, "dec-verify-pass", "文档改完了")

	report := f.evaluate(t)
	if err := f.svc.verifyCompletion(ctx, f.task.ID, f.run.ID, "dec-verify-pass"); err != nil {
		t.Fatalf("verifyCompletion: %v", err)
	}

	if got := agent.callCount(); got != 1 {
		t.Fatalf("verifier calls = %d, want 1", got)
	}
	req := agent.requestAt(0)
	if !req.ReadOnly {
		t.Fatal("verifier turn must be read-only")
	}
	if !strings.HasPrefix(req.SessionKey, "verify-") || !strings.Contains(req.SessionKey, f.run.ID) {
		t.Fatalf("verifier session key = %q, want verify-…-<run>-…", req.SessionKey)
	}
	if strings.TrimSpace(req.Cwd) == "" {
		t.Fatal("verifier turn needs the task workspace as cwd")
	}
	if !strings.Contains(req.Prompt, "build-done") && !strings.Contains(req.Prompt, "完成证据") {
		t.Fatalf("verifier prompt must carry the gaps: %s", req.Prompt)
	}

	task, err := f.store.GetTask(ctx, f.task.ID)
	must(t, err, "GetTask")
	if task.Status != StatusSuccess {
		t.Fatalf("task status = %q, want success", task.Status)
	}
	completion := completionRecord(t, f)
	if completion.Status != CompletionStatusCompleted || completion.VerifierVerdict != CompletionVerdictPass {
		t.Fatalf("completion = %+v, want completed/pass", completion)
	}
	if len(report.Items) == 0 {
		t.Fatal("fixture report should carry the uncovered items")
	}
}

// TestVerifierFailBlocks：复核判定 fail 就按未完成处理并落事件。
func TestVerifierFailBlocks(t *testing.T) {
	ctx := context.Background()
	f := newCompletionFixture(t, nil)
	agent := &verifierAgent{reply: `{"verdict":"fail","findings":["工作区没有任何改动"],"missing":["build 日志"]}`}
	f.svc.SetRouterAgent(agent)
	insertVerifierDecision(t, f, "dec-verify-fail", "都做完了")

	if err := f.svc.verifyCompletion(ctx, f.task.ID, f.run.ID, "dec-verify-fail"); err != nil {
		t.Fatalf("verifyCompletion: %v", err)
	}

	task, err := f.store.GetTask(ctx, f.task.ID)
	must(t, err, "GetTask")
	if task.Status != StatusWaitingUser {
		t.Fatalf("task status = %q, want waiting_user", task.Status)
	}
	completion := completionRecord(t, f)
	if completion.Status != CompletionStatusBlocked || completion.VerifierVerdict != CompletionVerdictFail {
		t.Fatalf("completion = %+v, want blocked/fail", completion)
	}
	if events := completionBlockedEvents(t, f); len(events) != 1 {
		t.Fatalf("completion_blocked events = %d, want 1", len(events))
	}
	rows := completionSystemRows(t, f, "completion-verifier:")
	if len(rows) != 1 || !strings.Contains(rows[0].Body, "工作区没有任何改动") {
		t.Fatalf("verifier finding row = %+v, want it to quote the finding", rows)
	}
}

// TestVerifierGarbageIsUncertain：读不懂的输出不能当通过。
func TestVerifierGarbageIsUncertain(t *testing.T) {
	verdict, findings, err := parseVerifierVerdict("好的，我看了一下，应该没问题。")
	if verdict != CompletionVerdictUncertain || err == nil {
		t.Fatalf("garbage verdict = %q err=%v, want uncertain with an error", verdict, err)
	}
	if len(findings) != 0 {
		t.Fatalf("garbage findings = %v, want none", findings)
	}

	ctx := context.Background()
	f := newCompletionFixture(t, nil)
	f.svc.SetRouterAgent(&verifierAgent{reply: "我觉得可以收尾了"})
	insertVerifierDecision(t, f, "dec-verify-garbage", "好了")

	if err := f.svc.verifyCompletion(ctx, f.task.ID, f.run.ID, "dec-verify-garbage"); err != nil {
		t.Fatalf("verifyCompletion: %v", err)
	}
	if completion := completionRecord(t, f); completion.Status != CompletionStatusBlocked || completion.VerifierVerdict != CompletionVerdictUncertain {
		t.Fatalf("completion = %+v, want blocked/uncertain", completion)
	}
}

// TestVerifierUnavailableFailsClosed：没有 Claude 复核能力时按未完成处理，
// 并且明写「复核不可用」。
func TestVerifierUnavailableFailsClosed(t *testing.T) {
	ctx := context.Background()
	f := newCompletionFixture(t, nil)
	insertVerifierDecision(t, f, "dec-verify-missing", "好了")

	if err := f.svc.verifyCompletion(ctx, f.task.ID, f.run.ID, "dec-verify-missing"); err != nil {
		t.Fatalf("verifyCompletion: %v", err)
	}

	task, err := f.store.GetTask(ctx, f.task.ID)
	must(t, err, "GetTask")
	if task.Status != StatusWaitingUser {
		t.Fatalf("task status = %q, want waiting_user", task.Status)
	}
	if completion := completionRecord(t, f); completion.Status != CompletionStatusBlocked {
		t.Fatalf("completion = %+v, want blocked", completion)
	}
	rows := completionSystemRows(t, f, "completion-verifier-unavailable:")
	if len(rows) != 1 || !strings.Contains(rows[0].Body, "复核不可用") {
		t.Fatalf("unavailable row = %+v, want it to say the verifier is unavailable", rows)
	}
}

// TestVerifierRestartReconcilesBlocked：重启把停在 verifying 的行收口成
// blocked，并留一条可见说明，不留静默悬挂。
func TestVerifierRestartReconcilesBlocked(t *testing.T) {
	ctx := context.Background()
	f := newCompletionFixture(t, nil)
	must(t, f.store.UpsertTaskCompletion(ctx, TaskCompletion{
		TaskID:      f.task.ID,
		RunID:       f.run.ID,
		Status:      CompletionStatusVerifying,
		VerifierKey: "verify-" + f.run.ID + "-abc12345",
		DecisionID:  "dec-interrupted",
	}), "UpsertTaskCompletion")

	if _, err := f.svc.RecoverOnStartupForRoot(ctx, f.root.ID, nil); err != nil {
		t.Fatalf("RecoverOnStartupForRoot: %v", err)
	}

	completion := completionRecord(t, f)
	if completion.Status != CompletionStatusBlocked {
		t.Fatalf("completion status = %q, want blocked after restart", completion.Status)
	}
	rows := completionSystemRows(t, f, "completion-verifier-interrupted:")
	if len(rows) != 1 || !strings.Contains(rows[0].Body, "复核被重启打断") {
		t.Fatalf("interrupted row = %+v, want a visible explanation", rows)
	}
}

// TestVerifierReusesVerdictByReportHash：同一 (run, 缺口) 只复核一次，重复
// 的完成声明直接复用已存结论。
func TestVerifierReusesVerdictByReportHash(t *testing.T) {
	ctx := context.Background()
	f := newCompletionFixture(t, nil)
	agent := &verifierAgent{reply: `{"verdict":"fail","findings":["还差东西"]}`}
	f.svc.SetRouterAgent(agent)
	decision := insertVerifierDecision(t, f, "dec-reuse-1", "好了")

	report := f.evaluate(t)
	if err := f.svc.beginCompletionVerification(ctx, f.store, f.task, f.run, report, decision, "好了"); err != nil {
		t.Fatalf("beginCompletionVerification: %v", err)
	}
	// begin 之后复核在后台跑，等它把结论写回来。
	waitCompletionStatus(t, f, CompletionStatusBlocked, 10*time.Second)
	if got := agent.callCount(); got != 1 {
		t.Fatalf("verifier calls = %d, want 1", got)
	}

	// 同一缺口再宣告一次完成：不再调模型，直接按已存结论驳回。
	again := insertVerifierDecision(t, f, "dec-reuse-2", "真的好了")
	if err := f.svc.beginCompletionVerification(ctx, f.store, f.task, f.run, report, again, "真的好了"); err != nil {
		t.Fatalf("second beginCompletionVerification: %v", err)
	}
	if got := agent.callCount(); got != 1 {
		t.Fatalf("verifier calls after reuse = %d, want 1", got)
	}
	if completion := completionRecord(t, f); completion.Status != CompletionStatusBlocked || completion.VerifierVerdict != CompletionVerdictFail {
		t.Fatalf("completion = %+v, want the stored fail verdict applied", completion)
	}
}

// TestVerifierApplyReturnsBeforeVerdict：apply 只落 verifying 就返回，复核在
// 后台跑完再改结论 —— 复核回合不能占住 Router 回合。
func TestVerifierApplyReturnsBeforeVerdict(t *testing.T) {
	ctx := context.Background()
	f := newCompletionFixture(t, nil)
	agent := &verifierAgent{reply: `{"verdict":"pass"}`, block: make(chan struct{})}
	f.svc.SetRouterAgent(agent)
	decision := insertVerifierDecision(t, f, "dec-async", "好了")

	report := f.evaluate(t)
	if err := f.svc.beginCompletionVerification(ctx, f.store, f.task, f.run, report, decision, "好了"); err != nil {
		t.Fatalf("beginCompletionVerification: %v", err)
	}
	if completion := completionRecord(t, f); completion.Status != CompletionStatusVerifying {
		t.Fatalf("completion status = %q, want verifying before the verdict arrives", completion.Status)
	}
	if rows := completionSystemRows(t, f, "completion-verifying:"); len(rows) != 1 {
		t.Fatalf("verifying rows = %d, want 1 visible note", len(rows))
	}

	close(agent.block)
	waitCompletionStatus(t, f, CompletionStatusCompleted, 10*time.Second)
}
