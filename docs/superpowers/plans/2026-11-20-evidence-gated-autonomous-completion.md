# 证据门 + 自动补做 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 Router 在证据成立时自己把任务判成完成，证据不成立时自动补做，补不动就交给人；同时修掉「配额撞墙空烧重试」与「信封格式坏了直接甩给人」两个现存缺陷，并把崩溃注入与变异验证做成可重放的工程工具。

**Architecture:** 在 kanban 包内新增「完成契约 → 服务端求值 → `complete_task` 动作 → 应用时重新求值 → 写系统行 + 走既有 `finishWorkflowRunByOperator`」这条链路；证据不成立时落 `completion_blocked` 事件，由 coordinate 再给一个回合做节点形状的补做，预算独立。复核（无契约时的模型判定）是异步任务，不占 Router 回合。配额挂起与信封修复是独立的、可先上线的小修。

**Tech Stack:** Go 1.26（现代码风格：手写 SQL、`database/sql`、无 ORM）、SQLite WAL、React/TS 前端、Vitest、bash 部署脚本。

**Spec:** `docs/superpowers/specs/2026-11-20-evidence-gated-autonomous-completion-design.md`

## Global Constraints

- 单文件硬线 **500 行**（含测试）；超线按职责拆分。
- Go 工具链在 `$HOME/.local/share/go`，裸跑 `go` / `make test`，**永不** `export PATH=...`。
- 提交信息 Conventional Commits（中文）+ `Scope: G-NP`；fork 定制必须登记
  `docs/upstream-customizations.yaml`，干净 worktree 上跑 `make check-upstream`。
- **只认 `git commit -- <pathspec>`**；禁止裸 `git stash` / `git checkout` 别人的改动。
- 本机 `sudo systemctl restart mindfs` **仅用户执行**；WSL/fn 可直接 `systemctl --user restart`；
  pc-win 认 PID + `/mindfs/health`。分发只走 `bash scripts/deploy-all.sh`，不推 `web/dist`。
- `router_events.payload_json` 整体（键与值）不得含 `output` / `session` / `git` / `transcript`
  子串；`completion_blocked` 允许空 RunID（照 `direct_stalled` 先例）。
- 生产配置保持 `router.model=ok`、`router.mode=coordinate`、`turn_timeout=180s`；
  新增配置键只走 `config.json`，缺省即安全值。
- 生产排查只读（SQLite `file:...?mode=ro`），不碰 7331。
- 每个任务先写失败测试（RED），跑出预期失败，再实现（GREEN），再做变异验证（至少一处）并还原。

## Review Focus

1. **没有契约、也没有可用 Claude agent 的任务**：复核不可用时必须 fail-closed 停 `waiting_user` 并写明原因，绝不能「大概没问题」自动完成。
2. **事件回合里无法补做的缺口**（缺文件 / gate 未批 / 还有 open 节点）：必须 `escalate` + `notify_user` 停人，不能空转重试把预算烧光。
3. **配额挂起的节点遇上用户取消 / 重开 / 删卡**：`not_before` 不得让已取消的 Run 复活，也不得让删掉的任务留下可被唤醒的行。
4. **复核会话串味**：必须独立 session key、只读工具、cwd 限定在任务工作区，不能推进任务主会话，不能写文件。
5. **求值对象是「决策发生时的 Run」**：事件回合只认 `brief.Event.RunID`；用户回合取当轮 Run；绝不回落到「最新 Run」——否则新 Run 的证据会替旧决策背书。

## 文件结构

| 文件 | 责任 |
|---|---|
| `server/internal/kanban/workflow_node_holds.go`（新） | 配额挂起：`not_before` 读写、是否到期、按 Run 查到期行 |
| `server/internal/kanban/workflow_completion.go`（新） | 契约类型 + `EvaluateCompletion` 纯读求值器 |
| `server/internal/kanban/workflow_completion_rules.go`（新） | 四种 kind 各自的判据与路径安全 |
| `server/internal/kanban/service_completion.go`（新） | `complete_task` 的应用路径：commit / block / 编号缺口 |
| `server/internal/kanban/service_completion_verifier.go`（新） | 异步复核回合与结论回写 |
| `server/internal/kanban/completion_store.go`（新） | `task_completion` 表读写 |
| `server/internal/kanban/router_tools_complete.go`（新） | `router_complete_task` 工具规格 |
| `server/internal/kanban/workflow_node_repair.go`（新） | 信封修复轮 |
| `server/internal/kanban/intake_policy.go`（新） | 入站校验形状 + 默认审核分类 |
| `server/internal/agent/conformance/`（新目录） | 跨 harness 探针 |
| `scripts/mutate.sh`、`scripts/crash-sim.sh`（新） | 工程工具 |
| 既有文件改动 | 见各任务 Files 段 |

---

### Task 1: 配额挂起到重置（A1）

**Files:**
- Modify: `server/internal/kanban/workflow_types.go`（`NodeRun` 加 `NotBefore string`）
- Modify: `server/internal/kanban/task_store_workflow_runs.go`（列迁移表加
  `{"workflow_node_runs", "not_before", "TEXT NOT NULL DEFAULT ''"}`）
