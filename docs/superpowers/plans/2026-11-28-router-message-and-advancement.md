# Router 消息与推进 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 `turn_settled` / `run_settled` 从「自动收口」改成可继续的检查点，让消息按价值选择、只在收口写总结、30 分钟内不重复，并为长任务增加 30 分钟固定心跳探针。

**Architecture:** 在现有 Router 运行期事件白名单上按触发类型放开 `router_message_agent` / `router_run_workflow`，用任务状态、活动执行、事件新鲜度做硬闸；把 summary 从「最后一条 say 自动提升」改为「显式 summary，或任务已终态的 run 结算才回退」；在 `router_say` 落库前增加跨触发的进度去重与统一的转交抑制；由现有 task health sweep 新增 `turn_heartbeat` 事件，以 Router 最后可见消息 / 上次心跳为 30 分钟锚点。

**Tech Stack:** Go 1.26.5、SQLite WAL、现有 `server/internal/kanban` 测试夹具；本期不改前端。

**Spec:** `docs/superpowers/specs/2026-11-28-router-task-owner-design.md` §5.4、§5.5、§5.10、§6。

## Global Constraints

- Go 工具链在 `$HOME/.local/share/go`，裸跑 `go` / `make test`；永远不要 `export PATH=...`。
- 提交用 Conventional Commits，中文描述；每个提交带 `Scope: G-XX`（Task 1 认领的组号）。
- 每个提交把本次改动的文件与新增测试登记进 `docs/upstream-customizations.yaml` 的同一组；提交后跑 `make check-upstream`，绿了再进下一任务。
- 本期只做消息与推进；不改任务契约、验收证据、状态机表、记忆、B2 工具层、前端。
- 不新增数据库表；心跳状态复用 `router_events` 账本，不新增持久化字段。
- 30 分钟是固定值，不因长构建 / 长测试放宽；心跳只进时间线，不调用 `router_notify_user`。
- 不恢复固定四段式标签，不设字数硬限；存储层已有的 1000 字符截断保持不变。
- 不改 `recordTurnResultSummary` 的直连兜底摘要；它仍是卡片兜底，Router 的 summary 只在收口覆盖它。
- 不改 `router_read_session` 的读端口与上限；本期只把它写进提示词工具清单。

## Review Focus

1. `paused` / `success` / `fail` / `cancelled` 任务上的检查点继续动作：必须拒绝，不能把已停任务重新拉起来。→ Task 1 `TestCheckpointTurnAllowsContinuationWithGuards`。
2. 旧 `run_settled` 迟到、更新的 Run 已经存在：必须拒绝继续动作，不能重开旧现场。→ Task 1 同一测试。
3. `turn_settled` 只有 progress、任务还没收口：不能写卡片摘要；只有显式 summary 或终态 run 结算才写。→ Task 2 `TestSettleSummaryRequiresExplicitSummaryUnlessClosing` 与集成测试。
4. 30 分钟内刚有 Router 可见汇报：心跳不能提前触发；上次心跳后 30 分钟才允许下一次。→ Task 3 `TestClassifyTaskHealthHeartbeatAnchor`。
5. 同一段 progress 在不同触发类型下重复：必须隐藏；同样正文的 summary 仍要落卡片。→ Task 4 `TestRepeatedReportIgnoresTriggerKind` 与集成测试。

## File Structure

- `server/internal/kanban/router_coordinate_turn.go` — 运行期动作白名单；检查点继续的硬闸。
- `server/internal/kanban/activity_route_apply.go` — 无来源留言时的动作放行；summary 决策调用点；`run_workflow` 的 nil 安全。
- `server/internal/kanban/activity_route_reply.go` — summary 选择与回退判据。
- `server/internal/kanban/activity_route_say.go` — 转交抑制、跨轮进度去重、summary 提升。
- `server/internal/kanban/inspection_dedupe.go` — 跨触发 `RepeatedReport`。
- `server/internal/kanban/router_types.go` — `turn_heartbeat` 触发常量。
- `server/internal/kanban/router_event_types.go` — `turn_heartbeat` 事件常量与已知类型。
- `server/internal/kanban/router_task_health.go` — 心跳锚点、分类、事件写入。
- `server/internal/kanban/router_coordinate.go` — 事件 → 触发映射。
- `server/internal/kanban/service_turn_settled.go` — 无来源留言的事件放行。
- `server/internal/kanban/router_event_brief.go` — 心跳事件的状态名。
- `server/internal/kanban/router_prompt.md` — 规则 6 / 9 / 11 / 12 / 15 / 18 / 22 与工具清单。

---

### Task 0: 认领 Scope 组并登记

**Files:**
- Modify: `docs/upstream-customizations.yaml`（新增本功能的组）
- Modify: `docs/superpowers/plans/2026-11-28-router-message-and-advancement.md`（脚本写入 `> Scope IDs:` 行）

- [ ] **Step 1: 认领组号**

Run: `go run ./scripts/scope-id claim docs/superpowers/plans/2026-11-28-router-message-and-advancement.md`
Expected: 计划头部出现 `> Scope IDs: G-XX`，`docs/upstream-customizations.yaml` 末尾出现同名占位组。

- [ ] **Step 2: 填写占位组**

在占位组里写清：标题「Router 消息与推进」、为什么必须保留（Router 作为任务负责人需要检查点继续、收口总结、30 分钟心跳与跨轮去重）、可见症状（结算后不继续、每轮回 summary、长任务静默、同一句进度刷屏）。本任务只登记组，不列代码文件；后续每个任务在自己的提交里往同一组补 `files` / `tests`。

- [ ] **Step 3: 提交登记**

```bash
git add docs/superpowers/plans/2026-11-28-router-message-and-advancement.md \
  docs/upstream-customizations.yaml
git commit -m "chore(upstream): 登记 Router 消息与推进定制（Scope: G-XX）"
```

`make check-upstream` 只比已提交的文件，所以本步可以先不跑；Task 1 提交后再开始每任务跑。

---

### Task 1: 检查点回合可以继续推进

