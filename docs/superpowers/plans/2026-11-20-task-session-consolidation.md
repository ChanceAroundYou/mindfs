# 任务会话收拢：一轮 Run 一条会话 + 跨轮 Router 选 new/continue + 侧栏按任务分组

- 日期：2026-11-20
- 状态：已实现，门禁绿；未提交 / 未部署 / 本机待用户重启
- 用户授权（2026-11-20，逐条裁定）：
  1. 多会话收拢走**方案 A**：UI 层按 `task_id` 分组，显示仍是现有子会话样式，零数据改动，不批量改结构。
  2. 策略期望：**一个工作流默认一个会话**，工作流内部可以自行设置要不要切新对话；跨工作流提交新任务时由 Router 选择接续还是开新会话（**默认新会话**）；UI 层面按任务分组。
  3. Agent 默认走**代码里显式任务默认**（方案 A，已由 `7888797c` 落地，本计划不含该改动）。

## 背景（调查结论）

- 用户报「一个任务有多个会话时要收拢成父子结构」「任务派生对话的默认行为是什么」。
- 现状（改动前）：`session_reuse_policy` 默认 `per_node`，一轮 workflow 的每个 Agent 节点各建一条会话；新建任务/跨轮又各自开新会话，于是同一任务在侧栏散成若干条互不相干的顶层会话，只有 `parent_session_key` 显式设置时才成父子。
- 侧栏只有一种层级（`parent_session_key`），没有按任务聚合的展示；会话行已有 `task_id`，所以分组可以在纯前端完成。

## 实施内容

### 1. 后端：一轮 Run 一条会话（默认 `run`）

- `types.go`：新增 `SessionReuseRun = "run"`，`SessionReusePerNode = "per_node"`；默认策略从 `per_node` 改为 `run`（显式 `per_node` / `always_new` / `same_stage` 照旧）。
- `workflow_types.go` + `task_store_workflow_runs.go`：`workflow_runs.session_key` 列（建表 / 重建 / `ensureTableColumn` 三处），`WorkflowRunSessionKey` 读、`SetWorkflowRunSessionKey` **先到先得**写（`WHERE session_key=''`，不回写时不推进 `revision`，避免撞 driver 的 CAS）、`LatestTaskSessionKey`（活动 → Run 共享会话 → 任务主会话）。
- `service.go` + `workflow_node_executor.go`：`lockWorkflowRunSession`（按 runID 的引用计数互斥锁）包住「读 Run 会话 → 建会话 → 写回」；并行分支同时起步时只有一个会话能胜出，DB 的先到先得是跨进程兜底。
- `workflow_session_name.go`：`WorkflowRunSessionName` = 任务基名 + ` · ` + 首条留言摘要；run 策略不带节点名（节点名只属于显式 `per_node`）。
- `appcontext_workflow.go`：`EnsureWorkflowNodeSession` 的 run 分支优先复用 `RunSessionKey`，其次回退 `NodeRun.SessionKey`（老 Run 没有共享会话时不把恢复的节点甩到新对话）；命名按策略分流。
- `task_store_archive.go`：`TaskRelatedSessionKeys` 增补 `workflow_runs.session_key`——run 共享会话只记在 Run 上，节点行反查不到，漏收会让归档后它继续留在侧栏。

### 2. 后端：Router 跨轮选 `session_mode`

- `router_types.go`：`RouterAction.SessionMode` + `RouterSessionModeNew` / `RouterSessionModeContinue`。
- `router_tools.go`：`router_run_workflow` 暴露 `session_mode`，缺省 `new`，非 `new|continue` 当轮拒绝；`validateRouterActions` 同样校验从库里回放的决策。
- `activity_route_apply.go` + `activity_service_turns.go`：`continue` 时在落 Run 前用 `LatestTaskSessionKey` 预填 `run.SessionKey`，`new` 保持空、由第一个节点建会话；legacy `CreateUserActivity` 路径传空（等价 `new`）。
- `router_prompt.md`：规则 4 写明「一轮 workflow 内部共用一条会话」+「跨轮由你选 `session_mode`，默认 `new`」。

### 3. 前端：侧栏按任务分组（方案 A，零数据改动）

- `sessionTaskGroups.ts`（新，纯函数）：`collectTaskSessionGroups`（≥2 条顶层会话才成组）、`taskSessionGroupLabel`（剥 ` · ` 摘要与 `/ #N` 节点后缀）、`orderTaskGroupedRows`（分组头出现在第一个成员位置，成员连续、缩进一级）。
- `sessionListTypes.ts` + `useSessionChildTree.ts`：`VisibleSessionRow` 增加 `task-group` 行与 `session.nested`；`buildRows` 在原有父子/子代理递归之上先过一遍任务分组。搜索结果是平铺命中列表，不分组。
- `TaskSessionGroupHeader.tsx`（新）：说明行 + 计数胶囊，不可点（没有动作，不做成按钮）。
- `SessionCard.tsx`：`nested` 只借子会话缩进（`paddingLeft 10px` / `gap 3px`），**不改 `isSubagent`**，顶部徽标与菜单行为不变。
- `SessionList.tsx` / `MultiProjectSessionList.tsx`：渲染分组头并把 `nested` 交给会话卡。
- i18n：`sessionList.taskGroupTitle` / `sessionList.taskGroupFallback`（zh/en）。

### 4. 前端：节点级「本节点另起会话」

- `workflowModel.ts`：`workflowNodeStartsNewSession` / `setWorkflowNodeStartsNewSession`（勾选写 `session_reuse_policy=always_new`，取消删参数回到 Run 共享会话；存量 `per_node`/`same_stage` 显示为勾选，不动就保留原值）。
- `WorkflowNodeInspector.tsx`：Agent 节点属性面板新增勾选框（`data-testid="workflow-node-new-session"`）。

## 测试与验证

- Go：`TestWorkflowRunSessionKeyFirstWriterWins`、`TestLatestTaskSessionKeyOrder`、`TestTaskRelatedSessionKeysIncludesRunSession`、`TestPrepareWorkflowNodeSharesOneRunSession`、`TestRouterRunWorkflowToolSessionMode`、`TestRouterPromptPrefersMessageAgentForContinuity`、`TestRouterRouteRunWorkflowSessionMode`、`TestWorkflowNodeSessionPolicyDefaultsToRun`、`TestWorkflowNodeAdapterSessionReusePolicies`、`TestWorkflowNodeAdapterSessionNamesDifferPerAttempt` 全绿；`go build ./server/...` clean。
- Web：`web/tests/session-task-group.test.mjs` 7 条（分组顺序/阈值/称呼剥离/两条侧栏接线/`nested` 不改 `isSubagent`/节点勾选语义/面板接线）；全量 `npm test` 722 通过 / 6 跳过，`tsc -b --noEmit` clean。
- 变异验证：把成组阈值改成 1、把「取消勾选删参数」改成永远写 `always_new`，对应用例分别打红，已还原复绿。

## 未做 / 边界

- 没有迁移任何存量会话结构：分组纯展示，会话归属、`parent_session_key`、JSONL 一律不动（用户选方案 A）。
- 存量散落会话不会被自动合并成一条；新一轮 Run 起才按 `run` 策略共用会话。
- 未做前端「任务分组折叠」（分组常显，成员始终可见）。
- 未部署、未提交；本机 system 单元重启仍只由用户执行。