- Modify: `server/internal/kanban/task_store_workflow_node_runs.go`（所有 `NodeRun` 的
  scan / insert / upsert 补 `not_before`）
- Modify: `server/internal/kanban/workflow_driver.go`（`advanceWorkflowNode` 的 agent 分支：
  行仍 `queued` 且 `NotBefore` 在未来 → 返回 `false, nil`，不 claim）
- Modify: `server/internal/kanban/router_supervision.go`（新增
  `SupervisionFailureQuota SupervisionFailureKind = "quota"`；分类时把 `rate limit` / `429` /
  `usage limit` / `overloaded` 判成 quota 而不是 retryable）
- Modify: `server/internal/kanban/router_supervision_actions.go`（quota 类允许 `retry_node` 且
  带 `not_before`，不消耗 `RetriesUsed`）
- Modify: `server/internal/kanban/router_types.go`（`RouterSupervisionConfig` 加
  `QuotaHold time.Duration`，`Normalized()` 缺省 15m 夹 [1m,6h]）
- Modify: `server/internal/kanban/router_coordinate.go`（每拍末尾：到期行改回 `queued` 并
  `WakeWorkflowRun`）
- Test: `server/internal/kanban/workflow_node_holds_test.go`（新）

**Interfaces:**
- Produces:
  - `func nodeRunHoldUntil(nr NodeRun) (time.Time, bool)` — 空 / 非法时间返回 false
  - `func (s *Service) releaseDueNodeHolds(ctx context.Context, store *TaskStore, now time.Time) ([]string, error)` — 返回被唤醒的 RunID
  - `func quotaHoldUntil(summary string, now time.Time, hold time.Duration) time.Time` — 解析
    `resets at` / `reset at` / `重置于` + RFC3339 或 `15:04`；结果必须落在
    `now+1m … now+7d`，否则 `now+hold`
  - `NodeRun.NotBefore string`（RFC3339 UTC，空 = 立即可跑）

- [ ] **Step 1: 写失败测试** `workflow_node_holds_test.go`
  - `TestQuotaHoldUntilParsesResetAndClamps`：`"usage limit reached, resets at 2026-11-20T18:30:00Z"` →
    该时刻；`"resets at 2030-01-01T00:00:00Z"`（超 7 天）→ `now+15m`；无时间 → `now+15m`。
  - `TestAdvanceSkipsHeldNode`：构造 Run + 一条 `queued` 且 `NotBefore=now+10m` 的 NodeRun，
    `advanceWorkflowNode` 返回 `advanced=false, err=nil`，且 NodeRun 仍 `queued`、未被绑定会话。
  - `TestReleaseDueNodeHoldsWakesRun`：`NotBefore=now-1s` → 行改回 `queued` 且返回该 RunID；
    `NotBefore=now+1m` → 不返回。取消的 Run（`StatusCancelled`）即使到期也不唤醒。
- [ ] **Step 2: 跑测试确认失败** — `go test ./server/internal/kanban/ -run 'QuotaHold|HeldNode|DueNodeHolds' -count=1`，期望 `undefined: quotaHoldUntil` 等编译失败。
- [ ] **Step 3: 实现**：先加列与字段（迁移用既有 `ensureColumn` 风格循环），再实现三个函数；
  driver 的 `advanced=false` 分支保持现有 `projectWorkflowTaskStatus` 语义不变。
- [ ] **Step 4: 跑测试确认通过** + `go test ./server/internal/kanban/ -run TestSupervision -count=1` 不回归。
- [ ] **Step 5: 变异验证**：把 `advanceWorkflowNode` 的 hold 判断去掉 → `TestAdvanceSkipsHeldNode` 必须红；还原。
- [ ] **Step 6: 提交** `feat(kanban): 撞用量上限的节点挂起到重置（Scope: G-NP）`。

---

### Task 2: 节点信封修复轮（A2）

**Files:**
- Create: `server/internal/kanban/workflow_node_repair.go`
- Modify: `server/internal/kanban/workflow_node_executor.go`（信封解析失败分支调用修复）
- Test: `server/internal/kanban/workflow_node_repair_test.go`（新）

**Interfaces:**
- Produces:
  - `const nodeEnvelopeRepairAttempts = 2`、`nodeEnvelopeRepairTimeout = 120 * time.Second`
  - `func (s *Service) repairNodeEnvelope(ctx context.Context, store *TaskStore, run *WorkflowRun, node WorkflowNode, nr NodeRun, bad string) (WorkflowNodeResult, bool, error)`
    —— 返回 `(result, repaired, err)`；`repaired=false` 表示两次都没修好，调用方按现状落 `unknown`
  - `func envelopeConsistentWithFacts(res WorkflowNodeResult, node WorkflowNode, nr NodeRun, toolWrites int) bool`
- 修复轮：同一会话、`ToolPolicy{AllowedTools: nil}`、提示词只说「只把最后一条消息重写成一个信封，
  不要重做工作、不要调用工具」。