**Files:**
- Modify: `server/internal/kanban/router_coordinate_turn.go:463-505`（`validateRuntimeEventActions`）
- Modify: `server/internal/kanban/activity_route_apply.go:175-178`（无来源留言闸门）、`:389`（`userAct.ID`）
- Modify: `server/internal/kanban/service_turn_settled.go:360-398`（同级新增 `routerTurnAllowsContinuation`）
- Modify: `server/internal/kanban/router_prompt.md:6`、`:9-34`、`:52-58`
- Modify: `server/internal/kanban/router_turn_settled_test.go:403-417`
- Modify: `server/internal/kanban/router_prompt_guidance_test.go`
- Test: `server/internal/kanban/router_continuation_test.go`（新建）

**Interfaces:**
- Produces:
  - `func routerTurnAllowsContinuation(kind RouterTriggerKind) bool` — 只对 `turn_settled` / `run_settled` / `turn_stalled` / `task_orphaned` / `turn_interrupted` 返回 true。
  - `func routeActionAllowedWithoutUserActivity(kind string, action RouterActionKind) bool` — 巡检回合没有来源用户留言时，say/ask/silent/notify/escalate 放行；message_agent/run_workflow 只在 `routerTurnAllowsContinuation` 为 true 时放行。
- Consumes: 现有 `SituationBrief`、`RouterDecision`、`RouterAction`、`isTerminalStatus`。

- [ ] **Step 1: 写失败测试**

新建 `server/internal/kanban/router_continuation_test.go`：

```go
func TestCheckpointTurnAllowsContinuationWithGuards(t *testing.T) {
	base := &SituationBrief{
		Task:    RouterBriefTask{Status: StatusWaitingUser},
		Trigger: RouterBriefTrigger{Kind: RouterTriggerTurnSettled},
		Event:   &RouterBriefEvent{Kind: string(RouterEventTurnSettled)},
	}
	for _, action := range []RouterAction{
		{Kind: RouterActionMessageAgent, Body: "继续"},
		{Kind: RouterActionRunWorkflow, WorkflowID: "wf-1"},
	} {
		if err := validateRuntimeEventActions(base, []RouterAction{action}, RouterConfig{}); err != nil {
			t.Fatalf("%s rejected on checkpoint: %v", action.Kind, err)
		}
	}
	paused := *base
	paused.Task.Status = StatusPaused
	if err := validateRuntimeEventActions(&paused, []RouterAction{{Kind: RouterActionMessageAgent, Body: "继续"}}, RouterConfig{}); err == nil {
		t.Fatal("paused task accepted continuation")
	}
	stale := *base
	stale.Trigger = RouterBriefTrigger{Kind: RouterTriggerRunSettled, Ref: "run-old"}
	stale.LatestRun = &RouterBriefRun{ID: "run-new"}
	if err := validateRuntimeEventActions(&stale, []RouterAction{{Kind: RouterActionRunWorkflow, WorkflowID: "wf-1"}}, RouterConfig{}); err == nil {
		t.Fatal("stale run_settled accepted continuation")
	}
	heartbeat := *base
	heartbeat.Trigger = RouterBriefTrigger{Kind: RouterTriggerKind("turn_heartbeat")}
	if err := validateRuntimeEventActions(&heartbeat, []RouterAction{{Kind: RouterActionMessageAgent, Body: "继续"}}, RouterConfig{}); err == nil {
		t.Fatal("heartbeat accepted continuation")
	}
}

func TestNoUserActivityContinuationGate(t *testing.T) {
	if !routeActionAllowedWithoutUserActivity(string(RouterTriggerTurnSettled), RouterActionMessageAgent) {
		t.Fatal("turn_settled should allow message_agent")
	}
	if !routeActionAllowedWithoutUserActivity(string(RouterTriggerRunSettled), RouterActionRunWorkflow) {
		t.Fatal("run_settled should allow run_workflow")
	}
	if routeActionAllowedWithoutUserActivity(string(RouterTriggerKind("turn_heartbeat")), RouterActionMessageAgent) {
		t.Fatal("heartbeat should not allow message_agent")
	}
	if !routeActionAllowedWithoutUserActivity(string(RouterTriggerTurnSettled), RouterActionSay) {
		t.Fatal("say should stay allowed")
	}
}
```

同时把 `router_turn_settled_test.go` 的 `TestTurnSettledAllowsNotifyUserButRejectsHandoff` 改为 `TestCheckpointTurnAllowsNotifyAndContinuation`：允许列表加入 `router_message_agent` / `router_run_workflow`；拒绝列表把 brief 的触发一并改成非检查点，再断言仍然拒绝：

```go
brief.Trigger = RouterBriefTrigger{Kind: RouterTriggerGateWaiting}
brief.Event = &RouterBriefEvent{Kind: string(RouterEventGateWaiting)}
if err := validateRuntimeEventActions(brief, []RouterAction{{Kind: RouterActionRunWorkflow, WorkflowID: "wf-1"}}, RouterConfig{}); err == nil {
	t.Fatal("gate_waiting accepted run_workflow")
}
```

