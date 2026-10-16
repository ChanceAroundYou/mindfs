package kanban

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBuildAgentPromptAppendsOnlyConfiguredContext(t *testing.T) {
	values := map[string]string{
		"previous_input":     "fix this",
		"task_initial_input": "first input",
		"task_number":        "12",
	}
	prompt := BuildAgentPrompt("Do: {previous_input}", values, TaskControlPromptContext{})
	if prompt != "Do: fix this" {
		t.Fatalf("prompt = %q", prompt)
	}
	withTaskNumber := BuildAgentPrompt("Do: {task_initial_input} #{task_number}", values, TaskControlPromptContext{})
	if withTaskNumber != "Do: first input #12" {
		t.Fatalf("task placeholders not replaced: %q", withTaskNumber)
	}
	legacy := BuildAgentPrompt("Root: {root_id}", values, TaskControlPromptContext{})
	if legacy != "Root: {root_id}" {
		t.Fatalf("legacy placeholder was replaced: %q", legacy)
	}
	withControl := BuildAgentPrompt("Do: {previous_input}", values, TaskControlPromptContext{
		RootID:            "root",
		TaskNumber:        12,
		CurrentStageIndex: "1",
		CurrentStageName:  "Agent",
		Enabled:           true,
	})
	if !containsAll(withControl, []string{"Task control context:", "task_number: 12", "mindfs root -task 12", "mindfs root -task 12 -next"}) {
		t.Fatalf("control prompt missing context: %q", withControl)
	}
}

// 完成契约与匹配判据必须同源：契约里让 agent 输出的那个标记，得是匹配时认的那个。
// 直接从契约文本里把标记抠出来再喂给 matcher —— 手抄一份字面量的话，
// 契约里段号写错这类错位就测不出来。
func TestStageExitContractMatchesTheMarkerItAsksFor(t *testing.T) {
	markerRe := regexp.MustCompile(`\[` + stageDoneTag + `:\d+\]`)
	for _, stageIndex := range []int{0, 1, 7} {
		contract := BuildStageExitContract(stageIndex)
		marker := markerRe.FindString(contract)
		if marker == "" {
			t.Fatalf("stage %d: contract asks for no done marker: %q", stageIndex, contract)
		}
		if want := "[" + stageDoneTag + ":" + strconv.Itoa(stageIndex) + "]"; marker != want {
			t.Fatalf("stage %d: contract asks for %q, want %q", stageIndex, marker, want)
		}
		if got := matchStageOutcome("干完了\n"+marker, stageIndex); got.Outcome != StageOutcomeDone {
			t.Fatalf("stage %d: matcher rejects the marker the contract asked for: %+v", stageIndex, got)
		}
		blockedMarker := "[" + stageBlockedTag + ":" + strconv.Itoa(stageIndex) + " 缺凭证]"
		if !strings.Contains(contract, blockedMarker[:len(blockedMarker)-len(" 缺凭证]")]) {
			t.Fatalf("stage %d: contract asks for no blocked marker: %q", stageIndex, contract)
		}
		got := matchStageOutcome(blockedMarker, stageIndex)
		if got.Outcome != StageOutcomeBlocked || got.Reason != "缺凭证" {
			t.Fatalf("stage %d: blocked = %+v, want Blocked/缺凭证", stageIndex, got)
		}
	}
}