- [ ] **Step 1: 写失败测试**
  - `TestRepairEnvelopeRecoversMalformedReply`：假 runner 第一次返回纯文本、第二次修复轮返回合法信封 → `repaired=true`、outcome 正确、attempt 不变（NodeRun 行数仍为 1）。
  - `TestRepairEnvelopeGivesUpAfterTwo`：修复轮连续两次仍非信封 → `repaired=false`，调用方落 `unknown`。
  - `TestRepairEnvelopeRejectsDoneWithoutWrites`：`writes_workspace=true`、`toolWrites=0`、修复轮回 `done` → 不接受，降为 `blocked` 且 `reason` 含「没有观察到写操作」。
- [ ] **Step 2: 跑测试确认失败**（编译失败即可）。
- [ ] **Step 3: 实现** 修复轮；`workflow_node_executor.go` 在 `parseWorkflowNodeOutput` 返回错误、
  且该 attempt 尚未写过终态时触发。
- [ ] **Step 4: 跑测试确认通过** + `go test ./server/internal/kanban/ -run 'WorkflowNode' -count=1`。
- [ ] **Step 5: 变异验证**：把 `toolWrites` 核对去掉 → `TestRepairEnvelopeRejectsDoneWithoutWrites` 必须红；还原。
- [ ] **Step 6: 提交** `feat(kanban): 信封解析失败先做格式修复轮（Scope: G-NP）`。

---

### Task 3: 完成契约类型 + 求值器 + 冻结守卫

**Files:**
- Modify: `server/internal/kanban/workflow_types.go`（`WorkflowDefinition` 加 `Completion *CompletionContract`）
- Create: `server/internal/kanban/workflow_completion.go`、`workflow_completion_rules.go`
- Modify: `server/internal/kanban/task_store_workflow_runs.go`（`EditWorkflowRunGraph` 与
  `SaveWorkflowRun` 的完成契约不可变守卫）
- Test: `server/internal/kanban/workflow_completion_test.go`（新）

**Interfaces:**
- Produces（与 spec §6.1/§6.2 逐字一致）：
  - `type CompletionContract struct { Requirements []CompletionRequirement; VerifyWhenUnverifiable *bool; MaxRepairs int }`
  - `type CompletionRequirement struct { ID, Label, Kind, NodeID string; Keys []string; Path, Decision string }`
  - `const CompletionKindNodeDone = "node_done"` 等四个常量
  - `type CompletionItemStatus struct { ID, Label, Kind string; Pass bool; Detail string }`
  - `type CompletionReport struct { RunID string; Contract bool; Items []CompletionItemStatus; Passed bool; Hash string }`
  - `func (s *Service) EvaluateCompletion(ctx context.Context, store *TaskStore, task Task, run *WorkflowRun) (CompletionReport, error)`
  - `func completionContractHash(c *CompletionContract) string`（规范化 JSON 的 sha256）
  - `func completionMaxRepairs(c *CompletionContract) int`（缺省 2，夹 [0,3]）
- 直连任务：`func evaluateDirectCompletion(ctx, store *TaskStore, task Task) (CompletionReport, error)`，
  三项 `reply_settled` / `no_live_reply` / `no_open_run`，`Contract=false`。

- [ ] **Step 1: 写失败测试**
  - `TestEvaluateCompletionNodeDone`：最新 attempt success + `output.outcome=done` + keys 齐全 → pass；keys 缺一个 → fail 且 Detail 写明缺哪个键。
  - `TestEvaluateCompletionOnlyLatestAttempt`：attempt 1 fail、attempt 2 success → pass。
  - `TestEvaluateCompletionNoContractFailsClosed`：无契约 → `Contract=false`、`Passed=false`、`Items[0].ID=="contract"`。
  - `TestEvaluateCompletionFileExistsGuards`：`../etc/passwd`、`/etc/passwd`、符号链接指向根外 → 全部 fail，且不 panic。
  - `TestEvaluateDirectCompletion`：三条各自的正反例（有 pending 回复 / 有 open Run）。
  - `TestCompletionContractImmutable`：`EditWorkflowRunGraph` 改 `completion` → 返回错误，原文含「不能在中途修改」；改别的字段仍成功。
- [ ] **Step 2: 跑测试确认失败**。
- [ ] **Step 3: 实现**；`file_exists` 的工作区解析顺序：`NodeID` 最新 attempt 的 `WorktreePath` → 任务 worktree → 项目根；只 `Lstat`。
- [ ] **Step 4: 跑测试确认通过** + `go test ./server/internal/kanban/ -run 'Completion' -count=1`。
- [ ] **Step 5: 变异验证**：把「只取最新 attempt」改成「任一 attempt」→ `TestEvaluateCompletionOnlyLatestAttempt` 必须红；还原。
- [ ] **Step 6: 提交** `feat(kanban): 完成契约与证据求值器（Scope: G-NP）`。

---

### Task 4: `complete_task` 动作、工具与校验

