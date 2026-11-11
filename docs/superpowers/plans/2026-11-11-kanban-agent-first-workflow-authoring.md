# Agent-first 工作流作者体验与参考模式迁移 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. 本文是后续实施计划，不授权产品代码实现、提交、合并、推送、部署或服务重启。

**Goal:** 基于现有 MindFS WorkflowRun、typed patch、CAS、dry-run 与发布提案，构建 Agent 先提出、用户在画布审阅和控制的工作流作者体验，并补全可表达后端已支持 Loop 的编辑器交互。

**Architecture:** 不重建执行器、版本库、提案服务或画布技术栈。Agent 读取当前 Draft/Run 快照和约束，产出自然语言说明及现有 typed `WorkflowPatch`；前端先校验、展示节点/边差异及 dry-run，再由用户逐项审阅和应用，最终仍经现有 CAS 和精确 revision 发布流程。参考项目只作为经固定版本核验的交互模式来源；代码复制须另行核对文件许可与分发义务。

**Tech Stack:** Go 1.25 module（工具链 1.26.5）、React/TypeScript、`@xyflow/react`、现有 Workflow HTTP façade、Kanban TaskStore/WorkflowStore。

**Spec:** `docs/superpowers/specs/2026-11-09-kanban-first-agent-workflows-design.md`；现有实现基线见 `docs/superpowers/plans/2026-11-10-kanban-agent-first-workflow-management.md`、`docs/superpowers/plans/2026-11-10-kanban-workflow-runtime-executor.md` 及 `docs/superpowers/plans/2026-11-10-kanban-workflow-worktree-lifecycle.md`。

## Global Constraints

- 复用现有 WorkflowDefinition、WorkflowPatch、Draft/Run CAS、服务端校验、dry-run 和 proposal API；不得建立第二套图、版本或发布真相。
- Agent 是作者协作者，不是发布者：生成只读提案；不可直接写数据库、绕过 typed patch/CAS、修改已触发节点/边或自动发布。
- 草稿提案应用必须绑定生成时 `base_revision`；revision 已变化时 fail closed，显示冲突并要求重取快照/重生成，不做 last-writer-wins 或隐式重基。
- 应用前展示自然语言说明、完整节点与边 diff、校验结果、dry-run trace；局部接受/拒绝不得产生未审阅的隐式变更。
- dry-run 只能静态模拟，不启动 Agent/session、Notify、worktree 或外部副作用。
- 首版 Loop 不支持嵌套 Loop 和 Loop 内 HumanGate；UI 必须明确表达限制并复用服务端权威校验。
- 首版保留 queue-only；普通 `SendMessage` 不能伪装成 steer 或确认回执。
- REST/WS 不新增鉴权；`user=`、`author`、`proposer` 不是授权凭证。Agent-first UI 不暗中提高 API 权限。
- 参考实现默认只借鉴模式；若提议复制源码/样式，须先记录仓库、固定 commit、文件范围、许可证及 MindFS 分发影响，并经用户另行批准。
- 每个代码阶段使用 targeted failing tests、必要的变异验证、独立审查、upstream customization/Scope 登记及门禁；本计划不授权提交、合并、推送、ship、部署或服务重启。

## Review Focus

1. Agent 在生成期间 Draft/Run 被另一操作者更新：旧提案必须显示 stale 并拒绝应用，不丢失对方变更。
2. 部分接受提案涉及节点和关联边：拒绝节点时不得留下悬空边、改变未选择项或绕过图校验。
3. Agent 返回 malformed/超限/未知操作、重复 operation ID、非法 Loop 子图或表达式：用户看到可定位错误，图和 revision 完全不变。
4. Loop 子图拖放、缩放、删除容器及导入 patch：子节点归属和跨边界连接保持合法，保存/重开结构一致。
5. 含已触发运行前缀的 Agent 提案或并发运行变化：只能修改允许的 future graph；冻结检查由后端负责，dry-run 不改变运行状态。

---

## 当前基线与参考实现（已核查，供实施者使用）

### MindFS 现状

