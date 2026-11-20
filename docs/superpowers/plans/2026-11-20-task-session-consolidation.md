# 任务会话收拢：一轮 Run 一条会话 + 跨轮 Router 选 new/continue + 侧栏按任务分组

- 日期：2026-11-20
- 状态：已提交 `0675f772`；干净 worktree 门禁全绿（Go 28 包 / web 732 通过 / `tsc` clean / `make check-upstream` 2044 delta·334 组）；已 `make build && make install` 装到本机 `v0.5.5-1200-14-g0675f772`，未部署其他节点，本机待用户重启
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
- 未做前端「任务分组折叠」的持久化：默认收起、点分组头展开（选中项所在组自动展开），但不记「展开过哪一组」的偏好。
- 只装到本机，未部署 WSL/fn/pc-win；本机 system 单元重启仍只由用户执行（前端 bundle 装完刷新页面即生效，后端会话策略要重启才生效）。

## 显示样式修正（2026-11-20，用户逐条确认「全部确认 按你建议」）

用户现场：分组头还是「说明行 + 计数胶囊」的旧形态，成员会话名又把任务名重复了一遍。两条裁定：

1. **分组头与会话行同一套字**：`taskSessionGroupHeading(label, taskId, fallback)` 产出 `任务名 # 91`（有任务号才补 `# id`），`13px / 500` 与未选中会话行一致，前面换成一个分组专用图标（三条横杠，18px 图标位与 `SessionCardIcon` 对齐）；**成员计数整条去掉**，不搬家。
2. **成员名剥掉共享前缀**：`taskSessionMemberLabel(fullName, groupLabel)` 只剥分组头那一段 —— `任务名 · 摘要` → `摘要`，`任务名 / #7 · 摘要` → `#7 · 摘要`（节点号是组内区分点，保留）。前缀必须在词边界上（`任务 A` 不切 `任务 AB · …`），改过名的会话不以任务基名开头就原样返回。只影响展示：`title` 与高亮走剥后的名字，重命名草稿仍编辑落库原名。

接线：`orderTaskGroupedRows` 的成员行带 `groupLabel` → `useSessionChildTree.append(entry.session, entry.nested, entry.groupLabel)` 落成 `stripNamePrefix` → 两条侧栏透传 → `SessionCard` 渲染时剥。真正的子会话（`parent_session_key` 命中）不带前缀，不受影响。

验证：`web/tests/session-task-group.test.mjs` 9 条（新增「分组头文案 = 任务名 + 任务号，和会话名同一行字」「成员名剥掉分组头的共享前缀，保留区分点」，并补 `SessionCard` 接线与「重命名仍编辑原名」断言）；全量 `npm test` 247 文件 / 745 通过 / 6 跳过、`tsc` clean。四处变异各自打红对应用例后还原：① 去掉前缀裁剪；② 分组头改回 `任务名 91`（无 `#`）；③ `SessionList` 不透传 `stripNamePrefix`；④ `SessionCard` 直接渲染 `displayName`。

安装：`make build-web` 后按 `make install` 的静态资源语义把 `web/dist` 覆盖进 `~/.local/share/mindfs/web/`（未重编二进制 —— 本次只动前端，避免把并行会话的在飞改动编进去）；线上 7331 `GET /mindfs/` 200 且 index 已指向新 bundle `assets/index-WeZGkol4.js`，刷新即生效。未部署 WSL/fn/pc-win。

提交与门禁：显示修正 `8974505c`（Scope: G-NG），登记锚点 `/why` 更新 `620e0806`（Scope: G-NG、G-MV）。干净 worktree（`620e0806`）`make check-upstream` 通过：2057 delta / 335 组全绿；共享工作区当前红在 G-MZ，是另一条会话在飞的 `router_brief.go` 改动，不属于本次变更。

## 第二轮用户反馈（2026-11-20，五条逐项）

