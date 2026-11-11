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

**已确认决策（2026-11-10）**：
- D14 = 保留历史 Run，每任务至多一个非终态 Run。`workflow_runs` 已重建去掉 `UNIQUE(task_id)`；`WorkflowRunForTask` 返回 engine 绑定的当前 Run（无绑定时取最新），`ListWorkflowRunsForTask` 暴露历史；创建第二条 Run 仅当绑定中的 Run 已终态，否则仍返回 `ErrTaskAlreadyWorkflow`。
- D2 = 每节点默认新 session；`session_reuse_policy=task_main` 才复用任务主会话；`same_stage` 在同一 `node + loop_path` 的 attempt/iteration 间复用；`always_new` 每次 attempt 新建。
- D5 = Agent 结果必须是严格 JSON envelope（`outcome=done|blocked|silent`、`output`、`reason`）；缺失/非 JSON/非法 outcome/要求字段缺失时 NodeRun 转 waiting_user，Condition/Loop 不猜值。
- D13 = 做最小 typed、append-only 的 run 事件账本（用户 2026-11-10 确认）：`workflow_run_events(run_id,seq,...)` 与投影更新同事务写，`seq = MAX(seq)+1`；投影可重建，不做 full event sourcing。
- D6 = 全图按 `node_id.output.*` 限定（用户 2026-11-10 确认）：表达式如 `build.output.files > 0`，对全图已持久化输出求值；引用节点必须在 Condition/Loop 上游，缺失节点/字段报错转 waiting_user，不静默为 false。Condition 布尔结果映射到 route `pass`/`fail`（与前端默认 routes 一致），未声明对应 route 视为配置错误。
- D3 = 按用户 2026-11-10 答复：正常情况下一条用户 message 对应一个 Agent 行动段。上一个已完成时，新消息开启新的执行阶段；恰好一个活动 Agent NodeRun 时直接追加；多个活动时**默认排队到多分支收束为唯一活动节点后再追加**，同时允许用户显式选择目标（带 expected node/run 校验）。排队期间不广播、不批准 HumanGate、不复活终态 Run；零活动时不绑定到任何 NodeRun。
- D4 = 专用 approve/reject（用户 2026-11-10 确认）：普通 comment 永不隐式批准 HumanGate；重复请求幂等，NodeRun 只从 waiting_user 转一次。

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

- [x] **Step 1: Write `TestClaimWorkflowNodeRunIsSingleWinner`** with two concurrent claims for the same unique key; assert exactly one `claimed=true`, one persisted row, no external callback.
- [x] **Step 2: Run `go test ./server/internal/kanban -run '^TestClaimWorkflowNodeRunIsSingleWinner$' -count=1`**; Expected: FAIL before claim API.
- [x] **Step 3: Reconcile existing competing request/NodeRun APIs first** — explicitly review `TaskStore.PostComment`, `OutboxService.RecordComment`, `EnsureOutboxTables`, `continue_requests` DDL migration/guard test, `NodeRunRecord` vs `NodeRun`, `GetNodeRun`, and `CreateNewAttempt`; choose one canonical workflow NodeRun identity and a lossless mapping for WP-03 legacy outbox rows. Add tests that old comments/recovery data remain readable.
- [x] **Step 4: Resolve D14 before schema work** — if multiple historical runs per task are supported, rebuild SQLite `workflow_runs` because `task_id UNIQUE` cannot be dropped in place; update `WorkflowRunForTask`, engine binding lookup (`TaskEngineFor`/`taskRunsOnWorkflow` in `service_engine.go`) and add upgrade/rollback fixture. If D14 chooses one run per task, test that later workflow choices are explicitly rejected.
- [x] **Step 5: Implement claim in one task DB transaction** with unique-key conflict interpreted as already claimed; do not call Runner inside transaction.
- [x] **Step 6: Add test** that claimed row survives store reopen and repeated claim returns existing identity without a second start.
- [x] **Step 7: Run targeted kanban tests**; Expected: PASS.

**Task 1 执行结果（2026-11-10）**：
- `ClaimWorkflowNodeRun` 用单条 `INSERT ... ON CONFLICT(run_id,node_id,loop_path,iteration,attempt) DO NOTHING` + `RowsAffected` 判定唯一赢家；输家回读原行。claim 只在 DB 内完成，不调用任何 Runner。`SetMaxOpenConns(1)` 让并发 claim 串行化，4 goroutine 测试恰好一个 winner。
- `UpsertWorkflowNodeRun` 的 conflict update 补齐 `agent/harness/model/session_key/input_json/started_at/prev_attempt_id`，否则 running 行的 session 绑定落不下去，重开后无法恢复。
- 身份解析 canonical = `workflow_node_runs`（新文件 `node_run_identity.go` 的 `ResolveNodeRun`），WP-03 `node_runs` 影子行兜底且保持只读可读；`GetNodeRun` 改为 canonical 优先。测试覆盖：canonical 优先、legacy 行/旧留言仍可读、同 id 冲突时 canonical 胜出。
- D14 迁移测试用旧 DDL 种子库验证：重建后 `UNIQUE(task_id)` 消失、历史行原样保留、同任务可再建 Run、重开幂等。
- 遗留（Task 5 处理，未擅自改）：`TaskStore.PostComment` 与 `OutboxService.RecordComment` 仍是两个 writer；HTTP 路径只走前者，且前者写的 `continue_requests` 行没有 outbox item。绑定活动 NodeRun 时需统一写入/补齐语义，不能在此处静默合并。

### Task 2：增加独立 WorkflowNodeRunner 端口和 app adapter

**Files:** Modify `types.go`, `service.go`, runner injection in `server/app/*`; create `workflow_node_executor.go`; tests in kanban and app packages.

**Interfaces:** Consumes Task 1 persisted claim. Produces exact `WorkflowNodeExecution`, `WorkflowNodeResult`, and `WorkflowNodeRunner` types described above; signature frozen by failing tests before implementation.

- [x] Step 1: Add fake-runner test `TestWorkflowAgentNodeUsesWorkflowRunnerNotLegacyStageRunner`; assert workflow node path receives NodeRun identity, node params and prompt, and legacy StageRun store remains unchanged.
- [x] Step 2: Run focused test; Expected: FAIL because no workflow runner port exists.
- [x] Step 3: Add `WorkflowNodeRunner` interface and inject it into `kanban.Service`; preserve existing `Runner` interface for legacy tasks.
- [x] Step 4: Add app adapter test for Ensure session + run call: assert selected agent/harness/model and node scope, captured session key, output and outcome; no live CLI.
- [x] Step 5: Implement app adapter by reusing agent pool/session APIs; session reuse obeys resolved D2, no synthetic StageRun creation.
- [x] Step 6: Run `go test ./server/internal/kanban ./server/app ./server/internal/api/usecase`; Expected: PASS.