只改 `Event.Kind` 不够：`validateRuntimeEventActions` 读的是 `brief.Trigger.Kind`。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./server/internal/kanban -run 'TestCheckpointTurnAllowsContinuationWithGuards|TestNoUserActivityContinuationGate' -count=1`
Expected: FAIL，`routerTurnAllowsContinuation` / `routeActionAllowedWithoutUserActivity` 未定义，或 `validateRuntimeEventActions` 仍拒绝。

- [ ] **Step 3: 实现**

在 `service_turn_settled.go` 的 `routerTurnAllowsNoUserActivity` 旁新增：

```go
func routerTurnAllowsContinuation(kind RouterTriggerKind) bool {
	switch kind {
	case RouterTriggerTurnSettled, RouterTriggerRunSettled,
		RouterTriggerTurnStalled, RouterTriggerTaskOrphaned, RouterTriggerTurnInterrupted:
		return true
	default:
		return false
	}
}
```

在 `activity_route_apply.go` 的闸门旁新增并替换条件：

```go
func routeActionAllowedWithoutUserActivity(kind string, action RouterActionKind) bool {
	switch action {
	case RouterActionSay, RouterActionAskUser, RouterActionSilent,
		RouterActionNotifyUser, RouterActionEscalate:
		return true
	case RouterActionMessageAgent, RouterActionRunWorkflow:
		return routerTurnAllowsContinuation(RouterTriggerKind(kind))
	default:
		return false
	}
}
```

把 `activity_route_apply.go:175` 的条件改为 `... && !routeActionAllowedWithoutUserActivity(...)`。在同一文件的 `applyRouteRunWorkflow` 中把 `userAct.ID` 改成 `routeUserActivityID(userAct)`。

在 `validateRuntimeEventActions` 的 switch 增加：

```go
case RouterActionMessageAgent, RouterActionRunWorkflow:
	if brief == nil || !routerTurnAllowsContinuation(brief.Trigger.Kind) {
		return fmt.Errorf("%s is not allowed for runtime event turns", action.Kind)
	}
	if brief.Task.Status == StatusPaused || isTerminalStatus(brief.Task.Status) {
		return fmt.Errorf("%s is not allowed on a %s task", action.Kind, brief.Task.Status)
	}
	if brief.ActiveExecution != nil {
		return fmt.Errorf("%s is not allowed while an execution is active", action.Kind)
	}
	if brief.Trigger.Kind == RouterTriggerRunSettled &&
		(brief.LatestRun == nil || (brief.Trigger.Ref != "" && brief.LatestRun.ID != brief.Trigger.Ref)) {
		return fmt.Errorf("%s is not allowed for a stale run event", action.Kind)
	}
	continue
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./server/internal/kanban -run 'TestCheckpointTurnAllowsContinuationWithGuards|TestNoUserActivityContinuationGate|TestCheckpointTurnAllowsNotifyAndContinuation' -count=1`
Expected: PASS。

- [ ] **Step 5: 更新提示词与提示词测试**

`router_prompt.md`：
- 第 6 行改为：`- 你可以读本任务的会话转录（router_read_session），但看不到工作区文件内容或 shell 输出；转录用于判断进展，不是执行结果。`
- 工具清单的只读段增加：`- 只读：router_read_session(session_key?, offset?, limit?)（读本任务最近会话文本；缺省读最近一次节点执行的会话，offset 往前翻，limit 最多 5 段）。`
- `router_say` 描述里的 `summary` 改为：`summary` 只在任务真正收口时用（见规则 18）。
- 规则 11 整体替换为“检查点 / 其它运行期事件 / 检查点三判断”三段；明确 `turn_settled` / `run_settled` / `turn_stalled` / `task_orphaned` / `turn_interrupted` 为检查点，允许 `router_message_agent` / `router_run_workflow`，条件是任务非 paused/终态、无活动执行、`run_settled` 是最新一轮；`turn_heartbeat` / `gate_waiting` / `completion_blocked` / `run_failed` 不放开继续动作。
- 规则 12 改为只依据 `brief.event` / `brief.evidence` 汇报新增事实，并保留“同一任务 30 分钟内不要重复同一段汇报”。

在 `router_prompt_guidance_test.go` 增加：

```go
func TestRouterPromptStatesCheckpointContinuation(t *testing.T) {
	prompt := RouterDeveloperInstructions()
	for _, want := range []string{"检查点", "router_message_agent", "router_run_workflow", "turn_heartbeat", "没有活动执行"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("提示词缺少「%s」", want)
		}
	}
}
```

- [ ] **Step 6: 跑提示词测试并提交**

Run: `go test ./server/internal/kanban -run 'TestRouterPromptStatesCheckpointContinuation|TestRouterPrompt' -count=1`
Expected: PASS。

在 `docs/upstream-customizations.yaml` 的 G-XX 组里加入本任务改动的文件与测试。提交：

```bash
git add server/internal/kanban/router_coordinate_turn.go \
  server/internal/kanban/activity_route_apply.go \
  server/internal/kanban/service_turn_settled.go \
  server/internal/kanban/router_prompt.md \
  server/internal/kanban/router_turn_settled_test.go \
  server/internal/kanban/router_continuation_test.go \
  server/internal/kanban/router_prompt_guidance_test.go \
  docs/upstream-customizations.yaml
