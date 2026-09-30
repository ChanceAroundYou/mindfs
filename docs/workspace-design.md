# 跨项目工作台设计

> 状态：**已实现**（2026-09，分支 `task-8`）
> 配套契约：[`main-view-switching-design.md`](./main-view-switching-design.md) —— 本文**不改**那边的
> R1–R10，只改 `workspace` 档位内部长什么样。

## 一、为什么重做

旧工作台（`components/WorkspaceKanban.tsx`，已删）是主区四态之一的 `workspace` 档位。
它按**任务状态**分成四段：等待你（大卡）→ 运行中（紧凑行）→ 快速发起 → 最近归档。问题不在配色，
是结构性的：

1. **信息架构反了**。工作台的用途是跨项目扫读，用户的心智是「我在做 mindfs 和另外两个项目」，
   屏幕给的却是「3 个任务在跑、2 个在等你」。项目名退化成一个灰色 chip 贴在任务后面。
2. **它本质是通知收件箱 + 跳转器**。`selectedKanbanTask` 只在当前项目的任务里找，所以点卡片走的是
   `openWorkspaceProject(rootId).then(() => setSelectedKanbanTaskId(id))` —— 先跳到该项目看板再开详情。
   工作台自己不持有任何任务详情。
3. **视觉体系绕开了刚落地的 token 纪律**。commit `14df0d7` 确立「强调色收敛为节点色唯一来源，
   选中底色统一为中性灰」，而它仍在用硬编码 `#0ea5e9` / `#b45309` / `rgba(148,163,184,...)`。
4. **卡片语言是项目看板的劣化复制**。看板卡里的 stage、worktree badge、aux 徽章全没了，
   只剩「#编号 + 名字 + 项目 chip + 两个按钮」。
5. **项目维度完全缺席**。`managedRootIds` / `getDisplayNodeColor` / `getNodeIdForRoot`，
   App 里早就算好了，工作台一个都没用。
6. **移动端零处理**。316 行里没有任何 `isMobile` 分支。

外部参考（Linear / GitHub Projects / Raycast / VS Code / JetBrains / Datadog / Grafana）的结论一致：

| 观察 | 出处 | 对本项目的含义 |
|---|---|---|
| 静态层级到 10–15 项就崩 | 通用 | 不能靠「多加一层」解决项目变多 |
| 跨项目视图的正解是「分组 + 筛选/搜索」 | Linear、GitHub Projects | 筛选项要存在，且要跨会话记住 |
| 跨项目可见性刻意做弱 | Linear 的跨项目视图是待办队列而非作品集 | 本项目不追求「全局作品集」，只做扫读入口 |
| "card soup" 是被记录的反模式 | Datadog / Grafana | 不能靠平铺更多卡片来解决扫读 |

## 二、四个已确认的决策

| # | 决策 | 选择 | 理由 |
|---|---|---|---|
| D1 | 主信息架构 | **按项目分组 + 顶部待办条** | 见上表；备选的「项目列」在 10 项后不可扫读，「主从双栏」在只有主区一条竖屏时无处安放右栏 |
| D2 | 右栏 `MultiProjectSessionList` | **保持现状** | 工作台只管任务，会话侧已有自己的跨节点扇出（见 D3），两套并存只会打架 |
| D3 | 跨节点 | **前端扇出**，后端零改动 | 见 §四 |
| D4 | 数据陈旧 | **修 bug + 不再整包重拉** | 见 §五 |

## 三、布局

```
┌─ 工作台 · 4 个项目 ──────────────────────── [+新建任务] ↻┐
│ 需要你 (3)                                                │
│  ┌ #12 mindfs 重构登录 ┐ ┌ #3 api 补限流 ┐               │  ← 没人等时整条收起
├──────────────────────────────────────────────────────────┤
│ mindfs      需要你 1 · 2                            [▾]  │  ← 名字+折叠是两个热区
│  ┌──────────────┐ ┌──────────────┐ ┌──────────────┐      │
│  │#12 重构登录态 │ │#13 加 OAuth  │ │#14 补限流    │      │  ← 看板同款卡，不显示正文
│  │· 已完成       │ │· 执行中      │ │· 待审核     │      │
│  │[会][✓][✕]    │ │[会][⏸][✓][✕]│ │[会][⏵][✓][✕]│      │
│  └──────────────┘ └──────────────┘ └──────────────┘      │
│ api-gateway                                           [▸] │
└──────────────────────────────────────────────────────────┘
```

**为什么「需要你」条带能按状态分区**：它回答的是一个与项目无关的问题 ——「现在该我做什么」。
其余全部按项目组织，回答「各项目在干什么」。跨项目维度只出现一次，不会有第二处按状态分区的地方。