func TestMatchStageOutcome(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		stageIndex int
		want       StageOutcome
		wantReason string
	}{
		{"done", "改完了，看过测试\n[MINDFS-STAGE-DONE:1]", 1, StageOutcomeDone, ""},
		{"done on first stage", "[MINDFS-STAGE-DONE:0]", 0, StageOutcomeDone, ""},
		{"blocked with reason", "跑不动，缺数据库迁移文件\n[MINDFS-STAGE-BLOCKED:1 缺数据库迁移文件]", 1, StageOutcomeBlocked, "缺数据库迁移文件"},
		{"blocked without reason", "[MINDFS-STAGE-BLOCKED:2]", 2, StageOutcomeBlocked, ""},
		// 段号不吻合 = 上一段的残留标记，不许拿来当本段完成。
		{"stale marker from previous stage", "上轮标记\n[MINDFS-STAGE-DONE:1]", 2, StageOutcomeSilent, ""},
		{"stale blocked marker from previous stage", "[MINDFS-STAGE-BLOCKED:0 旧的]", 1, StageOutcomeSilent, ""},
		{"silent", "我先看看代码", 1, StageOutcomeSilent, ""},
		{"empty", "", 1, StageOutcomeSilent, ""},
		// 先说做完了又补一句「其实有地方要确认」：按做完算（marker 是显式契约，
		// 比正文里的语气可信）。
		{"done wins over blocked", "[MINDFS-STAGE-DONE:1]\n[MINDFS-STAGE-BLOCKED:1 顺带一提]", 1, StageOutcomeDone, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchStageOutcome(tt.text, tt.stageIndex)
			if got.Outcome != tt.want || got.Reason != tt.wantReason {
				t.Fatalf("matchStageOutcome(%q, %d) = %+v, want outcome %v reason %q", tt.text, tt.stageIndex, got, tt.want, tt.wantReason)
			}
		})
	}
}

// agent 没显式回报完成，本段就不许算成功——否则 AutoAdvance 会把下一段植进来，
// 前一段的活没干完，下一段已经在错误前提上开跑（本次要消灭的阶段错乱）。
func TestAgentStageWithoutDoneMarkerStopsAtCurrentStage(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{result: StageResult{Outcome: StageOutcomeSilent}})
	implement := agentStage("Implement", "Implement {previous_input}")
	implement.AutoAdvance = true
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Describe"),
			implement,
			userStage("Review"),
		},
		Input: "change",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	waitForCondition(t, func() bool {
		detail, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && detail.Task.Status == StatusWaitingUser
	})
	// 停在第 1 段，下一段没被植进来。
	if detail.Task.CurrentStageIndex != 1 {
		t.Fatalf("current stage = %d, want 1（没回报完成不许推进）", detail.Task.CurrentStageIndex)
	}
	if len(detail.StageRuns) != 2 {
		t.Fatalf("stage run count = %d, want 2（下一段不许开跑）", len(detail.StageRuns))
	}
	latest := detail.StageRuns[len(detail.StageRuns)-1]
	if latest.Status != StageStatusWaitingUser {
		t.Fatalf("stage status = %s, want waiting_user", latest.Status)
	}
	if !strings.Contains(detail.Task.AuxFlags.SessionError, "未回报完成") {
		t.Fatalf("session error = %q, want it to say the stage never reported done", detail.Task.AuxFlags.SessionError)
	}
	// 人工补一句评论也不许顶掉没走完的 agent 段。
	if _, err := svc.AddStage(ctx, AddStageInput{
		RootID: root.ID,
		TaskID: detail.Task.ID,
		Stage:  StageTemplate{PromptTemplate: "补一句"},
	}); err == nil {
		t.Fatalf("AddStage on an unfinished agent stage must be rejected, got nil error")
	}
}

// agent 自己说受阻：原因原样摆到卡面上等人处理，同样不许推进。
func TestAgentStageBlockedStopsWithAgentReason(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{
		result: StageResult{Outcome: StageOutcomeBlocked, Reason: "缺数据库迁移文件"},
	})
	implement := agentStage("Implement", "Implement {previous_input}")
	implement.AutoAdvance = true
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{userStage("Describe"), implement, userStage("Review")},
		Input:  "change",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	waitForCondition(t, func() bool {
		detail, err = svc.GetTask(ctx, detail.Task.RootID, detail.Task.ID)
		return err == nil && detail.Task.Status == StatusWaitingUser
	})
	if detail.Task.CurrentStageIndex != 1 || len(detail.StageRuns) != 2 {
		t.Fatalf("blocked stage advanced: index=%d runs=%d", detail.Task.CurrentStageIndex, len(detail.StageRuns))
	}
	if detail.Task.AuxFlags.SessionError != "缺数据库迁移文件" {
		t.Fatalf("session error = %q, want the agent's own reason", detail.Task.AuxFlags.SessionError)
	}
}

