# App.tsx 剩余拆分方案（实测重划 · 2026-10-08）

> **本文所有数字都是 AST / `wc -l` 实测**，每一组抽取都过了 `verify.mjs` 的窗口校验。
> 前一篇 §三 的计划是在**工具错误**的前提下算的（见 §2），已被本文取代。
>
> ✅ **本方案已执行完毕（2026-10-08）**：`App.tsx` 4182 → **2957**，越过 ≤2999 验收线。
> 执行日志见 §1.2；落地时对 §5 的分组做了三处调整，见 §1.3。

---

## 1. 现状（已核对）

### 1.1 收工时

| 项 | 实测 |
|---|---|
| `web/src` 中 >1000 行的文件 | **无**。`App.tsx` 2957 是唯一接近的；其余最大 `components/action/ActionBar.tsx` 998 |
| `App.tsx` | **2957**（4182 → 2957，净 **-1225**） |
| `npm run typecheck` / `npm test` | ✅ 绿 / **210 tests, 209 pass, 1 skip, 0 fail** |
| `make build-web` + e2e | ✅ 构建通过 / **10 passed** |
| `go run ./scripts/check-upstream` | ✅ 721 个 delta 文件被 50 组完整覆盖 |
| 验收线 | ✅ 2957 **≤ 2999** |

### 1.2 执行日志（14 步，25 个新模块，合计 2566 行）

行数是**收工后**实测（第 14 步的两个模块在提交后又删掉了 cut.py 生成的空
`return {};`，所以比提交时的数字少 2-3 行）。

| 步 | commit | 主题 | 新模块（行数） |
|---|---|---|---:|
| 1 | `04575fe` | 置顶 / URL 状态 / 目录加载错误 | `useAppSessionPins` 61 / `useAppReplaceURLState` 36 / `useAppFormatDirectoryLoadError` 42 |
| 2 | `d37a8b0` | 启动期四件事（自更新/引导/E2EE） | `useAppBootstrapActions` 186 |
| 3 | `5ef82a7` | 会话重载与按复合键存的派生状态 | `useAppSessionReload` 99 / `useAppSessionKeyState` 91 |
| 4 | `e77184d` | 每根绑定/抽屉写入器与切项目锁复位 | `useAppRootBindingSetters` 122 |
| 5 | `bcc9597` | 会话选择的四个入口 | `useAppSessionSelection` 134 |
| 6 | `e1cfb55` | 插件信任状态机与插件渲染 | `useAppPluginTrust` 97 / `useAppPluginRender` 65 |
| 7 | `6538252` | 项目显示与路径点击 | `useAppProjectDisplay` 75 / `useAppFileNavigation` 137 |
| 8 | `95cfb54` | 会话快照/查找表、工作台动作、根反查 | `useAppSessionSnapshots` 108 / `useAppWorkspaceActions` 65 / `useAppRootResolution` 65 |
| 9-11 | `b82b40d` | 切项目落点、主视图动作、slash 查找、看板列 | `useAppBoundSessionDir` 80 / `useAppMainViewActions` 123 / `useAppSlashCommandResult` 50 / `useAppKanbanStageColumns` 74 |
| 12 | `fcc3741` | 会话主视图与三个根内容渲染器 | `useAppSessionView` 110 / `useAppRootContentRenderers` 169 |
| 13 | `cc1013a` | 看板面板 TaskBoardView | `useAppKanbanTaskPanel` 126 |
| 14 | `d21b808` | ref 镜像 + 同段 24 条 effect | `useAppRefSync` 220 |
| 14b | `e770a53` | bootstrap/E2EE 与插件渲染后的 effect | `useAppBootstrapEffects` 110 / `useAppPluginRenderEffects` 121 |

前置三刀（`6db9048`，本系列之前）：`useAppProjectRemoval` 102 /
`useAppTaskDrawer` 154 / `useAppAgentActions` 149。

### 1.3 落地时对 §5 分组的三处调整（都是「拆」）

