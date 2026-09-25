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
┌─ 工作台 ────────────── [全部|进行中|待处理] ──────↻─┐
│ 需要你 (3)                                          │
│  ┌ #12 mindfs 重构登录 ┐ ┌ #3 api 补限流 ┐          │  ← 没人等时整条收起
├───────────────────────────────────────────────────┤
│ ● mindfs      需要你 1 · 2 · 3 会话           [▾]  │  ← 节点色点 + 计数 + 折叠
│    重构登录流程    登录     ⏱2h   [✓][⏵][⏸]      │
│    加 OAuth 支持   OAuth   ⏱1d   [✓][⏸]          │
│ ● api-gateway  1 · 1 会话                    [▸]  │
│ ● webapp       空 · 新建任务                  [▸]  │  ← 空项目也出现
├───────────────────────────────────────────────────┤
│ 快速发起  [选择项目 ▾]  [输入任务内容…      ]     │  ← 常驻底部
└───────────────────────────────────────────────────┘
```

**为什么「需要你」条带能按状态分区**：它回答的是一个与项目无关的问题 ——「现在该我做什么」。
其余全部按项目组织，回答「各项目在干什么」。跨项目维度只出现一次，不会有第二处按状态分区的地方。

**空项目必须出现**。后端 `Overview()` 只 append 有任务的项目，拿返回的 items 建组会让
「一个任务都没有的项目」从工作台上彻底消失 —— 而空项目恰恰是最需要被看到的那批。
所以组以 `managedRootIds` 为基准建（`useWorkspaceBoard.ts` 的 `toRootEntries`）。

**筛选**（全部 / 进行中 / 待处理）是 Linear「saved filter」模式的最小实现：项目数增长时靠收窄，
不靠加层级。空项目只在「全部」下出现。筛选与折叠都存 localStorage，跨会话保留。

**就地操作**只保留三个不改任务内容的状态迁移：完成、立刻跑、暂停/继续。
改名、改段之类去项目看板 —— 工作台的职责是扫读，不是编辑。

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
| 项目色点、项目强调边框 | `getNodeColor(rootId)`，取不到回退 `var(--text-secondary)` |
| 选中/hover 底色 | **只**用 `var(--node-row-selected-bg)`（中性灰） |
| 计数 chip 底色 | **只**用 `var(--node-badge-bg)` |
| 节点色的半透明变体 | 运行时 `hexToRgbaApp(nodeColor, alpha)` 算出，不写死 |
| 状态语义色 | 复用 `taskCardIconButtonStyle` 既有口径，不引入新色值 |

`components/workspace/workspaceStyles.ts` 里**不得出现任何字面 hex**，有断言守着。

## 七、窄屏

`useResponsive()` 驱动三处：待办条带改纵向堆叠（220px 的卡在 375px 主区里只放得下一张半，
横向滚等于每张都要手动划）、卡片占满整宽、快速发起改列向。

## 八、文件

| 文件 | 职责 |
|---|---|
| `app/useWorkspaceBoard.ts` | 数据层：跨节点扇出 + 按项目建组 + 筛选 |
| `components/workspace/WorkspaceBoard.tsx` | 容器：工具条 + 条带 + 项目组 + 快速发起 |
| `components/workspace/WorkspaceAttentionBar.tsx` | 顶部「需要你」条带 |
| `components/workspace/WorkspaceProjectRow.tsx` | 单个项目组：可折叠头 + 任务行 |
| `components/workspace/WorkspaceTaskRow.tsx` | 单条任务行（紧凑，不复刻看板大卡） |
| `components/workspace/WorkspaceQuickLaunch.tsx` | 快速发起（Select + PromptEditor） |
| `components/workspace/workspaceStyles.ts` | 共享样式（token-only） |

契约测试：`tests/workspace-board.test.mjs`（工作台）、`tests/task-board-view.test.mjs`（项目看板）、
`tests/workspace-node-fanout.test.mjs`（扇出）、`tests/workspace-realtime-freshness.test.mjs`（新鲜度）。
