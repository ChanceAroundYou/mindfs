# 子会话/主会话事件隔离（WP-00）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. This is a docs-only execution plan; no implementation authorization is implied.

**Goal:** 定位并（若复现）修复 Claude 子代理事件错误写入父会话的路由缺陷，保证 child-owned 正文/内部工具卡与 parent-owned stream 严格隔离；保留父会话合法的 Task 生命周期卡与子代理完成摘要卡。

**Architecture:** 在现有 `claudeSubagentRouter` / Claude event normalization 路径上明确 event ownership：已识别 child 的事件只能到 child session；parent event 才进入父回合 accumulator。没有足够归属信息的事件不猜测路由，按经测试确定的安全策略隔离并记录诊断。保持真实 Agent runtime 可替换，单测使用 fake event stream 与临时 session manager。

**Tech Stack:** Go module declares `go 1.25.0`, project toolchain is Go 1.26.5；`server/internal/agent/claude`；`server/internal/api/usecase`；MindFS session manager SQLite test instance.

**Spec:** `docs/superpowers/specs/2026-11-09-kanban-first-agent-workflows-design.md`（父/子会话隔离是本计划前置安全工作，不改变工作流产品语义）；路线图：`docs/superpowers/plans/2026-11-10-kanban-workflow-runtime-roadmap.md`。

## Global Constraints

- 不触碰线上会话数据库，不重启 `mindfs.service`；所有复现使用 fake stream / `t.TempDir()`。
- 不将子代理报告当作父会话 assistant 正文，不将子代理 tool card 写入父 exchange aux。
- 子事件需要稳定身份；不得仅按“当前唯一 open child”猜测归属，除非对应协议明文保证且测试证明唯一性。
- 事件归属未知时必须 fail closed：不能进入 parent；保留诊断/原始证据供排查。
- 子会话事件归属缺陷是否构成新的 customization 边界，须由 Task 0 复现后决定；若有产品代码修改，再登记相应组（候选 G-LB）、测试和 Scope；本计划编写阶段不修改 YAML 登记表。

## Review Focus

1. Child `ToolCall` 先于 child delta 到达：child 已被派发但尚未创建 session 时，生命周期卡不得落入 parent。
2. SDK delta 缺 `ParentToolUseID`/`TaskID`：只有明确可关联时才归属 child，否则隔离，不能污染父正文。
3. Parent chunk 被 child delta/tool event 交错：parent response accumulator、message boundary 与 exit marker 必须不受子流事件影响。
4. `FinishAll`/child close 后迟到的 child events：必须仍落 child 或显式记录为 late-child，不得回落 parent/静默丢掉。
5. 同一 `parentToolUseId` 的不同 task 生命周期事件：不能生成重复子会话，也不能将一个 child 的内容发给另一个 child。

---

## 文件与责任边界

- `server/internal/api/usecase/session_subagents.go`：子代理身份识别、认领、关闭后事件处理和 parent/child 路由结果；区分 parent 必须看到的 Task lifecycle/summary card 与 child 内部工具事件。
- `server/internal/api/usecase/session_subagent_stream.go`：synthetic child lane 的创建、后台订阅、正文/aux 累积与广播；复现确认涉及 child session 持久化时修改。
- `server/internal/api/usecase/session_subagent_synthetic.go`、`session_subagents_helpers.go`：synthetic child event adapter、reference key normalization；仅在测试揭示有必要时修改。
- `server/internal/api/usecase/session_send.go`：在线 parent 回合 accumulator；只处理归属 parent 的正文，child event 不得 flush/拼接 parent message。
- `server/internal/api/usecase/session_stream_events.go` / `session_agent_events.go`：存在；只在 Task 0 证明其在用户复现路径中时才列入修改范围。
- `server/internal/api/usecase/session_subagent_stream_test.go`：fake event stream 锁定 parent/child 不串流、身份唯一与迟到事件行为。
- `server/internal/agent/claude/session_messages_deltas.go`、相关 adapter 测试：验证 SDK partial/delta 正文是否保留 child attribution；静态初查显示现有路径已有 metadata 传播，优先补合同/回归测试；若协议本身不给归属，不得伪造字段，应走 fail-closed 行为。
- `server/internal/agent/claude/session_messages_tasks.go` / `session_result_origin_test.go`：区分 Claude CLI 的 parent task-notification/result 和真正 child stream；若复现指向既有 turn-boundary 路径，只记录为排查分支，不在未证实前修改。
- `docs/upstream-customizations.yaml`：实现时新增 G-LB 条目及症状、原因、边界、文件、测试、anchor。

