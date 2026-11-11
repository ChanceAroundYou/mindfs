# Agent-first 工作流管理 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans task by task. 本文只规划，不授权实现。

**Goal:** 为 MindFS agent 提供受控、可审计、可验证的 Workflow Draft/Run 未来子图管理工具，使 agent 能提出和修改计划，但不能绕过 CAS、已触发节点冻结或人类发布确认。

**Architecture:** 复用当前 control-plane `WorkflowStore` 的 Draft/Version CAS API 与 task-node `WorkflowRun` CAS，不引入第二份版本真相。Agent 工具层封装结构化操作（读快照→提出 patch→diff→validate→dry-run），由服务端领域 API 原子校验并保存；发布只创建 pending proposal，用户确认具体 revision 后才调用既有 publish 流程。Agent 运行权限与人类确认权限分离。

**Tech Stack:** Go module declares `go 1.25.0`, project toolchain is Go 1.26.5；现有 `server/internal/kanban/workflow_store.go`、`task_store_workflow_runs.go`、`server/internal/api/http_workflows.go`；项目现有 agent tool/CLI surface（开工前确认选定入口）；现有前端 Draft editor / workflow validation。

**Spec:** `docs/superpowers/specs/2026-11-09-kanban-first-agent-workflows-design.md` §Agent-first workflow management；总路线图：`docs/superpowers/plans/2026-11-10-kanban-workflow-runtime-roadmap.md`。

## Global Constraints

- Agent 不直接写 workflow DB/文件，不绕过 service CAS，不直接运行 shell/JS 表达式。
- Draft/Version 属控制面；WorkflowRun/NodeRun 属 Task 所在数据面；不能把 Run state 写回共享 WorkflowStore。
- 已触发节点及已触发边不可变；仅未来未触发子图允许 agent 修改。
- 发布是外部共享影响：Agent 只可提 proposal；必须由人类确认准确 `workflow_id + revision` 后发布。
- 不自动发布成功任务产出的模板；只形成可审阅建议。
- 输入通过类型/范围校验，限制 payload 与节点数；validate/dry-run 不能启动真实 Agent 或造成工作区副作用。
- 新功能须 targeted tests、upstream customization、Scope、`make check-upstream`；本计划不授权产品代码修改、提交或部署。

## Review Focus

1. 两个 Agent 并发基于同一 revision 修改 Draft：一个保存成功，另一个 CAS conflict 并返回 current snapshot，不丢更新。
2. Agent patch 删除/改写已触发 node 或 edge：拒绝且完整返回 frozen reason。
3. Agent 请求 publish revision R 后 Draft 更新至 R+1：确认旧 R 不能发布 R+1，也不能悄悄发布 current。
4. malformed/过大/未知 node kind/悬空引用的 patch：确定性 validation error，不 panic、不部分保存。
5. dry-run 含 agent node/Notify：只做静态模拟，不启动 agent、不发通知、不改 NodeRun/Task 状态。

---

## Decision gate

**D12 已确认（2026-11-11）：只读快照 + typed patch。** 用户选择复用既有本机 HTTP façade，不新增鉴权：Agent 读 `GET /api/workflows/{id}/snapshot`（draft + limits），输出 typed patch；人类在 UI 粘贴、校验、应用，保存仍走既有 Draft CAS；发布只创建 pending proposal，仍须人类确认**精确 revision**。`author` / `proposer` 只是元数据，项目 no-REST-auth 规则不变，不能从 `user=` / `author` 推断授权。

CLI 实际位于根 module，不能直接 import `server/internal/kanban`。本实现未新增 CLI 命令：Agent 直接调用本机/所属节点 HTTP route；payload limits、run/task ownership validation、CAS/frozen node enforcement 全在 server 端做，前端与 CLI 侧限制只算 defense in depth。

## Files / ownership