- `web/src/components/task/WorkflowCanvas.tsx` 已使用 `@xyflow/react`，支持节点/边编辑、节点参数检查器、校验错误列表、dirty 标记、beforeunload 与移动端只读展示；Loop 检查器目前仅编辑 `max_iterations`，没有内部 DAG/退出表达式编辑器。
- `web/src/shared/workflowModel.ts` 定义五类节点；Loop 新建时已有 `max_iterations=5`、`exit_expression=""`，校验覆盖 1–20 与禁止嵌套图，但前端尚未呈现完整的 Loop 编辑体验。
- `web/src/components/task/WorkflowDraftEditor.tsx` 和 `useWorkflowDraftEditor.ts` 已承载 Draft API、Agent typed-patch 粘贴/validate/apply、proposal 列表/精确 revision 确认；后续应把自然语言作者流程接到这条既有边界，不另造后台服务。
- 后端 `/api/workflows/{id}/snapshot`、typed patch、validate/apply、dry-run、publish proposals 与 suggestion API 已存在；Run patch 只允许未触发部分并做 CAS/frozen 校验。
- Runtime 已实现 bounded Loop 执行；Loop 内 HumanGate 与嵌套 Loop 明确拒绝。Driver 当前 serial dispatch ready node；并行不是本计划首发目标。

### 已核查的参考模式（实施前仍须对本地固定版本重新核对）

- **Langflow**（MIT，核查 commit `71b28b924050c95a244227d5a1424f2495014e14`）：`src/frontend/src/components/core/assistantPanel/assistant-flow-preview.tsx`、`assistant-flow-edit-card.tsx`、`helpers/apply-flow-proposal.ts`、`helpers/apply-flow-update.ts` 展示 assistant proposal/preview/apply；适合参考“对话提出增量变更、预览后应用、校验事件”的交互，不提供 MindFS 的 CAS/发布治理保证。
- **Flowise**（核心 Apache-2.0；enterprise 子目录含商业许可边界，核查 commit `9291856d1ea4a4ceea9f8fef8ce14f4f6c81e8eb`）：`packages/agentflow/src/features/generator/GenerateFlowDialog.tsx` 与 `packages/server/src/services/agentflowv2-generator/index.ts` 展示 prompt-to-nodes/edges 草案；`packages/agentflow/src/features/canvas/containers/IterationNode.tsx`、`hooks/useDragAndDrop.ts` 展示 iteration 容器和子节点落点约束。只借鉴生成/容器交互，不能假设生成结果安全或可直接执行。
- **Dify**（modified Apache 2.0，有额外条件，核查 commit `b39a44c0e1bfda0da36c3bfcfa9ee13ef0495702`）：`web/app/components/workflow/workflow-generator/`、`graph-diff.ts`、`apply.ts` 可参考提案差异和 stale draft 处理；差异实现明确不包含完整 edges/layout，MindFS diff 必须自行覆盖节点、边、参数、Loop 子图和位置。Loop 子图交互见 `web/app/components/workflow/nodes/loop/use-interactions.ts`。
- **xyflow**（核心 MIT，项目已在用；核查设计 pin `1bc405cd113618c17fd181b931a9fe573bec9fee`）：`examples/react/src/examples/Subflow/index.tsx` 演示 `parentId`/`extent` 嵌套容器原语；它不定义 MindFS 图语义或运行时。
- **Kandev**（AGPL-3.0，设计 pin `36476ce8641da64df9e9bea509dbec2b6b818893`）：可参考 issue/任务协作和 step pipeline 思路；已检查的相邻 revision `a268e5c409049579576192814b347ffc61b2a4c9` 中 `apps/web/components/settings/workflow-pipeline-editor.tsx` 是排序步骤编辑器，不是任意 DAG/Loop 画布。不得把该相邻 revision 的代码细节冒充设计 pin 已核验结论。

上述记录只证明指定路径/版本里存在相关代码模式，不等于功能完整性、稳定性或许可意见。任何真正移植必须固定源 commit 并逐文件审查 LICENSE/NOTICE、依赖许可与修改/分发义务。

### Task 0 reference audit（2026-11-11，固定 commit 重核）

核查方式：对下列固定 commit 直接读取源文件与仓库 LICENSE（本地留档于执行 workspace），不依赖 README 或相邻 revision。结论全部为「仅借鉴交互/概念」，本计划不复制任何上游源码。

