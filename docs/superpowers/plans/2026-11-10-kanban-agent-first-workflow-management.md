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

**Decision gate:** 用户已决定：共享 WorkflowVersion 发布必须人类确认准确 revision。仍待确认 D12 仅包括工具入口和可信 Agent identity/授权机制；需遵守项目 no-REST-auth 规则，不可将 `user=` / `author` 当凭证。若无可信身份通道，首版 Agent 只输出 typed patch proposal，由人类 UI 导入、校验、保存。

CLI 实际位于根 module，不能直接 import `server/internal/kanban`。若批准 CLI façade，必须经本机/所属节点受控 HTTP route；payload limits、run/root ownership validation、CAS/frozen node enforcement 必须在 server 端做，不能只在 CLI 校验。

## Files / ownership

- `server/internal/kanban/workflow_store.go`、`task_store_workflow_runs.go`：复用 domain mutation/validation/CAS，不接触协议或 CLI IO。
- `server/internal/kanban/workflow_agent_ops.go`（新）：纯领域 patch 操作、diff 和 dry-run；不调用 shell/agent runner。
- `server/internal/api/http_workflows.go` (local API boundary): scope/resource validation, payload limits, CAS delegation; **no REST authentication** and no inference of trust from `user=`/`author`.
- Selected Agent tool/CLI adapter: typed call to the local HTTP façade; root `cli/cmd` must not import `server/internal/kanban` directly.
- Tests: `server/internal/kanban/workflow_agent_ops_test.go`, `server/internal/api/http_workflows_test.go`, selected CLI tests.

## Interfaces

- `WorkflowPatch`：带 `BaseRevision` 和 typed operation list（AddNode/UpdateFutureNode/RemoveFutureNode/AddEdge/RemoveFutureEdge），每个 operation 有 stable operation ID；禁止任意 JSON path set。
- `ApplyWorkflowDraftPatch(ctx, workflowID string, patch WorkflowPatch) (WorkflowDraft, WorkflowDiff, error)`：一次 CAS 更新；校验失败不写入；CAS stale 返回 current revision。
- `ApplyWorkflowRunPatch(ctx, rootID, taskID, runID string, baseRevision int, patch WorkflowPatch) (WorkflowRun, WorkflowDiff, error)`：仅修改未触发部分；冻结检查覆盖 node 和 edge。
- `ValidateWorkflowPatch(ctx context.Context, base WorkflowDefinition, patch WorkflowPatch) (WorkflowDiff, []ValidationIssue, error)`：纯校验，不落库、不执行 agent。
- `ProposeWorkflowPublish(ctx, workflowID, revision string, proposer string) (PublishProposal, error)`：创建提案记录；真正 publish 仍走现有 `PublishDraft(confirmed_revision)`，由授权 human action 调用。
- 上述名字为计划契约；若既有代码中已有同义接口，实施时优先复用并在 RED test 阶段锁定最终名称。

## Tasks

### Task 1：Typed patch 与纯 diff/validation

**Files:** Create `server/internal/kanban/workflow_agent_ops.go` and `workflow_agent_ops_test.go`; tests must inspect existing `ValidateWorkflowDefinition`/`EvalSafeExpression` grammar before adding expression operations; if D6 changes path roots, include `workflow_validate.go`, `web/src/shared/workflowModel.ts`, and their mirrored tests.

- [ ] Step 1: 写 `TestValidateWorkflowPatchRejectsUnknownKindAndDanglingEdge`，断言 issue 包含 operation ID 与 field，不修改 base graph。
- [ ] Step 2: 写 `TestWorkflowPatchDiffIsStableAndOrdered`，同输入多次 diff 字节等价，排序按 operation order/node ID。
- [ ] Step 3: `go test ./server/internal/kanban -run 'WorkflowPatch|ValidateWorkflowPatch'`; Expected RED。
- [ ] Step 4: 实现 typed operations、边界限制（payload/node count 按批准值）、确定性 diff 和 validation；无 eval/shell。
- [ ] Step 5: 再跑 targeted tests；Expected PASS。

### Task 2：Draft CAS patch + publish proposal

**Files:** Extend `workflow_agent_ops_test.go` (created in Task 1), modify `workflow_store.go`; expose through the D12-approved local HTTP façade used by the selected Agent tool/CLI bridge. The bridge calls the local API (root `cli/cmd` cannot import `server/internal/kanban` directly).