git commit -m "feat(router): 结算与巡检回合允许继续推进（Scope: G-XX）"
```

提交后跑 `make check-upstream`，必须绿。

---

### Task 2: 总结只在收口出现

**Files:**
- Modify: `server/internal/kanban/activity_route_reply.go:80-98`（`settleSummaryActionSeq`）
- Modify: `server/internal/kanban/activity_route_apply.go:61-76`（调用点）
- Modify: `server/internal/kanban/router_prompt.md:59-74`（规则 15 / 18）
- Modify: `server/internal/kanban/router_turn_settled_summary_test.go`
- Modify: `server/internal/kanban/router_settle_summary_test.go`
- Modify: `server/internal/kanban/turn_summary_ownership_test.go:187-200`
- Test: `server/internal/kanban/router_settle_summary_test.go`（新增单元测试）

**Interfaces:**
- Consumes: Task 1 的检查点语义。
- Produces:
  - `func settleSummaryActionSeq(decision *RouterDecision, task Task) int`
  - `func summaryFallbackAllowed(decision *RouterDecision, task Task) bool`
- 语义：显式 `purpose=summary` 永远优先；没有显式 summary 时，只有 `run_settled` 且任务已终态（success/fail/cancelled）才回退到最后一条 say；带执行动作时不回退。

- [ ] **Step 1: 写失败测试**

在 `router_settle_summary_test.go` 增加：

```go
func TestSettleSummaryRequiresExplicitSummaryUnlessClosing(t *testing.T) {
	progress := RouterAction{Seq: 0, Kind: RouterActionSay, Purpose: "progress", Body: "还在改。"}
	explicit := RouterAction{Seq: 1, Kind: RouterActionSay, Purpose: "summary", Body: "改完了。"}

	turn := &RouterDecision{TriggerKind: RouterTriggerTurnSettled, Actions: []RouterAction{progress}}
	if got := settleSummaryActionSeq(turn, Task{Status: StatusWaitingUser}); got != -1 {
		t.Fatalf("turn_settled progress-only summary seq = %d, want -1", got)
	}
	turn.Actions = append(turn.Actions, explicit)
	if got := settleSummaryActionSeq(turn, Task{Status: StatusWaitingUser}); got != 1 {
		t.Fatalf("explicit summary seq = %d, want 1", got)
	}
	withHandoff := &RouterDecision{
		TriggerKind: RouterTriggerTurnSettled,
		Actions: []RouterAction{
			explicit,
			{Seq: 2, Kind: RouterActionMessageAgent, Body: "继续"},
		},
	}
	if got := settleSummaryActionSeq(withHandoff, Task{Status: StatusWaitingUser}); got != -1 {
		t.Fatalf("summary with handoff seq = %d, want -1", got)
	}

	run := &RouterDecision{TriggerKind: RouterTriggerRunSettled, Actions: []RouterAction{progress}}
	if got := settleSummaryActionSeq(run, Task{Status: StatusSuccess}); got != 0 {
		t.Fatalf("terminal run fallback seq = %d, want 0", got)
	}
	if got := settleSummaryActionSeq(run, Task{Status: StatusWaitingUser}); got != -1 {
		t.Fatalf("open run fallback seq = %d, want -1", got)
	}
	run.Actions = append(run.Actions, RouterAction{Seq: 2, Kind: RouterActionMessageAgent, Body: "继续"})
	if got := settleSummaryActionSeq(run, Task{Status: StatusSuccess}); got != -1 {
		t.Fatalf("handoff run fallback seq = %d, want -1", got)
	}
}
```

把 `router_turn_settled_summary_test.go` 的 `TestTurnSettledPromotesLastSayWhenTurnHadToolCalls` 改为 `TestTurnSettledProgressOnlyDoesNotWriteCard`，并新增 `TestTurnSettledExplicitSummaryWritesCard`。两个测试都用 `answerDecisionFixture`，直接断言落库结果：

```go
func TestTurnSettledProgressOnlyDoesNotWriteCard(t *testing.T) {
	ctx := context.Background()
	store, activity, decision, _ := answerDecisionFixture(t,
		"task-progress-turn", "run-progress-turn", "decision-progress-turn")
	body := "还在改，先报进度。"
	decision.TriggerKind = RouterTriggerTurnSettled
	decision.TriggerRef = "turn-progress-turn"
	decision.PlaceholderActivityID = ""
	decision.Actions = []RouterAction{{Seq: 0, Kind: RouterActionSay, Purpose: "progress", Body: body}}
	if err := activity.ApplyRouteActions(ctx, decision); err != nil {
		t.Fatalf("ApplyRouteActions: %v", err)
	}
	task, err := store.GetTask(ctx, "task-progress-turn")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if strings.TrimSpace(task.ResultSummary) != "" {
		t.Fatalf("progress wrote card summary: %q", task.ResultSummary)
	}
	replies := routeRepliesWithBody(t, activity, "task-progress-turn", body)
	if len(replies) != 1 || replies[0].Purpose != "progress" {
		t.Fatalf("replies = %#v, want one visible progress", replies)
	}
}

func TestTurnSettledExplicitSummaryWritesCard(t *testing.T) {
	ctx := context.Background()
	store, activity, decision, _ := answerDecisionFixture(t,
		"task-summary-turn", "run-summary-turn", "decision-summary-turn")
	body := "迁移做完了，线上未复测。"
	decision.TriggerKind = RouterTriggerTurnSettled
	decision.TriggerRef = "turn-summary-turn"
	decision.PlaceholderActivityID = ""
	decision.Actions = []RouterAction{{Seq: 0, Kind: RouterActionSay, Purpose: "summary", Body: body}}
	if err := activity.ApplyRouteActions(ctx, decision); err != nil {
		t.Fatalf("ApplyRouteActions: %v", err)
	}
	task, err := store.GetTask(ctx, "task-summary-turn")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.ResultSummary != body {
		t.Fatalf("result_summary = %q, want %q", task.ResultSummary, body)
	}
}
```

run 的 open / terminal 差异由上面的单元测试覆盖；`TestRunSettledProgressSayIsPromotedToSummary` 保留（它用终态任务）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./server/internal/kanban -run 'TestSettleSummaryRequiresExplicitSummaryUnlessClosing|TestTurnSettledProgressOnlyDoesNotWriteCard|TestTurnSettledExplicitSummaryWritesCard' -count=1`
Expected: FAIL，progress-only 仍被提升为 summary。

- [ ] **Step 3: 实现**

把 `settleSummaryActionSeq` 改为：

```go
func settleSummaryActionSeq(decision *RouterDecision, task Task) int {
	if decision == nil || decisionHasHandoff(decision) {
		return -1
	}
	last := -1
	for _, action := range decision.Actions {
		if action.Kind != RouterActionSay {
			continue
		}
		if strings.TrimSpace(action.Purpose) == "summary" {
			return action.Seq
		}
		last = action.Seq
	}
	if last >= 0 && summaryFallbackAllowed(decision, task) {
		return last
	}
	return -1
}

func summaryFallbackAllowed(decision *RouterDecision, task Task) bool {
	if decision == nil {
		return false
	}
	return decision.TriggerKind == RouterTriggerRunSettled && isTerminalStatus(task.Status)
}
```

`activity_route_apply.go` 的 `ApplyRouteActions` 在 `state` 之前只加载一次任务快照，供 summary 判据使用：

```go
task, err := s.tasks.GetTask(ctx, decision.TaskID)
if err != nil {
	return fmt.Errorf("load task for summary decision: %w", err)
}
state := &routeApplyState{settleSummarySeq: -1}
```

两处调用改为 `settleSummaryActionSeq(decision, task)`；`turn_settled` 分支删除 `decision.HadToolCalls` 条件，始终调用（没有显式 summary 时函数自己返回 -1）。`settledRunSucceeded` 保持原义：只有成功结算的 Run 才算自动收口；显式 summary 仍由 `settleSummaryActionSeq` 在 `turn_settled` 与成功 `run_settled` 上放行。

`activity_route_say.go` 在 `body` 去空白之后、summary 提升之前再加一道同义闸：同一决策里带继续动作时，显式 summary 降级为 progress，避免 `UpdateTaskResultSummary` 无条件写卡片：

```go
if action.Purpose == "summary" && decisionHasHandoff(decision) {
	action.Purpose = "progress"
}
```

