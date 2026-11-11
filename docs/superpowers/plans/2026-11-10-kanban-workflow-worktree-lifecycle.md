# Workflow 多 worktree 生命周期接入 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. 此文仅是计划，不授权实现。

**Goal:** 将 workflow Agent NodeRun 的 workspace 声明接入现有 worktree registry、writer gate、自动合并和清理，使并行只读节点可并发、写入节点按获批 worktree 策略互斥，冲突时安全转人工处理。

**Architecture:** Workflow executor 只通过 Kanban Service 的明确生命周期端口 ensure/close/merge，不直接执行 git。节点启动前按节点参数与 D10 策略选择主工作树或独立工作树；每个 NodeRun 持久记录 worktree 身份；成功节点按策略合并，失败/取消/unknown 分别按确认后的保留语义处置。所有 merge target 在 run 创建时解析并快照，不允许运行时隐式落到字符串 `main`。

**Tech Stack:** Go module declares `go 1.25.0`, project toolchain is Go 1.26.5; `server/internal/kanban`, `gitview`, SQLite task-node DB.

**Spec:** `docs/superpowers/specs/2026-11-09-kanban-first-agent-workflows-design.md`；总路线图：`docs/superpowers/plans/2026-11-10-kanban-workflow-runtime-roadmap.md`；依赖 [Runtime Executor](2026-11-10-kanban-workflow-runtime-executor.md)。

## Global Constraints

- 先完成 WP-00 和 Runtime Executor Task 1–4，保证 NodeRun ID/state/run driver 有效。
- Gate key 按批准后的资源身份，至少同 worktree writer 串行；禁止只靠 task status 互斥。
- 不覆盖/清理 dirty 用户改动；merge conflict 保留两边 worktree/branch 并 needs-user，不自动 stash、abort 或解冲突。
- 不假定集成分支名为 `main`；分支目标必须显式解析并存入 Run snapshot。
- 单机内存 gate 不宣称跨进程互斥；重启依赖持久 NodeRun + registry 状态恢复。
- 本机服务不重启；不部署、不推送；每个改动遵守 customization / 测试 / Scope 规则。

## Review Focus

1. 同一个 task 由两个账户/Service 同时命中同一物理 worktree：不应因不同 worktree ID 绕过 writer gate。
2. 创建 worktree 后 NodeRun claim/启动失败：registry 不得留下无法识别的活跃孤儿；可幂等清理/恢复。
3. merge 成功但关闭目录失败：run 不得误标未合并，也不得重复 merge。
4. 主 checkout 当前分支不是 `main`：merge target 应是 run 中的显式集成分支，不能悄悄 merge 到 main。
5. 冲突或脏主树：两边文件保留，NodeRun/task 进入 needs-user，后续显式恢复有明确路径。

---

## Decisions / dependencies

- D10/D11/D15/D16 **已由用户确认（2026-11-10）**：
  - D10：默认共享 Task primary worktree；只有节点参数 `worktree=independent` 才独立建树；写节点默认 `writes_workspace=true`，只有显式 `false` 的节点可不取 writer gate。
  - D11：merge target 在 Run 创建时解析并快照；运行时只用 snapshot，禁止隐式落到字符串 `main`。
  - D15：app 层注入一个进程内共享 gate（跨账户 Service 共用）；durable owner 由 TaskStore 的 worktree owner 约束承担；跨进程不宣称 durable，必须显式检测/fail closed。
  - D16：`workflow_node_runs` 直接增加 worktree id/path/branch 列；活动树至多一个 NodeRun owner，同 branch 冲突拒绝且不覆盖既有 owner。
- Runtime NodeRun 是唯一节点身份来源；该 plan 不创建第二套 `node_runs` 事实表。
- 生命周期失败语义（Agent fail/unknown/cancel 时保留/关闭/合并）需与 Runtime Task 7 cancellation/recovery decision 对齐，不得自行推导。

## Files / ownership