| # | MindFS 缺口 | 已核验来源（commit / 文件） | 处置 |
|---|---|---|---|
| 1 | 自然语言目标输入、生成中/错误/建议提示 | Flowise `9291856d` `packages/agentflow/src/features/generator/GenerateFlowDialog.tsx`：prompt + model 选择 + suggestion chips + loading 禁用关闭 + error alert | 借鉴布局与状态，不复制组件 |
| 2 | Agent 输出必须是可校验的结构而非自由图 | Flowise 同 commit `packages/server/src/services/agentflowv2-generator/index.ts`：Zod `AgentFlowV2Type` 校验 nodes/edges | 采纳「先 schema 校验」原则；MindFS 用现有 typed `WorkflowPatch`，不引入 Zod |
| 3 | 提案预览、应用/替换、已应用/已忽略状态、撤销快照 | Langflow `71b28b92` `components/assistant-flow-preview.tsx`、`helpers/apply-flow-proposal.ts`、`helpers/apply-flow-update.ts`；预览节点数上限与 `pending/applied/dismissed` 状态机 | 借鉴「预览卡片 + 明确应用/忽略 + 已应用标记」；拒绝其直接改画布即持久化的语义，MindFS 只能走 CAS apply |
| 4 | 节点/边/参数/布局差异 | Dify `b39a44c0` `web/app/components/workflow/workflow-generator/graph-diff.ts`：只比 node id + `data`，明确忽略 edges 与 layout | 拒绝其粒度；MindFS diff 必须覆盖节点、边、参数、Loop 子图、位置 |
| 5 | 草案被并发修改的冲突 UX | Dify 同 commit `workflow-generator/apply.ts`：`draft_workflow_not_sync` 409 映射为「另一标签页已修改，需 reload」 | 采纳「冲突后要求重取，不自动合并」；复用 MindFS 既有 CAS 409 |
| 6 | Loop 子节点归属与拖拽约束 | Flowise 同 commit `features/canvas/hooks/useDragAndDrop.ts`：`findParentIterationNode` + `parentNode` + `extent: 'parent'`；Dify 同 commit `nodes/loop/use-interactions.ts`：`parentId` 子节点、容器 bounds 重算、受限位置 | 以概念为准，按 MindFS 的 `@xyflow/react` v12 使用 `parentId`/`extent` 重写；不复制其 store/runtime |
| 7 | Loop 容器嵌套原语 | xyflow `1bc405cd` `examples/react/src/examples/Subflow/index.tsx`：`parentId` + `extent: 'parent'` | 直接使用项目已依赖的 xyflow 原语，无新增依赖 |
| 8 | 步骤流水 vs 任意 DAG 的边界 | Kandev `36476ce8` `apps/web/components/settings/workflow-pipeline-editor.tsx`（与 `a268e5c4` 同名文件逐字相同）：`@dnd-kit` 可排序步骤列表，不是图画布 | 拒绝作为画布来源；仅证明不能把 step pipeline 当 DAG 迁移 |

许可结论（逐文件核对仓库 LICENSE）：Langflow MIT；Flowise 核心 Apache-2.0（`packages/server/src/enterprise` 与显式版权文件为商业许可，已排除在参考范围外）；Dify 为附加条件的 modified Apache-2.0，仅借鉴、不复制；xyflow 核心 MIT；Kandev AGPL-3.0，仅借鉴概念。**本计划无源码复制，故不触发新的分发义务。** 若后续要复制任何上游文件，须先固定 commit、逐文件核对 LICENSE/NOTICE 并经用户另行批准。

## 文件与职责地图

