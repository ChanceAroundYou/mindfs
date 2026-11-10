# Workflow Runtime Executor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Implementation is not authorized until open decisions below are resolved and this plan is approved.

**Goal:** 把 WorkflowRun 从纯状态快照变成可恢复的任务节点执行器：以持久化请求驱动稳定 NodeRun，复用明确 session，按图执行 Agent/Condition/HumanGate/Loop/Notify，并正确恢复、取消和投影状态。

**Architecture:** Kanban 数据面负责每 Task 的 run driver、持久化 claim/ledger/outbox 与 DAG 状态；`Runner` 增加 workflow-node 专用端口，由 app 层以既有 agent pool/session adapter 执行单个节点 turn，禁止通过合成 StageRun 双写 legacy 表。控制面只解析 Draft/Version，运行时只消费 Run 内联 graph snapshot。节点调度按 NodeRun claim 做幂等，副作用一律在相应事务提交后发起。

**Tech Stack:** Go module declares `go 1.25.0`, project toolchain is Go 1.26.5；`server/internal/kanban`、`server/internal/api`、`server/app`；SQLite task-node store；现有 agent Session/Pool adapter。

**Spec:** `docs/superpowers/specs/2026-11-09-kanban-first-agent-workflows-design.md`；路线图：`docs/superpowers/plans/2026-11-10-kanban-workflow-runtime-roadmap.md`。

## Global Constraints

- 必须先完成 WP-00 lane separation；不得用真实 CLI/生产会话作为单测依赖。
- 运行态只在任务所属节点 SQLite；WorkflowVersion 只在控制面；worker 不实时查询模板库。
- NodeRun 唯一键 `(run_id,node_id,loop_path,iteration,attempt)`；重复启动不得产生第二个外部 turn。
- Agent Silent/Blocked 不算成功；Unknown 不自动重试；显式重试创建新 attempt 并保留原留言/NodeRun 证据。
- steer 仅当 adapter capability + 协议回执允许时使用；`sent` 不能标 `confirmed`；否则 FIFO 投递到同一活动 Agent NodeRun 绑定的同一 session。
- 同一 session 不并发第二个 turn；同一 worktree 的 writer 串行。Phase 1 不跨节点接管、不引入独立队列服务。
- `server/internal/kanban/service_outbox.go` 当前超过后端 500 行硬线；任何后续编辑前先以测试守护职责拆分，不能继续增长超线文件。
- Run/task event ledger 的投影必须通过已注入的 `Runner.TaskUpdated(rootID, detail)` 或后续明确批准的 run-update 通道到达看板；每个运行态变更测试同时断言持久状态与 UI/API 可见状态，不能只验证 DB。

## Review Focus

1. 留言提交成功后进程在 outbox commit 与 agent send 间崩溃：恢复不得重复副作用，unknown 要可见。
2. 两个 driver/HTTP 请求同时 claim 同一节点：最多一个成功并启动 turn。
3. 工作流没有 Agent 节点、Condition 分支无匹配、expression 引用缺失字段：任务不能静默成功/卡死。
4. Loop 第 N 次迭代的结果、attempt 与 restart 重放：迭代不漏增、不重用唯一键，达到上限转 needs-user。
5. cancel/pause 与运行中的 session、未消费留言、worktree 并发发生：不能留幽灵 running 状态、不能删除活跃 cwd。

---

## 决策门（必须在写对应 RED 测试之前确认）

路线图 D1–D9、D13、D14 是本计划的产品决策清单。以下子计划只实现已确认项；没有答案的任务保持 blocked，不可自行把推荐默认当决定。当前明确已定：新留言绑定收到时的活动 Agent NodeRun，steer 可确认才确认，否则同一 session FIFO。

特别先解决：D1 driver/启动触发；D2 session 复用策略；D3 多活动 NodeRun 时 comment target；D4 HumanGate approve/reject；D5 agent structured output；D6 expression root；D7 legacy board verb 映射；D8 恢复证据；D9 queued/empty session 状态；D13 event ledger；D14 run/task cardinality。

## 文件与职责地图

