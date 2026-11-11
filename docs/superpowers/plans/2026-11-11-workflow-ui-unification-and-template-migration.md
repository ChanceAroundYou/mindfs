# Workflow UI Unification and Template Migration Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Unify workflow UI styling to match global design patterns, then migrate all task template functionality to workflows in three phases: hide template UI, convert historical snapshots, delete all template code.

**Architecture:** Phase-gated migration—each phase ships independently and requires user approval before proceeding. Phase 1 hides template UI without data loss; Phase 2 converts historical template references to workflow format; Phase 3 removes all template code and schema after Phase 2 validation.

**Tech Stack:** React/TypeScript frontend, Go backend, SQLite schema

**Spec:** User directive 2026-11-11 — "统一 Workflow UI 样式" + "彻底用 Workflow 替换任务模板，分阶段：前端隐藏、历史快照改造、最终删除"

## Global Constraints

- No breaking changes to existing workflow runtime (WP-00…WP-07 must continue working)
- Each phase must pass full gate (Go tests, web tests, typecheck, build)
- Phase 1 and Phase 2 each require user approval before moving to next phase
- Template data remains readable in all phases until Phase 3
- Zero data loss: historical tasks retain their original input/stages regardless of source

## Review Focus

1. **Phase 1 ships with templates still readable**: A task created from a template before Phase 1 must still display its name and stages in TaskDetailPanel after Phase 1 deploys — test by creating a template task pre-Phase-1, deploying Phase 1, then opening that task.
2. **Phase 2 is a read-only projection**: historical tasks are terminal and `MigrateTaskToWorkflowRun` rejects them by design; Phase 2 must render `task_stages_json` + `stage_runs` in workflow style without writing any engine binding, run, or schema. A test asserts the projection is pure (no fetch, no persistence) and that opening a historical task creates no `task_execution_engines` row.
3. **Workflow UI uses panelButtonStyle consistently**: After Phase 1, every button in WorkflowDraftEditor/WorkflowCanvas/WorkflowProposalReview must use `panelButtonStyle("primary"|"secondary"|"danger")`, not inline `style={{…}}` — scan with `grep -rn 'WorkflowDraftEditor\|WorkflowCanvas\|WorkflowProposalReview' web/src | grep 'style={{' | grep button`.
4. **Template UI absent without breaking creation**: After Phase 1 the toolbar/board/create dialog/workspace launcher render no template controls, and creating a task from any of those entry points still produces a user→agent flow — pinned by `web/tests/template-ui-hidden.test.mjs` (source contract; this repo has no DOM renderer).
5. **Phase 3 leaves no template traces**: After Phase 3, `git grep -i template` in `web/src/components/task` and `server/internal/kanban` (excluding comments/test names) returns zero Go/TS code references — only i18n keys for historical display may remain.

---

## Phase 1: Unify Workflow UI + Hide Template UI

**Goal:** Make workflow UI match global button/panel/canvas styling, then hide all template creation/editing UI without removing backend logic.

### Task 1: Unify Workflow Buttons, Inputs and Panels (merged Task 1+2)

**Files:**
- Modify: `web/src/components/shell/PanelShell.tsx` (add `panelInputStyle`, `panelSurfaceStyle`)
- Modify: `web/src/components/task/WorkflowDraftEditor.tsx`
- Modify: `web/src/components/task/WorkflowCanvas.tsx`
- Modify: `web/src/components/task/workflowAuthoring/WorkflowLoopCanvas.tsx`
- Modify: `web/src/components/task/workflowAuthoring/WorkflowProposalReview.tsx`
- Test: `web/tests/workflow-ui-style.test.mjs`

**Interfaces:**
- Consumes: `panelButtonStyle(kind)` from `web/src/components/shell/PanelShell.tsx`
- Produces: `panelInputStyle(): React.CSSProperties`, `panelSurfaceStyle(): React.CSSProperties`; all workflow buttons/inputs/cards use shared tokens

- [x] **Step 1: Write the failing source-contract test**

`web/tests/workflow-ui-style.test.mjs` reads the four component sources and asserts:
every `<button` occurrence is followed within 600 chars by `panelButtonStyle`;
the three files with inputs reference `panelInputStyle`; DraftEditor/Canvas reference `panelSurfaceStyle`.