1. **A11（第 8 步）拆成三组**。`verify.mjs` 报窗口 `[1763, 2249]`，但
   `resolveRootForSessionKey`(1120) 远在窗口之前、`selectedSessionSnapshot`(2488)
   远在窗口之后 —— 合成一组会把调用点塞到离依赖几百行外的地方。
2. **A15（第 12 步）拆成两组**。`sessionView` 单独验窗是 17 params / 原地可放；
   三个 `renderRoot*` 合起来 51 params、比拆三个（14+14+30=58）还省。
   硬凑一组反而要把 `sessionView` 下移 51 行。
3. **A17 从「组件」改成 hook**。方案写的是 `components/app/KanbanTaskPanel.tsx`，
   但包一层组件会往 React 树里插新边界（新挂载身份、新 props 对象），
   是可观察的行为变化；hook 返回同一个 `<TaskBoardView>` 元素，元素树逐字不变。

**规律**：§5 是按「同一文件 / 同一主题」分组的，而真正该按**「共享窗口」**分组。
A11 和 A15 都是同一类误判。

### 1.4 内联 effect 只能整段连续搬（第 14 步的关键约束）

React 按声明顺序跑 effect。把中间某几条挑走、或跨过别的 effect 搬，
**执行次序就变了** —— 那是可观察的行为变化，属于 §六红线。

所以第 14 步的 24 条 effect（原 850-969）是**一次连续声明，要么全搬要么不搬**，
不能只挑「像 ref 镜像的」那些。夹在中间的真副作用（加载 agents、监听 window、
记 last-root、排序持久化）留在同一组，不是因为同类，而是挪出去就会改次序。

同理第 14b 步的 8 条与 3 条各自是连续段。

**effect 的两个便利性质**（`verify.mjs` 的语义）：
- 依赖数组是**急切求值**的 ⇒ 用到的值必然都在本行之前声明；
- effect 不产生被引用的名字 ⇒ `hi` 恒为 `end`。

但**窗口的 `lo` 可能不是块自己的行**：`useAppPluginRenderEffects` 的 `lo=2235`
是 `useAppPluginRender` 解构出的 `pluginRender`，三条 effect 都依赖它，
硬放原地会 TDZ —— 调用点必须插到解构之后。**本系列三次遇到这个形态。**


已提交的三刀（`6db9048`）：

| 模块 | 行数 | 内容 |
|---|---:|---|
| `app/useAppProjectRemoval.ts` | 102 | findManagedRootByPath / handleRemoveCurrentRoot / handleRemoveCurrentWorktree |
| `app/useAppTaskDrawer.ts` | 154 | handleSessionChipClick / handleTaskSessionDrawerOpen / refreshTaskRelatedFiles / refreshTasksForRelatedSession / handleSelectKanbanTask |
| `app/useAppAgentActions.ts` | 149 | handleForkAgentMessage / handleRunAgentLifecycleCommand |

后续提交 `29cc76c`：G-Z 锚点随代码搬进 `useAppTaskDrawer.ts` + 登记本文档。

---

## 2. ⚠️ 两个必须先知道的前提

### 2.1 之前那套可行性工具是错的（已修）

`place.mjs` / `group.mjs` / `windows.mjs` / `pack.mjs` / `ctx.mjs` 都把
`const { x } = useHook()` 的 **`BindingElement` 当成「函数形参」记成第 0 行**，
也就是「外部作用域、永远可见」。结果：**TDZ 全部漏报，窗口普遍乐观**。

修正方式（`bindLine()`）：沿 `ObjectBindingPattern` / `ArrayBindingPattern` 往上走，
父节点是 `Parameter` 才算外部；是 `VariableDeclaration` 就记**真实行号**。

| 指标 | 修正前（错） | 修正后（对） |
|---|---:|---:|
| 可搬 `const X = call()` 块 | 79/81 | 67/70 |
| 可搬原始行数 | 1188 | 914 |
| 贪心打包净收益 | 845 | ~647 |

`useAppAgentLifecycle` 那一刀就是被它坑的：实际需要 `handleSendMessage@1884` 等，
却把调用点放在了第 1771 行 → `Block-scoped variable used before its declaration`。
**已回退重做为 `useAppAgentActions.ts`（调用点后移到第 1961 行）。**