- `docs/superpowers/plans/2026-11-11-kanban-agent-first-workflow-authoring.md`：本执行计划、参考证据、阶段状态及验收记录。
- `docs/plans/README.md`：唯一 Active 计划索引；本计划获批进入执行后与路线图建立双向链接并注明其依赖与当前状态。
- `web/src/components/task/WorkflowDraftEditor.tsx`：作者入口编排、提案预览与控制流；保持小组件/Hook 分工，不把完整生成逻辑塞进组件。
- 新增 `web/src/components/task/workflowAuthoring/`：自然语言输入、proposal summary、结构化 diff、逐项选择、dry-run 呈现等职责组件；按实际模块拆分，单文件不得超过项目硬线。
- 新增 `web/src/components/task/useWorkflowAuthoring.ts`：作者操作状态机、生成请求、快照 revision 绑定、冲突/取消处理；不直接复制 Draft 存储逻辑。
- `web/src/services/task/workflowDraft.ts`（或当前实际工作流 API 门面）：只新增必要的类型化 API 调用；复用现有 controlPath/appPath 路由规则。
- `web/src/shared/workflowModel.ts`：Loop 子图与参数的前端类型/轻量镜像校验；后端仍是权威。
- `web/src/components/task/WorkflowCanvas.tsx`：复用现有 xyflow 编辑器，增加 Loop 容器视图/子图编辑模式；必要时把 inspector/loop canvas 拆到同目录子组件。
- `web/tests/workflow-agent-authoring*.test.mjs`、`web/tests/workflow-loop-canvas.test.mjs`：作者预览/CAS 冲突/局部应用/Loop 交互与回归测试。
- Go 后端原则上不改。若参考核查或端到端测试证明现有 API 缺少必要的数据契约，只扩展现有 `http_workflow_agent.go`/domain typed patch，并同步 G-LG 登记；不得新建第二套生成或发布 API。
- `docs/upstream-customizations.yaml`：仅在代码改动确有新互斥定制边界时增加/扩展分组；沿用项目 G-A 计划文档前缀登记约定，不为计划文件另造 Scope 组。

## 交互与状态契约

1. Agent authoring mode 是编辑页可切换的协作面板；初始提供目标输入与上下文范围（创建 Draft / 修改 Draft / 修改 Run 的未来子图）。模式不能含糊切换目标对象。
2. 生成请求携带目标 scope、`workflow_id`/task/run 标识和读取时 revision。Agent 输出 `summary`、`rationale`、`base_revision`、typed operations、预期节点/边影响；禁止自由格式图覆盖作为落库输入。
3. 服务端 validate 与静态 dry-run 先于应用。提案 stale、校验失败或 dry-run 有阻断项时，Apply disabled；用户可修改目标再重新生成或手动调整。
4. diff 必须显示新增/删除/修改节点、边、参数、Loop 内部图、布局变化；运行副本还显示哪些触发/冻结边界受影响。对每项可接受/拒绝；结构依赖导致部分选择无效时拒绝该组合并解释，不做静默级联。
5. Apply 将用户选择的 operation subset 重新组装为一个带原 `base_revision` 的 typed patch，再由现有服务端 validate/apply + CAS 原子提交。CAS conflict 时保留提案供查看，但不可自动 rebase；必须重新获取快照并显式重新审阅。
6. 应用 Draft 后仍是 Draft；共享发布继续使用精确 `workflow_id + revision` proposal confirmation。Run future-graph patch 只改该 Task 所属节点的数据面快照，不回写共享 Draft/Version。
7. Loop 以容器节点作为主图节点；进入容器后编辑内部 DAG。子节点几何、端口、连接限制与容器尺寸由 UI 保证体验，服务端 validator 保证持久化 schema；退出条件使用现有安全表达式语言，不提供任意代码编辑器。
8. Agent 可在用户请求下解释或修订 Loop；Loop 上限、exit expression、内部图均属于 typed operations 可审阅内容。明确提示不支持嵌套 Loop 与内部 HumanGate。

## Tasks

### Task 0：固定版本参考实现复核与交互决策记录

**Files:** Read-only upstream reference trees and current MindFS editor; record results in this plan only.

**Interfaces:** Consumes the pinned source paths above. Produces a small interaction mapping: MindFS gap → verified source pattern → keep/adapt/reject → license disposition.

