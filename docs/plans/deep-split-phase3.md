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
| `services/session.ts` | **973** ✅ | <1000 | 0 | 已拆出 `sessionDispatcher.ts` 193 |
| `components/session/SessionViewer.tsx` | 1127 | <1000 | -128 | `SessionViewerInner` 1045 + imports 110 |
| `components/task/ScheduledAgentTaskDialog.tsx` | **798** ✅ | <1000 | 0 | 已拆出 `ScheduledCronEditor.tsx` 317 |
| `app/realtimeWsHandlers.ts` | 1084 | <1000 | -85 | `createRealtimeWsHandlers` 1044 |
| `components/file/MarkdownViewer.tsx` | **665** ✅ | <1000 | 0 | 已拆出 `MarkdownCodeBlocks.tsx` 435 |
| `app/useRealtimeEvents.ts` | 1075 | <1000 | -76 | `useRealtimeEvents` 930 |
| `components/file/DefaultListView.tsx` | **920** ✅ | <1000 | 0 | 已拆出 `listViewIcons.tsx` 119 |
| `components/stream/ToolCallCard.tsx` | **797** ✅ | <1000 | 0 | 已拆出 `toolCallRender.tsx` 224 |
| `app/useSessionLifecycle.ts` | 1016 | <1000 | -17 | `useSessionLifecycle` 918 |
| `components/action/ActionBar.tsx` | 1002 | <1000 | -3 | `ActionBar` 939（out_degree 55，cognitive 106） |

**合计需迁移约 2200 行。** 已完成 680 行（`MarkdownViewer` -83、`ScheduledAgentTaskDialog` -99、
`DefaultListView` -111、`ToolCallCard` -219、`ActionBar` -4、`useSessionLifecycle` -60、
`useRealtimeEvents` -118、`realtimeWsHandlers` -201、`session.ts` -165），
剩余约 1520 行（App.tsx 1183 + 其余 3 个文件 337：`AgentSelector` 191、`FileTree` 153、`SessionViewer` 127）。

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

## 三、Part A：App.tsx 4182 → **2957**（-1225）—— ✅ 已完成

> ⚠️ **本节原文（含 3.1 的构成表与 3.2 的 A1–A9）是按错误的可行性工具算的，已作废。**
> 工具把 `const { x } = useHook()` 的 `BindingElement` 当成函数形参记成第 0 行
> （＝「外部作用域、永远可见」），于是 **TDZ 全部漏报、窗口普遍乐观**。
> 修正后真实可搬池从 1188 行降到 **914 行**。
>
> **执行清单以 `docs/plans/app-tsx-next-split.md` 为准** —— 那是按修正后的实测重划的，
> 并且已经跑完（14 步、25 个新模块、2566 行）。本节只保留教训。

### 3.1 为什么难：AST 实测构成（**已作废，保留以记教训**）

App() 函数体共 **398 条顶层语句**，除 JSX 外最大单块仅 105 行 —— 文件已**高度碎片化**，
不存在「一刀切掉一大块」的机会。

| 组成 | 行数 | 性质 |
|---|---:|---|
| JSX return | 324 | 1 条语句，已大量委托 |
| **hook-call glue** | **~570**（原表 1215 是错误工具算的） | 已抽出 hook 的 props 对象，**已委托、无肉可切** |
| useEffect | 481 | 58 条，多数 7–16 行 |
| state / ref / useMemo / useCallback | 1811 | 领域状态与派生值 |
| 其它可搬逻辑 | 531 | 少量 handler 与工具函数 |

**真实结论**（修正后重测）：≥12 行的实块 **49 条 / 1057 行**；4–11 行的碎块
**72 条 / 509 行**（含 25 条内联 `useEffect` ≈ 258 行）；合计 **1566 行**，
净收益约 **1305 行**（~88%，每组 hook 的 import/type/doc/解构/调用开销约 6–14 行）。

### 3.2 抽取顺序（**已作废**）

原表列的是 A1–A9「10–12 次整域抽取」，但真实形态是
**18 个域 hook + 6 组 effect hook + ~10 组碎块折叠**，见
`app-tsx-next-split.md` §5。实际落地时又发现 §5 的分组还要按
**「共享窗口」而非「同一主题」**再调三处，见该文 §1.3。

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

## 八、进度日志

| 提交 | 动作 | 结果 |
|---|---|---|
| `c43ade2` | `MarkdownViewer` → `MarkdownCodeBlocks` | 1082 → **665**（+435 新文件） |
| `a6e4415` | `ScheduledAgentTaskDialog` → `ScheduledCronEditor` | 1098 → **798**（+317 新文件） |
| `f2521c6` | `DefaultListView` → `listViewIcons` | 1031 → **920**（+119 新文件） |
| `7fc57f9` | `ToolCallCard` → `toolCallRender` | 1016 → **797**（+224 新文件） |
| （删死码） | `ActionBar` 删重复的 `wsStatusDisplayDelay` | 1002 → **998** |
| | `useSessionLifecycle` → `useSessionLifecycleTypes` | 1016 → **956**（+71 新文件） |
| | `useRealtimeEvents` → `useRealtimeEventsContext` | 1075 → **957**（+130 新文件） |
| | `realtimeWsHandlers` → `realtimeWsSessionMeta` | 1084 → **883**（+235 新文件） |
| `dddf438` | `session.ts` → `sessionDispatcher` | 1138 → **973**（+193 新文件） |