### 2.2 「glue」不是可搬材料 —— 这是最容易重复踩的坑

`App.tsx` 里有 **~570 行** 长这样：

```
  const {                                    // ← const { … } = useHook({…})
    clearRootScopedClientState,               //   是已抽 hook 的 props 对象
    loadManagedRootPayloads,
    …
  } = useManagedRoots({ … });
```

**它们是 glue，搬走不减行**（要搬得把 hook 一起搬，而 hook 已经在 `app/` 里了）。
最大的一条 72 行，`pool.mjs` 里占了 "FirstStatement" 标签 —— 用
`ts.SyntaxKind[VariableStatement]` 会返回枚举里第一个同值名字 `FirstStatement`，
而 `d.name` 不是 Identifier（是解构模式），所以早期探针把它们和真正的
「大块」混在一起了。**`pool.mjs` 现在会跳过它们。**

同理跳过的还有 322 行的 `return`（`<AppShell sidebar={…} header={…} …>`，纯 props 转发）
和 `useRealtimeEvents({…})`（100 行，同样是 props 对象）。

---

## 3. 可搬池（`pool.mjs` 实测，已剔 glue）

| 池子 | 块数 | 行数 | 说明 |
|---|---:|---:|---|
| ≥12 行的真块 | 49 | **1057** | 其中 2 块见 §4 |
| 4–11 行的碎块 | 72 | **509** | 含 25 条内联 `useEffect`（≈258 行） |
| 合计 | 121 | **1566** | |

**净收益率实测约 88%**（三刀实测：61→55、103→91、94→84），
外加每个 hook 约 10–14 行的调用点/import 开销。

```
1057 - 74(selectRootNode) - 12(getNodeIdForRoot) = 971   → ~855 净
509（碎块，分批折叠）                                      → ~450 净
                                                    合计 ≈ 1305 净
需求 950  →  余量 ≈ 355
```

---

## 4. 两个特殊块（都不建议动）

### 4.1 `selectRootNode`（74 行）— **结构性卡死，建议原样留在 App**

```
FWD  selectRootNode   L639-712 (74)  win 1561..1391
     FWD: sessionListReloadTimerRef@1560 setGitStatus@777 setGitHistory@781
          setGitDiff@805 setGitStatusLoading@779 setGitHistoryLoading@783
```

它引用了**在第 777–805 行才解构出来的** git 状态 setter，而它自己在**第 1391 行**就被
当成另一个 hook 的入参用掉了 → 窗口 `[1561, 1391]` 为空，**无解**。

两个出路都要付代价：

- (a) 和「解构 git setter 的那个 hook」一起搬 —— 但那个 hook 是 `useGitData` 一类，
  和「切节点」不是同一个域，硬塞会把两个域重新搅在一起；
- (b) 把 `const sessionListReloadTimerRef = useRef(...)`（第 1560 行，1 行）上移到第 639 行之前，
  窗口就变成 `[806, 1390]` —— **行为等价**（`useRef` 无语义），但**动了 hook 调用顺序**，
  踩 §六红线文字，须用户单独批准。

**推荐 (c)：不动它。** 池子里还剩 1492 行，够 950 的指标（见 §3 算术），
没有理由为 74 行去碰红线或破坏领域边界。

### 4.2 `getNodeIdForRoot`（12 行）— 窗口只有 20 行宽

`win 450..469`，因为它在第 467 行就被 `scopedRootKey` 用掉。
只能单独成 hook 且**调用点必须原地**，净收益 ~3 行。**不值得，跳过。**

---

## 5. 已验证的抽取表（每一行都过了 `verify.mjs`）

> `窗口` = 调用点可落区间 `[lo, hi]`，`params` = 该组自由变量数。
> 组内块一律 `cut.py` 一次搬走，**不留转发 stub**。

### 5.1 18 个域 hook（合计 raw 1203 行）

> ⚠️ **下表的分组已按「共享窗口」再调过三处**（A11 拆三组、A15 拆两组、
> A17 从组件改成 hook），见 §1.3。执行时的实际分组以 §1.2 的日志为准。