- `server/internal/kanban/workflow_store.go`、`task_store_workflow_runs.go`：复用 domain mutation/validation/CAS，不接触协议或 CLI IO。
- `server/internal/kanban/workflow_agent_ops.go`（新）：纯领域 patch 操作、diff 和 dry-run；不调用 shell/agent runner。
- `server/internal/api/http_workflows.go` (local API boundary): scope/resource validation, payload limits, CAS delegation; **no REST authentication** and no inference of trust from `user=`/`author`.
- Selected Agent tool/CLI adapter: typed call to the local HTTP façade; root `cli/cmd` must not import `server/internal/kanban` directly.
- Tests: `server/internal/kanban/workflow_agent_ops_test.go`, `server/internal/api/http_workflows_test.go`, selected CLI tests.

## Interfaces

- `WorkflowPatch`：带 `BaseRevision` 和 typed operation list（AddNode/UpdateFutureNode/RemoveFutureNode/AddEdge/RemoveFutureEdge），每个 operation 有 stable operation ID；禁止任意 JSON path set。
- `ApplyWorkflowDraftPatch(ctx, workflowID string, patch WorkflowPatch) (WorkflowDraft, WorkflowDiff, error)`：一次 CAS 更新；校验失败不写入；CAS stale 返回 current revision。
- `ApplyWorkflowRunPatch(ctx, taskID, runID string, baseRevision int, patch WorkflowPatch) (WorkflowRun, WorkflowDiff, error)`：仅修改未触发部分；冻结检查覆盖 node 和 edge。实施时去掉了 `rootID`：调用方已由 `taskWorkflowStore` 解析到该 root 的 store，再传 root 只会多一处可漂移的真相。
- `ValidateWorkflowPatch(base WorkflowDefinition, patch WorkflowPatch) (WorkflowDiff, []WorkflowPatchIssue, error)`：纯校验，不落库、不执行 agent、不需要 ctx。issue 类型最终叫 `WorkflowPatchIssue`，`WorkflowPatchError` 携带 `[]WorkflowPatchIssue`。
- `ProposeWorkflowPublish(ctx, workflowID, revision string, proposer string) (PublishProposal, error)`：创建提案记录；真正 publish 仍走现有 `PublishDraft(confirmed_revision)`，由授权 human action 调用。提案状态持久化在 `workflowFile.Proposals`（pending/published/stale）。
- 上述名字为计划契约；最终签名以本段与代码为准。

## Tasks

### Task 1：Typed patch 与纯 diff/validation

**Files:** Create `server/internal/kanban/workflow_agent_ops.go` and `workflow_agent_ops_test.go`; tests must inspect existing `ValidateWorkflowDefinition`/`EvalSafeExpression` grammar before adding expression operations; if D6 changes path roots, include `workflow_validate.go`, `web/src/shared/workflowModel.ts`, and their mirrored tests.

- [x] Step 1: 写 `TestValidateWorkflowPatchRejectsUnknownKindAndDanglingEdge`，断言 issue 包含 operation ID 与 field，不修改 base graph。
- [x] Step 2: 写 `TestWorkflowPatchDiffIsStableAndOrdered`，同输入多次 diff 字节等价，排序按 operation order/node ID。
- [x] Step 3: `go test ./server/internal/kanban -run 'WorkflowPatch|ValidateWorkflowPatch'`; Expected RED。
- [x] Step 4: 实现 typed operations、边界限制（payload/node count 按批准值）、确定性 diff 和 validation；无 eval/shell。限额最终取 200 operations / 500 nodes / 64 KiB per node，HTTP body 1 MiB。
- [x] Step 5: 再跑 targeted tests；Expected PASS。

### Task 2：Draft CAS patch + publish proposal

**Files:** Extend `workflow_agent_ops_test.go` (created in Task 1), modify `workflow_store.go`; expose through the D12-approved local HTTP façade used by the selected Agent tool/CLI bridge. The bridge calls the local API (root `cli/cmd` cannot import `server/internal/kanban` directly).

