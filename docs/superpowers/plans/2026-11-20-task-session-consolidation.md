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

安装：先在本机隔离 worktree 里单独验证（只带本次前端改动的 bundle 为 `assets/index-9hzYg2VF.js`）；提交（`e44b11f9`）后从干净 worktree 的 `HEAD`（已含并行会话刚提交的 `0423671e`/`7cbec181`）整体重建，按 `make install` 的静态资源语义覆盖 `~/.local/share/mindfs/web/`。线上 7331 与 `https://home.xiaokubao.space/mindfs/` 均已指向 `assets/index-C83JjzLE.js`（200）。`make check-upstream` 2071 delta / 337 组全绿。**后端 `task_session_count` 要重启本机 system 单元才生效 —— 只由用户执行 `sudo systemctl restart mindfs`**；重启前前端按旧行为（本页成员数）判阈值，不会出回归。

## 第三轮用户反馈（2026-11-20，三条）

用户原话：「问题还是存在，mindfs 会话很多，当展开的时候是新样式，收起或者切换到其他节点的时候又变回旧样式。而且不要用下展开箭头，就用现在的 icon，但是大小稍微变大一点，它比普通对话的圆看起来小了一圈。并且『任务详情面板修改 / #87』的子会话还是完整的名字。」

1. **分组时有时无（第 4 条的第二现场）**：折叠本身溶解不了分组（`hiddenTaskIds` 只藏成员、分组头还在），真正的驱动是**数据窗口** —— 每次全量刷新都从「每根 6 条」的一页重建分组，成员滑出窗口就掉组；本轮实测本机在跑的进程（PID 2853550，12:05 启动）**根本没下发 `task_session_count`**，所以后端兜底也没生效；切到别的节点更直接 —— WSL/fn/pc-win 跑的还是旧二进制（无该字段）+ 旧的展示逻辑。三层修法：① 前端加**进程内记忆** `rememberTaskSessions` / `taskSessionSeenCount`（作用域 `节点\0项目`，只记顶层会话、只增不减），阈值取 `Math.max(本页成员数, task_session_count, 见过的条数)` —— 这一层让「这个标签页见过」的分组不再随刷新/切视图消失；② 本机后端 `task_session_count` 需要用户 `sudo systemctl restart mindfs`（重启前不回归，只是仍按窗口判）；③ binary + bundle 随本组部署到 WSL/fn/pc-win，节点间才真正同构。
2. **不要下展开箭头**：`TaskSessionGroupHeader` 删掉 12px 箭头那一格（连同它的 6px 间隙），那个两层卡片图标自己就是开关（外层仍是 `<button aria-expanded>`），悬停提亮 + 轻微放大；图标位 18px → 20px、图形 16px → 18px，比会话行的 18px / 16px（还带 10px agent 角标）大一号。文案左边缘因此从 ~44px 移到 ~28px，正好落在缩进成员（~31px）左侧一级。
3. **成员名剥完只剩任务号**：`任务详情面板修改 / #87` 剥掉共享前缀后是空串，旧代码 `return rest || name` 于是原样显示整行。改为先记住 `#N`：`rest` 为空但有任务号时显示 `#87`（组内唯一区分点），既没有 rest 也没有任务号才回落到完整名字。

验证：`web/tests/session-task-group.test.mjs` 13 条（新增「成员滑出这一页窗口后分组仍然成立（进程内记忆兜底）」含作用域隔离、子会话不计数、`buildRows` 接线断言；成员名用例补 `#87` 与空格写法两条；分组头用例补「没有箭头 / 图标 20px·18px」）；三处变异各自打红后还原：① 成员名回落改回 `rest || name`；② 阈值去掉 `seenCount`；③ 分组头把箭头加回来。全量 web 248 文件 / 760 通过 / 6 跳过、`tsc -b --noEmit` clean。

## 第四轮用户反馈（2026-11-20，分组头指示灯 / 时间 / 三点菜单）

用户原话：「我希望在前端上 也给任务会话组加上指示灯和时间(时间按任务组内的最新时间更新)，并且也补上三点操作按钮，禁用掉重命名和同步功能，归档和删除会递归操作全部，有额外的提示」。两个追问项的选择：指示灯 = **聚合状态**（「有在跑则空心灯，有问题和报错则传递出来，按照普通会话显示问号和感叹号」）；归档/删除范围 = **全部会话含子会话**。