**Files:**
- Modify: `server/internal/kanban/router_types.go`（加 `RouterActionComplete`，删 `RouterActionSuggestFinish`）
- Create: `server/internal/kanban/router_tools_complete.go`
- Modify: `server/internal/kanban/router_tools.go`（`tools()` 注册；`validateRouterActions` 分支；删 suggest_finish 分支）
- Modify: `server/internal/kanban/router_coordinate_turn.go`（`validateRuntimeEventActions` 白名单加 `complete_task`，不要求 supervision 段）
- Test: `server/internal/kanban/router_tools_complete_test.go`（新）

**Interfaces:**
- Produces: `RouterActionComplete RouterActionKind = "complete_task"`；
  `RouterAction.Summary`（新字段，≤500 字节）；工具 `router_complete_task{summary, reason?}`。
- 校验：一轮最多 1 个；与 `run_workflow`/`message_agent`/`deliver`/`answer_waiting_node` 互斥；
  与 `say` 可共存；`brief.ActiveExecution` 有 open 节点 → 拒绝；任务终态 → 拒绝。

- [ ] **Step 1: 写失败测试**：`TestCompleteTaskToolRecords`、`TestCompleteTaskConflictsWithExecution`
  （同轮 complete + run_workflow → 整条 rejected）、`TestCompleteTaskRejectedWithOpenNodes`、
  `TestCompleteTaskAllowedInRuntimeEventTurn`、`TestSuggestFinishEnumRemoved`
  （`validateRouterActions` 收到 `suggest_finish` → `invalid action kind`）。
- [ ] **Step 2: 跑测试确认失败**。
- [ ] **Step 3: 实现**。
- [ ] **Step 4: 跑测试确认通过** + `go test ./server/internal/kanban/ -run 'Router' -count=1`。
- [ ] **Step 5: 变异验证**：去掉互斥检查 → `TestCompleteTaskConflictsWithExecution` 必须红；还原。
- [ ] **Step 6: 提交** `feat(kanban): Router 新增 complete_task 动作（Scope: G-NP）`。

---

### Task 5: `task_completion` 表 + 同步应用路径（commit / block）

**Files:**
- Create: `server/internal/kanban/completion_store.go`、`server/internal/kanban/service_completion.go`
- Modify: `server/internal/kanban/task_store.go` 或 `task_store_schema*.go` 的 `migrateSchema`（建表）
- Modify: `server/internal/kanban/activity_route_apply.go`（`RouterActionComplete` 分支）
- Test: `server/internal/kanban/service_completion_test.go`（新）

**Interfaces:**
- Produces:
  - `type TaskCompletion struct { TaskID, RunID, Status, ReportJSON, VerifierKey, VerifierVerdict, DecisionID string; RepairsUsed int; UpdatedAt time.Time }`
  - `func (s *TaskStore) UpsertTaskCompletion(ctx context.Context, c TaskCompletion) error` /
    `GetTaskCompletion(ctx, taskID) (TaskCompletion, bool, error)`
  - `func (s *Service) applyCompleteTask(ctx context.Context, store *TaskStore, task Task, decision RouterDecision, action RouterAction) error`
  - `func (s *Service) commitCompletion(ctx, store, task, run *WorkflowRun, report CompletionReport, summary, decisionID string) error`
  - `func (s *Service) blockCompletion(ctx, store, task, run *WorkflowRun, report CompletionReport, decisionID string) error`
  - 系统行 ID：`completion-evidence:<decisionID>` / `completion-blocked:<decisionID>`（`InsertOrIgnore`）

- [ ] **Step 1: 写失败测试**：`TestCompleteTaskCommitsOnPass`（任务→success、Run→success、
  `task_completion.status=completed`、系统行存在）、`TestCompleteTaskBlocksWithNumberedGaps`
  （系统行含 `1. ` 与 `2. `，任务仍 `waiting_user`）、`TestCompleteTaskIdempotent`
  （同 decisionID 应用两次不产生第二行/第二条事件）、`TestCompleteTaskNoRunUsesSetTaskStatus`。
- [ ] **Step 2: 跑测试确认失败**。
- [ ] **Step 3: 实现**；完成写入必须复用 `finishWorkflowRunByOperator`，不新造收尾路径。
- [ ] **Step 4: 跑测试确认通过**。
- [ ] **Step 5: 变异验证**：把 `blockCompletion` 里的状态写入改成不动 → 无影响（应保持不动）；
  改为写 `running` → `TestCompleteTaskBlocksWithNumberedGaps` 必须红；还原。
- [ ] **Step 6: 提交** `feat(kanban): complete_task 应用路径与完成记录（Scope: G-NP）`。

---

### Task 6: `completion_blocked` 事件 + brief 段 + 自动补做