**Task 2 执行结果（2026-11-10）**：
- `WorkflowNodeRunner` 端口冻结为 `EnsureWorkflowNodeSession` + `RunWorkflowNode`，`Service.StartWorkflowNode` 只走该端口；fake 端口测试断言 legacy `Runner.RunAgentStage` 调用次数为 0，NodeRun 的 `id/run_id/node_id/loop_path/iteration/attempt` 原样到达，`session_reuse_policy` 空值落 `per_node`（D2）。
- D5 解析器 `ParseWorkflowNodeEnvelope` 用 `DisallowUnknownFields` + 二次 `Decode` 要求 `io.EOF`：纯文本、代码块外的多余 JSON、未知字段、非法 outcome、blocked/silent 缺 reason 全部报错；adapter 把这些统一转成 `Outcome=unknown`（nil transport error），交给 Task 3 投影 waiting_user。
- adapter（`appcontext_workflow.go`）按 D2 解析复用：`task_main` 复用任务主会话、`same_stage` 用同 run/node/loop_path 最近一次绑定（`LatestWorkflowNodeSessionKey`），`per_node` 只在自己 NodeRun 已有绑定时续聊，`always_new` 永不复用；建会话/广播复用现有 usecase，不写 StageRun。
- 默认 `per_node` 让同一节点的第二次 attempt 新建会话；`session_reuse_policy=same_stage` 才跨 attempt/iteration 续聊。
- 单测接缝 `workflowTurnOverride` 只在测试中设置，生产为 nil；覆盖 agent/model/mode/effort/fast_service/plan_mode 透传、prompt 附加 JSON envelope 契约、结果解析和 session key/起止时间回填。
- `server/app/workspace.go` 与 `workspace_build.go` 均调用 `SetWorkflowNodeRunner(services)`；`go test ./server/internal/kanban ./server/internal/api ./server/app` 全绿。
- 未做：Activity/outbox 关联、NodeRun 状态落库、driver 推进（Task 3+）。

### Task 3：Agent node attempt lifecycle, output, Activity and after-commit

**Files:** Create/modify `workflow_executor.go`, `task_store_workflow_runs.go`, `task_store_comments.go` (migrations), `service_outbox.go`; tests in kanban/API.

**Interfaces:** Consumes Task 1 claim + Task 2 runner port. Produces `StartWorkflowNode(ctx, run, node)`; persists queued/running/result/terminal transitions; writes exactly one correlated `agent_reply`/attention Activity if D5-approved output contract permits.

- [x] Step 1: Test a failing runner call: NodeRun transitions `queued→running→fail`, task projects fail, error/reason is retained and an attention activity references run/node.
- [x] Step 2: Test Silent and Blocked outcomes: neither transitions to success; both project to waiting_user with distinct reason.
- [x] Step 3: Test transaction boundary: a failed NodeRun claim or outbox insert must not call fake runner; after commit, exactly one dispatch occurs.
- [x] Step 4: Run targeted tests to confirm RED.
- [x] Step 5: Implement state transitions with explicit allowed-state validation and append-only event ledger if D13 is approved.
- [x] Step 6: Implement Activity correlation columns/foreign keys or explicit stable IDs; preserve legacy activity reads.
- [x] Step 7: Re-run focused tests; Expected: PASS.

**Task 3 执行结果（2026-11-10）**：
- `RunWorkflowNodeAttempt` 串起完整生命周期：`ClaimWorkflowNodeRun`（queued 事件与 claim 同事务）→ `BeginWorkflowNodeRun`（queued→running + `node_started`）→ `PrepareWorkflowNode` → `BindWorkflowNodeSession`（外部回合开始前落 session + `node_session_bound`）→ `RunPreparedWorkflowNode` → `FinishWorkflowNodeRun`（running→终态 + 终态事件，CAS 不允许多次收尾）→ 投影 `WorkflowRun.NodeStates` + `Task.Status` + 关联 Activity → `Runner.TaskUpdated` 广播。
- outcome 映射：`done→success/node_succeeded/agent_reply`；`blocked→waiting_user/node_blocked/attention`；`silent→waiting_user/node_silent/attention`；非法 envelope（Task 2 转出的 `unknown`）`→unknown/node_unknown/attention`；传输/准备/绑定失败 `→fail/node_failed/attention` 且错误原样返回给调用方（不会因“已落库”而吞掉）。
- Activity 用稳定 ID `wf:<node_run_id>:<event_type>` + `AppendTaskActivity` 的 `ON CONFLICT(task_id,id) DO NOTHING`，重复回调不会复制回复；`task_activities.run_id/node_run_id` 为增量 ALTER，legacy 留言保持空串可读。
- 账本 D13：`workflow_run_events` 主键 `(run_id,seq)`，`appendWorkflowRunEventTx` 只允许在投影事务内调用；节点状态投影只由终态事件同事务写入。没有额外的「从账本重放」生产辅助器——本轮没有读取方，留成死代码比不写更糟（独立评审发现后移除）。
- 测试（`workflow_executor_test.go`）：失败回合保留 error、task fail、Activity 关联 run/node、账本顺序 `queued→started→session_bound→failed`；blocked/silent 均落 waiting_user 且 reason 区分；同一 key 重试只派发一次；store 关闭导致 claim 失败时 runner 零调用。
- RED 记录（如实）：本轮实现与测试同批落地，先跑出的失败是“失败回合被 `finishWorkflowNodeAttempt` 吞成 nil error”，修复后转绿；随后用两次变异证明测试确实会红——把 `blocked` 映射改成 success、把 lost-claim 守卫改成直通，对应用例分别失败，恢复后全绿。未伪造“先红后绿”的记录。
- 未做：outbox 写入边界（Step 3 的 outbox 部分）留 Task 5，因为 `PostComment` / `RecordComment` 两个 writer 的统一是 Task 5 的范围，这里不静默合并。

### Task 4：DAG progression, Condition, skip/join, Notify and status projection

**Files:** Modify/create `workflow_executor.go`, `workflow_engine.go`, TaskStore; tests: `workflow_test.go`, new `workflow_executor_test.go`.

**Interfaces:** Consumes `EngineReadyNodes`, safe expression evaluator, Task 1–3 persisted result. Produces `EvaluateWorkflowCondition(run, node) (route string, err error)` plus deterministic driver scheduling.

- [x] Step 1: Add failing executor tests: ready node starts once; unselected route materializes skipped state; join waits only selected predecessors; selected route persists across reopen.
- [x] Step 2: Add condition-root test using the D6-approved fully qualified path; assert missing field is an error/needs-user as D5/D6 define, never silently false.
- [x] Step 3: Add Notify test: produces persisted NodeRun + TaskActivity, no Session/Runner call.
- [x] Step 4: Run `go test ./server/internal/kanban -run 'Workflow(Ready|Condition|Join|Notify)' -count=1`; Expected: RED before driver implementation.
- [x] Step 5: Implement one driver progression iteration: reload authoritative run, apply ready/skipped transitions, claim before start, persist selected routes, repeat until no synchronously runnable nodes or terminal/waiting state.
- [x] Step 6: Wire `ProjectStatusFromRun` to TaskStore update + `Runner.TaskUpdated` only after successful persistence; test running/waiting/success/fail/cancel projections.
- [x] Step 7: Re-run focused executor tests; Expected: PASS.