更新 `activity_route_say.go:26-29` 的注释与 `activity_route_reply.go` 的函数注释，说明“只有收口才回退；带继续动作时 summary 不算收口”。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./server/internal/kanban -run 'TestSettleSummaryRequiresExplicitSummaryUnlessClosing|TestTurnSettledProgressOnlyDoesNotWriteCard|TestTurnSettledExplicitSummaryWritesCard|TestRunSettledProgressSay' -count=1`
Expected: PASS。

- [ ] **Step 5: 重写提示词规则 18 与相关测试**

`router_prompt.md`：
- 规则 15 里“结算总结是唯一例外 —— 规则 18 规定的四个标签行必须原样写”改为“结算总结是唯一例外，按规则 18 写；不要求固定标签”。
- 规则 18 整体替换为：

```markdown
18. **收口总结（只在任务真正收口时写）**：`run_settled` / `turn_settled` 只是结算点，不等于收口。只有目标已达成、明确失败、或需要用户拍板停止时，才用 `router_say(purpose="summary")` 写一条；它是任务结果摘要，会替换卡片上的节点原文与系统兜底摘要。
   - 写什么：结论、影响、未验证/风险、下一步。不要求固定标签，不要求字数；按内容需要写。
   - 只写增量：progress 里已经说过的不要重复；不要照搬节点输出或转录原文。文件、命令、哈希、PID 只在用户据此判断时出现。
   - 一个收口回合只写一条 summary。没到收口就写 `progress`；没有新信息就 `router_silent`。summary 之后同一轮再写 progress，系统会直接丢弃它。
   - 失败收口：先写失败原因（用 `brief.error` 说人话；`failed=true` 只是证据，不要原样抄），再写已完成的部分（没有写「无」），最后写下一步（重试 / 换模型档 / 需要用户处理）。
   - 待用户决策的事项不要只写进 summary：同一轮再用 `router_ask_user` 做成 2–4 个选项的选择题；规则 17 的七类事项永远先问。
```

把 `turn_summary_ownership_test.go` 的 `TestRouterPromptStatesSummaryContract` 改为断言：`"turn_settled"`、`"收口"`、`"不要求固定标签"`、`"系统会直接丢弃它"`、`"failed=true"` 存在，且 `"硬上限 800 字"`、`"四个标签行"` 不再存在。

- [ ] **Step 6: 跑测试并提交**

Run: `go test ./server/internal/kanban -run 'TestRouterPromptStatesSummaryContract|TestTurnSettled|TestRunSettled' -count=1`
Expected: PASS。

更新 `docs/upstream-customizations.yaml` 的 G-XX 组。提交：

```bash
git add server/internal/kanban/activity_route_reply.go \
  server/internal/kanban/activity_route_apply.go \
  server/internal/kanban/activity_route_say.go \
  server/internal/kanban/router_prompt.md \
  server/internal/kanban/router_turn_settled_summary_test.go \
  server/internal/kanban/router_settle_summary_test.go \
  server/internal/kanban/turn_summary_ownership_test.go \
  docs/upstream-customizations.yaml
git commit -m "feat(router): 总结只在收口出现（Scope: G-XX）"
```

提交后跑 `make check-upstream`，必须绿。

---

### Task 3: 长任务 30 分钟心跳

**Files:**
- Modify: `server/internal/kanban/router_types.go:147-170`（新增触发常量）
- Modify: `server/internal/kanban/router_event_types.go:18-45`、`:130-170`（事件常量、已知类型、实例键错误分支）
- Modify: `server/internal/kanban/router_task_health.go`（常量、输入、查询、分类、事件写入）
- Modify: `server/internal/kanban/router_coordinate.go:54-95`（事件映射）
- Modify: `server/internal/kanban/service_turn_settled.go:360-398`（无来源留言放行）
- Modify: `server/internal/kanban/inspection_dedupe.go:117-127`（心跳纳入巡检去重）
- Modify: `server/internal/kanban/router_event_brief.go:358-385`（状态名）
- Modify: `server/internal/kanban/router_task_health.go:300-340`（未验证证据）
- Modify: `server/internal/kanban/router_prompt.md`（规则 12 / 新增 22）
- Test: `server/internal/kanban/router_task_health_test.go`

**Interfaces:**
- Produces:
  - `RouterEventTurnHeartbeat RouterEventKind = "turn_heartbeat"`
  - `RouterTriggerTurnHeartbeat RouterTriggerKind = "turn_heartbeat"`
  - `const taskHealthHeartbeatAfter = 30 * time.Minute`
  - `taskHealthHeartbeatDue taskHealth = "heartbeat_due"`
  - `type taskHealthHeartbeat struct { ActivityID, RunID, SessionKey string; StartedAt time.Time }`
  - `func heartbeatAnchor(in taskHealthInput) time.Time`
  - `func routerEventHeartbeatInstanceKey(kind RouterEventKind, ref string, anchor time.Time) string`
- Consumes: `taskHealthSweep`、`router_events` 账本、`task_activities` 的 `author='router'` 行。

- [ ] **Step 1: 写失败测试**

在 `router_task_health_test.go`：
- 把顶层 `runningReply.CreatedAt` 从 `now.Add(-30 * time.Minute)` 改为 `now.Add(-5 * time.Minute)`，避免既有 healthy 用例被心跳抢走；`staleReply` 仍靠 `LastOutput` 判停滞。
- 增加：

```go
func TestClassifyTaskHealthHeartbeatAnchor(t *testing.T) {
	start := time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)
	in := taskHealthInput{
		Status: StatusRunning,
		Now:    start.Add(31 * time.Minute),
		Replies: []taskHealthReply{{
			ActivityID: "act-hb", SessionKey: "sess-hb", CreatedAt: start,
		}},
		Heartbeat: &taskHealthHeartbeat{
			ActivityID: "act-hb", SessionKey: "sess-hb", StartedAt: start,
		},
	}
	if got := classifyTaskHealth(in); got != taskHealthHeartbeatDue {
		t.Fatalf("heartbeat = %q, want %q", got, taskHealthHeartbeatDue)
	}
	in.LastReportAt = start.Add(10 * time.Minute)
	if got := classifyTaskHealth(in); got != taskHealthHealthy {
		t.Fatalf("recent report heartbeat = %q, want healthy", got)
	}
	in.LastReportAt = time.Time{}
	in.LastHeartbeatAt = start.Add(30 * time.Minute)
	if got := classifyTaskHealth(in); got != taskHealthHealthy {
		t.Fatalf("recent heartbeat = %q, want healthy", got)
	}
}