用户原话五条：「1. 现在显示的是『对话命名问题 # task_270967ce0da8157』2. 左侧的 icon 也要更有语义感一点 且跟随主题色 3. 具体任务子会话前面的『#91 ·』也去掉吧。4. 现在两套显示在跳来跳去，一刷新就变成旧的了 5. 默认不展开会话 除非点击进去，和子会话的展开一个逻辑」。第 4 条追问后确认：刷新后「分组整个消失，会话平铺」，客户端是桌面端 PWA。

1. **任务号不再拿 `task_id` 充数**：`taskSessionGroupHeading(label, number, fallback)` 只接任务号；任务号由 `taskSessionGroupNumber(sessions)` 从会话名里的 `/#(\d+)` 取（后端 `TaskSessionName(name, taskNumber)` 拼的就是看板上的数字号）。共享 Run 会话名（`任务名 · 摘要`）里没有任务号 → 宁可不显示，也不显示哈希。
2. **分组图标跟随主题色**：`TaskSessionGroupHeader` 换成两层叠放的卡片轮廓（语义：一组会话），颜色 `var(--accent-color)`，18px 图标位与 `SessionCardIcon` 对齐。
3. **成员名连任务号一起剥**：`taskSessionMemberLabel` 在剥掉共享前缀后再剥 `#\d+ ·` —— `任务名 / #91 · run · 摘要` → `run · 摘要`，`任务名 / #91 · 摘要` → `摘要`。任务号已经在分组头上。
4. **分组时有时无（刷新后平铺）**：根因是阈值只看「这一页装了几条」。多项目侧栏每根只取 6 条（`multiRootSessionLimit`），实测线上任务 #91 的 4 条顶层会话在 `limit_per_root=6` 只回来 1 条、`limit_per_root=50` 回来 4 条 —— 同一份数据在单项目面板成组、在多项目面板平铺，刷新/切视图就是「两套显示跳来跳去」。修法：后端列表查询下发 `task_session_count`（`selectSessionListSQL` 的相关子查询：同一 `task_id`、顶层、未归档、非 router），前端阈值取 `Math.max(本页成员数, task_session_count)`。该列只在列表查询里算；单条读取/广播恒 0，**为 0 时不下发这个键**（下发 0 会让前端把「不知道」当成「确认只有一条」）。没有该字段（旧后端）时退回按本页成员数判，行为与改动前一致。
5. **默认收起**：`expandedTaskGroups` 默认空（全收起），分组头是 `<button aria-expanded>`，点一下切换；选中的会话在组内时由 `taskGroupShowsMembers` 强制展开（否则点进一条会话它自己从列表里消失）。

验证：`web/tests/session-task-group.test.mjs` 12 条（新增「任务还有别的会话没装进这一页时也要成组」「任务号从会话名里取，不拿 task_id 充数」「任务分组默认收起，点开才展开，选中项所在组自动展开」并更新文案/成员名断言）；`server/internal/session` 的 `TestListReportsTaskSessionCount`（task_a：2 顶层 + 1 子会话 + 1 归档 → 2；单条任务 → 1；无任务 → 0）与 `server/internal/api` 的 `TestSessionListResponseCarriesTaskSessionCount`（0 时不下发键）。变异验证：把阈值改回 `members.length`、把「0 时不下发」改成无条件下发，对应用例分别打红后还原。

门禁：`go test -count=1 ./...` 全绿、`go build ./server/...` clean、gofmt 干净；web 248 文件 / 758 通过 / 6 跳过、`tsc -b --noEmit` clean。

安装：前端从 **HEAD + 本次前端改动的隔离 worktree**（`/tmp/mindfs-webbuild`，`node_modules` 软链回主仓）构建，避免把其他并行会话在飞的 `WorkflowDraftEditor` / `DefaultListView` 改动编进产物；按 `make install` 的静态资源语义覆盖 `~/.local/share/mindfs/web/`。线上 7331 与 `https://home.xiaokubao.space/mindfs/` 均已指向 `assets/index-9hzYg2VF.js`（200）。**后端 `task_session_count` 要重启本机 system 单元才生效 —— 只由用户执行 `sudo systemctl restart mindfs`**；重启前前端按旧行为（本页成员数）判阈值，不会出回归。