- [x] **Step 1: Reuse `TestDraftCASAndPublishRevisionPin`** in `workflow_test.go`; extend the assertion to require proposal creation not to create a WorkflowVersion。
- [x] **Step 2: Add `TestStalePublishProposalCannotConfirmNewRevision`**: a proposal for old Draft revision cannot publish the current revision; conflict is explicit and no fallback occurs。
- [x] **Step 3: Verify RED for proposal-specific behavior**; retain existing Draft CAS test as GREEN regression coverage。
- [x] **Step 4: Implement proposal creation without publishing**; actual human confirmation uses `PublishDraft(ctx, workflowID, confirmedRevision, publishedBy)` with the string Draft revision。实现偏差：确认走 `ConfirmWorkflowPublishProposal`，内部复用同一把 `mu` 下的 `publishDraftLocked`，版本写入与提案状态一次落盘；确认过的提案重复确认返回已记录版本（幂等），草稿已前进的提案标 `stale` 并拒绝。
- [x] **Step 5: targeted kanban/API tests**; Expected PASS。

### Task 3：WorkflowRun future-graph patch

**Files:** Modify `task_store_workflow_runs.go` / graph edit validation; tests `workflow_agent_ops_test.go`, `task_store_workflow_runs_test.go`; extend existing `TestRunRevisionCASAndFrozenNodes` in `workflow_test.go`.

- [x] **Step 1: Add failing edge-freeze tests**: edit/delete an edge incident to triggered NodeRun must conflict; stale base revision must conflict。Reuse `TestRunRevisionCASAndFrozenNodes` for node freeze/CAS。
- [x] **Step 2: Implement graph+edges complete freeze validation and patch CAS**, retaining snapshots of triggered node definitions。冻结集合抽成 `workflowRunFrozenNodes`，同时覆盖 NodeRun 行与 Loop 内部节点的 `LoopPath`；`EditWorkflowRunGraph` 与 typed patch 共用它。
- [x] **Step 3: Run existing and new tests**; Expected: existing node/CAS tests green, new edge tests pass。

### Task 4：Dry-run 仅静态模拟

**Files:** `workflow_agent_ops.go`, tests。

- [x] Step 1: 写 `TestWorkflowDryRunNeverInvokesRunnerOrWritesTaskState`，fake runner 调用计数为 0，DB run/node/task 行与调用前一致。实现偏差：dry-run 是纯函数，构造上就不持有 Runner/Service/Store，因此测试改为断言输入图/输出 map 未被修改 + API 测试断言 Run 的 revision/NodeStates 不变；没有 fake runner 可计数。
- [x] Step 2: 覆盖 Condition route、skipped join、Loop budget、HumanGate stop 和 Notify “would notify” 描述。
- [x] Step 3: targeted tests RED。
- [x] Step 4: 实现 dry-run evaluator 只执行纯表达式与既有图算法，返回可解释模拟 trace；不得真实发 Notify 或启动 Session。复用 `EngineReadyNodes` 与 `evaluateWorkflowConditionRoot`，条件路由沿用生产契约 true→`pass` / false→`fail`。
- [x] Step 5: targeted tests PASS。

### Task 5：Agent surface + scope/authorization tests

**Files:** 根据 D12 选定入口修改 CLI/http/tool adapter；`http_workflows_test.go` / CLI tests；注册 G-LG。