func TestTaskHealthSweepEmitsHeartbeatOncePerAnchor(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	f := newTaskHealthFixture(t, "task-heartbeat", StatusRunning, "sess-hb")
	f.insertRunningReply(t, "act-hb", "sess-hb", now.Add(-31*time.Minute))
	f.svc.NoteSessionTurnOutput(ctx, f.rootID, "sess-hb", now.Add(-time.Minute))

	events, err := f.svc.taskHealthSweep(ctx, f.rootID, now)
	must(t, err, "first sweep")
	if len(events) != 1 || events[0].Kind != RouterEventTurnHeartbeat {
		t.Fatalf("first sweep events = %#v, want one turn_heartbeat", events)
	}
	if !strings.Contains(events[0].PayloadJSON, `"activity_id":"act-hb"`) {
		t.Fatalf("payload %s missing activity id", events[0].PayloadJSON)
	}

	events, err = f.svc.taskHealthSweep(ctx, f.rootID, now.Add(time.Minute))
	must(t, err, "second sweep")
	if len(events) != 0 {
		t.Fatalf("second sweep events = %d, want 0 in same anchor", len(events))
	}

	f.svc.NoteSessionTurnOutput(ctx, f.rootID, "sess-hb", now.Add(29*time.Minute))
	events, err = f.svc.taskHealthSweep(ctx, f.rootID, now.Add(30*time.Minute))
	must(t, err, "third sweep")
	if len(events) != 1 || events[0].Kind != RouterEventTurnHeartbeat {
		t.Fatalf("third sweep events = %#v, want next heartbeat", events)
	}
}