**层级靠卡片而不是靠底色**。早期版本里项目名和每条任务都铺同一个灰底，整屏一个色调，
项目与任务的分层根本看不出来。现在项目名直接复用左侧项目列表那套徽章（中性灰底 +
主体色字，`rootBadgeButtonStyle`），任务则是复用看板卡面（`taskCardSurfaceStyle`）的
小卡片 —— 两个层级两种语言，扫读时项目一眼就分得出来。名字前**没有色点**：名字本身
已经是节点色，再配一个同色圆点等于把同一个信息说两遍，还占了名字的缩进。

**卡片两行就是看板卡的两行**（`components/TaskCardRows.tsx`）：标题行（编号 / 名字 /
阶段 / 状态 / worktree）+ 操作行（会话、aux 徽章、开始/暂停/继续/完成/删除）。工作台
和项目看板**引用同一个组件**，差别只在参数上 —— 阶段名工作台按状态给（看板按列给）、
状态文字工作台恒显示且带色（看板只有「已结束」列显示）。以前两边各写各的，工作台那张卡
只有一个名字和三个按钮，既看不见会话也没有配色。

卡片**不显示任务正文**：正文是 `TaskCardRows` 的 `children`，看板传（要全文 + 展开/折叠），
工作台不传 —— 那一段连相关 props 都不进这个组件。列宽 220px 对齐看板单列：150px 时长
任务名几乎必被截断，而工作台是扫读场景，名字看不全等于这张卡白给。

**状态色只有一份**（`appTask.taskStatusColor`），看板卡、工作台卡、任务详情面板都从它拿，
色值走 index.css 的 `--status-ok/-warn/-bad` token，深浅主题各自给值。

**项目组头只有两个热区**：项目名（跳该项目看板）和折叠箭头。箭头**无框无底**
（`background: transparent` + `border: none`）—— 头里已经有「有底色的名字 + 中性徽章」，
再加一个带框按钮就三块底色打架，反而看不出该点哪。热区仍撑成 40×24（12px 图标点不准），
用留白而不是底纹来撑。

**点任务卡不跳项目**。工作台就地弹任务详情面板（`openWorkspaceTaskDetail`）：按项目内
任务号取那一条的完整详情灌进 `taskDetailsById`，由 `selectedKanbanTask` 接手，主区不动。
详情面板要 `stage_runs`/`events` 而 overview 不带，所以不能直接复用 overview 那份。
选中态守卫（`App.tsx`）对工作台开了口子：它按 `currentRootId` 过滤 `kanbanTasks`，
跨项目选中的任务天然不在里面，不放行的话刚弹出的详情会被立刻关掉。

**面板标题不是项目名**。工作台不属于任何项目，顶部却一直显示当前选中的项目名（面包屑），
让人误以为正看着那个项目。工作台态改成「工作台 · N 个项目」，和 MainViewSwitcher 的
「工作台/看板/文件/对话」对齐；面包屑只留给真正的项目视图。

**新建任务一步到位**。按钮（工具栏右侧，紧贴刷新键）直接打开新建任务面板，只多一个
「项目」下拉（`TaskInlineEditState.allowProjectSwitch`）。早期版本叫「快速发起」，中间
还夹了一层「选项目 + 选模板」的小弹窗 —— 两层弹窗两次点击，而且第二层里真正能改的
东西全被挡在后面。看板入口不带这个下拉 —— 那里项目已经定了，多一个选不了的下拉只是
噪声。面板里的 worktree 三个开关、分支列表、偏好存取都跟**面板的目标项目**而不是
`currentRootId`，否则从工作台发起时会串到当前项目。

**筛选**（全部 / 进行中 / 待审核）是 Linear「saved filter」模式的最小实现：项目数增长时靠收窄，
不靠加层级。「待审核」和 `task.status.waitingUser` / `task.column.waitingUser` 用同一个词 ——
同一个状态在工作台上曾经叫「待处理」、在看板上叫「待审核」。空项目只在「全部」下出现。
筛选与折叠都存 localStorage，跨会话保留。

**就地操作**就是看板那一套（完成、立刻跑、暂停/继续、跳会话、删除），因为卡片两行
本来就是同一个组件。改名、改段之类内容编辑仍要去项目看板 —— 工作台的职责是扫读。

## 四、跨节点：为什么是前端扇出

后端 `Overview()` 遍历的是**本节点**的 `s.Roots.ListRoots()`，它根本不知道自己被哪个节点调用 ——
请求打到哪台机器，返回的就是那台的项目。所以「往 `TaskOverviewItem` 加 `node_id`」后端做不了，
也不必做：前端对每个节点各发一次请求，自己打标。