1. **聚合状态**：新增纯函数 `taskGroupAggregateState(members)` —— `running`（任一成员 `pending` / `presence=running|delegating`）、`awaiting`、`askingUser`（`aux_flags.ask_user_waiting` 条数）、`errors`（`aux_flags.session_error` 条数）、`latestAt`（成员里最晚的 `closed_at || updated_at`）、`archived`（成员全归档）。分组头右侧列与会话行同宽同对齐（`minWidth 24px`、10px 次级字）：问号与感叹号沿用普通会话那套 `taskAuxBadgeStyle` + `renderToolIcon("ask_user")` / `TaskSessionErrorIcon`，按条数出 title；有在跑或等回答时是 8px 空心圆（`mindfs-hollow-pulse`，与 `SessionCard` 同动画），否则显示组内最新时间。刻意不聚合 plan/todo/task 内容徽标 —— 它们描述单条会话，堆到分组头只会挤掉名字。

2. **三点菜单** `TaskSessionGroupMenu`：重命名、同步**保留但置灰**（分组不知道该落到哪一条成员上，title 写明原因），归档/删除可用；归档视图里归档项变「取消归档」，没有 `onDelete` 时删除项不渲染。弹层定位/裁剪判据复用会话卡那套（`clipBounds` 改为从 `SessionCardOverflowMenu` 导出）。

3. **整组动作只确认一次**：`taskGroupActions.ts` 的 `archiveTaskGroup` / `deleteTaskGroup` 先弹一个 `confirmDialog`（文案写明「组内 {count} 条会话及其全部子会话」，删除用 danger），确认后逐条**串行**调用原处理器 —— 并发会让两份列表状态互相覆盖。`count` 只数组内顶层成员数：子会话按需懒加载，客户端不保证持有完整子树，真正的级联由后端按 `parent_session_key` 完成。

4. **唯一删除出口加批量通道**：`handleDeleteSession(session, options?: { confirm?: boolean })`，整组删除传 `confirm: false`（外层已确认），其余调用点不传、行为不变。多项目侧栏的分组成员补 `root_id` overlay（项目号在 group 上），否则整组归档/删除会落到「当前选中项目」。

验证：`web/tests/session-task-group.test.mjs` 16 条（新增聚合状态、菜单置灰与整组确认、成员下发与两条侧栏接线；补 `members` 与多项目 `root_id` overlay 断言）。五处变异各自打红后还原：① `latestAt` 改成第一个成员；② 整组删除改成逐条 `onDelete(session)`（不再带 `confirm:false`）；③ 菜单去掉重命名的 `disabled`；④ `buildRows` 不再下发 `members`；⑤ 多项目去掉 `root_id` overlay。`session-archive.test.mjs` 的「确认弹窗早于 deleteSession」守卫改为嵌套 `if` 写法后仍绿。

## 第五轮用户反馈（2026-11-20，整组置顶 + 右侧信息列对齐）

用户原话：「1.置顶按钮不应该移除 2. 会话组的时间没有对齐」「灯也没对齐」。两条都是第四轮引入的回归。

1. **置顶落了**：第四轮的菜单只搬了重命名/同步/归档/删除，把会话卡菜单第一项「置顶」丢掉了。置顶是按会话存的（`services/pins` 的 `root::sessionKey` 表），分组没有自己的条目 —— 所以 `TaskGroupAggregate` 新增 `pinned`（成员**全部**置顶才算整组置顶，半置顶按未置顶显示，再点一下把剩下的补齐），新增 `pinTaskGroup` 逐条串行写同一批成员的 `onPin`。置顶可逆、与单条置顶一致，**不弹确认**。菜单顺序恢复成与会话卡一致：置顶 → 同步 → 重命名 → 归档 → 删除。两条侧栏把 `onPin` 透传给分组头；多项目那层 `root_id` overlay 同时服务置顶（`handlePinSession` 用 `session.root_id || currentRootIdRef`，缺了会写到当前选中项目的表里）。