**Task 4 执行结果（2026-11-10）**：
- `AdvanceWorkflowRun(ctx, store, taskID)` 每次迭代都重新 `WorkflowRunForTask` 读权威快照，再跑 `EngineReadyNodes`：先物化 skipped，再按图顺序取第一个 ready 节点推进，循环直到没有可同步推进的节点。1000 步上限把引擎 bug 变成显式错误，不无限自旋。
- Condition：`EvaluateWorkflowCondition` 按 D6 建根 `{node_id: {output: ...}}`，布尔 true→`pass`、false→`fail`；缺字段/非法表达式 → NodeRun `waiting_user` + `node_unknown` + attention，**绝不静默走 fail 分支**。选中的 route、NodeRun 终态、`NodeStates/NodeOutputs/SelectedRoutes` 与账本事件在 `CommitWorkflowNodeTransition` 的**同一事务**里提交；路由不落盘会让两条分支同时可跑，所以不能分开写。
- Skip/Notify：skip 直接物化 `skipped` NodeRun + `node_skipped`；Notify 物化成功 NodeRun + `node_notified` + `system_event` Activity，不碰 session runner（测试断言 ensure/run 均为 0）。
- Agent：driver 只负责生成顶层 attempt 身份 `(loop_path="", iteration=0, attempt=1)` 并调用 Task 3 的 `RunWorkflowNodeAttempt`；成功/失败/等待都由它投影，driver 下一轮重读。
- `HumanGate` / `Loop` 在 driver 里**明确停住**，不猜审批或迭代语义（分别留给 Task 5 / Task 6）。
- 测试（`workflow_driver_test.go`）：线性 notify→agent→notify 一次推进到底且重复推进零重复派发；条件分支 join 只跑选中分支、未选中分支 `skipped` 且重开 DB 后 route/状态仍在；缺字段条件停 waiting_user 且另一分支无 NodeRun；Notify 零 session 调用。变异验证：删掉 route 持久化后条件用例立即红。
- 未做：多分支并发派发（当前串行取第一个 ready，`writes_workspace` writer gate 与并行只读留 Task 6/7）、HumanGate/Loop 语义、driver 触发/恢复。

### Task 5：HumanGate + user comment binding to active Agent NodeRun

**Files:** Modify `http_task_comments.go`, `service_comments.go`, `task_store_comments.go`, `service_outbox.go`, workflow HTTP routes; frontend only if approval UX needed; tests at kanban/API/web.

**Interfaces:** Consumes user decision D3/D4 plus NodeRun claim. Produces `BindCommentToActiveNodeRun(ctx, taskID, commentID)`, which persists fixed `node_run_id` and `session_key`, and `ResolveHumanGate(ctx, runID, nodeID, decision, commentID)`.

- [x] **Step 1: Reuse `TestOutbox_SteerConfirmedOnlyRealReceipt`, `TestOutbox_TimeoutUnknownNoReplay`, `TestOutbox_FIFOSameSessionPreservesOrder`, and `TestOutbox_RecordCommentIdempotent`**; extend them with runtime NodeRun binding/after-commit assertions instead of duplicating their unit invariants.
- [x] **Step 2: Add integration test**: comment arrival while one Agent NodeRun is active binds the exact `node_run_id` and `session_key`; after graph advances the binding is immutable.
- [x] **Step 3: Add test** for multiple active read-only nodes and zero active Agent NodeRuns (HumanGate/between nodes/terminal): behavior follows resolved D3; no implicit fan-out, accidental approval, or terminal-run resurrection.
- [x] **Step 4: Add test** that queued FIFO drains only after bound NodeRun is terminal, creates/updates the explicitly decided turn identity, and sends to same session in order.
- [x] **Step 5: Add test** normal comment does not approve HumanGate; explicit decision follows D4 and duplicate request is idempotent.
- [x] **Step 6: Implement only missing binding/driver integration**; preserve existing outbox receipt and no-replay behavior.
- [x] **Step 7: Run targeted Go tests**; Expected: PASS.

**Task 5 执行结果（2026-11-10）**：
- 绑定（D3）：`PostComment` 在写入 activity/ContinueRequest 的**同一事务**里判定 `decideCommentBinding`——显式目标必须是当前 run 的活动 Agent NodeRun 且已有 session；恰好一个活动且已绑 session → 立即绑定；多个活动或唯一活动尚未绑 session → `queued` 不绑；零活动 → `pending_phase` 不绑。绑定一旦落库不可改投：重试携带不同 `expected_node_run_id` 返回 `ErrCommentBindingConflict`，显式目标不在活动集返回 `ErrCommentTargetNotActive`（事务回滚，不留下半条留言）。legacy 任务没有 workflow run，保持原 `queued` 行为。
- 收敛（D3）：`reconcileWorkflowCommentsAfterNodeRun` 在 NodeRun 终态后运行。多分支收束到唯一活动节点时把未绑定 `queued` 行改绑到它；收束到刚结束的唯一 Agent 节点且结果为 success 时改绑到该节点；绑定节点非成功结束、任务终态或绑定目标已不再活动 → `blocked`，绝不转发到之后启动的节点；`pending_phase` 永远保持不绑定（新阶段启动属 Task 7 的 comment trigger）。
- 投递：绑定节点成功后按 `created_at, request_id` FIFO 投递到提交时冻结的那个 `session_key`；`SendWorkflowComment` 是普通续聊回合，**不附加** JSON envelope 契约、不解析结果。首条发送失败标记 `delivery_failed` + attention，后续队列全部 `blocked` 保证不再乱序；sender 未接线或正文为空同样 fail closed。已投递行 `consumed`。
- 持久投影：`continue_requests` 是 canonical，`outbox_items` 只是它的副本。留言正文/作者在 `insertContinueReq` 一次写入；三处 outbox 写入（首次、绑定更新、状态更新）都用 `COALESCE((SELECT message FROM continue_requests …),'')` 复制正文，避免投影是空消息行。FIFO 用例用 `OutboxService.GetRequest` 断言 `Message == 原正文` 且 `node_run_id/session_key` 与冻结绑定一致；把 `insertContinueReq` 的 message 改成空串后该断言变红，恢复后绿。
- HumanGate（D4）：`ResolveWorkflowHumanGate` 只接受传输层的专用 approve/reject；普通留言在 gate 等待时最多形成 `pending_phase` 留言，不会改变 gate 状态。approve → `success` + `human_approved` + 立即推进下游；reject → `cancelled` + `human_rejected`，任务取消并 `BlockWorkflowCommentQueue`，下游节点不会启动。重复同一决定幂等（`changed=false`），相反决定返回 `ErrWorkflowHumanGateAlreadyDecided`；状态读取在事务内完成，避免 `MaxOpenConns(1)` 下自锁。
- 账本：`human_waiting` / `human_approved` / `human_rejected` 是 typed append-only 事件，gate 投影在同一事务内分别写成 waiting_user / success / cancelled。HTTP 新增 `POST /api/tasks/{id}/human-gate`（`root_id/node_id/decision/comment_id`，409 表示已决定或不在等待，404 表示 NodeRun 不存在）。
- 测试：`workflow_comments_test.go` 覆盖单活动精确绑定与不可变、多活动排队后收束、零活动 `pending_phase` 不被后来节点认领、终态任务不复活、FIFO 同 session 顺序投递、gate 普通留言不批准、approve/reject 一次性与幂等、非法决定保持等待；`http_task_human_gate_test.go` 覆盖路由层「普通留言不批准 + approve 幂等 + 相反决定 409」；四个既有 outbox 用例补充了绑定/after-commit 断言（confirmed/unknown 保留绑定、FIFO 全部投到同一 session、幂等重试读回同一绑定）。
- RED 记录（如实）：本包的实现与测试同批落地，没有伪造先红后绿。用三次变异证明新守卫确实会红：删掉重试目标冲突检查 → 不可变用例红；把重复 approve 的幂等分支短路 → gate 用例红；把 409 映射改成 400 → 路由用例红；恢复后 `go test ./server/internal/kanban ./server/internal/api ./server/app` 全绿。
- 未做：HumanGate 的前端审批按钮（Task 8 的 API/UI 纵向验收）；`pending_phase` 留言触发新执行阶段（Task 7 driver triggers）；steer 真实回执路径本轮未接生产 adapter，outbox 既有「无回执不确认、unknown 不重放」语义保持不变。