- [x] **Step 1: Write scope tests** for missing root/task, mismatch between `root_id` and the Task's actual data-plane store, and a Run ID belonging to another Task in that same store。Do not write cross-account caller-authorization tests: repo policy explicitly says REST/WS do not authenticate `user=`。落地：缺 `root_id` → 400、任务无 workflow run → 404 在 `TestTaskWorkflowRunAgentPatchScope`；跨 Task run ID 在 domain `TestWorkflowRunPatchRejectsFrozenEdgeAndStaleRevision` 断言 `ErrWorkflowRunNotFound`。
- [x] **Step 2: Add payload-size, node-count and invalid-operation RED tests** at the server HTTP/service boundary; CLI-side limits are defense in depth only。
- [x] Step 3: 只实现 typed façade，所有路径转入前述服务接口；HTTP 不信任请求体 `author` 来授予 human publish 权限。新路由：`GET /api/workflows/{id}/snapshot`、`POST /api/workflows/{id}/patch/validate|apply`、`POST /api/workflows/{id}/publish-proposals`、`POST /api/workflows/publish-proposals/{id}/confirm`、`POST /api/workflows/dry-run`、`POST /api/tasks/{id}/workflow-run/patch/validate|apply`、`POST /api/tasks/{id}/workflow-run/dry-run`、`GET /api/tasks/{id}/workflow-run/suggestion`。Draft/提案/dry-run 属控制面（`/api/workflows` 在 worker 上 403）；task run 路由按既有分类留在数据面，必须带 `root_id` 且经 `taskWorkflowStore` 校验任务归属。
- [x] Step 4: 添加前端可呈现的结构化 diff / validation issue 类型（如 Agent surface 需要），不得在 UI 中展示未确认的 proposal 为已发布版本。落地在 Draft editor 的 patch 面板：粘贴 JSON → validate/apply，展示 `workflow-patch-issues` 与 `workflow-patch-diff`；画布 dirty 时拒绝导入；粘贴内容必须自带 `base_revision`，缺失即拒绝（fail closed），不由前端补当前 revision。
- [x] Step 5: `go test ./server/internal/kanban ./server/internal/api ./cli/cmd` + targeted Vitest；Expected PASS。

### Task 6：建议沉淀与发布边界

**Files:** Extend existing success-suggestion behavior in `server/internal/kanban/workflow_test.go` (`TestSuggestTemplateFromSuccess`) and add proposal persistence/API tests only if new state is required.

- [x] **Step 1: Reuse `TestSuggestTemplateFromSuccess`**; extend it to assert a successful task creates a reviewable proposal but not a WorkflowVersion。落地为 `GET /api/tasks/{id}/workflow-run/suggestion`，返回 `Published:false` 的 `SuggestTemplateFromRun`；未新增自动持久化实体。
- [x] **Step 2: Test human confirmation** against exact string Draft revision via existing `TestDraftCASAndPublishRevisionPin` and publish handler tests; add only missing stale-proposal coupling。
- [x] **Step 3: Persist proposal source task/run and status** if it is a new product entity; keep suggestion non-publishing。判断：发布提案不是模板建议，而是 Draft revision 的审阅实体，因此持久化 `WorkflowPublishProposal`（workflow id / revision / proposer / status / version）；模板建议仍不落库。
- [x] **Step 4: Kanban/API tests PASS。**

### Task 7：全量审查与门禁

- [x] Step 1: 覆盖并发 CAS、frozen edge、跨账户/跨节点边界的 targeted tests。
- [x] Step 2: `go test ./server/... ./cli/...`、相关 Vitest、`make build-web`。
- [ ] Step 3: 添加 G-LG customization 和 anchors/tests（已完成），提交 `Scope: G-LG`，提交后 `make check-upstream`。**未执行**：提交未获授权；`make check-upstream` 只比较已提交文件，现在跑会因新文件不在 delta 内而红。
- [x] Step 4: 记录真实基线失败；不在本计划中合并/部署/重启。

## Execution record（2026-11-11）

- **新增文件**：`workflow_agent_ops.go`（纯 typed patch/validate/diff）、`workflow_agent_draft.go`（Draft patch + proposal）、`workflow_agent_dryrun.go`（纯静态模拟）、`task_store_workflow_patch.go`（Run 未来子图 CAS）、`http_workflow_agent.go`（本机 HTTP façade）。
- **修改文件**：`workflow_store.go`（`publishDraftLocked` 拆出，供提案确认原子复用）、`task_store_workflow_runs.go`（复用 `workflowRunFrozenNodes`）、`http_routes.go`（10 条路由）。
- **测试**：`workflow_agent_ops_test.go`、`http_workflow_agent_test.go`、`web/tests/workflow-editor-board-entry.test.mjs`（patch 面板/controlPath/结构化 issue）。
- **验证（2026-11-11 复跑）**：
  - `go test ./server/... ./cli/... -count=1` 全绿（kanban 74.2s、api 20.5s）。
  - `go test -race ./server/internal/kanban -count=1` 绿（133.7s）。
  - `npm test`：214 个测试文件通过 / 5 跳过，488 tests 通过 / 6 跳过。
  - `npx tsc -b --noEmit` 绿；`make build-web` 绿（46.6s）。
  - targeted `workflow-editor-board-entry.test.mjs` 绿。
  - 变异验证：移除 stale-proposal 守卫 → `TestStalePublishProposalCannotConfirmNewRevision` 红（status 仍 pending）；移除 frozen-edge 守卫 → `TestWorkflowRunPatchRejectsFrozenEdgeAndStaleRevision` 红（未拒绝冻结边）；恢复后两测试绿。
  - `make build` 绿（含 web dist + `go build ./cli/cmd`）。
  - `make check-upstream` **未跑绿**：工具只比较已提交文件，G-LG 新文件尚未提交，按预期会报不在 delta；需授权提交后重跑。