- `server/internal/kanban/task_store_workflow_runs.go`：NodeRun 原子 claim、按键读取、状态迁移与 run CAS；仅在既有方法不足时拆出 `task_store_workflow_claim.go`。
- `server/internal/kanban/workflow_executor.go`（新）：driver loop、节点 dispatch、调度边界；不含 app/HTTP 细节。
- `server/internal/kanban/workflow_node_executor.go`（新）：Agent 节点执行协议、Agent 结果结构；Condition/HumanGate/Loop/Notify 调度在独立文件/函数中。
- `server/internal/kanban/service.go` / `types.go`：Runner 接口与 Service dependencies；新节点接口不得复用 `AgentStageExecution`。
- `server/internal/kanban/service_outbox.go` / `task_store_comments.go`：comment→active NodeRun binding/outbox after-commit；先明确 `node_runs` 与 `workflow_node_runs` canonical identity。
- `server/internal/kanban/service_recover.go` / `service_recover_startup.go`：workflow run reconciliation、unknown/needs-user 状态和 startup driver enqueue。
- `server/internal/api/usecase/*` / `server/app/*`：从 agent Pool/Session 创建和执行 workflow NodeRun 的生产 adapter，并把活动节点信息路由给 Kanban Service。
- `server/internal/api/http_workflows.go` / `http_routes.go`：仅补经决策批准的 pause/resume/cancel/approve/retry 与 run status API。
- 对应 `_test.go`：TaskStore 用现有测试 DB；Service 使用 fake WorkflowNodeRunner、fake steer/session probe；app adapter 单测验证 session binding 与结果捕获。

## Interfaces（计划级，D2/D5 决策后冻结具体字段）

- `WorkflowNodeExecution`：包含 RootID、Task、Run、Node、NodeRun（含 ID/loop_path/iteration/attempt）、SessionPolicy、Prompt、RuntimeRootPath；不得塞 StageRun。
- `WorkflowNodeResult`：包含 `Outcome`（done/blocked/silent/unknown）、`SessionKey`、`Output map[string]any`、`Reason` 与 start/finish evidence。
- `WorkflowNodeRunner`：`EnsureWorkflowNodeSession(ctx, exec) (sessionKey string, err error)` 和 `RunWorkflowNode(ctx, exec) (WorkflowNodeResult, error)`；实际 signature 按现有 app runner 注入模式复核，决定后在 Task 2 测试先锁定。
- `WorkflowDriver`：`Advance(ctx, rootID, taskID string) error`；只由 D1 决定的 trigger 启动，循环直到等待外部事件/terminal/未知状态，不自行创建后台常驻服务。

## Tasks（依赖顺序）

### Task 1：定 canonical NodeRun/request 映射与持久化 claim

**Files:** Modify `workflow_test.go`/`task_store_workflow_runs_test.go` and a test migration fixture; only if D14 permits historical runs, rebuild `workflow_runs` to remove `UNIQUE(task_id)`, recreate indexes, update `WorkflowRunForTask` and engine binding lookup in `service_engine.go`, and test migration/rollback against an existing DB.

**Interfaces:** Consumes existing TaskStore transactions and WP-01/WP-03 tables. Produces atomic `ClaimWorkflowNodeRun(ctx, runID, nodeID, loopPath string, iteration, attempt int, node NodeRun) (NodeRun, claimed bool, err error)` or equivalent CAS signature (must be finalized before tests).