### Task 6：Loop bounded execution and graph editing freeze

**Files:** Modify `workflow_engine.go`, `task_store_workflow_runs.go`, validator as needed; tests in `workflow_test.go` and `workflow_executor_test.go`.

**Interfaces:** Consumes resolved D5/D6 output contract; produces deterministic flattened inner-node identifiers and `LoopState` transitions persisted with the run.

- [x] Step 1: Add failing test for iteration 1 complete → iteration increments exactly once and new inner NodeRun key uses loop path/iteration.
- [x] Step 2: Add restart test between inner node completion and next iteration scheduling; assert no repeated inner attempt and no missed increment.
- [x] Step 3: Add maximum bound test: 1..20 respected; after final non-exiting pass, state is waiting_user and prior output remains queryable.
- [x] Step 4: Add edit freeze test: graph edits to triggered node and edges incident to triggered node conflict; future untriggered subtree remains editable under revision CAS.
- [x] Step 5: Run focused tests to verify RED.
- [x] Step 6: Implement flatten/iteration ownership and CAS-safe graph validation; implementer may not silently change loop semantics from spec.
- [x] Step 7: Run kanban tests; Expected: PASS.

**Task 6 执行结果（2026-11-10）**：
- 语义（spec §Loop 容器）：`max_iterations` 必填 1–20、`exit_expression` 必填并复用安全表达式、内部 DAG 必填；禁止嵌套 Loop。未达到退出条件且已到上限 → Loop 容器 `waiting_user`、任务投影 `waiting_user`，每轮输出留在各自 NodeRun 里，不静默成功。
- 投影边界：内部节点是**逐轮事实**，只写 `workflow_node_runs`（`loop_path=loopID`、`iteration`、`attempt`），永不写 run 级 `NodeStates/NodeOutputs` —— 否则第二轮会覆盖第一轮，且会遮蔽同 id 顶层节点。Loop 容器本身才写 run 级投影与 `LoopState`。
- 推进：`advanceWorkflowLoop` 每次只落一个迁移（开轮/一个内部节点/自增/退出/触顶）。开轮写 `loop_iteration`；一个内部节点按 (node_id, loop_path, iteration, attempt) 唯一键推进；全部内部节点终态后才求值退出表达式；未退出且未到上限 → iteration+1（同一函数只自增一次）；退出 → Loop `success` + 输出 `{iterations, outputs}`；内部节点 fail/unknown/waiting → 容器同态停止，不继续下一轮。
- 恢复：重启窗口由持久化 `LoopState` + NodeRun 行判定。迭代末自增前崩溃 → 下次从已持久化 iteration 重算并自增一次；自增后崩溃 → 直接进入下一轮，不重跑上一轮。测试 `TestWorkflowLoopRestartBetweenIterationsDoesNotRepeatOrMissIncrement` 钉住「恰好一个新 NodeRun」。
- 表达式作用域（D6）：条件与退出表达式都读 `{node_id: {output: ...}}`；Loop 内求值时把 run 级输出与当前轮内部输出合并。内部 id 在整图（含各 Loop 内）全局唯一，由校验器强制，避免遮蔽。内部 condition 的 route 存在其 NodeRun output 上，恢复时重建 skip/join，不新增 run 级字段。
- 图编辑冻结（Task 6 Step 4）：已触发节点（`NodeStates` 非 pending）与有 NodeRun 行的节点（含 Loop 内部节点）都冻结；Loop 容器一旦有内部执行也冻结。边以 `(from,to,route)` 多重集比较，任何增删改只要触及冻结节点即 `ErrFrozenNodeEdit`。**这是比 spec「未来前沿边可调」更严格的 fail-closed v1**：当前无法证明新加的出边没有被已运行节点推理过，宁可拒绝；未触发子图与不触及冻结节点的边仍可在 revision CAS 下编辑。规则可后续放宽而不影响历史。
- 留言绑定：活动 Agent 判定递归到 Loop 内部图（`workflowAgentNodeIDs`），内部 Agent 是合法的 D3 绑定目标；node 查找同样递归（`workflowNodeByID`），投递时能拿到正确的 agent profile。活动 NodeRun 读回也带 `loop_path/iteration/attempt`，测试 `TestWorkflowLoopInnerAgentIsVisibleToCommentBinding` 钉住「内部 agent 可见、内部 notify 不可见」。
- 校验：补上 `exit_expression`/内部图必填、内部 id 全局唯一、初版明确拒绝 Loop 内 `human_gate`（审批 API 目前只寻址顶层 NodeRun，放进来会永久卡住本轮）。两个旧用例（嵌套 Loop、上限边界）按新契约补全必填参数。
- RED 与变异（如实）：四个新用例在实现前全部按预期红（runner 调用 0 次 / 边重写未报错）。实现后做了三次变异并观察红：去掉 `Iteration++` → 三个用例超步数红；让退出表达式永不生效 → 迭代数与派发次数断言红；清空冻结节点集合 → 边重写断言红；把内部 agent 判定退回顶层 → 绑定可见性用例红。恢复后 `go test ./server/internal/kanban ./server/internal/api ./server/app` 全绿，`-race` 下 Loop/冻结用例绿。
- 测试：`workflow_loop_test.go`（迭代推进与作用域、重启幂等、上限暂停且保留历史输出、内部 agent 绑定可见性、冻结节点与关联边）；`workflow_test.go`（嵌套 Loop、必填参数、内部 id 冲突）。
- 未做：并行只读分支与 `writes_workspace` writer gate（Task 7/后续）；Loop 超时/预算；Loop 内 HumanGate 的审批寻址；前端 Loop 画布编辑（Task 8 及管理面计划）。