- **独立评审修复（2026-11-11，第二轮）**：
  - **MEDIUM 1（重复提案/版本覆盖）**：`ProposeWorkflowPublish` 对同一 `(workflow, revision)` 返回已有 pending（否则已有 published）提案，不再重复建；`publishDraftLocked` 对同一 revision 的既有版本幂等返回，不同 revision 的覆盖直接报错。新增 `TestDuplicatePublishProposalAndRepublishStayImmutable`，并扩展 API 用例断言重复提案同 ID、重复确认不改 `PublishedBy`。变异验证：关闭去重 → 重复 ID 失败；关闭版本守卫 → 确认方覆盖原发布者。
  - **MEDIUM 2（dry-run 忽略运行态）**：`WorkflowDryRunInput` 增加 `NodeStates/SelectedRoutes`；API 从持久化 Run 合并 `NodeStates/SelectedRoutes/Outputs/LoopState`，请求只补未运行节点的输出；只有空/pending 节点会被输出标成 success，已失败/已完成节点不被改写。dry-run 的节点级 issue 改用 `NodeID`，不再占用 `OperationID`。新增 `TestWorkflowDryRunHonorsPersistedRunState`、`TestWorkflowDryRunReportsNodeScopedIssues` 与 API 层「已完成节点不重放、合并输出后 done」断言。变异验证：去掉 NodeStates 播种 → 已完成无输出节点被重放；去掉 API 合并 → 节点 a 被重放。
  - **MEDIUM 3（提案不可发现）**：新增 `GET /api/workflows/{id}/publish-proposals`；前端 Draft editor 增加提案面板、刷新与逐条确认（dirty/非 pending 时禁确认，确认文案钉住精确 workflow+revision），全部走 `controlPath`。扩展 `workflow-editor-board-entry.test.mjs` 钉住 controlPath 与 pending-only 守卫。
  - **LOW**：请求体改为整包读取后限长（含尾随空白）→ 413；快照/建议/dry-run/列表的存储错误区分 404 与 500；`newPublishProposalID` 处理 `rand.Read` 错误。LOW 4/10 复核为不成立：run patch 已把 `ErrWorkflowRunNotFound` 映射 404，当前文件不存在重复 if/else。
  - 新增测试放入 `workflow_agent_review_test.go`，避免测试文件超过 500 行硬线。
  - 复跑：`go test ./server/internal/kanban`、`./server/internal/api`、targeted Vitest、`tsc -b --noEmit` 均绿；全量 Go 一次运行出现既有 `TestTaskHumanGateRouteRequiresDedicatedDecision` 间歇失败（单跑、`-count=20`、`-race -count=5` 均通过，与本轮改动无调用关系），记录为环境性抖动并继续观察。
- **Scope**：G-LG，已登记 `docs/upstream-customizations.yaml`（含 anchors，逐个核对存在）。
- **未执行**：commit / merge / push / deploy / ship / 重启任何 mindfs 服务。

## Out of scope

- Agent 自动发布共享 WorkflowVersion。
- 任意 shell/JS/eval 表达式或通过 Agent patch 直接改数据库。
- 跨节点 workflow runner、向别的账户/Task 派发、对外通知。
- 自动从成功任务发布模板；只可生成待审阅建议。