**Files:**
- Modify: `server/internal/kanban/router_event_types.go`（新 kind + trigger 常量）
- Modify: `server/internal/kanban/router_event_store.go`（空 RunID 白名单加该 kind）
- Modify: `server/internal/kanban/router_coordinate.go`（`routerTriggerForEvent` 映射）
- Modify: `server/internal/kanban/router_brief.go` + `router_brief_cap.go`（`Completion *RouterBriefCompletion`）
- Modify: `server/internal/kanban/router_supervision.go`（`supervisionActionsLastHour` 的 IN 列表加
  `completion_blocked`；`SupervisionObservation` 增 `EvidenceFailedNodes`）
- Modify: `server/internal/kanban/router_supervision_actions.go`（允许对 `evidence_failed` 节点 `retry_node`）
- Modify: `server/internal/kanban/router_prompt.md`（补「证据缺口」段）
- Test: `server/internal/kanban/router_completion_event_test.go`（新）

**Interfaces:**
- Produces: `RouterEventCompletionBlocked RouterEventKind = "completion_blocked"`；
  `RouterTriggerCompletionBlocked`；`RouterBriefCompletion{Present, Items, RepairsUsed, RepairsMax, CanComplete, VerifierAvailable}`；
  `RouterBriefCompletionItem{Index, ID, Label, Kind, Pass, Detail}`。
- 唯一键 `completion_blocked:<runID|taskID>:<reportHash>`；payload 只放
  `{task_id, run_id?, items:[{id,kind,pass}], repairs_used, repairs_max}`。

- [x] **Step 1: 写失败测试**：`TestCompletionBlockedEventDeduped`（同 reportHash 两次只落一条）、
  `TestCompletionBlockedAllowsEmptyRunID`（直连任务可落，其它 kind 仍拒绝）、
  `TestCompletionBlockedPayloadAvoidsForbiddenSubstrings`、
  `TestCompletionBlockedTurnCanRetryEvidenceFailedNode`、
  `TestCompletionBlockedBudgetExhaustedStops`、`TestCompletionBlockedBriefCarriesGaps`
  （实现期补：brief 段的修复额度与条目）。
- [x] **Step 2: 跑测试确认失败**（未定义符号，RED）。
- [x] **Step 3: 实现**。
- [x] **Step 4: 跑测试确认通过**：定向 7/7；提交后在本提交的干净 worktree 上跑全包
  `go test ./server/internal/kanban/ -count=1` → ok 228.6s。
- [x] **Step 5: 变异验证**：计划指定的「去掉唯一键里的 reportHash」**不会**打红
  `TestCompletionBlockedEventDeduped`（同缺口去重照样成立，它只压掉「缺口变了」这一半）。
  补 `TestCompletionBlockedNewGapIsNewEvent`：在变异下先红，还原后绿。
- [x] **Step 6: 提交** `2b001410 feat(kanban): 证据缺口事件与自动补做（Scope: G-NP）`。

**执行记录（2026-11-20/21）**：`completion_blocked` 落地为缺口哈希去重的事件
（`completion_blocked:<run|task>:<hash>`，直连任务键退 taskID，RunID 为空白名单同时放行它）；
brief 的 `completion` 段只在补做回合挂（12 条上限，最终由 `capSituationBrief` 压到 8）；
补做预算按契约 `MaxRepairs` 计，超出后写「需要你介入」的确定行、放回 `waiting_user`、不产事件；
`EvidenceFailedNodes` 只收最新 attempt 停在 fail/unknown/waiting_user 的节点（重试路径不接受
success/cancelled），并对这些节点跳过每节点重试上限（预算已由契约把关）；补做回合不占每留言
3 回合预算、仍计入每小时监管动作预算；提示词补第 16 条「证据缺口回合」与 `router_complete_task`。
共享工作区里三个文件混着并发会话的未提交改动，提交用 `git apply --cached` 只暂存本组 hunk。

---

### Task 7: 异步复核回合

**Files:**
- Create: `server/internal/kanban/service_completion_verifier.go`
- Modify: `server/internal/api/router_runtime.go`（`RunRouterTurn` 支持 `SessionKeyOverride` 与只读工具集）
- Modify: `server/internal/kanban/router_service_recovery.go` 或启动对账入口（把 `verifying` 收口成 `blocked`）
- Test: `server/internal/kanban/service_completion_verifier_test.go`（新）

**Interfaces:**
- Produces:
  - `func (s *Service) startCompletionVerification(taskID, runID, decisionID string)`（异步，服务 ctx）
  - `func parseVerifierVerdict(raw string) (verdict string, findings []string, err error)` —
    严格 JSON `{verdict, findings, missing}`，非法即 `uncertain`
  - `const completionVerifierTimeout = 90 * time.Second`
- 会话 key：`verify:<taskID>:<runID>:<reportHash[:8]>`；只读工具 `Read` / `Grep` / `Glob`；
  结论按 `(run_id, report_hash)` 复用。

- [x] **Step 1: 写失败测试**：`TestVerifierPassCommits`、`TestVerifierFailBlocks`、
  `TestVerifierGarbageIsUncertain`、`TestVerifierUnavailableFailsClosed`（无 Claude agent →
  `blocked` + 系统行写明「复核不可用」）、`TestVerifierRestartReconcilesBlocked`
  （启动时 `verifying` → `blocked`）、`TestVerifierReusesVerdictByReportHash`。
  另加 `TestVerifierApplyReturnsBeforeVerdict`：阻塞式假 agent 证明 begin 已返回、行仍是
  `verifying`，放开后才收尾。