| # | 目标模块 | 块（行号） | raw | 窗口 | params |
|---|---|---|---:|---|---:|
| A1 | `app/useAppSessionPins.ts` | sessionPinsByRoot 359-377 + applyPinSnapshot 379-391 | 32 | 359..384 | 1 |
| A2 | `app/useAppReplaceURLState.ts` | replaceURLState 1173-1189 | 17 | 844..1210 | 3 |
| A3 | `app/useAppBootstrapActions.ts` | handleStartUpdate 900-940 + handleOnboardingStepChange 1197-1220 + describeE2EEPromptError 2654-2672 + submitE2EESecret 2674-2691 | 102 | 1174..3665 | 21 |
| A4 | `app/useAppSessionReload.ts` | scheduleSessionListReload 1564-1583 + handleLoadOlderSessions 2585-2610 + clearSlashCommandResultForSession 1245-1258 + setMultiProjectSessionPending 1270-1291 | 82 | 1561..1861 | 12 |
| A5 | `app/useAppRootBindingSetters.ts` | setBoundSessionForRoot 1063-1072 + setDrawerSessionForRoot 1074-1084 + setDrawerOpenForRoot 1086-1095 + resetSessionLockForRoot 1097-1114 + resetLocksForRootTransition 1116-1125 | 59 | 468..1168 | 14 |
| A6 | `app/useAppSessionSelection.ts` | handleNewSession 1926-1944 + handleCancelCurrentTurn 1885-1899 + currentSelectionSource 1946-1960 + rememberCurrentFileScroll 1978-1989 | 61 | 1330..1968 | 18 |
| A7 | `app/useAppPluginTrust.ts` | handleTrustPendingPlugins 2291-2307 + invalidatePluginsForRoot 2319-2330 | 29 | 2267..2542 | 11 |
| A8 | `app/useAppPluginRender.ts` | pluginRender 2798-2811 + pluginThemeVars 2867-2886 | 34 | 2767..2814 ⚠ | 5 |
| A9 | `app/useAppFileNavigation.ts` | handleFileViewerPathClick 2371-2384 + handleGitDiffPathClick 2396-2409 + handleDirectoryPathClick 2411-2423 + gitFileStatsByPath 2433-2445 + notifyNodeLoadFailed 2200-2215 | 70 | 2068..2217 | 8 |
| A10 | `app/useAppDirectoryErrors.ts` | formatDirectoryLoadError 1510-1526 | 17 | 263..1546 | 1 |
| A11 | `app/useAppKanbanWorkbench.ts` | resolveRootForSessionKey 1292-1324 + selectedSessionSnapshot 2888-2899 + handleAskUserAnswer 3009-3026 + openWorkspaceProject 3414-3426 | 76 | 1992..2560 | 13 |
| A12 | `app/useAppBoundSessionDir.ts` | showBoundSessionOrRootDir 2120-2165 | 46 | 1992..2612 | 7 |
| A13 | `app/useAppMainViewActions.ts` | handleMainViewSwitcherChange 3378-3412 + switchToRawFileView 3503-3516 + switchToPluginView 3518-3530 | 62 | 2068..3659 | 16 |
| A14 | `app/renderRootSlashCommandResult.tsx` | slashCommandResultForSession 3039-3061 | 23 | 1223..3062 | 2 |
| A15 | `app/useAppRootContentRenderers.tsx` | sessionView 3066-3124 + renderRootWorktreeContent 3206-3226 + renderRootRelatedContent 3227-3247 + renderRootGitContent 3248-3282 | 136 | 3167..3674 | 62 |
| A16 | `app/kanbanStageColumns.ts` | kanbanStageColumns 3284-3330 | 47 | 410..3465 | 2 |
| A17 | `components/app/KanbanTaskPanel.tsx` | kanbanTaskPanel 3447-3498 | 52 | 3437..3743 | 46 |
| A18 | `app/useAppEffects.ts` ×6 | 25 条内联 effect（见 5.2） | 258 | 分段 | — |

**累计 raw 1203 → 净 ≈ 1059。做完 A18 即越过 3000 线。**

### 5.2 A18 的内联 effect 分组（已逐组验过窗口）