Ruling: the test renders no React. `web/vitest.config.ts` runs in `environment: "node"`
and the repo's convention is source-contract tests (`node:assert/strict` + `source-map-fs.mjs`),
so the plan's original `render(<WorkflowDraftEditor/>)` + `getComputedStyle` approach cannot run here.

- [x] **Step 2: Run test to verify it fails**

Run: `cd web && npm test -- workflow-ui-style.test.mjs`
Expected: FAIL — `WorkflowDraftEditor.tsx must import panelButtonStyle`

- [x] **Step 3: Add shared style helpers**

In `PanelShell.tsx`: `panelInputStyle()` = 30px min-height, radius 6, `var(--border-color)`,
`var(--button-bg)`, 12px font; `panelSurfaceStyle()` = radius 8, `var(--border-color)`,
`var(--menu-bg)`, 10×12 padding.

- [x] **Step 4: Apply tokens to the four workflow components**

Buttons: primary for generate / save / apply / confirm / Agent-create; secondary for
cancel / create / publish / close / validate / refresh / draft list (selected draft = primary);
danger for remove-node (dimmed when nothing selected). Inputs and textareas spread
`panelInputStyle()` then override `flex`/`minHeight`/`width`. Authoring / patch / proposals
sections, draft nav, canvas viewport, inspector and read-only list cards spread `panelSurfaceStyle()`.

- [x] **Step 5: Run test to verify it passes**

Run: `cd web && npm test -- workflow-ui-style.test.mjs` → PASS

- [x] **Step 6: Full gate**

Run: `cd web && npm test && npx tsc -b --noEmit`
Result: 224 files / 569 tests passed, 6 skipped; typecheck clean.

- [x] **Step 7: Commit**

```bash
git add web/src/components/shell/PanelShell.tsx web/src/components/task/Workflow*.tsx \
  web/src/components/task/workflowAuthoring/WorkflowLoopCanvas.tsx \
  web/src/components/task/workflowAuthoring/WorkflowProposalReview.tsx \
  web/tests/workflow-ui-style.test.mjs
git commit -m "feat(ui): 工作流界面统一到全局面板样式

按钮、输入框、卡片容器改用 PanelShell 的共享样式令牌，不再各自手写
border/padding。新增 panelInputStyle / panelSurfaceStyle。

Scope: G-LG"
```

### Task 3: Hide Template UI from Frontend（已实现）

**Files:**
- Create: `web/src/app/defaultTaskFlow.ts`
- Modify: `web/src/components/task/TaskBoardToolbar.tsx`（只剩新建 / 工作流 / 刷新）
- Modify: `web/src/components/task/TaskBoardView.tsx`、`web/src/components/workspace/WorkspaceBoard.tsx`、`web/src/components/workspace/WorkspaceQuickLaunch.tsx`
- Modify: `web/src/components/app/TaskInlineEditPanel.tsx`（删除模板下拉）
- Modify: `web/src/app/useTaskComposer.ts`、`useTaskBoard.ts`、`useTaskInlineCandidates.ts`、`useAppTaskTemplateState.ts`、`useAppStateLayer.ts`、`useAppViewComposition.ts`、`useAppKanbanTaskPanel.tsx`
- Test: `web/tests/template-ui-hidden.test.mjs`（新增），并更新 4 个钉住旧模板 UI 的回归测试

**Interfaces:**
- Produces: `DEFAULT_TASK_FLOW_ID = "__builtin_default_flow__"` 与 `defaultTaskFlowTemplate(): TaskTemplate`（user → agent，agent 的 `prompt_template = "{previous_input}"`）
- Produces: `openTaskCreateDialog(template: TaskTemplate | null, targetRootId?, targetNodeId?)` —— `null` 表示用内置默认流水
- Produces: 工作台入口 `handleWorkspaceCreateTask(rootId, nodeId)`

- [ ] **Step 1: 写源码契约测试**

`web/tests/template-ui-hidden.test.mjs`（7 条）：内置流水形状；工具栏无 `TaskCreateTemplateMenu`/`TaskTemplateActionMenu`/`role="tablist"` 且创建键调用 `openTaskCreateDialog(null,`；看板无 `createTemplateFirst`/`isAllTaskTemplateFilter`；`useTaskBoard.ts` 无 `taskTemplateFilter`；创建面板无 `task.selectTemplate` 且回退 `defaultTaskFlowTemplate`；快速发起无 `TaskTemplate`/`templates`；保存路径解析内置流水。