- [x] **Step 2: 跑测试确认失败**（`go vet` → `verifyCompletion undefined`）。
- [x] **Step 3: 实现**；apply 只落 `verifying` 并起 goroutine，不阻塞回合。
- [x] **Step 4: 跑测试确认通过**（`go test ./server/internal/kanban/ -run Verifier -count=1`
  → ok 4.1s）。
- [x] **Step 5: 变异验证**：把非法 JSON 的默认结论改成 `pass` → `TestVerifierGarbageIsUncertain`
  红（`garbage verdict = "pass"`）；还原后绿。
- [x] **Step 6: 提交** `feat(kanban): 异步复核回合（Scope: G-NP）`。

**执行记录（2026-11-21）**：会话 key 用 `verify-<task>-<run|direct>-<hash8>` 而不是计划里的
`verify:<task>:<run>:<hash>` —— 会话 key 会落成文件名，`:` 在 Windows 上非法（ledger 有
Ruling）。分流规则收敛进 `completionNeedsVerifier`：有契约而不成立是硬证据、直接驳回；只有
无契约（直连任务 / 未声明证据的工作流）才问复核；契约可写 `verify_when_unverifiable=false`
显式退出。复核提示不带会话转录，只带任务名、最后一条用户留言、被驳回的声明与缺口清单；
cwd 优先任务 worktree、树没了回落项目根；`pool` 层只认 Claude（ToolPolicy 只在 Claude 适配器
强制），没有可用 Claude agent 时写「复核不可用」行并 fail-closed。结论 `fail`/`uncertain` 与
不可用都把任务放回 `waiting_user`（不让卡片停在「进行中」骗人），补做仍可在该状态上继续。
api 层 `RunRouterTurn` 新增 `ReadOnly` 分支：`ensureVerifierSession` 建隐藏会话
（`TypeRouter`、名「复核」、key 由 kanban 给）、honor `req.Cwd`、`routerVerifierToolPolicy`
只放 Read/Grep/Glob、且不碰 Router 自己的工具绑定（否则一轮复核会把 Router 回合的 MCP 句柄
清掉）。重启对账把 `verifying` 收口成 `blocked` + 「复核被重启打断」确定行。
一处测试调整：`TestVerifierReusesVerdictByReportHash` 原先在 begin 之后又同步调
`verifyCompletion`，而 begin 已经起了后台复核 —— 会跑两次模型；改成等 `blocked` 再断言调用数，
复用语义不变。门禁：`go test ./server/internal/kanban/ -count=1` → ok 327.8s，
`go test ./server/internal/api/ -count=1` → ok 69.0s。

---

### Task 8: 完成状态接口与前端清单

**Files:**
- Modify: `server/internal/api/http_routes.go`（`r.Get("/api/tasks/{id}/completion", ...)`）
- Create: `server/internal/api/http_task_completion.go`
- Modify: `server/internal/api/http_router_status.go` 或对应文件（`completion_blocks_last_hour`）
- Create: `web/src/components/TaskCompletionChecklist.tsx`（或并入现有详情组件）
- Modify: 任务详情运行视图挂载点
- Test: `server/internal/api/http_task_completion_test.go`、`web/tests/task-completion-checklist.test.mjs`

**Interfaces:**
- `GET /api/tasks/{id}/completion` → `{"completion": {"present":bool,"status":string,"items":[{index,id,label,kind,pass,detail}],"passed":bool,"repairs_used":int,"repairs_max":int,"verifier_verdict":string,"updated_at":string}}`；
  任务不存在 → 404；无记录 → `{"completion": null}`。
- 前端：清单紧凑行，未通过项按编号显示，复核结论带「模型判定」标签。

- [x] **Step 1: 写失败测试**（Go handler 三态 + 前端源码契约：组件读 `completion.items`、渲染编号、标注复核）。
- [x] **Step 2: 跑测试确认失败**。
- [x] **Step 3: 实现**。
- [x] **Step 4: 跑测试确认通过** + `tsc -b --noEmit` + `npx vitest run`。
- [x] **Step 5: 变异验证**：去掉 `verifier_verdict` 的「模型判定」标签 → 前端用例必须红；还原。
- [x] **Step 6: 提交** `feat: 任务完成证据清单接口与前端（Scope: G-NP）`。