// 回报完成后照旧推进（第一段之外的行为不许被新契约改掉）。
func TestAgentStageWithDoneMarkerAutoAdvances(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{result: StageResult{Outcome: StageOutcomeDone}})
	implement := agentStage("Implement", "Implement {previous_input}")
	implement.AutoAdvance = true
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{userStage("Describe"), implement, userStage("Review")},
		Input:  "change",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	waitForCondition(t, func() bool {
		detail, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && detail.Task.CurrentStageIndex == 2 && detail.Task.Status == StatusWaitingUser
	})
	// 契约必须真的随 prompt 送出去了，否则 agent 不知道要输出什么。
	runner := svc.Runner.(*fakeRunner)
	runner.mu.Lock()
	prompts := append([]string(nil), runner.prompts...)
	runner.mu.Unlock()
	if len(prompts) == 0 {
		t.Fatalf("agent stage ran without a prompt")
	}
	if !strings.Contains(prompts[len(prompts)-1], "[MINDFS-STAGE-DONE:1]") {
		t.Fatalf("stage exit contract missing from the rendered prompt: %q", prompts[len(prompts)-1])
	}
}

func TestSchedulerRunsAgentStageAndStoresSessionKey(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Describe"),
			agentStage("Fix", "Fix #{task_number}:\n{previous_input}"),
		},
		Input: "broken save button",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	detail, err = svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID, Reason: "ready for agent"})
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	waitForCondition(t, func() bool {
		detail, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && detail.Task.CurrentStageIndex == 1 && detail.Task.Status == StatusWaitingUser
	})
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if detail.Task.CurrentStageIndex != 1 || detail.Task.Status != StatusWaitingUser {
		t.Fatalf("task stage/status = %d/%s, want 1/waiting_user", detail.Task.CurrentStageIndex, detail.Task.Status)
	}
	if detail.Task.MainSessionKey == "" {
		t.Fatalf("main session key not stored")
	}
	agentRun := detail.StageRuns[len(detail.StageRuns)-1]
	if agentRun.SessionKey == "" {
		t.Fatalf("agent run session key empty: %#v", agentRun)
	}
	if !strings.Contains(agentRun.RenderedPrompt, "broken save button") {
		t.Fatalf("rendered prompt missing input: %q", agentRun.RenderedPrompt)
	}
	if !strings.Contains(agentRun.RenderedPrompt, "Fix #1") {
		t.Fatalf("rendered prompt missing task number: %q", agentRun.RenderedPrompt)
	}
	runner := svc.Runner.(*fakeRunner)
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.execs) != 1 {
		t.Fatalf("runner exec count=%d, want 1", len(runner.execs))
	}
	if runner.execs[0].Run.SessionKey != agentRun.SessionKey {
		t.Fatalf("runner session=%q, run session=%q", runner.execs[0].Run.SessionKey, agentRun.SessionKey)
	}
}