- [ ] Step 1: 在设计固定 commit 上重核 Langflow proposal/apply helpers、Flowise generator/iteration container、Dify diff/Loop interactions、xyflow Subflow example；记录每个使用的准确文件与 commit。
- [ ] Step 2: 对比 MindFS `WorkflowCanvas`, `WorkflowDraftEditor`, `workflowModel`, typed-patch API，列出不超过 10 个具体可借鉴交互；剔除不适用于 MindFS CAS/运行快照的模式。
- [ ] Step 3: 核验 LICENSE/NOTICE 与目标源码目录，给每个参考项标记“仅借鉴概念”或“代码复制需另批”；Kandev AGPL 与 Dify modified Apache 默认仅借鉴，Flowise enterprise 子目录排除。
- [ ] Step 4: 将映射结果补记到本计划的 Reference audit；验收为每个后续 UI 功能都有来源模式或明确 MindFS 自有设计，且没有未经许可的代码复制。

### Task 1：Agent proposal 纯模型与 revision-bound 状态机

**Files:** Create `web/src/components/task/useWorkflowAuthoring.ts`, focused types under `web/src/components/task/workflowAuthoring/types.ts`; tests `web/tests/workflow-agent-authoring-state.test.mjs`.

**Interfaces:**
- Consumes: existing `WorkflowPatch`, Draft/Run revision, workflow validate/dry-run service functions.
- Produces: `WorkflowAuthoringProposal` with `{scope, resourceID, baseRevision, summary, rationale, operations, validation, dryRun, status}` and state transitions `idle → generating → preview → stale|invalid|ready → applying → applied|conflict|error`.

- [ ] Step 1: 写 `WorkflowAuthoringProposal` 状态 reducer 测试：生成期收到新 revision 后旧响应变 stale，不覆盖新快照。
- [ ] Step 2: 跑 `npm --prefix web test -- --run tests/workflow-agent-authoring-state.test.mjs`，确认缺失状态行为时 RED。
- [ ] Step 3: 实现纯状态 reducer/类型；所有提案绑定目标 scope 与读取时 revision，不能由响应覆盖目标对象。
- [ ] Step 4: 加 malformed response、重复 operation ID、超限 operations、取消生成/切换目标后迟到响应测试；响应失效不得修改 UI 当前 proposal。
- [ ] Step 5: 重跑目标 Vitest 与 typecheck，确认 stale/late-response 测试通过。

### Task 2：Agent 生成请求与现有 API 安全接线

**Files:** Modify actual workflow draft service facade; create/modify `useWorkflowAuthoring.ts`; backend/API tests only if current contract is proven insufficient.

**Interfaces:**
- Consumes: Task 1 proposal state and current Workflow snapshot APIs.
- Produces: `generateWorkflowProposal(input: {scope, targetID, baseRevision, instruction})` returning an untrusted candidate; UI must call existing validate/dry-run before it can become ready.

- [ ] Step 1: 用 fake generator/session seam 写 `TestWorkflowAuthoringUsesCurrentSnapshotAndDoesNotWriteBeforeApproval`（若当前生成来源由现有 Agent session 提供，则 fake 对话 adapter），断言请求包含当前 graph/constraints/revision，不调用 draft apply/publish。
- [ ] Step 2: 写前端/HTTP contract tests：创建目标、Draft 修改、Run future-subgraph 修改三种 scope 不串；请求体过大、unknown operation、错误 revision 明确拒绝。
- [ ] Step 3: 实现单一 authoring gateway：先读取权威 snapshot/limits，再让 Agent 返回 typed proposal；不得将 API key、权限或 author 自声明作为授权判断。
- [ ] Step 4: 对响应执行 schema/size/operation 校验，然后依次调用现有 validate 与静态 dry-run；服务端权威错误直接呈现，不降级为前端校验通过。
- [ ] Step 5: 证明无 Apply 时 Draft/Run revision、proposal store、NodeRun、Task 状态不变；运行 `go test ./server/internal/kanban ./server/internal/api` 与目标 Vitest（只有触及后端时跑 Go）。
- [ ] Step 6: 若发现必须新增 API，先扩展既有 facade/domain contract 和 G-LG anchors/tests；禁止并行建立 agent-generator backend 或新版本存储。

### Task 3：可解释图 diff、逐项审阅与原子 CAS 应用

**Status:** complete（2026-11-11，已提交 `a5966dbe`；证据见 SDD ledger）。

**Files:** Create `workflowAuthoring/WorkflowProposalReview.tsx`, `workflowAuthoring/WorkflowProposalDiff.tsx`, `workflowAuthoring/WorkflowProposalSummary.tsx`; modify `WorkflowDraftEditor.tsx`, `useWorkflowAuthoring.ts`; tests `workflow-agent-authoring-review.test.mjs`.