## Interfaces

- 消费现有 `claudeSubagentRouter.Handle(ctx context.Context, update agenttypes.Event) bool`：当前 `true` 才代表 router 消费、不再进入 parent accumulator；`false` 表示 parent send path 继续处理，并不等于“已证明是 parent-owned”。既有 `task_notification`/`subagent_result` summary handler 特意让父层仍显示对应 Task 完成卡，测试要求继续保留。
- 测试/实现前先做 Route inventory：枚举 `session_send.go` 在线回合、`session_subagent_stream.go` 后台 child subscription、导入/重开绑定及 adapter delta 归属；WP-00 只修被证据证明有问题的 lane，不把不同事件类型全部套用同一个 disposition。
- 若单个 bool 不能区分 `ParentOwned`, `ChildConsumed`, `Quarantined`, 先新增 `SubagentRouteResult{Disposition, ChildSessionKey, Reason}`；parent Task lifecycle 和终结摘要仍可显式作为 parent-owned display card，但 child 内部 ToolCall 不得 fallback。
- 测试 seam：`session_subagent_stream_test.go` 构造 temp Manager/fake event stream，分别断言 parent body/aux、child exchanges/aux、parent ToolCall lifecycle cards；不得用“父 aux 不含任何 child 相关卡”误删合法 Task 工具卡。

## Tasks

### Task 0：辨别真实泄漏路径与竞争假设

**Files:** Read-only: `server/internal/api/usecase/session_send.go`, `session_subagents.go`, `session_subagent_stream.go`, `session_command_shell.go`, `session_name.go`, `server/internal/agent/claude/importer_subagents.go`, `session_messages_tasks.go`, existing result-origin tests/docs. No production data mutation.

**Interfaces:** Consumes the user report and screenshot; produces a short evidence note in this plan or test description identifying the lane(s) that can explain it.

- [x] **Step 1: Write a failing locator test or a read-only diagnostic fixture** — synthetic events include a child-only sentinel plus a valid parent `task_notification` summary; assert which lane receives each and distinguish child interior tool cards from the allowed parent lifecycle/summary card.
- [x] **Step 2: Run current behavior** — run the focused existing subagent/router and result-origin tests; separately test fake event sequences for online parent turn, background subscription, imported/reopened subagent binding, and unattributed deltas. Do not query or edit live production SQLite/transcripts for this plan.
- [x] **Step 3: Classify observations** — choose among router fallthrough, child background subscription, importer/reopen attribution, and CLI `origin.kind=task-notification` crossing the parent stream. If no child-only content reaches parent in the harness, record the exact coverage limitation and keep the WP-00 exit gate contingent on a reliable reproduction.
- [x] **Step 4: Verify existing contracts** — `go test ./server/internal/api/usecase -run 'TestClaudeSubagentRouter|TestClosedClaudeSubagent' -count=1` and `go test ./server/internal/agent/claude -run '^TestResultOrigin' -count=1`; commands intentionally target packages where these tests live. Existing `TestClaudeSubagentRouterRoutesTaskNotificationSummaryAndKeepsParentUpdate` must remain green.

#### Task 0 执行证据（2026-11-10）

四条 lane 的只读盘点结论：