### Task 7：Driver triggers, cancellation, startup reconciliation and explicit retry

**Files:** Modify `service_recover.go`, `service_recover_startup.go`, `service_run_moves*.go`, `service_execution.go`, `http_routes.go`, `http_workflows.go`, app wiring; tests: recovery, moves, API.

**Interfaces:** Consumes resolved D1/D7/D8/D9; produces `WakeWorkflowRun(ctx, rootID, taskID, cause)`, `CancelWorkflowRun`, `PauseWorkflowRun`, `ResumeWorkflowRun`, `CreateWorkflowNodeAttempt` (exact exported names frozen in tests).

- [x] Step 1: Test each approved trigger (`comment`, migration/RunNow/resume) starts or wakes one driver and duplicate trigger coalesces.
- [x] Step 2: Test pause blocks new claims but lets in-flight node reach terminal; resume wakes same Run.
- [x] Step 3: Test cancel stops driver, calls bounded per-NodeRun session cancellation, persists cancelled state, blocks unconsumed comments and never deletes an active worktree.
- [x] Step 4: Test startup state matrix: persisted queued-not-sent, active NodeRun with known completed session, active NodeRun with ambiguous external side effect; assert D8/D9 mapping to resume vs unknown/needs_user.
- [x] Step 5: Test explicit retry uses new attempt ID linked to original NodeRun/request and does not mutate original comment identity or overwrite evidence.
- [x] Step 6: Run tests to verify RED.
- [x] Step 7: Implement per-task single-flight driver and restart reconciliation using task-node state; no resident distributed worker and no `context.Background()` unbounded cancellation.
- [x] Step 8: Run `go test ./server/internal/kanban ./server/internal/api ./server/app`; Expected: PASS.

**Task 7 执行结果（2026-11-10）**：
- 触发面（D1）：所有已批准入口都汇入 `WakeWorkflowRun` —— `pending_phase` 留言、迁移、`Next`、`RunNow`、`Resume`、HumanGate approve、启动对账、显式 retry。每任务 single-flight（键 `rootID+"\x00"+taskID`），重复触发只把当前 driver 标为 `pending` 并让同一 goroutine 重读权威 run；driver ctx 由 `context.WithCancel(context.WithoutCancel(ctx))` 派生，HTTP/WS 请求结束不会杀掉执行步骤。
- 暂停/恢复：`PauseWorkflowRun` 只挡新 claim，不取消 driver ctx，让在途节点自然到终态；`ResumeWorkflowRun` 要求非终态、投影 `running`、写 `run_resumed` 后唤醒同一个 run。暂停中的任务也参与启动对账，但不被唤醒、任务状态不被对账覆盖（否则进程重启会留下永远 running 的孤儿行）。
- 取消：`CancelWorkflowRun` 顺序固定为「有界中断每个活动 session → 在行仍 open 时标记 cancelled → 取消 driver ctx → 投影 run/task → 阻断未消费留言」，worktree 一律不删。中断按 `workflowCancelTimeout`（默认 10s）逐行限时，SDK 不 ack 也不会拖住操作。
- 启动对账（D8/D9）：queued-not-sent、running 但无 session、running 且有可恢复 session 证据 → requeue + 唤醒；running 且 side effect 不明确 → `unknown` + needs_user，绝不自动重放。证据矩阵由 `TestWorkflowRuntimeStartupReconciliationMatrix` 钉住。
- 显式 retry：`CreateWorkflowNodeAttempt` 只接受 fail/unknown/waiting_user 的原 NodeRun（任务自身可已是终态 —— 可重试性由原行决定），新建 attempt+1 并写 `PrevAttemptID`，原行、输出和留言绑定一律不改；`workflow_run_events.workflow_event` 带 `run_id`，HTTP 新增 `POST /api/tasks/{id}/workflow-run/nodes/{nodeRunID}/retry`（404/409/400 映射）。
- D7 旧动词映射：`Next`/`RunNow`/`Pause`/`Resume`/`Cancel` 已按执行语义走 WorkflowRun；`Complete`/`Fail` 由 `finishWorkflowRunByOperator` 冻结 open 工作（有界中断、行标记 cancelled、停 driver、阻断留言）后持久化 success/fail，并分别写 `run_completed`/`run_failed`，不把历史 NodeRun 改写成假的成功。已完成的 run 不能再被 HumanGate approve 复活（未决 gate 返回 `ErrWorkflowNodeRunNotActionable`，已决 gate 仍幂等）。
- RED/变异（如实）：Comment 触发、pause/resume、cancel、启动矩阵、retry 五个用例均先实现后补测试的只有本包集成部分；对新增断言做了三次变异并观察红：把 Complete 退回 legacy `setTaskStatus` → 新用例红（canceller 调用为 0）；去掉 HumanGate 终态守卫 → 复活用例红（approve 返回 nil）；把 retry 的原行状态校验放开 → 既有 retry 用例红。恢复后 `go test ./server/internal/kanban ./server/internal/api ./server/app` 全绿。
- 未做：steer 的生产 adapter 仍未接（outbox 只在真实回执确认，unknown 不重放）；并行只读分支与 `writes_workspace` writer gate 仍未实现；取消不等待 driver goroutine 完全退出（行先变 cancelled，迟到写入是 CAS no-op），若未来需要严格 quiescence 再加显式 wait。

### Task 8：Run status/API/UI vertical acceptance

**Files:** Modify `http_workflows.go`, routes, `RunViewEntry.tsx`, `useRunViewEntry.ts`, `RunTimeline.tsx` and related i18n only as required; tests `web/tests/run-timeline-drill-in.test.ts`, API integration tests.

**Interfaces:** Consumes Task 3–7 status and NodeRun payload. Produces REST status/operations consistent with actual driver state; polling stays phase-1 unless separately approved.

- [x] **Step 1: Reuse `TestWorkflowRunAndNodeRunAreIsolatedByTaskNodeDatabase`** and add API integration: migrate task → approved explicit trigger → fake Runner finishes node → GET run/node-runs shows terminal result and Task status; legacy task remains legacy.
- [x] Step 2: Add conflict/permission/scope tests for cancel/pause/resume/retry/HumanGate endpoints as decided.
- [x] Step 3: Run frontend test asserting run view renders each persisted NodeRun and a bound session link; statuses/error messages do not claim success for unknown.
- [x] Step 4: Implement API/UI only after tests fail for missing behavior.
- [x] Step 5: Run `go test ./server/... ./cli/...`, frontend tests, typecheck, `make build-web`; expected all pass except documented baseline failures.