**Interfaces:**
- Consumes: untrusted proposal and server validate/dry-run results.
- Produces: reviewed operation subset preserving original `baseRevision`; Apply calls existing typed validate/apply endpoint exactly once.

- [ ] Step 1: 写组件测试：diff 显示 node/edge/params/Loop/layout changes；默认无 operation 被接受；Apply 在 invalid/stale/blocked dry-run 时 disabled。
- [ ] Step 2: 写 partial-selection tests：删除节点必须处理关联边；拒绝依赖节点时不得留下悬空边；选中子集须重跑 validate/dry-run。
- [ ] Step 3: 写 CAS test：应用期间 revision 变化返回 conflict；保留对方 Draft/Run 与当前提案，不自动重试/重基。
- [ ] Step 4: 实现摘要、rationale、逐项选择和运行影响呈现；diff 不省略边、参数、布局或子图变化。
- [ ] Step 5: 应用前对选中子集重新请求 server validate + dry-run；成功后只提交现有 typed patch，不允许 proposal 自带“confirmed/published”。
- [ ] Step 6: 验证重复点击只产生一次 apply 请求；关闭/切换目标前 dirty 与 pending proposal 的保护符合当前编辑器行为。

### Task 4：Loop 子图画布与安全编辑

**Status:** complete（2026-11-11，已提交 `a5966dbe`；证据见 SDD ledger）。

**Files:** Modify `workflowModel.ts`; create `workflowAuthoring/WorkflowLoopCanvas.tsx` and focused inspector/toolbar components; modify `WorkflowCanvas.tsx`; tests `workflow-loop-canvas.test.mjs` and model tests.

**Interfaces:**
- Consumes: existing `WorkflowNodeDef`, Loop params (`max_iterations`, `exit_expression`, internal graph schema), service-side `ValidateWorkflowDefinition` / typed patch.
- Produces: stable `WorkflowNodeDef` Loop serialization with internal DAG; nested UI state never becomes a second graph truth.

- [ ] Step 1: 先扩展类型/fixture，写 round-trip tests：Loop 内图、edges、positions、exit expression、max_iterations 往返不丢；顶层 id 与内部 id 冲突拒绝。
- [ ] Step 2: 写 UI interaction tests：双击/按钮进入 Loop 子图、面包屑返回、添加/连线/删除内部节点、跨容器边拒绝、拖动不误改业务参数。
- [ ] Step 3: 写约束 tests：max_iterations 仅 1–20；exit expression 必填并走既有安全 grammar；nested Loop 与 internal HumanGate 显示明确错误且不能保存。
- [ ] Step 4: 使用 xyflow `parentId`/`extent` 或独立子画布实现 Loop container；根据 Task 0 audit 采用明确的 ReactFlow 嵌套交互，不迁移另一套 canvas/runtime。
- [ ] Step 5: 将 Loop 检查器扩展为迭代上限和 exit expression 编辑，提供可理解的字段说明、表达式引用提示和服务端错误定位。
- [ ] Step 6: 用已有 Draft save/reopen 与 Run patch CAS 做 UI/API 纵向测试；触发 Loop 或其子节点/边冻结时必须显示后端冲突，不尝试前端绕过。

### Task 5：Agent 提案与 Loop 编辑端到端验收

**Files:** `web/tests/workflow-agent-authoring-e2e.test.mjs`, `web/tests/workflow-loop-canvas.test.mjs`, existing workflow editor/API tests; only production files with demonstrated gaps.

**Interfaces:** Consumes Tasks 1–4. Produces a browser-level proof of authoring from natural-language goal to reviewed graph mutation without unauthorized persistence.

**Status:** complete（2026-11-11，已提交 `a5966dbe`；`e2e/test_workflow_authoring.py` 5/5 通过，证据见 SDD ledger）。浏览器验收用假 Agent 只替换「生成提案」一跳，其余（subset validate/dry-run、CAS apply、发布确认）全部走真后端。Run 级 authoring UI 不在本计划范围，见 Task 6。