effect 的好处：**依赖数组是急切求值的**，所以它用到的值天然都在本行之前声明，
调用点就在原地；而且 effect 不产生被引用的名字，`hi` 恒为 `end`。

| 子组 | 块（行号） | raw | 窗口 | params |
|---|---|---:|---|---:|
| E2 早期启动 | 492-499, 728-742, 743-748 | 29 | 726..end | 10 |
| E3 偏好/布局 | 948-962, 963-971, 990-998, 1005-1009, 1027-1033, 1040-1045, 1046-1054, 1055-1061 | 71 | 860..end | 23 |
| E4 插件/看板 | 2349-2369, 2447-2450, 2452-2458, 2460-2473 | 50 | 2334..end | 13 |
| E5 目录/滚动 | 2579-2583, 2614-2619, 2625-2629, 2631-2635, 2950-2954 | 28 | 2889..end | 9 |
| E6 渲染器段 | 2772-2796, 2813-2845, 2847-2864 | 76 | 2767..end | — |
| E7 杂项 | 1237-1244, 1614-1622, 2193-2197 | 22 | — | — |

### 5.3 碎块折叠（把 4–11 行的碎块并进同域的 hook）

碎块单独成 hook 不划算（开销吃掉一半），但**并进 A1–A17 里已有的同域 hook 几乎免费**。
已验过的 4 组：

| 组 | 块（行号） | raw | 窗口 | params |
|---|---|---:|---|---:|
| E1 早段 key/颜色 | getRootDisplayName 598-605 + getDisplayNodeColor 608-618 + taskCreateProjectOptions 626-636 + scopedRootKey 467-471 | 39 | 454..490 ⚠ | 7 |
| E7 kanban 快照 | sessionByKey 2900-2904 + selectedKanbanTask 2922-2932 + selectedKanbanTaskSessionKey 2933-2937 + selectedKanbanTaskSessionSnapshot 2938-2948 | 32 | 1327..2919 | 7 |
| E9 actionBar 派生 | actionBarSessionKey 2736-2739 + actionBarSession 2726-2735 + actionBarInputHistory 2740-2745 + actionBarQueuedMessages 2756-2764 | 26 | 2721..3781 | 13 |
| E10 工作台 | toggleWorkspaceProject 3337-3345 + liveTasksById 3355-3361 + openWorkspaceTaskDetail 3436-3446 | 27 | 3337..3362 ⚠ | 4 |
| — | `boardSessionByKey` 2911-2920（单列，见 §7 锚点警告） | 10 | 2901..3475 | 2 |

> ⚠ `workspaceBoard`(3363-3373)、`sessionPinsByRoot` 类的 `useWorkspaceBoard({…})` glue
> **不要搬** —— 它是 props 对象，搬了不减行。

---

## 6. 建议执行顺序（按「窗口宽 + 收益确定」排，cumulative 从 3949 起）

| 步 | 内容 | net | 剩余 |
|---|---|---:|---:|
| 1 | A1 置顶 + A2 replaceURLState + A10 目录错误 | -57 | 3892 |
| 2 | A3 bootstrap | -90 | 3802 |
| 3 | A4 会话重载 | -72 | 3730 |
| 4 | A5 每根绑定 setter | -48 | 3682 |
| 5 | A6 会话选择 | -53 | 3629 |
| 6 | A7/A8 插件 | -54 | 3575 |
| 7 | A9 路径点击 + E1 早段 key | -101 | 3474 |
| 8 | A11 看板/工作台 + E7 + `boardSessionByKey` | -101 | 3373 |
| 9 | A12 showBoundSessionOrRootDir | -39 | 3334 |
| 10 | A13 主视图 | -54 | 3280 |
| 11 | A14 + A16 | -60 | 3220 |
| 12 | A15 根内容渲染器 | -124 | 3096 |
| 13 | A17 KanbanTaskPanel | -44 | 3052 |
| 14 | A18 内联 effect（6 组） | -230 | 2822 ✅ |
| 15 | E9/E10 + 其余碎块折叠 | -150 | 2672 |