func TestAgentStageSessionErrorWaitsForUser(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{runErr: errors.New("agent unavailable")})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Describe"),
			agentStage("Fix", "Fix this:\n{previous_input}"),
		},
		Input: "broken",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	detail, err = svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID})
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	waitForCondition(t, func() bool {
		detail, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && detail.Task.AuxFlags.SessionError != ""
	})
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if detail.Task.Status != StatusWaitingUser {
		t.Fatalf("task status = %s, want waiting_user", detail.Task.Status)
	}
	run := detail.StageRuns[len(detail.StageRuns)-1]
	if run.Status != StageStatusFail || run.FinishedAt == "" {
		t.Fatalf("stage status/finished_at = %s/%q, want fail/non-empty", run.Status, run.FinishedAt)
	}
	if detail.Task.AuxFlags.SessionError != "agent unavailable" {
		t.Fatalf("session error = %q, want agent unavailable", detail.Task.AuxFlags.SessionError)
	}
	time.Sleep(50 * time.Millisecond)
	runner := svc.Runner.(*fakeRunner)
	runner.mu.Lock()
	execCount := len(runner.execs)
	runner.mu.Unlock()
	if execCount != 1 {
		t.Fatalf("agent exec count = %d, want 1", execCount)
	}
}

func TestAutoAdvanceAgentStageFailureWaitsForUserAtCurrentStage(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{runErr: errors.New("agent unavailable")})
	implement := agentStage("Implement", "Implement {previous_input}")
	implement.AutoAdvance = true
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Describe"),
			implement,
			userStage("Review"),
		},
		Input: "change",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	waitForCondition(t, func() bool {
		detail, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && detail.Task.Status == StatusWaitingUser && detail.Task.AuxFlags.SessionError != ""
	})
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if detail.Task.CurrentStageIndex != 1 || detail.Task.Status != StatusWaitingUser {
		t.Fatalf("task stage/status = %d/%s, want 1/waiting_user", detail.Task.CurrentStageIndex, detail.Task.Status)
	}
	if len(detail.StageRuns) != 2 {
		t.Fatalf("stage run count = %d, want 2 (no auto-advanced run)", len(detail.StageRuns))
	}
	latest := detail.StageRuns[len(detail.StageRuns)-1]
	if latest.Status != StageStatusFail {
		t.Fatalf("stage status = %s, want fail", latest.Status)
	}
}

// 失败段不许被一句「下一段」推过去。这条曾经是反着写的
// （TestNextAdvancesFailedCurrentStageAfterUserReview），当时把
// 「失败段可以靠下一次 Next 强推」当成期望行为，实际就是阶段错乱的来源：
// 前一段的活没干完，下一段已经在错误前提上开跑。
// 现在失败段只能靠 RerunStage 重跑或改任务离开，Next 必须被拒。
func TestNextRejectsFailedAgentStageUntilRerun(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{runErr: errors.New("agent unavailable")})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Describe"),
			agentStage("Fix", "Fix this:\n{previous_input}"),
			agentStage("Review", "Review."),
		},
		Input: "broken",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next to agent: %v", err)
	}
	waitForCondition(t, func() bool {
		detail, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && detail.Task.CurrentStageIndex == 1 && detail.Task.AuxFlags.SessionError != ""
	})
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err == nil {
		t.Fatalf("Next from a failed stage must be rejected, got nil error")
	}
	detail, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if detail.Task.CurrentStageIndex != 1 {
		t.Fatalf("current stage = %d, want 1（失败段不许被推走）", detail.Task.CurrentStageIndex)
	}
	latest := detail.StageRuns[len(detail.StageRuns)-1]
	if latest.Status != StageStatusFail {
		t.Fatalf("stage status = %s, want fail（不许被改成 approved）", latest.Status)
	}
}

func TestAgentStageAllowsBlankModel(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	blank := agentStage("Fix", "Fix this:\n{previous_input}")
	blank.Model = ""
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{userStage("Describe"), blank},
		Input:  "use defaults",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	waitForCondition(t, func() bool {
		runner := svc.Runner.(*fakeRunner)
		runner.mu.Lock()
		defer runner.mu.Unlock()
		return len(runner.execs) > 0
	})
	runner := svc.Runner.(*fakeRunner)
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.execs) > 0 && runner.execs[0].Stage.Model != "" {
		t.Fatalf("execution model = %q, want empty", runner.execs[0].Stage.Model)
	}
}