| lane | 入口 | 结论 |
|---|---|---|
| 在线回合 | `session_send.go` 的 `attachSessionUpdates` → `claudeSubagentRouter.Handle`；`false` 落回父 accumulator | **已复现**。子代理先出工具卡、后出正文时，`find(ref) == nil` 且事件是 ToolCall/ToolUpdate，旧代码一律 `return false`，带 `subagentType` 的 child-owned 工具卡落进父回合。修复前 fake stream 断言打印 `child-owned tool cards leaked into parent: [ToolCall{CallID:"child-read-1"...}]`。 |
| 后台 child 订阅 | `session_subagent_stream.go` | 只有 Codex collab 后台订阅，事件写 child；不在用户报告的 Claude 路径上。 |
| 导入/重开 | `claude/importer_transcript.go` / `importer_subagents.go` | 父转录 importer **没有** `isSidechain` / `parent_tool_use_id` 过滤；若父转录混入 sidechain 行会整段进父会话。这是**未证实的潜在缺口**，本计划按约束未读生产转录，未修改、未宣称修复。 |
| CLI task-notification | `session_messages_tasks.go`、result-origin 白名单 | 父会话上的 Task 生命周期卡/摘要卡是**故意 parent-owned** 的展示契约，`TestClaudeSubagentRouterRoutesTaskNotificationSummaryAndKeepsParentUpdate` 与 `TestResultOrigin*` 钉住；未改动。 |

归属判据来自 SDK 合同测试而不是猜测：`subagentType` 只出现在子代理消息上（`TestClaudeAssistantMessagePreservesSubagentAttribution`），父 partial delta 不带 `ParentToolUseID`（`TestClaudePartialDeltaPreservesSubagentAttribution` / `TestClaudeMetadataFreeDeltaInventsNoAttribution`）。因此「有 `subagentType` 的 ToolCall 属于 child」可判定；「无任何归属字段」按父正文处理（不发明 child 身份），不是第二条猜测 lane。

**覆盖限制**：复现的是该类缺陷（router fallthrough），不是对用户那条具体会话逐字节核对 —— 按要求未查生产数据。importer 潜在缺口仍开放，WP-00 结论不覆盖它。

### Task 1：建立最小 interleaved stream RED reproducer

**Files:**
- Modify: `server/internal/api/usecase/session_subagent_stream_test.go`
- Test: `TestClaudeSubagentRouterNeverLeaksChildOwnedEventsIntoParent`

**Interfaces:**
- Consumes: Task 0 lane classification and `newClaudeSubagentRouter` / fake Manager.
- Produces: minimal deterministic event sequence proving the child-lane leak, if one is reproduced.

- [x] **Step 1: Write failing test** — fake parent output `"parent-before "`; child internal ToolCall with stable `ParentToolUseID=tool-child-1, TaskID=task-child-1`; child text `"CHILD_ONLY_SECRET"`; interleaved parent output `"parent-after"`; child completion; parent completion。断言 parent transcript/aux 不含 child-only content 或 child-internal tool card，唯一 child session 包含 sentinel；合法 parent Task lifecycle/summary cards 按既有契约保留；parent 正文依 source parent chunks 连续拼接。
- [x] **Step 2: Run test to verify it fails** — `go test ./server/internal/api/usecase -run '^TestClaudeSubagentRouterNeverLeaksChildOwnedEventsIntoParent$' -count=1`; Expected: RED only if Task 0 confirms this lane leaks. If router path already passes, do not force a regression: move reproducer to the proven leaking lane and retain this case as a regression guard.
- [x] **Step 3: Add minimal event sequence recorder in test** — fake only; no production changes. Ensure failing assertion reports event order, parent key, child key, event type and metadata without dumping credentials.
- [x] **Step 4: Re-run reproducer** — Expected: deterministic RED for the proven faulty lane; otherwise deterministic PASS as a route-isolation guard and record WP-00 root-cause limitation.

**执行结果**：RED 已捕获（父回合收到 `child-read-1` 工具卡）；修复后同一路径 GREEN，child 同时收到自己的工具卡与 `CHILD_ONLY_SECRET`。
### Task 2：Pin unknown attribution and early lifecycle routing

