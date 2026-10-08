# 深度拆分第三阶段计划（新目标：App.tsx <3000，其余 <1000）

> 用户新目标（2026-10-07）：**App.tsx < 3000 行**，且**除 i18n 外所有前端文件 < 1000 行**。
> 这是对上一阶段「App ≤4500、其余 <1200」的收紧。

## 一、基线（AST `wc -l` 实测，非估算）

### 超标清单：13 个文件

| 文件 | 当前 | 目标 | 需减少 | 内部最大块 |
|---|---:|---:|---:|---|
| `App.tsx` | 4182 | <3000 | **-1183** | 碎片化，见 §3 |
| `components/agent/AgentSelector.tsx` | 1191 | <1000 | -192 | `AgentSelector` 1131 行单体组件 |
| `components/file/FileTree.tsx` | 1153 | <1000 | -154 | `FileTreeInner` 927 + imports 180 |
| `services/session.ts` | 1138 | <1000 | -139 | `SessionService` 类 997 |
| `components/session/SessionViewer.tsx` | 1127 | <1000 | -128 | `SessionViewerInner` 1045 + imports 110 |
| `components/task/ScheduledAgentTaskDialog.tsx` | 1098 | <1000 | -99 | 主组件 692 + `CronEditor` 144 |
| `app/realtimeWsHandlers.ts` | 1084 | <1000 | -85 | `createRealtimeWsHandlers` 1044 |
| `components/file/MarkdownViewer.tsx` | 1082 | <1000 | -83 | `MarkdownViewerInner` 459 + `MarkdownCodeBlock` 185 + `MermaidBlock` 110 |
| `app/useRealtimeEvents.ts` | 1075 | <1000 | -76 | `useRealtimeEvents` 930 |
| `components/file/DefaultListView.tsx` | 1031 | <1000 | -32 | 主组件 830 + 4 个图标组件 |
| `components/stream/ToolCallCard.tsx` | 1016 | <1000 | -17 | 主组件 451 + `renderToolIcon` 89 |
| `app/useSessionLifecycle.ts` | 1016 | <1000 | -17 | `useSessionLifecycle` 918 |
| `components/action/ActionBar.tsx` | 1002 | <1000 | -3 | `ActionBar` 939（out_degree 55，cognitive 106） |

**合计需迁移约 2200 行。**

## 二、Part B：12 个「只超一点」的文件（先做，收益确定、风险低）

这些文件几乎都是**一个巨型组件 + 若干已在文件内的兄弟块**。修法统一为
**兄弟块外移（sibling extraction）**：把已经自成一体的子组件/纯函数搬到新文件，
主组件 import 回来。多数文件一次搬运即达标。

| 文件 | 一次搬运即可达标的方案 | 搬走 | 搬后估算 |
|---|---|---:|---:|
| `MarkdownViewer.tsx` | `MarkdownCodeBlock` + `MermaidBlock` + `MarkdownImage` → `markdown/blocks.tsx` | 378 | ~704 |
| `ScheduledAgentTaskDialog.tsx` | `CronEditor` + 3 个图标 → `ScheduledCronEditor.tsx` / `scheduledIcons.tsx` | 207 | ~891 |
| `DefaultListView.tsx` | 4 个图标 + `formatCompactTime` → `listViewIcons.tsx` | 125 | ~906 |
| `ToolCallCard.tsx` | `renderToolIcon` + `applyAnsiCodes` + `AnsiOutput` → `toolCallRender.tsx` | 197 | ~819 |
| `FileTree.tsx` | 抽 1–2 个子面板（如命名 popover / 发送快捷键 popover 的剩余部分） | ~160 | ~993 |
| `SessionViewer.tsx` | 抽头部/滚动区子组件 | ~140 | ~987 |
| `AgentSelector.tsx` | 抽菜单分区（provider 列表 / 模型列表） | ~200 | ~991 |
| `session.ts` | 继续把 HTTP/WS 细节推到既有 `sessionApi.ts` / 新建 `sessionSocket.ts` | ~145 | ~993 |
| `realtimeWsHandlers.ts` | 按事件族拆出 handler 工厂 | ~90 | ~994 |
| `useRealtimeEvents.ts` | 按事件族拆出 sub-hook | ~80 | ~995 |
| `useSessionLifecycle.ts` | 抽 cold-start / restore 子 hook | ~25 | ~991 |
| `ActionBar.tsx` | 抽 `wsStatusDisplayDelay` + 一个子组件（如状态条） | ~15 | ~987 |

**注意**：这些文件都被 `web/tests/source-map.mjs` 的既有逻辑路径引用（或本身是单路径）。
新增子文件后必须：
- 若原文件在 source-map 的某个逻辑路径里 → 把新文件**追加到同一逻辑路径的数组**，
  否则 scan 类断言计数会掉。
- 逻辑路径指向单文件（如 `src/services/session.ts`）→ **不要把新文件塞进该路径**，
  而是让守卫指向真正拥有该代码的模块（上一阶段踩过：会让 `assert.equal(len, 1)` 变 2）。

## 三、Part A：App.tsx 4182 → <3000（-1183）

### 3.1 为什么难：AST 实测构成

App() 函数体共 **398 条顶层语句**，除 JSX 外最大单块仅 105 行 —— 文件已**高度碎片化**，
不存在「一刀切掉一大块」的机会。

| 组成 | 行数 | 性质 |
|---|---:|---|
| JSX return | 324 | 1 条语句，已大量委托 |
| **hook-call glue** | **1215** | 68 个已抽出 hook 的 props 对象，**已委托、无肉可切** |
| useEffect | 481 | 58 条，多数 7–16 行 |
| state / ref / useMemo / useCallback | 1811 | 领域状态与派生值 |
| 其它可搬逻辑 | 531 | 少量 handler 与工具函数 |