**Task 8 执行结果（2026-11-10）**：
- 纵向路径：`TestWorkflowRunCreateAndEditCAS` 不再手工插 NodeRun，而是迁移任务 → 设置 fake `WorkflowNodeRunner` → 等 single-flight driver 落真实 NodeRun → 通过 task-node REST 读回 `session-a` 与 run 快照；`TestWorkflowNodeRunRetryEndpointCreatesLinkedAttempt` 覆盖失败 attempt 1 → HTTP retry → attempt 2 linked/success、原行保持 fail、成功行再 retry 返回 409。`TestWorkflowTaskPauseResumeCancelMapToRunLifecycle` 覆盖 migrate → pause → resume → cancel 的状态与终态投影。
- D7 动词：新增 `TestWorkflowTaskCompleteAndFailFreezeRun`，Complete/Fail 后 REST 返回 success/fail，且同一 run 中仍在等待的 HumanGate approve 返回 409（不会复活）。kanban 侧 `TestWorkflowRuntimeCompleteAndFailFreezeOpenWork` 断言有界 session 中断、NodeRun 保持 cancelled 证据、下游节点不创建、留言 blocked、`run_completed`/`run_failed` 账本事件存在。
- 前端：`run-timeline-drill-in.test.ts` 增补 unknown 用例 —— `data-status="unknown"` 必须保留，且整份输出不得出现 `data-status="success"`；既有用例继续钉住 run id/version、节点名、输入/输出/错误、仅有 `session_key` 才出现按 NodeRun id 键控的会话跳转、空态与 i18n 键。
- 门禁：`go test ./server/... ./cli/...` 全绿（kanban 60.7s、api 19.6s、app 1.0s）；`npm test` 214 文件 / 488 用例通过、6 跳过；`npm run typecheck` 无输出；`make build-web` 成功（仅有既存 chunk >500kB 警告）。为适配新增引擎分派，两处按源码切片断言的 web 测试改为切到下一个函数边界（`worktree-finish` 的 Resume 准入顺序、`task-card-status` 的 Cancel 不看 worktree）—— 断言意图不变，避免新增分支一出现就误红。
- HumanGate 前端审批（2026-11-11 补齐）：运行视图只对「graph 节点类型 `human_gate` 且状态 `waiting_user`」显示批准/驳回，且只调用专用 `POST /api/tasks/{id}/human-gate`（`services/task/humanGate.ts`）；审批由 `useRunViewEntry` 发出，一次只允许一个决定在途，成功后重拉 run/node runs，失败只展示不重试。`run-timeline-human-gate.test.mjs` 钉住节点类型门槛、只读用法不出按钮、busy 双禁用、专用端点与 i18n 键；`RunTimeline` 仍不 import services、不 fetch，原只读/下钻契约测试继续通过。
- 未做：`go test -race` 放在 Task 9 统一跑。

### Task 9：Whole Runtime review and upstream gate

- [x] **Step 1: Add G-LE** entry for executor’s independent boundary, exact symptom, why, files, tests and anchors (G-LC 已覆盖持久层、G-LD 已覆盖 Agent 端口；执行器/driver/留言/HumanGate 需要独立组)。
- [x] **Step 2: Run race-sensitive claims/driver/outbox tests** under `go test -race`.
- [x] **Step 3: Repair upstream test baseline** — G-KN's current anchor points to ignored/untracked `web/capacitor.config.json`; re-anchor to a tracked invariant covered by G-KN or deliberately alter tracking under approved scope, then verify that group. Do not call it a permanent baseline failure.
- [x] Step 4: Run full Go suite and relevant Vitest tests; record fresh-worktree behavior. Capacitor generated config tests need a deterministic fixture or explicit skip policy.
- [ ] Step 5: Commit each authorized self-contained slice with its registered Scope; `make check-upstream` after commit. Future implementation only; this plan authorizes no commit, merge, deploy, or restart.

**Task 9 执行结果（2026-11-10）**：
- G-LE 已扩展到本轮全部实现面：driver 触发/取消/暂停/恢复/启动对账/显式 retry、Loop、D7 Complete/Fail、运行视图前端与 API；files/tests/anchors 全部按实际路径补全，锚点避开 `.gitignore` 生成物。
- `go test -race ./server/internal/kanban -run 'TestWorkflowRuntime|TestWorkflowHumanGate|TestWorkflowLoop|TestWorkflowDriver|TestWorkflowNodeRunRetry'` 通过（11.6s）。
- G-KN 的旧锚点（`web/capacitor.config.json`）已在本轮之前改为 tracked 的 `build:shell` 命令、`.gitignore:dist-shell/` 与入仓 SPM `Package.swift`；本文件与该组测试一致。
- 全量门禁：`go test ./server/... ./cli/...` 全绿；`npm test` 214 文件 / 488 用例通过、6 跳过；typecheck 通过；`make build-web` 通过。
- `make check-upstream` **当前红**：10 个 G-LE 新文件尚未提交，工具只比较 `merge-base..HEAD` 的已提交 delta，因此报「已不在 delta」。这是未提交状态导致的预期结果，不是实现被上游覆盖；任何授权的提交之后必须重跑并确认转绿。
- 独立评审（2026-11-10）提出 8 项，全部处理并补了会失败的针对性测试：
  1. **HIGH** 节点终态事务与 run 投影事务分开，投影 CAS 冲突会让已执行的 Agent 节点留下旧投影、driver 反复重跑：新增 `saveWorkflowNodeProjection`，冲突时重读 run、只重放本节点 keys（保留并发写者的字段），最多 5 次；`TestWorkflowNodeProjectionRetriesRevisionConflict` 先让另一个写者推进 revision，断言 `build` 与并发写的 `elsewhere` 同时在投影里。变异（冲突直接返回）→ 红。
  2. **HIGH** 单节点图完成时先投影任务 success、再投递留言，reconcile 看到终态把属于刚结束节点的留言 block 掉：把 `reconcileWorkflowCommentsAfterNodeRun` 移到任务终态投影之前；`TestWorkflowCommentDeliveredWhenFinishingNodeCompletesTask` 断言留言在该 session 投递后才 success。变异（恢复旧顺序）→ 红。
  3. **HIGH** driver 步首检查与 queued→running 之间有窗口，操作员 complete/fail/cancel/pause 后仍可能启动 Agent 回合：把终态/暂停闸门放进 `BeginWorkflowNodeRun` 的同一条 UPDATE（`EXISTS workflow_runs JOIN tasks`）；`TestBeginWorkflowNodeRunRefusesTerminalOrPausedTask` 覆盖 success/fail/cancelled/paused，行保持 queued 且 `started_at` 为空。变异（删掉 EXISTS）→ 红。
  4. **MEDIUM** HumanGate approve 在请求 ctx 上无界等待下游 Agent 回合：改为最多 5s 的同步等待，超时/请求取消只表示响应里暂看不到下游结果，决定已 durable；`TestHumanGateApproveDoesNotBlockOnLongDownstreamTurn` 用阻塞 runner + 150ms ctx 验证及时返回。
  5. **MEDIUM** 暂停任务的启动对账没有测试钉住：`TestWorkflowRuntimeStartupReconciliationMatrix/paused_task...` 断言 queued 行被安全重排、不为 paused 任务唤醒 driver、任务状态不被覆盖。
  6. **MEDIUM** `task-card-status` 的 Cancel 守卫被切片放宽：改为切到函数自身结束行，并新增「两个分支都不得读 worktree/session」断言；变异（在 Cancel 里读 `task.WorktreePath`）→ 红。
  7. **LOW** `ReplayWorkflowNodeStates` 无生产读取方：删除该死代码，计划里改成「投影只由终态事件同事务写入，不做额外重放辅助器」。
  8. **LOW** cancel/unknown 会清空 `started_at`，抹掉在飞回合的唯一时间证据：`transitionWorkflowNodeRun` 只在 requeue 时清 `started_at`，终态转换保留；`TestCancelAndUnknownPreserveNodeRunStartedAt` 覆盖 cancel/unknown。变异（恢复清空）→ 红。