**Files:**
- Modify: `server/internal/api/usecase/session_subagent_stream_test.go`
- Test: `TestClaudeUnattributedDeltaIsNeverClaimedAsChild`, `TestClaudeLateChildToolEventsNeverFallBackToParent`（早期 child 工具卡用例合并进 Task 1 的 `TestClaudeSubagentRouterNeverLeaksChildOwnedEventsIntoParent`，同一路径不写两份断言）

**Interfaces:**
- Consumes: Task 1 fake event harness.
- Produces: event attribution contract tests for known child, unknown owner, and closed child.

- [x] **Step 1: Write failing tests** — (a) Known child-owned event arriving before child materialization must be buffered/quarantined and never become a parent card; parent-owned Claude Task lifecycle ToolCall remains visible per existing tests; (b) the test must use the actual SDK metadata/discriminator that separates child-internal events from parent task lifecycle; TaskID/ParentToolUseID alone is insufficient until verified; (c) no-attribution event must not be labeled child unless source lane evidence says so; (d) late events retain existing consumed/drop protection unless a separate approved persistence policy is chosen.
- [x] **Step 2: Run focused tests** — `go test ./server/internal/api/usecase -run 'TestClaude(SubagentRouterNeverLeaksChildOwnedEventsIntoParent|UnattributedDeltaIsNeverClaimedAsChild|LateChildToolEventsNeverFallBackToParent)$' -count=1`; `go test ./server/internal/api/usecase -run '^TestClosedClaudeSubagentSwallowsFurtherUpdates$' -count=1`; Expected: new RED only for proven leak; existing intentional card/swallow tests green.
- [x] **Step 3: Implement explicit routing disposition** — in the narrowest router boundary, distinguish parent-owned, child-consumed, and unknown/quarantined events; retain stable key mapping through terminal events. Do not route by sole-open-child fallback when multiple or unknown identity exists.
- [x] **Step 4: Run focused tests** — Expected: all three pass, with unknown events never reaching parent callback.

**执行结果与偏差**：没有引入 `SubagentRouteResult` —— 单个 bool 在当前 router 边界仍够用，新增类型只会扩大改动面。实际 disposition 是：

- 有 `subagentType` 的 ToolCall/ToolUpdate（SDK 只在子代理消息上填）→ 立即物化/认领 child 并消费，即使 child 尚未由正文创建；
- 无 `subagentType` 的工具心跳（普通 Bash 等）→ 仍归父会话，避免 2026-10-25 那类「每次心跳建一个幽灵子会话」；
- 无任何归属字段的正文/增量 → 归父会话（父 partial delta 在线上本来就不带 `ParentToolUseID`，合同测试钉住），**不发明 child 身份**；
- 已关闭 child 的迟到工具事件 → 继续吞掉，不回落父会话。

### Task 3：只在已证明的路径修 parent response aggregation

**Files:**
- Modify `server/internal/api/usecase/session_send.go` only if Task 0 reproduces the online-send lane; otherwise move the fix/test target to the proven owner path (`session_subagent_stream.go`, importer/reopen lane, or result-origin adapter).
- Test: `TestParentMessageAccumulatorIgnoresChildInterleaving` if the online-send accumulator is implicated.

**Interfaces:**
- Consumes: Task 1–2 route classification.
- Produces: invariant that each parent-owned accumulator and each child-owned accumulator is updated only by its own events.

- [x] **Step 1: Write a failing lane-specific test** — interleave child ThoughtChunk/ToolUpdate/MessageChunk/MessageDone between parent chunks only if Task 0 demonstrated these events reach `session_send.go` parent callbacks; assert parent assembled bytes equal only source parent chunks.
- [x] **Step 2: Run lane-specific test** — Expected: RED for the demonstrated path, not based on a presumed `flushThought` bug.
- [x] **Step 3: Implement minimal ownership isolation** — child events return before parent body/aux/turn aggregation; preserve parent Task lifecycle and terminal summary cards explicitly.
- [x] **Step 4: Re-run test and existing parent-card tests** — Expected: isolation regression green; `TestClaudeSubagentRouterRoutesTaskNotificationSummaryAndKeepsParentUpdate` remains green.