- Modify `server/internal/kanban/service_worktree_noderun.go`：通过已实现 Ensure/Close API 提供幂等 NodeRun 生命周期。
- Modify `server/internal/kanban/service_worktree_merge.go`：传入已解析 integration branch；若禁止隐式默认，移除 `"main"` fallback，并在 workflow run 创建校验中要求 branch 已解析。
- Modify `server/internal/kanban/writer_gate.go` / `service.go`：WriterGate 当前未由 Service 持有，也无生产 Acquire caller；由 app 层决定/注入共享 gate registry，key 规范化 physical worktree path + branch。G-KU anchors/tests 如需改变，需登记互斥边界变更。
- Modify `server/internal/kanban/task_store_workflow_runs.go` or a dedicated association table：持久化 NodeRun 与 WorktreeEntry 关联；当前 workflow_node_runs schema 无 worktree_id/path，不可假设字段已存在。
- Modify `server/internal/kanban/worktree_registry.go`：同 branch 被不同活动 NodeRun 声明时明确 conflict，不可 `ON CONFLICT` 静默重写 owner。
- Modify workflow executor integration：传入 run-pinned branch 和 NodeRun↔WorktreeEntry 关联。与 Executor 子计划共享文件，须在同一集成波次指定单一文件 owner。
- Tests: `writer_gate_test.go`（保留/迁移当前不同 task key 独立获取 gate 的既有行为）、`service_worktree_multi_test.go`, `service_worktree_merge_test.go`, new `workflow_worktree_lifecycle_test.go`。

## Tasks

### Task 1：为工作流节点增加 workspace intent 和 gate RED tests

**Interfaces:** Consumes Runtime `WorkflowNodeExecution.NodeRun` + resolved D10/D11/D15/D16. Produces `AcquireNodeWriter(ctx, physicalWorktreeKey) (release func(), err error)` from the app-shared gate; final signature must be frozen by tests before implementation. Existing `WriterGate` has no production call site and is not yet a Service interface; inspect before reuse.

- [x] Step 1: 写测试：`writes_workspace=false` 只读节点不取 writer gate；按 spec 默认共享 worktree 的两个写节点串行；不同经 D10 批准的独立 worktree 可并发。
- [x] Step 2: 测试不同账户/Task Service 对同一 physical worktree 的并发请求；决策 D15 必须规定 gate owner/key 和 durable/跨进程范围。
- [x] Step 3: Existing `task_worktrees.node_run_id` already stores owner, but `UpsertWorktreeEntry` currently overwrites it on `ON CONFLICT(task_id,worktree_id)`. Add a failing concurrent same-branch test; assert second NodeRun receives owner conflict and existing row remains unchanged (no new column is assumed).
- [x] Step 4: 对比现有 `writer_gate_test.go` 的语义和 G-KU anchors；若 key/ownership 改动，明确迁移现有 tests/anchors 并注册 G-LF，不能破坏当前 documented task-scoped semantics 无说明。
- [x] Step 5: `go test ./server/internal/kanban -run 'WriterGate|Workflow.*Writer' -count=1`; Expected: RED 来自新 workflow production integration/跨 Service test，而不是重写既有已绿单测。
- [x] Step 6: 依据批准的 D15 把 gate 注入 app-shared Kanban Service dependencies；若只能单进程，跨进程必须明确不支持/检测冲突并 fail closed，不能宣称 durable per-Task semantics 已满足。

### Task 2：NodeRun start/finish 接线 Ensure/Close

**Interfaces:** Consumes Task 2 gate + Runtime `ClaimWorkflowNodeRun`. Produces lifecycle helper `PrepareWorkflowNodeWorktree(ctx, execution) (WorktreeEntry, release, error)` and `FinishWorkflowNodeWorktree(ctx, execution, outcome) error` (freeze names/signatures in failing tests after reviewing existing APIs).