- [ ] Step 1: 隔离实例 fake Agent 流程：读取现有 Draft → 提出 node/edge patch 与解释 → validate/dry-run → 用户拒绝一项、接受一项 → CAS 保存 → reload 看到同 revision graph。
- [ ] Step 2: 覆盖 stale proposal、malformed response、dry-run 阻断、partial patch dangling edge、并发编辑冲突；每种路径均断言持久状态未被未确认修改。
- [ ] Step 3: 浏览器流程创建 Loop、配置内部 DAG/退出表达式、保存重开、dry-run；检查移动端保持可读并不要求复杂画布编辑。
- [ ] Step 4: 验证精确 revision publish proposal 仍由用户单独确认；未确认不能出现新 WorkflowVersion。
- [ ] Step 5: 跑定向 Vitest、全量前端 tests/typecheck、Go 全量 tests（若有后端差异）、`make build-web`、`make build`。
- [ ] Step 6: 运行独立 reviewer；处理所有 CRITICAL/HIGH；对关键 stale CAS、冻结、未确认不写、Loop 限制做 mutation verification。
- [ ] Step 7: 检查 customization manifest/anchors，按 G-LG 扩展或创建有独立互斥边界的组；在授权的后续实现提交中标 `Scope: G-X`，提交后运行 `make check-upstream`。本计划本身不授权提交。

### Task 6：后续单独立项（不阻塞本计划）

- Run 级 authoring UI：后端 Run 网关已可用（`POST /api/tasks/{id}/workflow-run/authoring/proposals`，冻结集合只允许未触发未来子图），但本计划只做 Draft 编辑器内的生成与审阅；Run 级入口、冻结原因可视化与运行中补丁交互另行立项，不计入本计划验收。
- HumanGate/retry 的运行时操作入口：依现有 API 单独做 UX/浏览器验收；不把只读 RunTimeline 改成编辑画布。
- 成功任务 suggestion 的 Agent-first 流程：Agent 解释可复用步骤→用户导入 Draft→proposal review；不自动发布。
- 安全并行：另开计划，先证明只读隔离是否真实强制；若只是声明，按物理 worktree 隔离设计，并覆盖多活跃 NodeRun 留言目标、seq、join、cancel/recovery。
- M6 worktree 关闭入口：另行确认关闭前 owner/session/dirty/merge 条件；默认保留失败现场，不做定时自动回收。
- WP-00 importer/reopen `isSidechain`：有独立复现证据后单独处理，不把在线 router 修复宣称为 importer 覆盖。
- 生产 steer：可选增强；仅在某个 adapter 有真实可验证协议回执后设计，queue-only 保持有效默认。

## Acceptance and status reporting

- 计划实现阶段只负责 integration worktree；merge、push、deploy、ship、生产 restart 和生产验收均为独立授权，不在任何 task 的默认动作内。
- “作者体验完成”要求 Task 5 隔离端到端路径通过；只通过单元、API 或存在组件不算完成。
- “Loop 编辑完成”要求保存/重开/dry-run 纵向通过；仅显示 Loop 节点和上限输入不算完成。
- “Agent-first 完成”要求自然语言目标能形成解释清晰、可审阅、可局部拒绝、可校验、可 dry-run、revision-bound 的提案；直接生成或粘贴 JSON 仅是高级 fallback。
- 收尾报告分别给出：作者体验、Loop 编辑、运行时基础复用、参考模式采纳/拒绝、门禁、提交/部署状态；不混成单一完成百分比。

## Open implementation questions

以下不阻塞计划认可，但 Task 0/2 开始时必须先按建议确认或仅在改变事实时提出窄问题：

- **现有 Agent 生成入口**：当前 app 是否已有适合编辑器的 Agent session/tool surface。优先复用已有 Agent 会话作为生成器；若没有，第一版提供 editor 内 Agent chat/请求端口，但不新增独立调度后端。不能安全接入时保留本地 proposal adapter seam，不退回“必须手工粘贴 patch”的主路径。
- **模型调用位置**：优先让 MindFS 已有 harness/Agent 产生 patch，而不是增加新的外部 LLM API/key 配置；不得把外部密钥写到浏览器或请求日志。
- **部分接受语义**：推荐按 typed operation 做选择，依赖闭包不合法就要求用户调整选择；不自动级联应用用户未确认的 operation。
- **Loop wire schema**：实施前从现有后端 `WorkflowNode`/validator 确认内部图字段的确切 JSON key 与 edge ID 规则；本计划只要求稳定往返，不臆造字段名。