- [ ] **Step 1: Write `TestClaimWorkflowNodeRunIsSingleWinner`** with two concurrent claims for the same unique key; assert exactly one `claimed=true`, one persisted row, no external callback.
- [ ] **Step 2: Run `go test ./server/internal/kanban -run '^TestClaimWorkflowNodeRunIsSingleWinner$' -count=1`**; Expected: FAIL before claim API.
- [ ] **Step 3: Reconcile existing competing request/NodeRun APIs first** — explicitly review `TaskStore.PostComment`, `OutboxService.RecordComment`, `EnsureOutboxTables`, `continue_requests` DDL migration/guard test, `NodeRunRecord` vs `NodeRun`, `GetNodeRun`, and `CreateNewAttempt`; choose one canonical workflow NodeRun identity and a lossless mapping for WP-03 legacy outbox rows. Add tests that old comments/recovery data remain readable.
- [ ] **Step 4: Resolve D14 before schema work** — if multiple historical runs per task are supported, rebuild SQLite `workflow_runs` because `task_id UNIQUE` cannot be dropped in place; update `WorkflowRunForTask`, engine binding lookup (`TaskEngineFor`/`taskRunsOnWorkflow` in `service_engine.go`) and add upgrade/rollback fixture. If D14 chooses one run per task, test that later workflow choices are explicitly rejected.
- [ ] **Step 5: Implement claim in one task DB transaction** with unique-key conflict interpreted as already claimed; do not call Runner inside transaction.
- [ ] **Step 6: Add test** that claimed row survives store reopen and repeated claim returns existing identity without a second start.
- [ ] **Step 7: Run targeted kanban tests**; Expected: PASS.

### Task 2：增加独立 WorkflowNodeRunner 端口和 app adapter

**Files:** Modify `types.go`, `service.go`, runner injection in `server/app/*`; create `workflow_node_executor.go`; tests in kanban and app packages.

**Interfaces:** Consumes Task 1 persisted claim. Produces exact `WorkflowNodeExecution`, `WorkflowNodeResult`, and `WorkflowNodeRunner` types described above; signature frozen by failing tests before implementation.

- [ ] Step 1: Add fake-runner test `TestWorkflowAgentNodeUsesWorkflowRunnerNotLegacyStageRunner`; assert workflow node path receives NodeRun identity, node params and prompt, and legacy StageRun store remains unchanged.
- [ ] Step 2: Run focused test; Expected: FAIL because no workflow runner port exists.
- [ ] Step 3: Add `WorkflowNodeRunner` interface and inject it into `kanban.Service`; preserve existing `Runner` interface for legacy tasks.
- [ ] Step 4: Add app adapter test for Ensure session + run call: assert selected agent/harness/model and node scope, captured session key, output and outcome; no live CLI.
- [ ] Step 5: Implement app adapter by reusing agent pool/session APIs; session reuse obeys resolved D2, no synthetic StageRun creation.
- [ ] Step 6: Run `go test ./server/internal/kanban ./server/app ./server/internal/api/usecase`; Expected: PASS.

### Task 3：Agent node attempt lifecycle, output, Activity and after-commit

**Files:** Create/modify `workflow_executor.go`, `task_store_workflow_runs.go`, `task_store_comments.go` (migrations), `service_outbox.go`; tests in kanban/API.

**Interfaces:** Consumes Task 1 claim + Task 2 runner port. Produces `StartWorkflowNode(ctx, run, node)`; persists queued/running/result/terminal transitions; writes exactly one correlated `agent_reply`/attention Activity if D5-approved output contract permits.

- [ ] Step 1: Test a failing runner call: NodeRun transitions `queued→running→fail`, task projects fail, error/reason is retained and an attention activity references run/node.
- [ ] Step 2: Test Silent and Blocked outcomes: neither transitions to success; both project to waiting_user with distinct reason.
- [ ] Step 3: Test transaction boundary: a failed NodeRun claim or outbox insert must not call fake runner; after commit, exactly one dispatch occurs.
- [ ] Step 4: Run targeted tests to confirm RED.
- [ ] Step 5: Implement state transitions with explicit allowed-state validation and append-only event ledger if D13 is approved.
- [ ] Step 6: Implement Activity correlation columns/foreign keys or explicit stable IDs; preserve legacy activity reads.
- [ ] Step 7: Re-run focused tests; Expected: PASS.

### Task 4：DAG progression, Condition, skip/join, Notify and status projection

**Files:** Modify/create `workflow_executor.go`, `workflow_engine.go`, TaskStore; tests: `workflow_test.go`, new `workflow_executor_test.go`.

**Interfaces:** Consumes `EngineReadyNodes`, safe expression evaluator, Task 1–3 persisted result. Produces `EvaluateWorkflowCondition(run, node) (route string, err error)` plus deterministic driver scheduling.

