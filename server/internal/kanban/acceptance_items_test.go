package kanban

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// W1 T3：验收项与证据等级。Router 登记「这一轮算做完了」的具体条目，证据门按
// 等级核对：自述永远只是声称，产物必须真的存在，独立复核要有复核结论。

func acceptanceContract() *CompletionContract {
	return &CompletionContract{Requirements: []CompletionRequirement{{
		ID: "nodes", Kind: CompletionKindAllNodesTerminal,
	}}}
}

func recordAcceptance(t *testing.T, f *completionFixture, items ...AcceptanceItemInput) {
	t.Helper()
	if _, err := f.store.ReplaceAcceptanceItems(context.Background(), f.task.ID, f.run.ID, items); err != nil {
		t.Fatalf("ReplaceAcceptanceItems: %v", err)
	}
}

func TestAcceptanceSelfReportIsOnlyAClaim(t *testing.T) {
	f := newCompletionFixture(t, acceptanceContract())
	f.addAttempt(t, "build", NodeRunSuccess, map[string]any{"outcome": "done"}, "")
	// 没登记验收项时行为不变：契约对得上就算过（存量路径不受影响）。
	if report := f.evaluate(t); !report.Passed {
		t.Fatalf("no acceptance items must not block completion: %+v", report)
	}
	recordAcceptance(t, f, AcceptanceItemInput{Text: "把 a.go 改好了", EvidenceClass: AcceptanceSelfReport})
	report := f.evaluate(t)
	if report.Passed {
		t.Fatal("a self report alone must never satisfy the gate")
	}
	item := completionItem(t, report, acceptanceItemID(f.task.ID, f.run.ID, 1))
	if item.Pass || !strings.Contains(item.Detail, "自述") {
		t.Fatalf("item = %+v, want a failing self-report item", item)
	}
}

func TestAcceptanceArtifactRequiresRealWorkspaceFile(t *testing.T) {
	f := newCompletionFixture(t, acceptanceContract())
	f.addAttempt(t, "build", NodeRunSuccess, map[string]any{"outcome": "done"}, "")
	recordAcceptance(t, f, AcceptanceItemInput{
		Text: "生成了报告", EvidenceClass: AcceptanceArtifact, EvidenceRef: "report.md",
	})
	report := f.evaluate(t)
	if report.Passed {
		t.Fatal("artifact item passed without the file existing")
	}
	if item := completionItem(t, report, acceptanceItemID(f.task.ID, f.run.ID, 1)); !strings.Contains(item.Detail, "不存在") {
		t.Fatalf("item detail = %q, want it to say the artifact is missing", item.Detail)
	}
	must(t, os.WriteFile(filepath.Join(f.root.RootPath, "report.md"), []byte("# ok"), 0o600), "write report.md")
	if report := f.evaluate(t); !report.Passed {
		t.Fatalf("artifact item must pass once the file exists: %+v", report)
	}
}

func TestAcceptanceArtifactRejectsSymlinkEscape(t *testing.T) {
	f := newCompletionFixture(t, acceptanceContract())
	f.addAttempt(t, "build", NodeRunSuccess, map[string]any{"outcome": "done"}, "")
	outside := filepath.Join(t.TempDir(), "secret.txt")
	must(t, os.WriteFile(outside, []byte("x"), 0o600), "write outside file")
	must(t, os.Symlink(outside, filepath.Join(f.root.RootPath, "escape.txt")), "symlink escape.txt")
	recordAcceptance(t, f, AcceptanceItemInput{
		Text: "产物在工作区里", EvidenceClass: AcceptanceArtifact, EvidenceRef: "escape.txt",
	})
	report := f.evaluate(t)
	if report.Passed {
		t.Fatal("artifact item followed a symlink out of the workspace")
	}
	if item := completionItem(t, report, acceptanceItemID(f.task.ID, f.run.ID, 1)); !strings.Contains(item.Detail, "符号链接") {
		t.Fatalf("item detail = %q, want the symlink guard to be named", item.Detail)
	}
	// 父目录是链接时最后一段本身是普通文件，只看 Lstat 会把它当成工作区里的
	// 产物 —— 必须按真实路径再判一次。
	outsideDir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(outsideDir, "real.txt"), []byte("x"), 0o600), "write outside dir file")
	must(t, os.Symlink(outsideDir, filepath.Join(f.root.RootPath, "linkdir")), "symlink linkdir")
	recordAcceptance(t, f, AcceptanceItemInput{
		Text: "产物在工作区里", EvidenceClass: AcceptanceArtifact, EvidenceRef: "linkdir/real.txt",
	})
	report = f.evaluate(t)
	if report.Passed {
		t.Fatal("artifact item followed a symlinked parent directory out of the workspace")
	}
	if item := completionItem(t, report, acceptanceItemID(f.task.ID, f.run.ID, 1)); !strings.Contains(item.Detail, "工作区之外") {
		t.Fatalf("item detail = %q, want the parent-directory escape to be named", item.Detail)
	}
}