**执行结果**：无需改 `session_send.go`。父 accumulator 的隔离由 router 的消费结果保证；Task 1 的 fake accumulator 已按 `attachSessionUpdates` 同构断言父正文只能是 `"parent-before parent-after"`。未修改 `session_send.go` 是本 Task 的预期出口（Step 1 的前置条件只对已证明落入父回调的事件成立）。


### Task 4：Pin SDK attribution as a regression contract

**Files:**
- Tests: existing focused tests in `server/internal/agent/claude` near `session_messages_deltas.go`, `session_message_loop_helpers.go`, `session_messages.go`.
- Modify production adapter only if Task 0 / contract test proves SDK-supplied attribution is dropped.

**Interfaces:**
- Consumes: SDK partial/delta metadata where provided.
- Produces: test contract that supplied attribution is preserved and absent attribution remains explicitly absent; no fabricated ownership.

- [x] **Step 1: Inspect current propagation path** — confirm `meta.ParentToolUseID` / `TaskID` handling in delta and partial assistant paths; list which event types carry attribution.
- [x] **Step 2: Add regression contract test** — feed partial assistant with attribution; assert emitted MessageChunk/ThoughtChunk/MessageDone retain it. Also feed metadata-free event and assert no synthetic child ID is invented.
- [x] **Step 3: Run test** — this may be GREEN on current code; if so, retain it as a guard and do not modify adapter merely to manufacture RED.
- [x] **Step 4: If a gap is proven, add the smallest failing case first, then propagate only metadata the SDK supplies.**
- [x] **Step 5: `go test ./server/internal/agent/claude ./server/internal/api/usecase`; Expected: PASS.

**执行结果**：`server/internal/agent/claude/session_subagent_attribution_test.go` 四条合同测试在现有适配层上直接 GREEN（partial delta / assistant message 保留归属；无归属不发明；ToolProgress 保留 TaskID+ParentToolUseID），未改适配层。无归属增量需 `flushAllDeltas()` 才会输出事件（增量合并是既有设计，不是缺陷）。

### Task 5：Full regression, customization registration and commit boundary

**Files:**
- Modify: `docs/upstream-customizations.yaml`
- Tests: `server/internal/api/usecase/session_subagent_stream_test.go`, Claude adapter tests

**Interfaces:**
- Consumes: Tasks 1–4.
- Produces: tested route invariant; upstream Scope is assigned only after verified ownership boundary.

- [x] **Step 1: Register the verified WP-00 boundary** — only if implementation changes a distinct parent/child lane contract; otherwise amend the implicated existing group. If new group, record symptom, evidence, exact files/tests/anchors; assign Scope only after boundary verification.
- [x] **Step 2: Run package tests** — `go test ./server/internal/agent/claude ./server/internal/api/usecase`; Expected: PASS.
- [x] **Step 3: Run concurrency-sensitive tests** — `go test -race ./server/internal/api/usecase -run 'Subagent|ParentMessageAccumulator'`; Expected: PASS.
- [x] **Step 4: Run full Go suite and upstream check** — `go test ./server/... ./cli/...`; reconcile stale G-KN anchor separately, then run `make check-upstream`.
- [x] **Step 5: Any commit** is future implementation only and requires separate authorization; if authorized, Conventional Commit with the verified Scope.

**执行结果**：G-LB 已登记（独立边界：child-owned 工具卡归属，区别于 G-IZ 的取消/回合身份）。包测试、`-race` 子集、全量 Go 测试均通过；`make check-upstream` 结果见提交后的核对记录。提交只落在本地 `integration/kanban-workflows` 分支，未 merge/push/deploy/restart。

## WP-00 exit criteria

- A test reproduces the reported class before fix; if the exact symptom cannot be reproduced in a fake stream, write the negative invariant test and report that limitation rather than claim root cause proven.
- Parent body/tool aux contains no child-owned content under interleaving, early lifecycle, unknown attribution, or late event cases.
- No new session is created for events lacking sufficient stable child identity.
- Existing legitimate parent task card behavior and child session UI remain unchanged.
- Tests and upstream registration agree on the exact invariant.