- [ ] Step 1: Add failing executor tests: ready node starts once; unselected route materializes skipped state; join waits only selected predecessors; selected route persists across reopen.
- [ ] Step 2: Add condition-root test using the D6-approved fully qualified path; assert missing field is an error/needs-user as D5/D6 define, never silently false.
- [ ] Step 3: Add Notify test: produces persisted NodeRun + TaskActivity, no Session/Runner call.
- [ ] Step 4: Run `go test ./server/internal/kanban -run 'Workflow(Ready|Condition|Join|Notify)' -count=1`; Expected: RED before driver implementation.
- [ ] Step 5: Implement one driver progression iteration: reload authoritative run, apply ready/skipped transitions, claim before start, persist selected routes, repeat until no synchronously runnable nodes or terminal/waiting state.
- [ ] Step 6: Wire `ProjectStatusFromRun` to TaskStore update + `Runner.TaskUpdated` only after successful persistence; test running/waiting/success/fail/cancel projections.
- [ ] Step 7: Re-run focused executor tests; Expected: PASS.

### Task 5：HumanGate + user comment binding to active Agent NodeRun

**Files:** Modify `http_task_comments.go`, `service_comments.go`, `task_store_comments.go`, `service_outbox.go`, workflow HTTP routes; frontend only if approval UX needed; tests at kanban/API/web.

**Interfaces:** Consumes user decision D3/D4 plus NodeRun claim. Produces `BindCommentToActiveNodeRun(ctx, taskID, commentID)`, which persists fixed `node_run_id` and `session_key`, and `ResolveHumanGate(ctx, runID, nodeID, decision, commentID)`.

- [ ] **Step 1: Reuse `TestOutbox_SteerConfirmedOnlyRealReceipt`, `TestOutbox_TimeoutUnknownNoReplay`, `TestOutbox_FIFOSameSessionPreservesOrder`, and `TestOutbox_RecordCommentIdempotent`**; extend them with runtime NodeRun binding/after-commit assertions instead of duplicating their unit invariants.
- [ ] **Step 2: Add integration test**: comment arrival while one Agent NodeRun is active binds the exact `node_run_id` and `session_key`; after graph advances the binding is immutable.
- [ ] **Step 3: Add test** for multiple active read-only nodes and zero active Agent NodeRuns (HumanGate/between nodes/terminal): behavior follows resolved D3; no implicit fan-out, accidental approval, or terminal-run resurrection.
- [ ] **Step 4: Add test** that queued FIFO drains only after bound NodeRun is terminal, creates/updates the explicitly decided turn identity, and sends to same session in order.
- [ ] **Step 5: Add test** normal comment does not approve HumanGate; explicit decision follows D4 and duplicate request is idempotent.
- [ ] **Step 6: Implement only missing binding/driver integration**; preserve existing outbox receipt and no-replay behavior.
- [ ] **Step 7: Run targeted Go tests**; Expected: PASS.

### Task 6：Loop bounded execution and graph editing freeze

**Files:** Modify `workflow_engine.go`, `task_store_workflow_runs.go`, validator as needed; tests in `workflow_test.go` and `workflow_executor_test.go`.

**Interfaces:** Consumes resolved D5/D6 output contract; produces deterministic flattened inner-node identifiers and `LoopState` transitions persisted with the run.

- [ ] Step 1: Add failing test for iteration 1 complete → iteration increments exactly once and new inner NodeRun key uses loop path/iteration.
- [ ] Step 2: Add restart test between inner node completion and next iteration scheduling; assert no repeated inner attempt and no missed increment.
- [ ] Step 3: Add maximum bound test: 1..20 respected; after final non-exiting pass, state is waiting_user and prior output remains queryable.
- [ ] Step 4: Add edit freeze test: graph edits to triggered node and edges incident to triggered node conflict; future untriggered subtree remains editable under revision CAS.
- [ ] Step 5: Run focused tests to verify RED.
- [ ] Step 6: Implement flatten/iteration ownership and CAS-safe graph validation; implementer may not silently change loop semantics from spec.
- [ ] Step 7: Run kanban tests; Expected: PASS.

### Task 7：Driver triggers, cancellation, startup reconciliation and explicit retry