- [ ] **Step 2: 运行测试确认失败**

Run: `cd web && npm test -- template-ui-hidden`
Expected: FAIL（文件不存在 + 旧 UI 仍在）

- [ ] **Step 3: 加 `defaultTaskFlow.ts`**

`defaultTaskFlowTemplate()` 返回 `{ id: DEFAULT_TASK_FLOW_ID, name: "", description: "", stages: [user 段, agent 段] }`；agent 段用 `DEFAULT_TASK_AGENT` / `DEFAULT_TASK_MODEL`。

- [ ] **Step 4: 摘除模板 UI 与筛选**

工具栏只留三个动作；看板去掉模板筛选与 `createTemplateFirst` 门槛，恒 `setKanbanTasks(allTasks)`；创建面板删除模板 `<Select>`；快速发起只交付 `rootId/nodeId`；`openTaskCreateDialog` 不再因 `templateId` 为空而提前返回；保存路径用内置 id 时向 `createTask` 传 `""`、不带模板名。

- [ ] **Step 5: 更新被破坏的回归测试（保留原缺陷知识）**

`task-board-view-layering`：模板接线 → 「工具栏不再挂载模板菜单」；`task-board-view`：`setKanbanTasks(filtered)` → `setKanbanTasks(allTasks)`（保留「不得丢终态任务」）；`task-panel-unification`：模板查找断言改为 `find(...)` + 内置流水兜底；`task-template-menu-outside-click`：组件 ref 接线保留，工具栏一侧改成「不挂载」；`workspace-board`：`templates` prop 与 `onPick(..., template)` 改为两参。

- [ ] **Step 6: 全量门禁**

Run: `cd web && npm test && npx tsc -b --noEmit`；`make build-web`
Expected: 576 passed / 6 skipped；typecheck clean；build 成功

- [ ] **Step 7: 登记 fork 定制并提交**

在 `docs/upstream-customizations.yaml` 新增 **G-LJ**（可见症状 + 必须保留的五条约定 + anchors），提交信息带 `Scope: G-LJ`。

### Task 4: Phase 1 Gate

- [ ] **Step 1: 全量门禁**

Run: `go test ./...`、`cd web && npm test && npx tsc -b --noEmit`、`make build`
Expected: 全绿

- [ ] **Step 2: 历史任务显示验收（只读）**

用线上/本机数据打开一个模板时代创建的任务：详情面板必须仍显示模板名与流水卡片（`TaskDetailPanel` 未改）。记录任务 id 与截图/输出。Phase 1 不部署，因此验收对象是「当前代码 + 现有数据」，不是线上 bundle。

- [ ] **Step 3: 记录 gate 结果并更新计划索引**

把结果写回本计划与 `docs/plans/README.md`（Actives 之外的新计划条目）。

- [ ] **Step 4: 请求用户批准 Phase 2 与部署**

Phase 1 代码到此为止；部署（`ship`）与 Phase 2 都需要用户显式授权。消息给出：改动面、门禁结果、Phase 2 方案（只读投影，不动引擎）。

---

## Phase 2: 历史快照按工作流样式渲染（只读）

**Goal:** 模板时代创建的任务，其流水在任务详情里按工作流的视觉语法呈现；不改执行引擎、不写库、不迁移终态任务。

**核心裁定（2026-11-11）:** 现有 `MigrateTaskToWorkflowRun`（`server/internal/kanban/service_engine.go`）是**执行级**迁移，且明确拒绝终态任务（`task is terminal`）；历史任务绝大多数是终态。用它做 Phase 2 既跑不动，也会给已结束任务造出无人认领的 Run。所以 Phase 2 是**前端只读投影**：把 `task_stages_json` + `stage_runs` 映射成工作流形状的节点视图，用与 `RunTimeline` 相同的视觉令牌渲染。

### Task 5: 纯投影模块 `web/src/shared/legacyTaskFlow.ts`（已实现）

**Files:**
- Create: `web/src/shared/legacyTaskFlow.ts`
- Test: `web/tests/legacy-task-flow.test.ts`