func TestHeartbeatEventMapsToTrigger(t *testing.T) {
	trigger, kind := routerTriggerForEvent(RouterEvent{Kind: RouterEventTurnHeartbeat})
	if kind != RouterTriggerTurnHeartbeat || trigger.Kind != RouterTriggerTurnHeartbeat {
		t.Fatalf("heartbeat mapping = %#v / %q", trigger, kind)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./server/internal/kanban -run 'TestClassifyTaskHealthHeartbeatAnchor|TestTaskHealthSweepEmitsHeartbeatOncePerAnchor|TestHeartbeatEventMapsToTrigger' -count=1`
Expected: FAIL，常量 / 字段 / 事件类型未定义。

- [ ] **Step 3: 实现事件与触发**

`router_types.go` 在 `RouterTriggerTurnStalled` 后加 `RouterTriggerTurnHeartbeat RouterTriggerKind = "turn_heartbeat"`。
`router_event_types.go` 在 `RouterEventTurnStalled` 后加 `RouterEventTurnHeartbeat RouterEventKind = "turn_heartbeat"`；`knownRouterEventKind` 与 `routerEventHealthKind` 加入；`RouterEventInstanceKey` 的 health 分支加入 `RouterEventTurnHeartbeat`，错误文案改为“use routerEventHealthInstanceKey / routerEventHeartbeatInstanceKey”。
`router_coordinate.go` 的 `routerTriggerForEvent` 加 `case RouterEventTurnHeartbeat: kind = RouterTriggerTurnHeartbeat`，并在函数末尾的 ref 兜底里加 `if triggerRef == "" && kind == RouterTriggerTurnHeartbeat { triggerRef = ev.TaskID }`。
`service_turn_settled.go` 的 `routerTurnAllowsNoUserActivity` 加 `RouterTriggerTurnHeartbeat`。
`inspection_dedupe.go` 的 `inspectionDedupeApplies` 加 `RouterTriggerTurnHeartbeat`。
`router_event_brief.go` 的 `routerEventRunStatus` 加 `case RouterEventTurnHeartbeat: return "turn_heartbeat"`；`router_task_health.go` 的 `routerEventHealthUnverified` 加一条 `heartbeat event: turn still running since <since>; progress is unverified until the transcript is read`。

- [ ] **Step 4: 实现心跳分类与事件写入**

`router_task_health.go`：

```go
const taskHealthHeartbeatAfter = 30 * time.Minute
const taskHealthHeartbeatDue taskHealth = "heartbeat_due"

type taskHealthHeartbeat struct {
	ActivityID string
	RunID      string
	SessionKey string
	StartedAt  time.Time
}

type taskHealthInput struct {
	// 现有字段...
	Heartbeat       *taskHealthHeartbeat
	LastReportAt    time.Time
	LastHeartbeatAt time.Time
}
```

`taskHealthInputFor` 增加：
- `LastReportAt` 查询：`SELECT COALESCE(MAX(created_at), '') FROM task_activities WHERE task_id=? AND author='router' AND hidden=0`，用 `parseActivityTime` 解析；查询失败返回 error。
- `LastHeartbeatAt` 查询：`SELECT COALESCE(MAX(created_at), '') FROM router_events WHERE task_id=? AND kind=?`（kind 用 `string(RouterEventTurnHeartbeat)`），用 `parseRouterTime` 解析；查询失败返回 error。
- `Heartbeat`：优先取 `runningTurnReplies` 里最早的一条，填 `ActivityID` / `SessionKey` / `CreatedAt`；没有在飞回复时，用：

```sql
SELECT r.id, COALESCE(r.session_key, ''), r.updated_at
FROM workflow_node_runs n
JOIN workflow_runs r ON r.id = n.run_id
WHERE r.task_id = ? AND n.status = ?
ORDER BY r.updated_at DESC
LIMIT 1
```

  （`n.status` 用 `NodeRunRunning`；空行返回 nil）填 `RunID` / `SessionKey` / `StartedAt = parseRouterTime(updated_at)`。查询包装成 `taskHeartbeat(ctx, store, taskID, replies) (*taskHealthHeartbeat, error)`。

`heartbeatAnchor`：

```go
func heartbeatAnchor(in taskHealthInput) time.Time {
	if in.Heartbeat == nil {
		return time.Time{}
	}
	anchor := in.Heartbeat.StartedAt
	if in.LastReportAt.After(anchor) {
		anchor = in.LastReportAt
	}
	if in.LastHeartbeatAt.After(anchor) {
		anchor = in.LastHeartbeatAt
	}
	return anchor
}
```

`classifyTaskHealth` 的 `StatusRunning` 分支有两处要加同一个心跳判据：`len(in.Replies)==0` 且 `in.OpenNodes || in.SessionLive` 的分支，在返回 healthy 之前；以及有回复行时，在停滞循环之后、返回 healthy 之前：

```go
if anchor := heartbeatAnchor(in); !anchor.IsZero() && in.Now.Sub(anchor) >= taskHealthHeartbeatAfter {
	return taskHealthHeartbeatDue
}
```

`taskHealthSweep` 的 switch 加：

```go
case taskHealthHeartbeatDue:
	kind = RouterEventTurnHeartbeat
	if in.Heartbeat.ActivityID != "" {
		payload["activity_id"] = in.Heartbeat.ActivityID
	}
	if in.Heartbeat.RunID != "" {
		payload["run_id"] = in.Heartbeat.RunID
	}
	payload["session_key"] = in.Heartbeat.SessionKey
	payload["since"] = heartbeatAnchor(in).UTC().Format(routerTimeLayout)
	payload["running_for_ms"] = in.Now.Sub(in.Heartbeat.StartedAt).Milliseconds()
	if !in.LastReportAt.IsZero() {
		payload["last_report_at"] = in.LastReportAt.UTC().Format(routerTimeLayout)
	}
```

事件 `RunID` 填 `in.Heartbeat.RunID`；`ref` 优先 `ActivityID`，其次 `RunID`，最后 `task.ID`。实例键按 kind 分派：

```go
instanceKey := routerEventHealthInstanceKey(kind, ref, now)
if kind == RouterEventTurnHeartbeat {
	instanceKey = routerEventHeartbeatInstanceKey(kind, ref, heartbeatAnchor(in))
}
```

`InsertEvent` 的 `RouterEvent` 字面量同时加 `RunID: in.Heartbeat.RunID`。同时把 `taskHealthFixture.events` 的过滤条件加上 `RouterEventTurnHeartbeat`，否则后续断言看不到心跳事件。

```go
func routerEventHeartbeatInstanceKey(kind RouterEventKind, ref string, anchor time.Time) string {
	return string(kind) + ":" + strings.TrimSpace(ref) + ":" + strconv.FormatInt(anchor.UTC().Unix(), 10)
}
```

同一锚点只发一次；Router 汇报或上一次心跳会推进锚点，下一次心跳在 30 分钟后。

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./server/internal/kanban -run 'TestClassifyTaskHealth|TestTaskHealthSweep|TestHeartbeatEventMapsToTrigger' -count=1`
Expected: PASS。

- [ ] **Step 6: 更新提示词并提交**

`router_prompt.md`：
- 规则 12 的触发清单加 `turn_heartbeat`。
- 新增规则 22（现有提示词已用到 21，不重排既有编号）：

```markdown
22. **长任务心跳（brief.trigger.kind=turn_heartbeat）**：任务已经 30 分钟没有你的用户可见汇报，但还在跑。这一轮只做探针和简报：
   - 用 router_read_session 读最近转录，优先看 brief.event.payload.last_report_at 之后的内容；整段代码块和思考先跳过，除非看不懂在做什么。必要时用只读工具核对产物是否存在、内容是否对得上。
   - 只报实况：在做什么、到哪了、下一步 / 是否卡住。没有新增进展就写一句「还在做 X，暂无新进展」，不要复述以前的结论。
   - 用 purpose=progress，不要用 summary；只进时间线，不要 router_notify_user。
   - 汇报后不要再调「继续」动作：任务还在跑，下一次心跳或结算会再叫你。
```

更新 `docs/upstream-customizations.yaml` 的 G-XX 组。提交：

```bash
git add server/internal/kanban/router_types.go \
  server/internal/kanban/router_event_types.go \
  server/internal/kanban/router_task_health.go \
  server/internal/kanban/router_coordinate.go \
  server/internal/kanban/service_turn_settled.go \
  server/internal/kanban/inspection_dedupe.go \
  server/internal/kanban/router_event_brief.go \
  server/internal/kanban/router_prompt.md \
  server/internal/kanban/router_task_health_test.go \
  docs/upstream-customizations.yaml
git commit -m "feat(router): 长任务 30 分钟心跳探针（Scope: G-XX）"
```

提交后跑 `make check-upstream`，必须绿。

---

### Task 4: 跨轮汇报去重与细节分层

**Files:**
- Modify: `server/internal/kanban/inspection_dedupe.go`（`reports` 映射与 `RepeatedReport`）
- Modify: `server/internal/kanban/activity_route_say.go:26-75`（转交抑制、进度去重、顺序调整）
- Modify: `server/internal/kanban/router_prompt.md:9`、`:12`、`:18`
- Modify: `server/internal/kanban/route_say_repetition_test.go`
- Test: `server/internal/kanban/route_say_repetition_test.go`

**Interfaces:**
- Produces:
  - `func (d *InspectionDedupe) RepeatedReport(taskID, body string) bool` — 跨触发、同任务、归一化正文相同、30 分钟窗口。
  - `func routeSaySuppressedByHandoff(decision *RouterDecision, action RouterAction) bool` — 转交轮的 progress 一律不发，不再只限 `user_message`。
- Consumes: Task 2 的 summary 语义、Task 3 的 `turn_heartbeat` 触发。

- [ ] **Step 1: 写失败测试**

在 `route_say_repetition_test.go` 增加：

```go
func TestRepeatedReportIgnoresTriggerKind(t *testing.T) {
	d := newInspectionDedupe()
	now := time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return now }
	body := "还在做迁移，暂无新进展。"
	if d.RepeatedReport("task-1", body) {
		t.Fatal("first report hidden")
	}
	now = now.Add(10 * time.Minute)
	if !d.RepeatedReport("task-1", "还在做迁移 暂无新进展") {
		t.Fatal("same body across triggers not hidden")
	}
	if d.RepeatedReport("task-2", body) {
		t.Fatal("different task hidden")
	}
	now = now.Add(21 * time.Minute)
	if d.RepeatedReport("task-1", body) {
		t.Fatal("report hidden after 30-minute window")
	}
}

func TestRepeatedProgressAcrossTriggersIsHidden(t *testing.T) {
	ctx := context.Background()
	store, activity, decision, _ := answerDecisionFixture(t,
		"task-progress-repeat", "run-progress-repeat", "decision-progress-a")
	body := "迁移脚本已经跑完前半段，后半段还在写。"
	decision.TriggerKind = RouterTriggerTurnSettled
	decision.TriggerRef = "turn-progress-repeat"
	decision.PlaceholderActivityID = ""
	decision.Actions = []RouterAction{{Seq: 0, Kind: RouterActionSay, Purpose: "progress", Body: body}}
	if err := activity.ApplyRouteActions(ctx, decision); err != nil {
		t.Fatalf("first progress: %v", err)
	}

	second := *decision
	second.ID = "decision-progress-b"
	second.TriggerKind = RouterTriggerRunSettled
	second.TriggerRef = "run-progress-repeat"
	second.Actions = []RouterAction{{Seq: 0, Kind: RouterActionSay, Purpose: "progress", Body: body}}
	if err := NewRouterStore(store.db).InsertDecision(ctx, &second); err != nil {
		t.Fatalf("InsertDecision: %v", err)
	}
	if err := activity.ApplyRouteActions(ctx, &second); err != nil {
		t.Fatalf("second progress: %v", err)
	}

	replies := routeRepliesWithBody(t, activity, "task-progress-repeat", body)
	if len(replies) != 2 {
		t.Fatalf("replies = %d, want 2", len(replies))
	}
	if replies[0].Hidden {
		t.Fatal("first progress should stay visible")
	}
	if !replies[1].Hidden {
		t.Fatal("second progress should be hidden as a recent duplicate")
	}
}
```

测试用同一个 `answerDecisionFixture` 的 `store` / `activity`，第二条决策在同一库内插入并应用；不要为第二个回合另起 fixture（那时第一条回复在另一个临时库里，`RepeatedReport` 看不到）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./server/internal/kanban -run 'TestRepeatedReportIgnoresTriggerKind|TestRepeatedProgressAcrossTriggersIsHidden' -count=1`
Expected: FAIL，`RepeatedReport` 未定义。

- [ ] **Step 3: 实现跨触发去重**

`inspection_dedupe.go`：
- `InspectionDedupe` 增加 `reports map[string]inspectionDedupeEntry`，构造函数初始化。
- 新增 `RepeatedReport`：key 只用 `taskID`，指纹用 `sha256(normalizeInspectionText(body))`；窗口仍为 `inspectionDedupeWindow`；条目清理同时清空 `at` 与 `reports`。
- 更新结构体注释：巡检去重 + 跨触发进度去重。

`activity_route_say.go` 在 `hiddenReason` 计算处，按以下顺序调整：
1. summary 提升与 S4 降级不变。
2. 转交抑制提前到去重之前：
   ```go
   if routeSaySuppressedByHandoff(decision, action) {
       return nil
   }
   ```
   其中 `routeSaySuppressedByHandoff` = `action.Purpose == "progress" && decisionHasHandoff(decision)`。
3. 保留原有巡检去重。
4. 新增：
   ```go
   if hiddenReason == "" && action.Purpose == "progress" &&
       s.inspectionDedupeFn().RepeatedReport(task.ID, body) {
       hiddenReason = "duplicate_recent_report"
   }
   ```
5. `redundant_progress` 与 summary 记录不变。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./server/internal/kanban -run 'TestRepeatedReportIgnoresTriggerKind|TestRepeatedProgressAcrossTriggersIsHidden|TestInspectionDuplicateSayIsHidden' -count=1`
Expected: PASS。

- [ ] **Step 5: 更新提示词规则 9 / 12**

`router_prompt.md`：
- 规则 9 替换为：

```markdown
9. 默认中文。每条消息只写用户此刻需要知道的：结果 / 影响 / 风险 / 下一步；文件名、命令、路径、哈希、PID、inode 只在用户据此判断时出现，其余留在任务详情。只有确实有多个独立输出时才多条 router_say（最多 3 条）。
```

- 规则 12 末尾补一句：`同一段结论不要换一种说法重念；系统会把 30 分钟内的重复 progress 收进隐藏通道，但那是兜底，不是表达渠道。`

- [ ] **Step 6: 跑测试并提交**

Run: `go test ./server/internal/kanban -run 'TestRepeatedReport|TestRepeatedProgress|TestInspectionDuplicate|TestInspectionDedupe|TestRouterPrompt' -count=1`
Expected: PASS。

更新 `docs/upstream-customizations.yaml` 的 G-XX 组。提交：

```bash
git add server/internal/kanban/inspection_dedupe.go \
  server/internal/kanban/activity_route_say.go \
  server/internal/kanban/router_prompt.md \
  server/internal/kanban/route_say_repetition_test.go \
  docs/upstream-customizations.yaml
git commit -m "feat(router): 跨轮汇报去重与细节分层（Scope: G-XX）"
```

提交后跑 `make check-upstream`，必须绿。

---

## 执行后验证

按顺序跑：

```bash
go test ./server/internal/kanban -count=1
go test ./server/internal/api -count=1
make test
make check-upstream
```

最后做一次真实事件的只读核对（不改生产）：

```bash
# 看当前任务的事件与活动，确认 turn_heartbeat 的实例键只在锚点变化时新增
python3 - <<'PY'
import sqlite3, os
p = os.path.expanduser('~/.local/share/mindfs/kanban.db')
con = sqlite3.connect(f'file:{p}?mode=ro', uri=True)
for row in con.execute("SELECT kind, count(*), max(created_at) FROM router_events GROUP BY kind ORDER BY kind"):
    print(row)
PY
```

预期：
- `turn_heartbeat` 每个任务 / 锚点最多一条；30 分钟内没有同锚点重复。
- 没有 `paused` / 终态任务收到 `router_message_agent` / `router_run_workflow` 的成功应用记录。
- 收口回合的 `purpose=summary` 仍能覆盖卡片；progress-only 的 `turn_settled` 不再写卡片摘要。

## 不在本期范围

- 任务契约 / Run 快照 / 三类证据提供者 / 验收报告（计划 3）。
- 状态机表、启动对账、幂等重派、崩溃恢复重试预算（计划 4）。
- 记忆、决策记录、用户偏好（计划 5）。
- Router 文件 / shell 工具与 B2 硬门（计划 2）。
- 前端契约 / 决策记录 / 记忆视图。
- 直连兜底摘要 `recordTurnResultSummary` 的存废与压缩策略。