- [x] **Step 1: Extend NodeRun persistence tests**: existing `NodeRun`/`workflow_node_runs` do not contain `worktree_id/path`. Add migration + scan/write/reopen test for a one-to-one NodeRun↔WorktreeEntry association (columns or association table chosen under D16).
- [x] **Step 2: Write failing prepare test**: independent Agent node claims/registers its worktree; NodeRun↔worktree association is durable and unique; same active branch requested by another NodeRun conflicts without changing registry owner.
- [x] **Step 3: Test create failure**: no Runner call occurs and NodeRun enters explicit fail/needs-user state.
- [x] **Step 4: Test success/failure/cancel/unknown close or retain semantics** exactly as resolved by D7–D9/D16; unknown must not delete a directory an agent may still be writing.
- [x] **Step 5: Run tests to confirm RED, then wire `EnsureNodeWorktree` / `CloseNodeWorktree`**; do not duplicate owner truth outside NodeRun/approved association.
- [x] **Step 6: Test repeated start/recovery** does not create a second worktree; Expected PASS.


### Task 3：成功 merge 与冲突 needs-user 路径接线

**Interfaces:** Consumes Task 2 terminal outcome + run-pinned integration branch. Produces deterministic merge result attached to NodeRun/activity.

- [x] Step 1: 测试 NodeRun success + independent worktree → `AutoMergeWorktree` 恰调用一次、目标为 run snapshot integration branch。
- [x] Step 2: 测试当前 checkout branch 非 `main` 时仍只使用 run snapshot 中显式集成分支；空分支配置必须在创建 Run 前失败，不能调用 merge。
- [x] **Step 3: Test merge conflict**: registry `needs_user`, activity includes run/node/worktree/conflict files, NodeRun/Task project waiting_user; both worktree and source branch remain.
- [x] **Step 4: Test dirty main checkout**: no stash/abort/overwrite/delete; explicit actionable error.
- [x] **Step 5: Test merge success + close failure**: record merge success durably, close can retry without invoking a second merge.
- [x] **Step 6: Add failing tests before connecting `AutoMergeWorktree`**; `IntegrationBranch` must be provided from Run snapshot and never fallback to implicit `main`.
- [x] **Step 7: `go test ./server/internal/kanban -run 'Workflow.*(Merge|Conflict|Dirty)' -count=1`; Expected PASS.


### Task 4：节点生命周期端到端集成

**Files:** Modify `service_worktree_merge.go`, `service_worktree_noderun.go`, `writer_gate.go`, `service.go`, `worktree_registry.go`, `workflow_types.go`/`task_store_workflow_runs.go` for owner persistence, and app layer for shared gate injection. Tests include existing writer/worktree tests plus new workflow lifecycle integration tests.

- [x] Step 1: Fake Agent workflow: queued→running→success→merge→close，断言 NodeRun、registry、commit target 与 Task status 一致。
- [x] Step 2: Fake merge conflict: assert both branches remain and user-attention activity is queryable.
- [x] **Step 3: Restart between NodeRun success and merge**: resume merge without re-running Agent and without duplicate merge commit.
- [x] **Step 4: Run `go test -race ./server/internal/kanban`**; Expected: PASS.
- [x] **Step 5: Check upstream boundaries** — G-KU explicitly covers only in-process task/worktree gate; G-KV's registered title/why includes NodeRun owner lifecycle and merge, so verify whether this is truly a new boundary or an extension of G-KV. Durable cross-account physical locking exceeds the existing G-KU boundary and must use G-LF; record the selected group and anchors in YAML.
- **Step 6: Future implementation commits use the confirmed Scope; this planning request authorizes no commit.**

## G-LF registration

- G-LF owns the app-shared process-local physical workspace writer gate and durable worktree owner claim; targeted files and anchors are recorded in `docs/upstream-customizations.yaml`. G-KV remains responsible for NodeRun worktree lifecycle and merge/recovery.

## Execution results（2026-11-10）

**Scope decision（Task 4 Step 5）**：NodeRun 生命周期/合并适配归入 **G-KV**（其 title/why 本已覆盖「按 NodeRun 归属创建/关闭 + 自动合并」），新增 `service_workflow_workspace.go`、`service_workflow_integration_branch.go`、`workflow_worktree_lifecycle_test.go` 与对应 anchors；跨账户进程内 gate 与 durable owner 仍是 **G-LF**（boundary 明确不承诺跨进程）；G-KU 的 task/worktree 进程内互斥语义未改。`workflow_executor.go` / `workflow_runtime.go` 的接线落在已登记的 G-LE 文件内，未另建组。