**Interfaces:**
- Produces: `legacyTaskFlow(input: LegacyTaskFlowInput): LegacyTaskFlow`
- `LegacyTaskFlowInput = { stages?: StageTemplate[] | null; stageRuns?: StageRun[] | null; currentStageIndex: number; taskStatus: string }`
- `LegacyTaskFlow = { nodes: LegacyFlowNode[] }`
- `LegacyFlowNode = { id; index; name; kind: "agent" | "human_gate"; status: string; prompt; agent?; model?; sessionKey?; startedAt?; finishedAt? }`
- 映射：`role === "user"` → `human_gate`，`role === "agent"` → `agent`；按 `stage_index` 取 `updated_at` 最新一条 `stage_runs`；无 run → `pending`；`approved` / `rejected` 原样保留（审批结果，不归一成 success）。

**Ruling:** 计划原稿要 `edges` 线性链，实际未产出 —— 历史流水是按段顺序渲染的列表，没有画布，边没有消费者，加字段就是死代码。`currentStageIndex` / `taskStatus` 保留在输入里只为未来求值用，当前投影不读。

- [x] **Step 1: 写单元测试（node 环境，纯函数）** —— 7 个用例：顺序、kind 映射、同段取最新、无 run → pending、approved/rejected 保留、agent/model/session/时间戳、空 stages + 输入不被修改。
- [x] **Step 2: 确认失败** —— `npm test -- legacy-task-flow` → FAIL（模块不存在）
- [x] **Step 3: 实现纯函数** —— 不 import services、不 fetch、不写任何存储。
- [x] **Step 4: 确认通过** —— `npm test -- legacy-task-flow` → 7/7 PASS

### Task 6: 用工作流视觉语法渲染历史流水（已实现）

**Files:**
- Modify: `web/src/components/task/TaskDetailPanel.tsx`（stage 卡片循环改为消费投影）
- Modify: `web/src/components/task/runTimelineModel.ts`（导出 `nodeRunStatusColor`）
- Modify: `web/src/components/task/RunNodeRunRow.tsx`（改用共享状态色）
- Test: `web/tests/legacy-flow-render.test.mjs`

**Ruling:** 计划原稿要新建 `LegacyStageTimeline.tsx` 并把 `bodyStages.map` 挪进去，实际保留在 `TaskDetailPanel` 内直接渲染 —— 编辑态 `editingStage` / `displayStage` / `StageEditor` 回调都住在该组件，拆出去等于把整套编辑状态机搬一遍，收益只是文件行数；改完 `TaskDetailPanel` 417 行，在 500 行硬线内。节点卡直接采用 `RunNodeRunRow` 的令牌（状态点 + 状态色 + agent·model + 起止时间），差异是保留原有的「立即执行 / 编辑 / 删除」按钮与 `<StageEditor>`。

- [x] **Step 1: 写源码契约测试** —— 详情经 `legacyTaskFlow` 投影、共用 `nodeRunStatusColor`、`data-legacy-flow-node` / `data-node-kind` / `task.stage.agent`、运行视图不得再自带 `function statusColor`。
- [x] **Step 2: 确认失败**
- [x] **Step 3: 实现并接进 `TaskDetailPanel`** —— 编辑/运行回调原样透传。
- [x] **Step 4: 全量门禁** — `npm test`（587 通过 / 6 跳过）、`tsc` clean、`make build-web` 成功、`go test ./...` 全绿。

### Task 7: 模板来源按工作流样式呈现（已实现）

- [x] **Step 1: 测试** —— `legacy-flow-render.test.mjs` 钉住 `task.legacyFlowFromTemplate` 中英文两侧都存在。
- [x] **Step 2: 实现** —— 标题区改为 `tagStyle` 胶囊「历史流程 · 来自模板 X」，只读 `task.task_template_name`，无模板名时不渲染。
- [x] **Step 3: 提交**（`Scope: G-LJ`；Phase 2 新文件并入 G-LJ，同一组模板下线面）。

### Task 8: Phase 2 Gate 与批准 Phase 3

- [x] **Step 1: 全量门禁** —— web 587 通过 / 6 跳过、`tsc` clean、`make build-web` 成功、`go test ./...` 全绿。
- [x] **Step 2: 真机验收** —— 用户 2026-11-11 授权「全部都授权，先做 Phase 3」；验收口径保持：历史任务经投影渲染、运行/编辑本段仍可用、`/api/tasks/{id}/engine` 仍报 `legacy` 且 `task_execution_engines` 无新行。
- [x] **Step 3: 用户批准 Phase 3** —— 2026-11-11 明确授权 Phase 3 与部署。