2. **对齐**：根因不是列宽而是**层级** —— 会话卡里右侧信息列（灯/时间）是行的**直接子元素**，不在可点的那层盒子里；分组头把它放进了展开按钮内部，而那个按钮有 `padding: 7px 4px 7px 2px` 的 4px 右内边距，于是灯和时间整体左移 4px。修法是把信息列移到 `</button>` 之后、`TaskSessionGroupMenu` 之前，几何直接与会话行同源（`minWidth 24px`、`justifyContent flex-end`、`paddingLeft 2px`、10px 次级字）。副作用：点时间/灯不再切换展开 —— 与会话行一致。

验证：`session-task-group.test.mjs` 18 条（新增「信息列在按钮之外」结构断言、与会话行四个几何属性逐项比对、整组置顶聚合与动作断言）；三处变异各自打红后还原 —— ① 把信息列挪回按钮内部（对齐用例红）；② 聚合里删掉 `pinned_at` 判定（置顶与聚合两条红）；③ 多项目侧栏去掉 `onPin`（置顶用例红）。

## 第六轮用户反馈（2026-11-20，会话组只占一份项目预算）

用户原话：「在侧边栏的时候，无论展开还是收起，请把会话组作为一个会话，不要因组里有好几个对话而显示不完，出现『还有n个对话』」。

现场：多项目侧栏每根只取 6 条顶层会话（`limit_per_root=6`），项目末尾按 `totalCount - 已加载顶层数` 决定要不要显示「还有 N 个会话」。分组之后这个算术不再成立 —— 本机 root `mindfs` 实测 `total_count=7`、已加载 6 条顶层，其中 3 条属任务 #91 收成 1 组，于是列表实际只显示 4 行，却仍按 6 条会话判「装满了」，末尾照样冒出「还有 1 个会话」；收起时更夸张，只有 1 行也照显示。

修法：

1. **预算按行算**：新增纯函数 `taskGroupedRowCount(topLevel, seenCount?)` —— 组头算 1 行，成员不占位。行数不需要 `task_session_count` / 进程内记忆那套阈值：它们只决定「1 条会话算不算单独成组」，而一条会话无论成不成组都占 1 行，所以按本页成员数判就与渲染出来的行数一致。
2. **按缺口补拉**：`useMultiProjectSessionList` 新增一个 effect，项目行数没占满 `MULTI_PROJECT_VISIBLE_LIMIT` 且后端还有更多时继续翻页。三条闸门：一次只补一个项目（`break`）、正在加载的跳过、记住上一次补拉时的顶层会话条数并「没涨就停」（一页回来全是已加载会话的子会话或已有分组的成员时 `remaining` 仍 > 0，再拉只是重复请求）。不需要补拉时清掉记录，换账户/刷新换了一批会话不会被旧记录挡住。
3. **「还有 N 个会话」的显示条件**改为 `expanded && 行数 >= MULTI_PROJECT_VISIBLE_LIMIT && remaining > 0`，数字仍按未加载的**会话**数如实报。单项目侧栏（`SessionList` 的「加载更多」）没有这一行，保持原样。

验证：`session-task-group.test.mjs` 19 条（新增行数用例 6 条断言 + 补拉/显示条件/无进展闸门源码断言）；四处变异各自打红后还原 —— ① `taskGroupedRowCount` 直接返回会话数；② 组件把显示条件改回 `group.totalCount > LIMIT`；③ 去掉无进展闸门；④ 去掉「按行补拉」判据。全量门禁见提交记录。

**线上验收（2026-11-20，`e2e/live` 只读脚本，真实账户）**：换上新 bundle（`assets/index-BXfijVVt.js`，本机 7331 与外网 nginx 均 200）后对着线上跑两组：① 展开态 —— 任务 #91 的 4 条会话全部在列表里（第 4 条本来在第二页，只有补拉生效才会出现），项目末尾**没有**「还有 N 个会话」；② 收起态 —— 不点分组头，同样没有该行，且确实发出了 `before_time=` 翻页请求。冷启动瞬间会连发几次同样的翻页请求（多项目并发返回会把分组重置回第一页，本 effect 随即补回），收敛后 15 s 内不再请求，最终状态正确 —— 属既有「多项目响应整体重建分组」的副作用，不在本轮范围内。