**固定开销** 1215 + 324 = 1539。要降到 3000，需从 1811+481+531 = 2823 中搬走 1183 行（约 42%）。
即 **约 10–12 次整域抽取**，每次 80–200 行。

### 3.2 抽取顺序（按 收益 × 安全性 排序）

| # | 领域 | 目标模块 | 预计搬走 |
|---|---|---|---:|
| A1 | 会话列表 / 归档面板状态与懒加载 | `useSessionListPanel.ts` | ~150 |
| A2 | 节点 / 项目加载与切换（含 `selectRootNode` 77 行） | `useNodeProjectSwitch.ts` | ~250 |
| A3 | 文件树 / 目录加载与刷新 | `useFileTreeDomain.ts` | ~200 |
| A4 | 会话选择 / 抽屉 / 绑定 | `useSessionSelectionDomain.ts` | ~200 |
| A5 | agent 生命周期与 fork（`handleForkAgentMessage` 60 + `handleRunAgentLifecycleCommand` 39） | `useAgentActions.ts` | ~150 |
| A6 | 插件域 | `usePluginDomain.ts` | ~100 |
| A7 | e2ee / 更新 / onboarding | `useAppBootstrapDomain.ts` | ~100 |
| A8 | 主视图切换与 URL 同步 | `useMainViewActions.ts` | ~120 |
| A9 | 兜底：剩余分散 handler | 按领域归并 | ~150 |

累计目标 ~1420 行，足以越过 3000 线并留出余量。

### 3.3 每刀固定流程

1. `node /tmp/mfsprobe/appblocks.mjs <App.tsx> App 400` 取准确行列边界（禁止手数行号）；
2. 写新 hook（领域 props 对象，散参不超过约 20 个）；
3. 替换 App.tsx 中的声明/调用点，删掉原块，**不留转发 stub**；
4. `npm run typecheck` → 修到 0 错；
5. 在 `web/tests/source-map.mjs` 的 `src/App.tsx` 逻辑路径**追加新文件**；
6. `npm test`（基线 210 tests / 209 pass / 1 skip / 0 fail）；
7. `wc -l` 复核 App.tsx 与新模块，确认新模块 <1000；
8. 独立提交，信息以 `Scope: G-BA` 结尾。

## 四、验收与门禁

| 门禁 | 通过标准 |
|---|---|
| `npm run typecheck` | 0 错 |
| `npm test` | 210 tests / 209 pass / 1 skip / **0 fail** |
| `make build-web` | 成功 |
| e2e | `10 passed` |
| `go run ./scripts/check-upstream` | 全绿（**先提交**，delta 由 `baseline..HEAD` 算，未跟踪新文件不算） |
| 尺寸 | `App.tsx < 3000`；除 i18n 外**无文件 ≥1000** |

尺寸复核命令：

```bash
cd web/src && find . \( -name '*.ts' -o -name '*.tsx' \) -not -path './i18n/*' \
  | xargs wc -l | sort -rn | awk '$1>=1000 && $2!="total"'
```

输出为空即达标。

## 五、工具与红线

- **结构分析用 `codebase-memory-mcp` CLI**（本机 `~/.local/bin/codebase-memory-mcp`）：
  `cli search_graph` / `cli get_architecture` / `cli trace_path`。
  项目名 `home-xiaokubao-projects-mindfs-.worktree-task-35`；索引已刷新（11431 节点 / 51222 边）。
  注意参数是 `repo_path`（索引）与 `project`（查询），不是 `path`。
- **精确边界用 `/tmp/mfsprobe/*.mjs`**（TypeScript Compiler API，**不落仓库**）：
  `appblocks.mjs`（App 体块表）、`blocks.mjs`（任意文件顶层块表）、`glue.mjs`（App 构成量化）。
- 红线（沿用既有教训）：
  1. 禁止手数行号切嵌套 JSX —— 手工切块反复产生 `TS1005/TS17008/TS2657`，失败就 `git restore` 重来；
  2. 新文件不进仓库外目录；临时脚本只在 `/tmp/mfsprobe/`；
  3. 不合并 effect、不改依赖数组语义、不改状态清理顺序；
  4. 不新增「转发 stub」；旧块必须真删；
  5. ref 是稳定身份，补进依赖数组不改变触发时机 —— 允许；改 effect 顺序 —— 禁止；
  6. 每刀独立提交，绝不 `git add -A` 之外的批量操作跨刀混提。

## 六、停损条件（任一条命中即停当前刀并重划边界）

- 需要改业务逻辑 / 依赖数组 / 调用顺序 / 状态清理顺序；
- 定向测试失败且不是纯路径映射问题；
- 新 hook 需要超过约 20 个**无领域意义**的散参（有明确领域对象不算）；
- 新文件超过 1000 行；
- 为过测试而**削弱**已有断言（加强不算）；
- 同一文件反复 `git restore` 后仍无法给出可靠 AST 边界。

## 七、执行顺序总览

1. **先做 Part B**（12 个文件，收益确定、单文件独立、互不影响）—— 每个文件一次提交；
2. **再做 Part A**（App.tsx，逐域抽取）—— 每域一次提交；
3. 最后跑全量门禁（typecheck + npm test + build-web + e2e + check-upstream）并复核尺寸；
4. 刷新本文件与 `docs/plans/frontend-split-refactor.md` 的真实行数。