- 修复后复跑（2026-11-10）：`go test ./server/... ./cli/... -count=1` 全绿；`go test -race ./server/internal/kanban -run 'TestWorkflow|TestHumanGate|TestBeginWorkflow|TestCancelAndUnknown' -count=1` 27.6s 通过；前端 `vitest run` 214 文件 / 488 用例通过、6 跳过；`npm run typecheck`、`make build-web` 通过。
- `make check-upstream` 仍为预期红：上述 10 个 G-LE 文件未提交，工具只比 `merge-base..HEAD` 已提交 delta；没有 commit 授权，故不宣称转绿。
- 未执行：commit / merge / push / deploy / ship / 重启任何 mindfs 服务（均需单独授权）。

**第二轮独立评审（2026-11-10，H1–H5 / M1–M6 / L1–L4）**
- **H1 已修**：`shortID` 原实现截取完整 id 前 8 个字符；NodeRun id 形如 `run-<hex>:<node>:<loop>:<iteration>:<attempt>`，同一 Run 的所有节点前缀相同，会撞进同一棵 worktree/分支。改为对完整 id 做 SHA-256 取前 8 位；`TestShortIDDistinguishesNodeRunsOfTheSameRun` 钉住（恢复截断即红）。
- **H2 已修**：取消路径与 merge 收尾路径共用每 NodeRun 收尾锁；收尾先重读行，非 `running` 直接返回，绝不补 merge。终态 CAS 与投影/留言/广播拆成 `persistWorkflowNodeTerminal` / `finalizeWorkflowNodeTerminal`，取消不再等慢收尾。`TestWorkflowNodeCancelWinsAgainstMergeFinalize` 在锁内取消后断言状态 cancelled、零 merge commit、树保留（去掉锁内重读即红）。
- **H3 已修**：`AutoMergeWorktree` 与 `FinishTaskWorktree` 在 `gitview.MergeBranch` 前取得主 checkout 的物理 writer gate，并持有到关闭完成；`TestAutoMergeWorktreeWaitsForMainCheckoutGate` 持有主树 gate 时 merge 必须等待（去掉 gate 即红）。
- **H4 已修**：留言续聊回合原用可能已失效的 `task.WorktreePath`。新增 `workflowCommentDeliveryWorkspace`：优先节点独立树 → 任务主树 → 项目根，全部不可用则 fail closed；每次发送都取同一物理 writer gate，`RuntimeRootPath` 使用解析后的真实目录。`TestWorkflowCommentDeliveryPrefersNodeWorktreeAndHoldsGate` / `...FallsBack...` / `...FailsClosed...` 覆盖（退化回旧路径或去掉 gate 即红）。
- **H5 已修**：重启恢复续 merge 原用非递归 `run.Graph.node`，查不到 Loop 内部节点会中断整个 root 对账；改用 `workflowNodeByID` 递归查找。`TestWorkflowRecoveryResumesLoopInnerAgentDone` 钉住（恢复非递归即红）。
- **M1–M4 已修**：workspace gate 键在路径非空时只按规范化路径（Abs + EvalSymlinks），避免同目录不同分支两把锁；共享主树节点在 Task worktree 目录被删时明确报错、不在主 checkout 猜路径；只剩未提交用户改动归入 `waiting_user`；Run 集成分支快照优先任务 worktree 分支、目录失效才回落主 checkout 当前分支。对应测试 `TestNormalizeWorkspaceKeyUsesPathOnlyWhenPresent` / `TestPrepareWorkflowNodeWorktreeFailsWhenTaskWorktreeDeleted` / `TestWorkflowNeedsUserMergeErrorIncludesUserChanges` / `TestSnapshotRunIntegrationBranchPrefersTaskWorktreeBranch`，均做变异验证。
- **M5 已修**：收尾错误不再早期返回；投影/activity/留言/任务投影/广播逐项执行并记录首个错误，避免 mid-failure 把已排队留言永久搁置。
- **M6 未修（明确残留风险）**：失败/取消保留的独立树目前没有单独的关闭入口；用户只能通过既有 worktree 收尾流程处理。自动回收需要先批准产品语义（何时允许删除失败现场），本轮不猜。
- 复跑（2026-11-10）：`go test ./server/internal/kanban -count=1` 全绿；`go test ./... -count=1` 全绿；`go test -race ./server/internal/kanban -count=1` 通过（116.473s）。全仓手写 Go 文件仍 ≤500 行。`make check-upstream` 仍为预期红：G-LE/G-KV 新文件未提交，工具只比较已提交 delta。




## Out of scope

- Cross-node workflow takeover or distributed leader election.
- Automatic exactly-once guarantees for external agent tools.
- Hidden heuristic routing of comments to whichever session “looks active”.
- Reusing legacy StageRun as a parallel status truth for WorkflowRun.
- Agent publishing shared workflow versions without user confirmation.

**第三轮独立评审修复（2026-11-10，CRITICAL/HIGH/LOW）**
- **CRITICAL 已修**：`RunWorkflowNodeAttempt` 原用 `defer lease.Release()` 把节点 workspace 的 writer gate 握到 finalize；收尾阶段 `reconcileWorkflowCommentsAfterNodeRun` 会为同一个 workspace 再申请同一把**不可重入** gate，driver 永久挂起且 gate 泄漏。现在外部回合返回后立即释放租约（defer 仅作 panic/早退兜底），再进入终态/投影/留言投递。新增 `TestRunWorkflowNodeAttemptDeliversCommentWithoutGateDeadlock`：走真实 `RunWorkflowNodeAttempt`，runner 在回合中投递留言，断言 10s 内完成且留言 `consumed`；去掉显式释放即红（实测 10.2s 超时）。
- **HIGH 已修**：取消会无界等待 NodeRun 收尾锁 —— 旧 finalize 在等主 checkout gate 时仍握着收尾锁，而 gate 可能被一个无关的 agent 回合长期占用。收尾改为「锁内重读 + 写 agent-done → **释放锁** → 锁外解析并等待主 gate → 再取锁重读状态 → 锁内 merge + 终态 CAS」。等待期间取消可正常拿到收尾锁并获胜；finalize 随后看到非 running 直接放弃 merge。`TestWorkflowNodeCancelWinsAgainstMergeFinalize` 改为走生产 `cancelOpenWorkflowNodeRuns`：持有主 gate 时取消必须在 2s 内返回，释放 gate 后零 merge commit、树保留；把锁挪回 gate 等待之外即红。
- **LOW 已修**：空正文留言原先会 `blockCommentRows(toDeliver[i:])`，把 FIFO 后面所有正常留言一起饿死；现在只阻塞该条并继续下一条。`TestWorkflowCommentEmptyBodyDoesNotStarveLaterComments` 钉住（恢复整批阻塞即红）。
- **LOW 已修**：`service_outbox.go` 531 行超过 500 行硬线；纯声明（状态常量、记录类型、Steer 端口/策略、OutboxService 结构）拆到 `service_outbox_types.go`（143 行），原文件 395 行，全仓手写 Go 文件均 ≤500。
- 复跑（2026-11-10）：`go test ./server/internal/kanban -count=1` 全绿；`go test ./... -count=1` 全绿；`go test -race ./server/internal/kanban -count=1` 通过（123.095s）。`make check-upstream` 仍为预期红：G-LE/G-KV 文件未提交，工具只比较已提交 delta。
- 未执行：commit / merge / push / deploy / ship / 重启任何 mindfs 服务（均需单独授权）。