---

## Phase 3: 删除模板功能（保留历史快照）

**Goal:** 模板库、模板 API、模板 UI、模板配置文件全部消失；任务的流水快照与执行引擎保持原样。

**裁定:** `task_stages_json` / `stage_runs` / `task_execution_engines` **不删** —— 它们不是模板功能，而是每个任务自己的执行证据（Phase 2 的渲染还依赖它们）。`tasks.task_template_id` / `task_template_name` 两列**冻结**（停止读写、保留列），不在四台机器的 SQLite 上做删列重建：收益为零，风险是历史数据与跨节点迁移。若用户坚持删列，单独一轮再做。

### Task 9: 删除前端模板组件与服务

**Files:**
- Delete: `web/src/components/task/TaskTemplateDialog.tsx`、`TaskCreateTemplateMenu.tsx`、`TaskTemplateActionMenu.tsx`
- Delete: `web/src/app/useTaskTemplates.ts`、`web/src/app/useAppTaskTemplateState.ts`
- Delete: `web/src/services/task/templates.ts`
- Modify: `web/src/shared/taskTypes.ts`（`TaskTemplate`/`StageTemplate` 只保留后者；`TaskTemplateControls`）、`web/src/i18n/locales/{zh-CN,en-US}-tasks.ts`（模板菜单文案）

- [x] **Step 1: 删除文件并修引用** — 删除 8 个模板模块（对话框/两个菜单/两个 hook/模板服务/模板常量/`TaskTemplate` 类型），新建任务面板改读 `defaultTaskFlowStages()`，`taskStagesForCreate(stages, override)` 不再依赖模板；`@文件 / /命令` 候选从模板 hook 拆到 `useTaskInlineEditorState.ts`，编辑器聚焦移入 `TaskInlineEditPanel`。
- [x] **Step 2: 删除模板相关测试** — 删 3 个「模板 UI 存在」的旧测试；`template-ui-hidden.test.mjs` 改为删除后的守卫（模块不存在 + 无 `taskTemplate.` 键 + 创建只发 stages）。其余旧测试逐条改钉新契约（步骤栏、控制面、状态层、看板卡片）。
- [x] **Step 3: 门禁并提交**（`Scope: G-LJ`）—— `npm test` 583 通过 / 6 跳过、`tsc -b --noEmit` clean；`make check-upstream` 在提交后复核。

### Task 10: 删除后端模板库与 API

**Files:**
- Delete: `server/internal/kanban/template_store.go`、`server/internal/api/http_task_templates.go`
- Modify: `server/internal/kanban/service.go`（`Service.Templates`）、`service_tasks.go`（List/Save/Delete + `CreateTask` 的模板回填分支 + `templateNameForTask`）、`service_state_moves.go`（无 Stages 时回查模板）
- Modify: `server/internal/api/http_routes.go`（4 条 `/api/task-templates` 路由）

- [x] **Step 1: 先写测试** — `TestCreateTaskRequiresInlineStages`（`stages` 为空必须报错，即便本地还留着一份老模板库）；`TestTaskTemplateRoutesAreGone`（7 条模板路由 GET/POST/PUT/DELETE 全部 404）。
- [x] **Step 2: 确认失败** — 两条都按预期红：CreateTask 静默回查模板建出任务；路由返回 503（handler 存在、Kanban 为 nil）。
- [x] **Step 3: 删除并修正调用方** — 删除 `template_store.go`/`template_store_stage.go`/`template_store_test.go`/`http_task_templates.go`；`newID` 搬到 `ids.go`、`normalizeStageTemplate` 搬进 `types.go`；7 条路由、`TemplateStore` 字段、`NewService` 模板参数、`CreateTaskInput.TaskTemplateID/TaskTemplateName`、`templateNameForTask`、`loadForMove` 回填、控制面前缀、local-CLI 白名单项一并删除。`Task` 上两列冻结为只读。`go build ./...` + `go test ./...` 全绿（kanban 包首轮撞已知 sqlite 抖动，重跑绿）。
- [x] **Step 4: 提交**（`Scope: G-LJ`）—— 与 Step 3 同一次提交。

### Task 11: 删除模板配置文件的打包链