**Files:** Modify `service_recover.go`, `service_recover_startup.go`, `service_run_moves*.go`, `service_execution.go`, `http_routes.go`, `http_workflows.go`, app wiring; tests: recovery, moves, API.

**Interfaces:** Consumes resolved D1/D7/D8/D9; produces `WakeWorkflowRun(ctx, rootID, taskID, cause)`, `CancelWorkflowRun`, `PauseWorkflowRun`, `ResumeWorkflowRun`, `CreateWorkflowNodeAttempt` (exact exported names frozen in tests).

- [ ] Step 1: Test each approved trigger (`comment`, migration/RunNow/resume) starts or wakes one driver and duplicate trigger coalesces.
- [ ] Step 2: Test pause blocks new claims but lets in-flight node reach terminal; resume wakes same Run.
- [ ] Step 3: Test cancel stops driver, calls bounded per-NodeRun session cancellation, persists cancelled state, blocks unconsumed comments and never deletes an active worktree.
- [ ] Step 4: Test startup state matrix: persisted queued-not-sent, active NodeRun with known completed session, active NodeRun with ambiguous external side effect; assert D8/D9 mapping to resume vs unknown/needs_user.
- [ ] Step 5: Test explicit retry uses new attempt ID linked to original NodeRun/request and does not mutate original comment identity or overwrite evidence.
- [ ] Step 6: Run tests to verify RED.
- [ ] Step 7: Implement per-task single-flight driver and restart reconciliation using task-node state; no resident distributed worker and no `context.Background()` unbounded cancellation.
- [ ] Step 8: Run `go test ./server/internal/kanban ./server/internal/api ./server/app`; Expected: PASS.

### Task 8：Run status/API/UI vertical acceptance

**Files:** Modify `http_workflows.go`, routes, `RunViewEntry.tsx`, `useRunViewEntry.ts`, `RunTimeline.tsx` and related i18n only as required; tests `web/tests/run-timeline-drill-in.test.ts`, API integration tests.

**Interfaces:** Consumes Task 3–7 status and NodeRun payload. Produces REST status/operations consistent with actual driver state; polling stays phase-1 unless separately approved.

- [ ] **Step 1: Reuse `TestWorkflowRunAndNodeRunAreIsolatedByTaskNodeDatabase`** and add API integration: migrate task → approved explicit trigger → fake Runner finishes node → GET run/node-runs shows terminal result and Task status; legacy task remains legacy.
- [ ] Step 2: Add conflict/permission/scope tests for cancel/pause/resume/retry/HumanGate endpoints as decided.
- [ ] Step 3: Run frontend test asserting run view renders each persisted NodeRun and a bound session link; statuses/error messages do not claim success for unknown.
- [ ] Step 4: Implement API/UI only after tests fail for missing behavior.
- [ ] Step 5: Run `go test ./server/... ./cli/...`, frontend tests, typecheck, `make build-web`; expected all pass except documented baseline failures.

### Task 9：Whole Runtime review and upstream gate

- [ ] **Step 1: Add G-LC** entry for executor’s independent boundary, exact symptom, why, files, tests and anchors.
- [ ] **Step 2: Run race-sensitive claims/driver/outbox tests** under `go test -race`.
- [ ] **Step 3: Repair upstream test baseline** — G-KN's current anchor points to ignored/untracked `web/capacitor.config.json`; re-anchor to a tracked invariant covered by G-KN or deliberately alter tracking under approved scope, then verify that group. Do not call it a permanent baseline failure.
- [ ] **Step 4: Run full Go suite and relevant Vitest tests**; record fresh-worktree behavior. Capacitor generated config tests need a deterministic fixture or explicit skip policy.
- [ ] **Step 5: Commit each authorized self-contained slice** with its registered Scope; `make check-upstream` after commit. Future implementation only; this plan authorizes no commit, merge, deploy, or restart.




## Out of scope

- Cross-node workflow takeover or distributed leader election.
- Automatic exactly-once guarantees for external agent tools.
- Hidden heuristic routing of comments to whichever session “looks active”.
- Reusing legacy StageRun as a parallel status truth for WorkflowRun.
- Agent publishing shared workflow versions without user confirmation.