func TestAcceptanceIndependentVerificationWaitsForVerifier(t *testing.T) {
	f := newCompletionFixture(t, acceptanceContract())
	f.addAttempt(t, "build", NodeRunSuccess, map[string]any{"outcome": "done"}, "")
	recordAcceptance(t, f, AcceptanceItemInput{
		Text: "go test ./... 通过", EvidenceClass: AcceptanceIndependentVerification,
		EvidenceRef: "go test ./...",
	})
	report := f.evaluate(t)
	if report.Passed {
		t.Fatal("independent_verification must not pass before the verifier returns")
	}
	if !report.AcceptanceNeedsVerification {
		t.Fatal("report must ask for an independent verification")
	}
	// 契约本身对得上，但验收项要复核：不能因为「有契约」就跳过复核。
	if !completionNeedsVerifier(f.run, report) {
		t.Fatal("acceptance items needing verification must still start a verifier turn")
	}
	verified := markAcceptanceVerified(report)
	if !verified.Passed {
		t.Fatalf("verifier pass must settle the item: %+v", verified)
	}
	if verified.AcceptanceNeedsVerification {
		t.Fatal("verified report still asks for verification")
	}
	if item := completionItem(t, verified, acceptanceItemID(f.task.ID, f.run.ID, 1)); !item.Pass || !strings.Contains(item.Detail, "复核") {
		t.Fatalf("item = %+v, want it to cite the verifier verdict", item)
	}
}

func TestAcceptanceRecordActionValidation(t *testing.T) {
	brief := &SituationBrief{
		Task:      RouterBriefTask{ID: "task-c", Status: StatusWaitingUser},
		LatestRun: &RouterBriefRun{ID: "run-c"},
	}
	ok := RouterAction{Kind: RouterActionRecordAcceptance, Items: []AcceptanceItemInput{
		{Text: "改了 a.go", EvidenceClass: AcceptanceSelfReport},
		{Text: "生成了报告", EvidenceClass: AcceptanceArtifact, EvidenceRef: "report.md"},
	}}
	actions := []RouterAction{ok, {Seq: 1, Kind: RouterActionComplete, Summary: "做完了"}}
	if err := validateRouterActions(brief, actions, RouterConfig{}.Normalized()); err != nil {
		t.Fatalf("record_acceptance alongside complete_task must be allowed: %v", err)
	}
	cases := map[string][]AcceptanceItemInput{
		"no items":        nil,
		"blank text":      {{Text: "  ", EvidenceClass: AcceptanceSelfReport}},
		"unknown class":   {{Text: "x", EvidenceClass: "vibes"}},
		"artifact no ref": {{Text: "x", EvidenceClass: AcceptanceArtifact}},
		"too many items":  make([]AcceptanceItemInput, 9),
	}
	for name, items := range cases {
		t.Run(name, func(t *testing.T) {
			for i := range items {
				if items[i].EvidenceClass == "" {
					items[i] = AcceptanceItemInput{Text: "条目", EvidenceClass: AcceptanceSelfReport}
				}
			}
			action := RouterAction{Kind: RouterActionRecordAcceptance, Items: items}
			if err := validateRouterActions(brief, []RouterAction{action}, RouterConfig{}.Normalized()); err == nil {
				t.Fatalf("%s must be rejected", name)
			}
		})
	}
}