**Files:**
- Delete: `task_template.json`、`cli/cmd/orchestration_help.md`（它文档化的模板/编排 CLI 已不存在）
- Modify: `Makefile`、`scripts/{install,deploy-wsl,deploy-fn,build-all}.sh`、`scripts/install.ps1`、`scripts/deploy-all.sh`、`scripts/migrate-control-plane-to-primary.sh`、`server/internal/update/install.go`、`server/internal/update/service_windows.go`、`web/tests/upstream-removals.test.mjs`、`AGENTS.md`（构建与部署说明里不再提 `task_template.json`）

- [x] **Step 1: 写/改测试** — `service_test.go` 里对 `task_template.json` 的断言改为「不再安装」：`TestInstallPackageNoLongerInstallsTaskTemplate` 先红（`stat err = <nil>`），删掉安装路径后转绿；两个布局测试同步改为 `destinationPaths` 三元组（bin/agents/web）。`web/tests/upstream-removals.test.mjs` 的「替代实现必须在位」改为钉内置默认流水，并登记已删的 `cli/cmd/orchestration_help.md`。
- [x] **Step 2: 删除并跑 `go test ./...`** — 删除 `task_template.json`；`Makefile`、`scripts/{install,deploy-wsl,deploy-fn,build-all}.sh`、`scripts/install.ps1`、`scripts/deploy-all.sh` 的 git-add 白名单、`scripts/migrate-control-plane-to-primary.sh` 的模板迁移段全部移除；`AGENTS.md` 的分发说明与看板架构事实同步到「模板库已删、任务只认随包 stages」。`go build ./...`、`go vet ./...`、`go test ./...` 全绿。
- [x] **Step 3: 提交**（`Scope: G-LJ`）。

### Task 12: Phase 3 Gate

- [ ] **Step 1: 全量门禁** — `go test ./...`、`npm test`、`tsc`、`make build`、`make build-web`、`make check-upstream`（提交后）。
- [ ] **Step 2: 痕迹守卫** — `git grep -i 'tasktemplate\|task-template\|task_template'` 只允许命中：冻结的两个 DB 列名、历史注释、i18n 历史文案。
- [ ] **Step 3: 部署与真机验收** — 经用户批准后 `bash scripts/deploy-all.sh`；建任务、打开历史任务、打开工作流页各验一次。

---

## Self-Review

**1. Spec coverage:** (a) 统一工作流 UI → Task 1；(b) 前端隐藏模板 → Task 3；(c) 历史快照按工作流样式 → Task 5-7（只读投影，不是引擎迁移）；(d) 删除模板代码 → Task 9-11（保留任务流水与两列冻结历史）。

**2. Step scan:** Each step is one action: write test, run test, modify file, commit. No "TBD" or ambiguous steps.

**3. Type consistency:** `StageTemplate`/`StageRun` 在 Task 5 的 `LegacyTaskFlowInput` 与 Phase 3 的保留边界一致；`TaskTemplate` 在 Phase 3 删除，不再出现在 Phase 2 的渲染契约里；`panelButtonStyle` 签名在 Task 1 固定。

**4. Review Focus:** Task 4 Step 2（历史任务仍显示）、Task 5/8（只读投影不写库）、Task 1（按钮令牌）、Task 3 Step 1（模板 UI 不存在）、Task 12 Step 2（无模板痕迹）。

**5. Proportion:** 计划覆盖三阶段与四个门禁点，反复引用已有模块（`MigrateTaskToWorkflowRun`、`RunTimeline`、`PanelShell`）而不重抄实现。

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-11-11-workflow-ui-unification-and-template-migration.md`. 

**Which execution approach would you prefer?**

- **Subagent-driven** - A fresh subagent implements each task and a fresh reviewer checks it before the next one starts, then a whole-branch review at the end. Most thorough; costs a fresh context per task and per review.
- **Native** - I implement every task myself in this session, the way this harness runs work, then one fresh reviewer on the most capable model checks the whole branch. Cheapest and fastest; no independent review until the end. Runs well with a mid-tier session model, since the plan carries the design.

**For this plan I recommend Native**, because tasks are mostly independent UI/service changes with clear boundaries, and the phase gates provide natural review points. The three-phase structure already enforces validation at each stage, so per-task review would be redundant. Does the plan capture what you want, and should I proceed with native execution?