**执行记录（2026-11-21）**：接口按计划落地 —— `GET /api/tasks/{id}/completion` 四态
（无记录 `{"completion": null}` / 有记录带 `items` / 未知任务 404 / 缺 `root_id` 400），
`TaskCompletionStatus` 只读回放落库的那份报告（不重新求值，避免清单与当初驳回理由不一致），
`repairs_max` 先取默认 2 再按 Run 的 `completionMaxRepairs` 覆盖。`completion_blocks_last_hour`
放在 `RouterStatusForRoot`，数的是 `router_decisions` 里 `trigger_kind='completion_blocked'`
的最近一小时条数 —— 事件表没有 HTTP 读口，决策表才是界面能看见的同一份事实。
前端：`useTaskCompletion`（挂载即拉、任务头更新重拉）+ `TaskCompletionChecklist`（编号行、
只列未通过项、复核结论带「模型判定」标签）+ `TaskCompletionEntry`（无记录整行隐藏，挂在
`SupervisionEntry` 之后）；文案键 zh/en 各 11 个。一处与计划的偏差：清单组件落在
`web/src/components/task/` 而不是计划里写的 `web/src/components/` —— 与 `SupervisionEntry`
同目录，符合现有布局（ledger 有 Ruling）。测试文件里 `import type` 与 TS 类型注解在 `.mjs`
中会被 esbuild 当 JS 解析而报 `Expected ',', got '{'`，改为纯 JS 对象与 `translateNow` 取文案，
顺带让断言不依赖运行环境语言。变异：把「模型判定」换成 `model` → 复核用例红；还原后绿。
门禁：`go test ./server/internal/api/ -run TestTaskCompletionRoute` ok、
`go test ./server/internal/kanban/ -run TestRouterStatusCountsCompletionBlocks` ok、
`tsc -b --noEmit` clean、`npx vitest run` 254 文件 / 804 通过 / 6 跳过。

---

### Task 9: 变异账本 `make mutate`

**Files:**
- Create: `scripts/mutate.sh`、`scripts/mutations.txt`
- Modify: `Makefile`（`mutate` 目标）
- Test: 本身即工具；用 `make mutate MUTATION=one` 自检。

**Interfaces:** `scripts/mutations.txt` 每行 `name|file|before|after|package|run`，
`before`/`after` 用 `\t` 表示制表、`\n` 表示换行。脚本逐条在 `git worktree` 临时副本上注入、
跑 `go test <package> -run '<run>' -count=1`，期望非零退出。

- [x] **Step 1: 写脚本**，先收 3 条已知变异点（Task 1 的 hold 判断、Task 5 的 block 状态、
  Task 6 的去重键）。
- [x] **Step 2: 自检**：`bash scripts/mutate.sh` 输出 `killed=3 survived=0`；手工把一条 `after`
  改成原样（等值替换）→ 输出 `invalid` 而非 `survived`。
- [x] **Step 3: 提交** `chore: 变异账本工具（Scope: G-NP）`。

**执行记录（2026-11-21）：** 三条变异全部 killed（`killed=3 survived=0 invalid=0`），等值
`after` 的临时账本输出 `invalid`、退出码 1。两处计划缺陷按裁定修正：① 格式里的 `|` 分隔符
与 Go 的 `||` 冲突（`if err != nil || !at.After(...)` 被切成三个字段），账本新增 `\p` 转义
表示字面竖线；② Step 1 写的「Task 6 的去重键」变异（把 `item.ID` 换成 `""`）是**等价变异** ——
缺口数仍由 `Join` 的分隔符编码，测试照样绿；改为把整条哈希换成常量 `sha256.Sum256(nil)`，
才真正打红 `TestCompletionBlockedNewGapIsNewEvent`。脚本从 `HEAD` 切临时 worktree（不带共享
工作区里其他会话的未提交改动），每条跑完 `git checkout -- <file>` 还原，退出时删 worktree。

---

### Task 10: 崩溃注入 `scripts/crash-sim.sh`

**Files:**
- Create: `scripts/crash-sim.sh`
- Test: 用隔离实例跑 1 轮自检。

- [x] **Step 1: 写脚本**：复用 `scripts/mindfs-iso.sh` 的隔离根与端口约定，起实例 → 建任务 →
  随机延迟后 `kill -9` → 重启 → 跑四条断言（无 running 回复残留 / 无「任务非终态 + Run 已结算 +
  投影已终态」悬挂 / 二次重启不新增事件 / `PRAGMA integrity_check` 为 ok）。
- [x] **Step 2: 自检 1 轮**：`CRASH_SIM_ROUNDS=1 bash scripts/crash-sim.sh` 输出 PASS 与 kill 点。
- [x] **Step 3: 提交** `chore: 崩溃注入脚本（Scope: G-NP）`。

**执行记录（2026-11-21）：** `CRASH_SIM_ROUNDS=3` 全过（`rounds=3 fails=0`）。每轮都先证明
这一刀砍在回合中间：kill 后立刻只读打开库，`2 条回复停在 pending/running`；重启后同一断言
为 0，说明启动对账确实结算了孤儿回复；② 在直连回合上必然空过（直连不建 Run），保留它是给
工作流崩溃回归用；③ 用真实计数比对（`{"router_events":0,"task_activities":3,"task_events":2}`
两轮完全相同）。三处计划缺陷按裁定修正：① `mindfs-iso.sh` 原来只从**工作区**编译，共享工作区
随时有别的会话在飞改动（本轮就撞上一次 `session_open.go` 编译不过），新增 `MINDFS_ISO_SRC`
让它可以从干净 HEAD worktree 编译，crash-sim 每条轮次都从 `HEAD` 切临时源码树（`web/dist` 与
`agents.json` 软链回工作区，因为两者都不进 git）；② 活动列表的字段是 `activities` 不是
`items`；③ 建任务返回 `{"task":{...}}`，`id` 在 `.task.id`。