## Execution record

- **状态（2026-11-11）**：已获用户认可的方向为“agent-first authoring，canvas as review and control”；用户认可自然语言提案、diff/校验/dry-run、局部审阅与 CAS 应用的推荐默认。本文只规划，不实现。
- **现有进度**：Backend WorkflowRun/runtime、typed patch/CAS/dry-run/proposal API 和基础 xyflow Draft canvas 已在 integration branch；详见上述基线计划。当前未声称已合并、部署或生产验收。
- **参考核查摘要**：Langflow assistant preview/apply；Flowise prompt-to-graph 与 iteration container；Dify graph proposal/diff/stale apply 与 Loop containment；xyflow subflow primitive。Kandev 是 step pipeline editor，不是一般 DAG canvas。许可分别为 MIT、Apache-2.0（Flowise enterprise 子目录另有边界）、modified Apache 2.0、MIT、AGPL-3.0；只作为研究记录，实施前重核固定 commit 与文件。
- **Review gap**：当前 reference audit 来自只读仓库树/source 核查；Task 0 仍需在计划实施前复核固定设计 commit 的准确源码和 LICENSE，不可将相邻 revision 的发现当成 pinned-revision 证据。
- **独立完成门槛**：Task 0–5 都有可单独审查交付；Task 6 是后续计划导航，不是本计划未完成计数项。
- **收尾（2026-11-11）**：Task 5 完成，浏览器验收 `e2e/test_workflow_authoring.py` 5/5（假 Agent 只替换生成一跳，其余 subset validate/dry-run、CAS apply、发布确认全部走真后端；Playwright 路由必须用正则匹配含 `?user=` 的完整 URL）。门禁：`npm --prefix web test` 537 通过 / 6 跳过、typecheck clean、`go test ./...` 全绿、`make build-web` 与 `make build` 成功。变异验证：冻结前缀、提议即发布、Loop 上限 20→200 三处均先让对应测试 RED 再还原；过程中补强了三处原本抓不到回归的断言（`agent-wf:v2`、完整 Loop 边界用例、dry-run `issues`）。定制登记：G-KZ 增加可编辑 Loop 子图，G-LG 增加自然语言生成网关与前端状态机。Run 级 authoring UI 明确排除（Task 6）。**已执行**：授权提交 `a5966dbe`（41 files），提交后 `make check-upstream` 转绿（1714 个 delta 文件被 284 组完整覆盖）。**未执行**：merge / push / deploy / ship / 重启任何 mindfs 服务。

- **对齐补丁（2026-11-11 第二轮）**：按计划契约补齐两处偏差。① 新增独立「创建 Draft」authoring scope：后端 `POST /api/workflows/authoring/proposals|validate|apply` 以确定性空图（`start` agent，`writes_workspace=true`）和合成 revision `r0` 审阅，提案阶段不落盘；apply 只对接受子集重校验后一次 `CreateDraft` 写成 `r1`，重复 id → 409、非法子集 → 422 且不留草稿；前端 Draft editor 增加「用 Agent 新建」入口，创建会话固定同一 id，应用后 adopt r1 并退出创建模式。② 审阅 diff 对 `update_future_node` 同时展示修改前/后节点状态（`proposalDiff.nodeChangeSides` + `data-field=before/after`）。测试：`TestWorkflowCreateAuthoring*` 4 个（提案/validate 不建草稿、原子创建、非法子集不留草稿）、`workflow-agent-authoring-service`（创建网关与独立 apply）、`workflow-agent-authoring-review`（前后值纯函数与 UI）、`workflow-agent-authoring-e2e`（创建模式接线）。门禁复跑：`npm --prefix web test` 542 通过 / 6 跳过、typecheck clean、`go test ./server/internal/kanban ./server/internal/api` 全绿。定制登记：G-LG 扩展创建 scope 与前后 diff。**未执行**：commit / merge / push / deploy / ship / 重启任何 mindfs 服务。