- [ ] **Step 1: Reuse `TestDraftCASAndPublishRevisionPin`** in `workflow_test.go`; extend the assertion to require proposal creation not to create a WorkflowVersion.
- [ ] **Step 2: Add `TestStalePublishProposalCannotConfirmNewRevision`**: a proposal for old Draft revision cannot publish the current revision; conflict is explicit and no fallback occurs.
- [ ] **Step 3: Verify RED for proposal-specific behavior**; retain existing Draft CAS test as GREEN regression coverage.
- [ ] **Step 4: Implement proposal creation without publishing**; actual human confirmation uses `PublishDraft(ctx, workflowID, confirmedRevision, publishedBy)` with the string Draft revision.
- [ ] **Step 5: targeted kanban/API tests**; Expected PASS.

### Task 3：WorkflowRun future-graph patch

**Files:** Modify `task_store_workflow_runs.go` / graph edit validation; tests `workflow_agent_ops_test.go`, `task_store_workflow_runs_test.go`; extend existing `TestRunRevisionCASAndFrozenNodes` in `workflow_test.go`.

- [ ] **Step 1: Add failing edge-freeze tests**: edit/delete an edge incident to triggered NodeRun must conflict; stale base revision must conflict. Reuse `TestRunRevisionCASAndFrozenNodes` for node freeze/CAS.
- [ ] **Step 2: Implement graph+edges complete freeze validation and patch CAS**, retaining snapshots of triggered node definitions.
- [ ] **Step 3: Run existing and new tests**; Expected: existing node/CAS tests green, new edge tests pass.

### Task 4：Dry-run 仅静态模拟

**Files:** `workflow_agent_ops.go`, tests。

- [ ] Step 1: 写 `TestWorkflowDryRunNeverInvokesRunnerOrWritesTaskState`，fake runner 调用计数为 0，DB run/node/task 行与调用前一致。
- [ ] Step 2: 覆盖 Condition route、skipped join、Loop budget、HumanGate stop 和 Notify “would notify” 描述。
- [ ] Step 3: targeted tests RED。
- [ ] Step 4: 实现 dry-run evaluator 只执行纯表达式与既有图算法，返回可解释模拟 trace；不得真实发 Notify 或启动 Session。
- [ ] Step 5: targeted tests PASS。

### Task 5：Agent surface + scope/authorization tests

**Files:** 根据 D12 选定入口修改 CLI/http/tool adapter；`http_workflows_test.go` / CLI tests；注册 G-LD。

- [ ] **Step 1: Write scope tests** for missing root/task, mismatch between `root_id` and the Task's actual data-plane store, and a Run ID belonging to another Task in that same store. Do not write cross-account caller-authorization tests: repo policy explicitly says REST/WS do not authenticate `user=`.
- [ ] **Step 2: Add payload-size, node-count and invalid-operation RED tests** at the server HTTP/service boundary; CLI-side limits are defense in depth only.
- [ ] Step 3: 只实现 typed façade，所有路径转入前述服务接口；HTTP 不信任请求体 `author` 来授予 human publish 权限。
- [ ] Step 4: 添加前端可呈现的结构化 diff / validation issue 类型（如 Agent surface 需要），不得在 UI 中展示未确认的 proposal 为已发布版本。
- [ ] Step 5: `go test ./server/internal/kanban ./server/internal/api ./cli/cmd` + targeted Vitest；Expected PASS。

### Task 6：建议沉淀与发布边界

**Files:** Extend existing success-suggestion behavior in `server/internal/kanban/workflow_test.go` (`TestSuggestTemplateFromSuccess`) and add proposal persistence/API tests only if new state is required.

- [ ] **Step 1: Reuse `TestSuggestTemplateFromSuccess`**; extend it to assert a successful task creates a reviewable proposal but not a WorkflowVersion.
- [ ] **Step 2: Test human confirmation** against exact string Draft revision via existing `TestDraftCASAndPublishRevisionPin` and publish handler tests; add only missing stale-proposal coupling.
- [ ] **Step 3: Persist proposal source task/run and status** if it is a new product entity; keep suggestion non-publishing.
- [ ] **Step 4: Kanban/API tests PASS.**

### Task 7：全量审查与门禁

- [ ] Step 1: 覆盖并发 CAS、frozen edge、跨账户/跨节点边界的 targeted tests。
- [ ] Step 2: `go test ./server/... ./cli/...`、相关 Vitest、`make build-web`。
- [ ] Step 3: 添加 G-LD customization 和 anchors/tests，提交 `Scope: G-LD`，提交后 `make check-upstream`。
- [ ] Step 4: 记录真实基线失败；不在本计划中合并/部署/重启。

## Out of scope

- Agent 自动发布共享 WorkflowVersion。
- 任意 shell/JS/eval 表达式或通过 Agent patch 直接改数据库。
- 跨节点 workflow runner、向别的账户/Task 派发、对外通知。
- 自动从成功任务发布模板；只可生成待审阅建议。