扇出照搬 `loadMultiProjectSessionGroups`（App.tsx）已验证的形状，不是新发明：

| 环节 | 做法 | 为什么 |
|---|---|---|
| 节点清单 | `getNodes()` ∪ `managedRootIds` 解析出的节点 | 两个来源取并集：前者初始化竞态下可能还是空，后者可能还没进 registry |
| 全空时 | 回退 `getActiveNode()` | 与会话侧同一口径，避免首批 nodeId 是空串 |
| 扇出 | `Promise.all` + 单节点 `catch(() => [])` | 一个节点挂了不该清空其余节点的数据 |
| 去重 | `nodeId::rootId::taskId` | 同名项目在两台机器上各算一条 |
| 分组键 | `scopeKey(nodeId, rootId)` | 复用 `services/scope.ts`，与文件树、会话同一套作用域语义 |

**后端零改动**，有断言钉住 Go 的 `TaskOverviewItem` 不出现 `NodeID` 字段。

## 五、数据新鲜度：修 bug 而不是加重拉

旧实现有个真实 bug：`useRealtimeEvents` 的 `task.updated` 只放行 `payload.root_id === currentRootId`，
非当前项目的推送被整条丢弃 → `taskDetailsById` 不动 → 依赖它的整包重拉 effect 不触发。
观感就是「在别处把任务点完成，工作台卡片不变」。

修法是放行 + 保持增量：workspace 模式下接受任意项目的推送（跨节点隔离 `payloadNid !== curNid`
仍然保留），**不**改成「每次 task.updated 都重跑一次扇出」——多节点下那会被事件风暴打爆。
刷新按钮是唯一的整包重拉入口。

同时修掉一个节点路由 bug：`TaskDetailPanel` 的 `nodeId` 写死 `currentRootNodeId`，
而 `accentColor` 却按 `selectedKanbanTask.root_id` 取色 —— 跨项目就地编辑会打到错节点。

## 六、色值纪律

沿用 commit `14df0d7`：

| 用途 | 取值 |
|---|---|
| 项目名字色、项目强调边框 | `getNodeColor(rootId)`，取不到回退 `var(--text-primary)` |
| 选中/hover 底色 | **只**用 `var(--node-row-selected-bg)`（中性灰） |
| 计数 chip 底色 | **只**用 `var(--node-badge-bg)` |
| 节点色的半透明变体 | 运行时 `hexToRgbaApp(nodeColor, alpha)` 算出，不写死 |
| 状态语义色 | `appTask.taskStatusColor` → `--status-ok/-warn/-bad`（index.css，深浅主题各给值） |
| 操作按钮色调 | 复用 `taskCardIconButtonStyle` 既有口径，不引入新色值 |

`components/workspace/workspaceStyles.ts` 里**不得出现任何字面 hex**，有断言守着。

## 七、窄屏

`useResponsive()` 驱动三处：待办条带改纵向堆叠（220px 的卡在 375px 主区里只放得下一张半，
横向滚等于每张都要手动划）、卡片占满整宽、新建任务键改列向。

## 八、文件

| 文件 | 职责 |
|---|---|
| `app/useWorkspaceBoard.ts` | 数据层：跨节点扇出 + 按项目建组 + 筛选 |
| `app/appTask.ts` | `taskStatusLabel` + `taskStatusColor`：状态文案与状态色，工作台/看板/详情面板共用 |
| `components/TaskCardRows.tsx` | **看板与工作台共用的卡片两行**（标题行 + 操作行；正文走 children） |
| `components/DefaultListView.tsx` | 承载主区；`workspaceMode` 时顶部用视图名替代项目名面包屑 |
| `components/workspace/WorkspaceBoard.tsx` | 容器：工具条（筛选 + 右侧动作区）+ 条带 + 项目组 |
| `components/workspace/WorkspaceAttentionBar.tsx` | 顶部「需要你」条带 |
| `components/workspace/WorkspaceProjectRow.tsx` | 单个项目组：项目名（唯一跳转热区）+ 折叠 + 任务卡网格 |
| `components/workspace/WorkspaceTaskRow.tsx` | 单张任务卡（卡面 + 共享两行，不传 children 所以没有正文） |
| `components/workspace/WorkspaceQuickLaunch.tsx` | 新建任务按钮（直接打开新建任务面板） |
| `components/workspace/workspaceStyles.ts` | 工作台自有样式（token-only）；卡片内部样式已归 `TaskCardRows` |

契约测试：`tests/workspace-board.test.mjs`（工作台）、`tests/task-board-view.test.mjs`（项目看板）、
`tests/workspace-node-fanout.test.mjs`（扇出）、`tests/workspace-realtime-freshness.test.mjs`（新鲜度）。