---

### Task 11: 入站准入与默认审核分类（B2）

**Files:**
- Create: `server/internal/kanban/intake_policy.go`
- Modify: `server/internal/kanban/router_prompt.md`（`policy_hints` 增审核分类）
- Test: `server/internal/kanban/intake_policy_test.go`

**Interfaces:**
- `type IntakeRequest struct { RootID, Title, Body, Source string }`；
  `func ValidateIntake(req IntakeRequest) []string` —— **一次性返回全部问题**；
  `var ReviewCategories = []string{"删除数据","停止服务","花钱","对外发消息","改产品方向","新建或轮换密钥","修改权限"}`。

- [x] **Step 1: 写失败测试**：未知字段 / 超长 / 空标题三条同时给 → 返回 3 条而非 1 条；
  分类表 7 项且顺序稳定。
- [x] **Step 2: 实现 → 绿 → 变异**（遇错即返 → 用例红）→ 还原。
- [x] **Step 3: 提交** `feat(kanban): 入站校验与默认审核分类（Scope: G-NP）`。

执行记录（2026-11-21）：一次给四处问题（空 root_id / 超长标题 / 超长正文 / 未知来源）
返回 4 条，`ValidateIntakeFields` 的未知键与值问题一起返回且未知键按名字排序（map 遍历
无序，问题列表顺序必须稳定）。分类表落在 `ReviewCategories` 常量；同一份清单由
`ReviewCategoriesHint()` 写进 `brief.policy_hints`，并被 `TestRouterPromptListsReviewCategories`
钉住与 `router_prompt.md` 第 17 条一致 —— 两份拷贝漂移时 Router 会按过时清单判断「要不要
问人」。两条变异（校验器遇错即返、brief 那行只带第一类）各自打红对应用例后还原；全包
`go test ./server/internal/kanban/` 两轮各 360s / 280s，唯一红项是既有 flake
`TestCoordinatorTickReclaimsStaleClaims`（TempDir 清理竞态，隔离复跑绿）。本轮不实现、
不注册 `TaskIntake` 端口（spec §6.11 最后一条）。

---

### Task 12: 跨 harness 一致性探针（B3）

**Files:**
- Create: `server/internal/agent/conformance/conformance.go`、`conformance_test.go`
- Test: 隔离实例上跑 claude 与 codex 各一组探针。

- [ ] **Step 1: 写探针**：断言两会话打开/关闭形状、错误事件、取消、用量字段存在性；
  harness 特有字段放例外表。
- [ ] **Step 2: 跑**：无可用 harness 时跳过并明确打印「skipped: harness unavailable」，不算通过。
- [ ] **Step 3: 提交** `test(agent): 跨 harness 一致性探针（Scope: G-NP）`。

---

### Task 13: 回合用量差分（B4）

**Files:**
- Create: `server/internal/kanban/completion_usage.go`
- Modify: `server/internal/api/http_task_completion.go`（响应加 `usage` 块）
- Modify: `server/internal/agent/fold/ledger.go`（若无 `Baseline` 字段则加；按会话首行时间戳兜底）
- Test: `server/internal/kanban/completion_usage_test.go`

**Interfaces:**
- `func (s *Service) completionUsage(ctx context.Context, task Task, run *WorkflowRun) (CompletionUsage, bool)`
- `type CompletionUsage struct { Input, Output, Cached int; Samples int }`
- 语义：只统计该 Run 绑定会话（`TaskRelatedSessionKeys` 里属于本 Run 的键）在 Run 时间窗内的账本行；
  读不到返回 `false`，接口给 `null` —— **不写 0**（「没有数」不能读成「没花钱」）。

- [ ] **Step 1: 写失败测试**：三条账本行落在窗口内 → 求和正确；窗口内无行 → `ok=false`；
  首条记录之前没有基线 → 仍按窗口聚合，且 `Samples` 如实计数。
- [ ] **Step 2: 跑测试确认失败 → 实现 → 绿 → 变异验证**（把 `ok=false` 改成返回 0 值 → 用例红）。
- [ ] **Step 3: 提交** `feat(kanban): 任务完成记录附回合用量差分（Scope: G-NP）`。

---

## 收尾（全部任务后）

- [ ] `go test -count=1 ./...` 全绿；`tsc -b --noEmit`；`npx vitest run`。
- [ ] `docs/upstream-customizations.yaml` 登记 G-NP（锚点逐个核对）。
- [ ] 干净 worktree 上 `make check-upstream` 全绿。
- [ ] `bash scripts/deploy-all.sh`（用户批准后）；本机重启命令打印给用户。
- [ ] `docs/plans/README.md` 写 Active 条目与执行结果。