**Task 2 实际接口**（与计划草案的 `FinishWorkflowNodeWorktree` 不同）：`PrepareWorkflowNodeWorktree(ctx, store, run, node, nodeRun) (*WorkflowNodeWorkspaceLease, error)` 返回目录 + registry entry + 已绑定 worktree 列的 NodeRun + release；收尾由 `finalizeWorkflowNodeResult` 调 `MergeWorkflowNodeWorktree`，不另造第二个 close helper，避免第二份 owner 真相。

**Task 2 证据**
- `TestWorkflowNodeRunPersistsWorktreeAssociationAcrossReopen` / `TestWorkflowNodeRunWorktreeColumnsMigrateExistingDatabase`：D16 列迁移 + 重开回读。
- `TestPrepareWorkflowNodeWorktreeClaimsIndependentTreeOnce`：同一 NodeRun 重复 prepare 复用同一树与 owner。
- `TestWorkflowNodeAttemptCreateWorktreeFailureDoesNotCallAgent`：建树失败时 runner 调用数 0，NodeRun 显式失败。
- `TestWorkflowNodeAttemptBranchConflictWaitsForUserAndKeepsOwner`：同活动分支被另一 NodeRun 占用 → waiting_user，registry owner 原样保留。
- `TestWorkflowNodeAttemptRetainsIndependentTree`（failure/unknown）/ `TestWorkflowNodeAttemptRepeatCreatesNoSecondWorktree`：失败/未知保留树，重复 attempt 不建第二棵。
- cancel 保留树由 G-LE 的 `TestWorkflowRuntimeCancelIsBoundedAndPreservesWorktree` 钉住。
- 变异验证：关闭幂等复用 → claim-once 测试红；关闭 owner 冲突分类 → waiting_user 测试红；恢复后全绿。

**Task 3 证据**
- `TestWorkflowNodeSuccessMergesToRunSnapshotBranch`：目标为 Run 快照 `release/2026-11`（feature 合入 release、不在 main），恰一个 merge commit，registry merged/closed 后删目录。
- `TestWorkflowNodeMergeRequiresRunSnapshotBranch` / `TestAutoMergeWorktreeRefusesEmptyIntegrationBranch`：无快照在调用 Agent/merge 前失败，`"main"` fallback 已移除且有守卫。
- `TestWorkflowNodeMergeConflictWaitsUserAndKeepsBothSides`：NodeRun/Task waiting_user、registry needs_user、两边分支与 MERGE_HEAD 保留、无 stash。
- `TestWorkflowNodeMergeRefusesDirtyMainCheckout`：用户文件未动、无 stash、registry 保持 active。
- `TestAutoMergeCloseFailureRetriesWithoutSecondMerge`：`merged` 已持久化，重试只补关闭，仍只有一个 merge commit。
- 变异验证：去掉 merge 调用 → 3 个测试红；恢复 `"main"` fallback → 空分支守卫红；禁用关闭重试 → close-failure 测试红。

**Task 4 证据**
- `TestWorkflowNodeRestartAfterAgentDoneResumesMergeOnce`：`node_agent_done` 账本事件 + 模拟进程死在 git merge 之后；恢复路径续 merge、不重跑 Agent、无第二个 merge commit。
- `TestWorkflowRecoveryResumeMergeWakesDownstream`：重启续 merge 把 NodeRun 推进成功后必须唤醒 driver，否则下游节点永远停在未开始；先以 RED 复现（下游 NodeRun 不存在），修 `recoverWorkflowRunsOnStartup` 在 resume 分支置 wake（暂停任务不唤醒），并用禁用 wake 的变异验证会红。
- `go test -race ./server/internal/kanban -count=1`：`ok mindfs/server/internal/kanban 118.158s`，无 `DATA RACE` / `--- FAIL`。
- 变异验证：禁用重启恢复分支 → resume plan 为空、测试红。

**Known residual risk**：树已创建但 `BindWorkflowNodeWorktree` 失败时，registry 会留下活跃行；当前不删目录（可能被外部引用），靠 owner 查找恢复。未在本 tranche 做自动回收，已保留为显式风险。