**第四轮独立评审修复（2026-11-11，CRITICAL/LOW）**
- **CRITICAL 已修（第三轮修复自身引入）**：`releaseGate, prepErr = s.AcquireNodeWriter(...)` 在错误路径（含 `context.Canceled`）会把 no-op release 覆盖成 `nil`，随后的 `releaseGate()` 直接 panic；`driveWorkflowRun` 是无 `recover` 的裸 goroutine，会让整个 mindfs 进程退出。新增 `Service.acquireWorkflowMergeGate`：任何错误都返回非 nil no-op release，executor 与 restart-resume 两条路径统一使用。`TestAcquireWorkflowMergeGateReturnsUsableReleaseOnCanceledWait` 直接钉住「canceled ctx + 持锁等待 → 返回可用 release」；把 helper 改回直接返回 `(release, err)` 即红。
- **CRITICAL 次生问题已修**：gate 等待期间 driver ctx 被操作员停止/停机取消时，原逻辑会把已返回的节点按执行失败落终态（且在已取消 ctx 上写库）。现在 `workflowDriverStopped` 识别 `context.Canceled`/`DeadlineExceeded`，不再写终态：executor 用 `context.WithoutCancel` 重读当前行并返回，取消已赢则状态是 cancelled，未赢则保留 running + `node_agent_done` 让重启续 merge；startup recovery 同样不写终态、不重排，返回错误终止本轮对账。`TestWorkflowFinalizeCancelDuringMergeGateWaitDoesNotPanic` 走真实 finalize：持主 gate、写 agent-done 后用生产 `cancelOpenWorkflowNodeRuns` 取消并取消 ctx，断言不 panic、状态 cancelled、零 merge；恢复旧的直接赋值即 panic / 状态不符。
- **LOW 已修**：`workflowFinishLocks` 每个 NodeRun 一个 `*sync.Mutex` 且永不删除，长期进程单调增长。改为带 `refs` 的 `workflowFinishLock`：持有者与等待者都计数，归零才从表里摘除；不能在有人等待时删条目，否则后来者会拿到新 mutex 与旧持有者并发。`TestWorkflowFinishLockTracksWaitersAndPrunes` 覆盖等待者计数与最终清空；去掉 delete 即红。
- 复跑（2026-11-11）：`go test ./server/internal/kanban -count=1` 全绿（61.062s）；`go test -race ./server/internal/kanban -count=1` 通过（133.272s）；`go test ./... -count=1` 全绿。`workflow_review_fixes_test.go` 拆分后 474 行、新增 `workflow_lock_review_test.go` 160 行，全仓手写 Go 文件仍 ≤500。
- **独立复核（2026-11-11）未发现 CONFIRMED 问题**：复核者用 overlay 变异独立复现了旧 nil release 的 panic 与旧泄漏条目，确认 helper 三条出口均非 nil、取消分支不写终态、引用计数锁的各交错无并发漏洞、锁序统一为 gate→finish；补充 `-race -count=2` 目标集 23.412s、整包 65.450s、`go vet`/`go build ./...` 干净。两条信息性观察（取消打断 merge 时 driver 不记日志、进程死在 merge 窗口看板保持进行中直到重启续做）均为 D8/D9 既定语义，不是本轮缺陷。
- 未执行：commit / merge / push / deploy / ship / 重启任何 mindfs 服务（均需单独授权）。

**真实 Agent 端到端验收（2026-11-11）**
- 环境：`scripts/mindfs-iso.sh` 起的隔离实例（`XDG_CONFIG_HOME=/tmp/mindfs-iso/config`，端口 7431；生产 7331），真实 HOME 与真实 Claude agent（`claude`，`haiku`，effort `low`），项目根 `/tmp/mindfs-iso/proj`（root id `proj`），账户 `u_06b357b1edcb31c6`。运行前后核对：生产指纹不变、生产服务在线、隔离进程对生产配置目录零文件句柄；验收后已停止隔离实例。
- 工作流 `e2e-human-gate-1794403241-v2:v1`：`agent1 → human_gate → agent2`，两段 Agent 节点各自真实起 Claude 回合。
- 首次尝试（v1，提示词要求纯 JSON、无工具调用）落 `unknown`：`workflow node output has trailing data after JSON envelope`。根因是**环境交互**：本机全局 Stop hook（`stop-gate`）对零工具回合注入反馈，Claude 因此再回一条消息，累积文本里出现两个 envelope；D5 严格解析按设计拒绝尾部数据，未改产品解析逻辑。v1 的任务 `task_f7e17bc9fb13f00c` / `run-b182ac34452f0e95` 保留为证据。
- 第二次（v2，提示词要求先做一次无害 `Read` 再输出 envelope）全链路通过：`agent1 success`（约 17s，输出 `{"step":"first","notes_exists":true,...}`）→ gate `waiting_user`（写入 `human_waiting` attention）→ 普通留言 `c-e2e-1` 提交后**未批准** gate → 专用 `POST /api/tasks/{id}/human-gate` approve 200 → gate `success`、下游 `agent2 success`（输出 `{"step":"second","content":"hello",...}`）→ run `node_states` 三项全 `success`，任务 `success`。重复 approve 200 幂等；相反决定 reject 返回 409 `workflow human gate already decided`。活动账本按序为 agent_reply → attention(human_waiting) → 用户留言 → `human:approve` system_event → agent2 reply。
- 留言路径的已知边界（不是本轮新缺陷）：在 gate 等待、零活动 Agent NodeRun 时提交的普通留言按 D3 落 `pending_phase`，本轮任务随后走完并 `success` 后该留言仍为 `pending_phase`。Task 5 已记录「`pending_phase` 留言触发新执行阶段」尚未实现，本次验收**不覆盖**该路径。
- 提交后 `make check-upstream` 转绿：baseline `9ab95722`（upstream/main），1783 个 delta 文件被 286 组完整覆盖；此前「预期红」确为未提交状态所致，实现未被上游覆盖。
- 本次只提交文档记录（`55b27021`）；未执行 merge / push / deploy / ship / 重启任何 mindfs 服务。