func TestAcceptanceStoreReplacesByRunAndTracksRework(t *testing.T) {
	f := newCompletionFixture(t, acceptanceContract())
	ctx := context.Background()
	recordAcceptance(t, f,
		AcceptanceItemInput{Text: "一", EvidenceClass: AcceptanceSelfReport},
		AcceptanceItemInput{Text: "二", EvidenceClass: AcceptanceSelfReport},
	)
	// 同一轮再登记一次 = 整体替换，不追加。
	recordAcceptance(t, f, AcceptanceItemInput{Text: "只剩一条", EvidenceClass: AcceptanceArtifact, EvidenceRef: "a.txt"})
	items, err := f.store.ListAcceptanceItems(ctx, f.task.ID, f.run.ID)
	must(t, err, "ListAcceptanceItems")
	if len(items) != 1 || items[0].Seq != 1 || items[0].EvidenceRef != "a.txt" {
		t.Fatalf("items = %+v, want a single replaced item", items)
	}
	must(t, f.store.BumpAcceptanceRework(ctx, f.task.ID, f.run.ID), "BumpAcceptanceRework")
	items, err = f.store.ListAcceptanceItems(ctx, f.task.ID, f.run.ID)
	must(t, err, "ListAcceptanceItems")
	if items[0].ReworkRound != 1 {
		t.Fatalf("rework_round = %d, want 1", items[0].ReworkRound)
	}
	must(t, f.store.MarkAcceptanceMet(ctx, f.task.ID, f.run.ID), "MarkAcceptanceMet")
	items, err = f.store.ListAcceptanceItems(ctx, f.task.ID, f.run.ID)
	must(t, err, "ListAcceptanceItems")
	if items[0].Status != AcceptanceStatusMet {
		t.Fatalf("status = %q, want met", items[0].Status)
	}
}

func TestRecordAcceptanceToolRecordsItems(t *testing.T) {
	env := &routerToolEnv{
		brief:     &SituationBrief{Task: RouterBriefTask{ID: "task-1", Status: StatusWaitingUser}, LatestRun: &RouterBriefRun{ID: "run-1"}},
		collector: newRouterTurnCollector(RouterConfig{}),
	}
	tool := env.recordAcceptanceTool()
	if tool.Name != "router_record_acceptance" || tool.Class != RouterToolWrite {
		t.Fatalf("tool = %q/%q, want router_record_acceptance/write", tool.Name, tool.Class)
	}
	raw, err := json.Marshal(map[string]any{"items": []map[string]any{
		{"text": "改了 a.go", "evidence_class": "self_report"},
		{"text": "生成了报告", "evidence_class": "artifact", "evidence_ref": "report.md"},
	}})
	must(t, err, "marshal args")
	res, err := tool.Handler(context.Background(), raw)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(res, "recorded") {
		t.Fatalf("handler result = %q, want recorded", res)
	}
	actions := env.collector.snapshot()
	if len(actions) != 1 || actions[0].Kind != RouterActionRecordAcceptance {
		t.Fatalf("actions = %+v, want one record_acceptance", actions)
	}
	if len(actions[0].Items) != 2 || actions[0].Items[1].EvidenceRef != "report.md" {
		t.Fatalf("items = %+v, want both items copied verbatim", actions[0].Items)
	}
	bad, err := json.Marshal(map[string]any{"items": []map[string]any{{"text": "x"}}})
	must(t, err, "marshal bad args")
	if _, err := tool.Handler(context.Background(), bad); err == nil {
		t.Fatal("an item without evidence_class must be rejected")
	}
}

func TestRecordAcceptanceAllowedInRuntimeEventTurn(t *testing.T) {
	brief := &SituationBrief{
		Task:  RouterBriefTask{ID: "task-1", Status: StatusWaitingUser},
		Event: &RouterBriefEvent{ID: "ev-1", Kind: string(RouterEventCompletionBlocked)},
	}
	actions := []RouterAction{
		{Seq: 0, Kind: RouterActionRecordAcceptance, Items: []AcceptanceItemInput{{Text: "补了测试", EvidenceClass: AcceptanceArtifact, EvidenceRef: "x_test.go"}}},
		{Seq: 1, Kind: RouterActionComplete, Summary: "做完了"},
	}
	if err := validateRuntimeEventActions(brief, actions, RouterConfig{}); err != nil {
		t.Fatalf("runtime event turn rejected record_acceptance: %v", err)
	}
}