**第 14 步即越过 3000；第 15 步是余量。**

顺序理由：窗口窄的（A8 `2767..2814`、E1 `454..490`、E10 `3337..3362`）**尽早做** ——
每做完一刀下面的行号都会漂，窗口只有十几行的组经不起漂。

---

## 7. 每刀固定流程（含两处这次新学到的注册义务）

```
1. pool.mjs / windows2.mjs 取准确行列（禁止手数行号）
   ⚠ 不要用 appblocks.mjs 的 getFullStart()：它把紧邻块注释算进来，起始行偏 2–3 行
2. verify.mjs 验窗：lo <= hi，且组内所有块都属于同一领域
3. 写新模块（<1000 行），cut.py 搬块（call_at 支持「窗口内后移插入」）
4. npm run typecheck
5. tests/source-map.mjs 的 "src/App.tsx" 逻辑路径**追加新模块**
   ← 那条路径是 42 项的扁平数组，**50 个测试文件**读它；漏登记 = 搬走的代码从断言里消失
6. npm test（基线 210 / 209 pass / 1 skip / 0 fail）
7. wc -l 复核 App.tsx 与新模块
8. make build-web && E2E_SKIP_BUILD=1 pytest e2e/ -q（10 passed）
9. 提交，信息以 Scope: G-BA 结尾
10. go run ./scripts/check-upstream（**必须在提交之后跑**，它比的是 HEAD）
```

### ⚠ 第 10 步会抓的两类真问题（这次都撞到了）

1. **锚点跟着代码走**。`handleSessionChipClick` 里的 `CUSTOM(G-Z)` 注释被搬进
   `useAppTaskDrawer.ts` 后，G-Z 的锚点 `web/src/App.tsx:CUSTOM(G-Z)` 就失配了。
   命中时改 `docs/upstream-customizations.yaml` 的锚点到新文件，**并把新文件加进该组的 `files:`**。
2. **未登记文件**。新模块落在 `G-AA` 的 `web/src/app/` 前缀里能过关；
   但 `docs/plans/*.md` 走的是文档组的显式列表，新文档必须手工加进去。

**本次已知的下一个锚点风险**：G-Z 的第二个锚点是
`web/src/App.tsx:const boardSessionByKey = useMemo`。
第 8 步搬 `boardSessionByKey` 时，必须同步把该锚点改到新模块。

---

## 8. 否决与红线

| 不做 | 原因 |
|---|---|
| 搬 `selectRootNode`(74) / `getNodeIdForRoot`(12) | 见 §4，前者窗口为空，后者净收益 ~3 行 |
| 搬 `const { … } = useHook({…})` glue / `return` / `useRealtimeEvents({…})` | 搬了不减行（§2.2） |
| 用「迟绑定 ref」或转发 stub 绕开窗口 | 把 TDZ 从编译期挪到运行期，测试抓不到 |
| 改任何 hook 调用顺序 | §六红线；`selectRootNode` 的选项 (b) 属此类，需单独批准 |
| 改依赖数组 / 调用顺序 / state 清理顺序 | §六红线 |

---

## 9. 文档订正清单（`docs/plans/deep-split-phase3.md` 现在多处失真）—— ✅ 已全部落实

- §三标题 `4182 → <3000（-1183）`：真实基线 4179、当前 3949、还需 **950**。
- §3.1 的构成表与「从 2823 行可动池搬 1183 行（42%）」：按**错误工具**算的。
  真实可搬池 **1566 行**（剔 glue 后），净收益 ~1305。
  表里「hook-call glue 1215 / useEffect 481 / state 1811 / 其它 531」也需要重核
  —— 实测 glue ≈570 行 + return 322 行。
- §3.2 的 A1–A9「10–12 次整域抽取」：真实形态是 **18 个域 hook + 6 个 effect hook + ~10 组碎块折叠**，
  见本文 §5。
- §八 日志补 `6db9048`、`29cc76c`；补 §2.1 / §2.2 两条教训。
- 本文 §5 的表就是执行清单，`deep-split-phase3.md` 只需回指。