**第二轮独立评审修复（2026-11-10）**
- `shortID` 改为对完整 NodeRun id 取 SHA-256 前 8 位，同 Run 不同节点不再共享 worktree/分支短名；`TestShortIDDistinguishesNodeRunsOfTheSameRun` 变异验证。
- `AutoMergeWorktree` / `FinishTaskWorktree` 在 merge 前取得主 checkout 物理 writer gate 并持有到关闭；`TestAutoMergeWorktreeWaitsForMainCheckoutGate` 钉住。
- 取消与 merge 收尾共用 NodeRun 收尾锁，锁内重读非 running 即放弃 merge；`TestWorkflowNodeCancelWinsAgainstMergeFinalize` 钉住。
- 留言续聊使用节点树 → 任务树 → 项目根的真实可用目录，并共用 writer gate；无可用工作区/无 gate 时 fail closed。
- 共享主树节点 worktree 目录被删时明确报错；Run 集成分支快照优先任务 worktree 分支；workspace gate 路径键不再被分支名拆成两把锁。
- **M6 残留风险未修**：失败/取消保留的树没有独立关闭入口，自动回收需先批准产品语义；当前依赖既有 worktree 收尾流程。

**未做**：没有提交、合并、推送、部署、ship 或重启任何服务；按用户约束，外向动作需单独授权。

**第三轮独立评审修复（2026-11-10）**
- **取消不再被主 checkout gate 拖死**：`AutoMergeWorktree` 拆成 `prepareAutoMerge`（只读解析：幂等状态、branch、mainDir、integration target）与 `executeAutoMerge`（调用方保证已持有 gate）。Workflow 收尾与重启续 merge 都改为「锁外 prepare + 等 gate → 锁内重读 running → 锁内 merge/CAS」，因此收尾锁不会被无关写者的长回合无限占用；取消仍与 merge 互斥，且取消先赢时绝不补 merge。`executeAutoMerge` 不再自行取 gate，避免非重入 gate 自锁。
- 幂等关闭路径（state 已是 `merged`、重试关闭）现在同样在 gate 内执行；已 `closed` 的行不加锁直接返回。
- 验证：`TestAutoMergeWorktreeWaitsForMainCheckoutGate` 仍绿；`TestWorkflowNodeCancelWinsAgainstMergeFinalize` 走生产取消路径，持有主 gate 时取消 2s 内返回、释放后零 merge commit；把收尾锁挪回 gate 等待之外会红。`go test ./...` 与 `go test -race ./server/internal/kanban` 全绿。

**第四轮独立评审修复（2026-11-11）**
- **gate 错误路径不得返回 nil release**：`AcquireWorkspace` 在 `ErrWorkflowWriterGateUnavailable` / identity 缺失 / `ctx.Err()` 上都返回 `nil, err`。第三轮 executor/recovery 把返回值直接赋给 no-op release，错误分支调用即 panic。新增 `Service.acquireWorkflowMergeGate` 统一返回非 nil no-op release，`MergeWorkflowNodeWorktree` / `AutoMergeWorktree` 仍走各自「错误先返回、成功才 defer」的安全形状。`TestAcquireWorkflowMergeGateReturnsUsableReleaseOnCanceledWait` 变异验证（恢复直接赋值即红）。
- **取消等待 gate 不写失败终态**：driver ctx 在 gate 等待中被取消时，按 `workflowDriverStopped` 处理 —— executor 重读当前行返回，不落 fail；recovery 不写终态也不重排，留给下次启动按 `node_agent_done` 续 merge。`TestWorkflowFinalizeCancelDuringMergeGateWaitDoesNotPanic` 走真实收尾 + 生产取消路径，断言不 panic、cancelled、零 merge。
- 收尾锁表改为引用计数并在归零后摘除（`TestWorkflowFinishLockTracksWaitersAndPrunes`），避免长期进程按 NodeRun 无限增长。
- 复跑：`go test ./server/internal/kanban -count=1`、`go test -race ./server/internal/kanban -count=1`、`go test ./... -count=1` 全绿；独立复核未发现 CONFIRMED 问题（overlay 变异复现旧 panic，gate→finish 锁序无倒置）。未提交、未部署、未重启任何服务。