// 复核通过这一条路过去没有用例：pass 分支除了收尾，还必须把 waiting 的
// 验收项一并改成通过，否则任务虽然 success，证据表还停在 open。
func TestAcceptanceVerifierPassCommitsAndMarksMet(t *testing.T) {
	f := newCompletionFixture(t, acceptanceContract())
	f.addAttempt(t, "build", NodeRunSuccess, map[string]any{"outcome": "done"}, "")
	recordAcceptance(t, f, AcceptanceItemInput{Text: "go test ./... 通过", EvidenceClass: AcceptanceIndependentVerification})
	report := f.evaluate(t)
	if report.Passed {
		t.Fatal("an item awaiting independent verification must not pass on its own")
	}
	decision := RouterDecision{ID: "dec-verify-pass", TaskID: f.task.ID, TurnID: "turn-verify"}
	must(t, f.svc.applyVerifierVerdict(context.Background(), f.store, f.task, f.run, report, decision, "做完了", CompletionVerdictPass, nil), "applyVerifierVerdict")
	stored, err := f.store.GetTask(context.Background(), f.task.ID)
	must(t, err, "GetTask")
	if stored.Status != StatusSuccess {
		t.Fatalf("task status = %q, want success", stored.Status)
	}
	items, err := f.store.ListAcceptanceItems(context.Background(), f.task.ID, f.run.ID)
	must(t, err, "ListAcceptanceItems")
	if len(items) != 1 || items[0].Status != AcceptanceStatusMet {
		t.Fatalf("items = %+v, want the verified item marked met", items)
	}
	// 落库的报告要反映「复核已通过」：仍写着 passed=false 的报告留着，日后复读
	// 只会看到一条自相矛盾的收尾结论。
	completion, found, err := f.store.GetTaskCompletion(context.Background(), f.task.ID)
	must(t, err, "GetTaskCompletion")
	if !found {
		t.Fatal("completion row missing")
	}
	var savedReport CompletionReport
	must(t, json.Unmarshal([]byte(completion.ReportJSON), &savedReport), "unmarshal ReportJSON")
	if !savedReport.Passed || savedReport.AcceptanceNeedsVerification {
		t.Fatalf("stored report = %+v, want passed with verification applied", savedReport)
	}
	for _, item := range savedReport.Items {
		if item.Source == "acceptance" && !item.Pass {
			t.Fatalf("stored acceptance item still failing: %+v", item)
		}
	}
}

// 每驳回一次记一轮返工；同一条决策重放不能再记一次（崩溃恢复会重放）。
func TestAcceptanceBlockBumpsReworkOncePerDecision(t *testing.T) {
	f := newCompletionFixture(t, acceptanceContract())
	f.addAttempt(t, "build", NodeRunSuccess, map[string]any{"outcome": "done"}, "")
	recordAcceptance(t, f, AcceptanceItemInput{Text: "把 a.go 改好了", EvidenceClass: AcceptanceSelfReport})
	report := f.evaluate(t)
	first := RouterDecision{ID: "dec-block-1", TaskID: f.task.ID, TurnID: "turn-1"}
	must(t, f.svc.blockCompletion(context.Background(), f.store, f.task, f.run, report, first), "blockCompletion")
	must(t, f.svc.blockCompletion(context.Background(), f.store, f.task, f.run, report, first), "blockCompletion replay")
	items, err := f.store.ListAcceptanceItems(context.Background(), f.task.ID, f.run.ID)
	must(t, err, "ListAcceptanceItems")
	if items[0].ReworkRound != 1 {
		t.Fatalf("rework_round = %d after replay, want 1", items[0].ReworkRound)
	}
	second := RouterDecision{ID: "dec-block-2", TaskID: f.task.ID, TurnID: "turn-2"}
	must(t, f.svc.blockCompletion(context.Background(), f.store, f.task, f.run, report, second), "blockCompletion second")
	items, err = f.store.ListAcceptanceItems(context.Background(), f.task.ID, f.run.ID)
	must(t, err, "ListAcceptanceItems")
	if items[0].ReworkRound != 2 {
		t.Fatalf("rework_round = %d after a second decision, want 2", items[0].ReworkRound)
	}
}