**落实结果**：§三 已标「已作废／见本文」并把标题改成实际终点 `4182 → 2957（-1225）`；
§3.1 / §3.2 已删去错误数字、改成「真实结论」并回指本文 §5 与 §1.3；
§八 已补 `6db9048` / `29cc76c` 与四条教训（工具漏报 TDZ、glue 不可搬、
effect 只能整段连续搬、逐字比对要防静默少比）。

---

## 10. 工具速查（`/tmp/mfsprobe/`，都依赖 `web/node_modules/typescript`）

### 10.1 工具清单

| 工具 | 用途 |
|---|---|
| `pool.mjs <file> <fn> <minSize>` | **总清单**：每条可搬语句的范围/大小/自由变量数，剔 glue，标 `FWD`（有后置依赖） |
| `windows2.mjs <file> <fn> <minSize>` | 每块的 `lo..hi` 窗口，标 `TIGHT` / `IMPOSSIBLE` |
| `verify.mjs <file> <fn> [name@]a-b …` | **开工前必跑**：一组块能否共用一个 hook、窗口落在哪、params 有哪些。**`name` 可省** —— 内联 `useEffect` 没有名字，全传 `useEffect@a-b` 会解析到同一条，必须只传区间 |
| `place.mjs <file> <fn> <callAt> name:a-b …` | 已定调用点时复核 `PARAM-TDZ` / `USE-TDZ` |
| `gentypes.mjs <file> <fn> <names>` | 按**变量名**选块：用 TS checker 吐 params 的真实类型 + 模块级 import 清单 |
| `gentypes2.mjs <file> <fn> a-b …` | 上者的**按行区间**版本，给没有名字的 effect 块用 |
| `cut.py`（stdin JSON） | 搬块 + 生成新模块 + 插 import；`call_at` 支持后移插入 |
| `verbatim.py <base> <模块:名字,…>` | 搬走的 `const X = …` 与基线逐字一致（去空白） |
| `verbatimeff.py <base> <模块>` | 搬走的**内联 effect** 与基线逐字一致（effect 没有名字，`verbatim.py` 覆盖不到） |

### 10.2 两个「静默少比」的坑（都真踩过，都已修）

逐字比对脚本最危险的失效方式不是报错，是**报「全一致」但只比了一小部分**：

1. `verbatim.py` 原本只认 `= useCallback|useMemo`，且从第一个 `(` 开始配平 ——
   对 `useCallback((next: URLState) => {` 这种，配平在第一个 `)` 就归零，
   **只比了首行**。改成从 `=` 起做全括号配平、并把名字与 `=` 之间的类型标注
   （可跨行）纳进来之后，立刻抓到第 1 步的 `replaceURLState` 少了两行注释
   （核对后是**上提成了模块 doc**，不是丢失；现在例外写成可自我证明的形式：
   脚本要求被上提的原文真在模块里逐字出现）。
2. `verbatimeff.py` 原本靠调用方传「最后一条的 deps 行」定结尾，可各条 effect
   的依赖数组互不相同，`rindex` 会停在中间那条 —— 报「6/6 一致」，实际只比了
   8 条里的前 6 条。改成扫到模块最后一个 effect 收尾行。

> 教训：**凡是「查找 + 截取 + 比对」的检查，都要有一条断言证明「比了多少」**
> （块数、行数、字符数），否则范围算错时会以「通过」的形态骗人。

### 10.3 cut.py 的两处补丁

- `hook_import_from` 从 `module` 推导（`web/src/app/x.ts` → `./app/x`）。
  原先是必填键，漏一次就 `KeyError` 一次、白跑一轮。
- `returned` 为空时生成普通调用语句 `useX({…});`，不再生成
  `const { } = useX({…})` + 末尾 `return {};` —— destructure 一个 void 返回值
  会让 tsc 报 TS2532。**注意 `make build-web`（vite）不做类型检查**，
  所以这个错误构建是通过的、只有 `npm run typecheck` 会红。

> 已修的坑：五个探针都把 `BindingElement` 记成第 0 行（§2.1）。
> 若再看到 `Block-scoped variable used before its declaration` 而代码看着没错，
> **先怀疑工具，别怀疑代码**。