**Part B 进度：12 个文件已完 9 个**，剩 3 个：`AgentSelector` 1191、`FileTree` 1153、`SessionViewer` 1127。

**Part A 进度：✅ 完成 14 步**，`App.tsx` 4182 → **2957**（验收线 ≤2999），
共 25 个新模块 / 2566 行。执行清单与逐步日志见 `docs/plans/app-tsx-next-split.md` §1.2。

前置三刀：`6db9048`（`useAppProjectRemoval` 102 / `useAppTaskDrawer` 154 /
`useAppAgentActions` 149，4179 → 3949）；`29cc76c`（G-Z 锚点随代码搬进
`useAppTaskDrawer.ts` + 登记 `app-tsx-next-split.md`）。

### 本阶段教训（Part A 这一批，两条最贵的）

1. **可行性工具错了会把 TDZ 全漏报**（详见 `app-tsx-next-split.md` §2.1）。
   五个探针都把 `const { x } = useHook()` 的 `BindingElement` 当成函数形参记成第 0 行，
   于是「外部作用域、永远可见」，窗口普遍乐观。**再看到
   `Block-scoped variable used before its declaration` 而代码看着没错，先怀疑工具。**
2. **「glue」不是可搬材料**（同文 §2.2）。`const { … } = useHook({…})` 这类 props
   对象约 570 行、`return` 约 322 行，搬了不减行 —— 它们已经是委托的结果，不是待搬的肉。
3. **内联 effect 只能整段连续搬**：React 按声明顺序跑 effect，挑走中间几条或跨过
   别的 effect 搬，执行次序就变了（可观察的行为变化）。见同文 §1.4。
4. **逐字比对脚本要防「静默少比」**：`verbatim.py` 与 `verbatimeff.py` 各踩过一次
   —— 报「全一致」而实际只比了片段。见同文 §10.2。

### 本阶段教训（`session.ts` 这一刀）

- **新类的构造器不能用 parameter property**（`constructor(private readonly x: T)`）：
  `node --test` 直接加载 `.ts` 走的是 Node 的 strip-only TS 模式，它不支持这个语法，
  运行期报 `ERR_UNSUPPORTED_TYPESCRIPT_SYNTAX`。`tsc` 与 Vite 都过得去，**只有测试会炸** ——
  写完必须真的跑一遍 `npm test`，不能只信 typecheck。
- **拆之前先看「这个类到底管几件事」**。`SessionService` 里 WS 传输层（重连/probe/计时器）
  和订阅派发层（谁订了谁、批帧怎么展开）是两件事，只是都碰 `session.*` 事件所以长在了一起。
  按「不碰 socket、不碰 pendingMessages」这个判据切，比按行数硬切干净得多。
- **源码结构守卫会跟着实现走，拆文件时必须一起改指向**。这次动了三条
  （`pendingStreams` 容量上限 ⑬、批帧形状归一、`emit` 先于 `updateActiveStreamState`）。
  判据**一字未动**，只把读的文件路径换成新模块 —— 换成 `sessionDomainSvc` 那种聚合簇
  反而是**放宽**（红线）。已改的 3 个测试里 2 个是单文件直读，1 个是聚合簇（本来就该含新文件）。
- 公开面跟着搬家时**只改真在用的调用点**（本刀 3 处），不要在宿主留转发 stub。

### 本阶段教训（`ScheduledAgentTaskDialog` 这一刀）

- 搬「cron 相关块」时把**同区的宿主函数** `emptyForm` / `formatTime` 一起搬走了
  （它们与 `splitCron` 相邻、且都在「非导出顶层块」里），导致 `TS2304: Cannot find name 'emptyForm'`。
  判据：搬之前对每个待搬块问一句「**它引用的符号定义在哪？**」——
  `emptyForm` 引用 `FormState`（宿主类型）、`formatTime` 引用 `Locale`（宿主 import），
  引用外移即说明该块不该走。
- `fieldStyle` 是**宿主与 `CronEditor` 共享**的样式常量，必须 `export` 出去由宿主 import，
  不能复制一份（会出现两份真相）。
- **`blocks.mjs` 报的行号比真实行号小约 2**（它把语句前的空行算进了上一块），
  所以脚本里一律用**首行内容断言**（`L(n).startswith(...)`）而不是信它的数字。
- **搬之前先查「谁在用」**：`renderToolIcon` 被 9 个外部模块 import，
  搬走就得改 9 处或加转发 stub（红线）—— 这种符号**留在宿主**，只搬它的私有依赖。
- **循环 import 的 TDZ 风险**：`terminalFontFamily`（`const`）同时被要搬走的 `AnsiOutput`
  和要留下的 `ExecuteToolDetails` 使用。若让它留在宿主、由新模块 import，
  而宿主又 import 新模块的 `AnsiOutput` → 循环 + `const` TDZ → `ReferenceError`。
  解法：把它**随 `AnsiOutput` 一起搬走并 export**，依赖方向保持单向。
