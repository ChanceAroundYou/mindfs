# App.tsx 剩余拆分方向（2026-10-08 实测重划）

> 写给下一轮执行者。**本文所有数字都是 `wc -l` / AST 实测**，不是估算。
> 现状：Part B 已收工，`web/src` 里 >1000 行的只剩 `App.tsx`；它还差 **-939 行**。
> 本文不含已批准范围之外的决策；标注 ⚠️ 的地方需要用户点头才能动。

---

## 一、现状核对

| 项 | 实测值 |
|---|---|
| Part B（12 个「只超一点」的文件） | **已完成并提交**（到 `17e3f4b` 为止全部绿） |
| `web/src` 中 >1000 行的文件 | 只剩 `./App.tsx` |
| `App.tsx` | 本次会话前 4179 → 现 **3938**（3 刀，-241），**尚未提交** |
| `npm run typecheck` | 🔴 2 条 |
| `npm test` | 🔴 208/210（基线 210 / 209 pass / 1 skip / 0 fail） |
| 距验收线 | 3938 → ≤2999，还差 **939 行** |

### 当前红灯明细

```
src/App.tsx(1775,63): error TS2448: Block-scoped variable 'handleSendMessage' used before its declaration.
src/App.tsx(1775,63): error TS2454: Variable 'handleSendMessage' is used before being assigned.
src/app/useAppAgentLifecycle.ts(24,46): error TS2304: Cannot find name 'SessionMode'.
tests/task-session-jump-root.test.mjs  (AssertionError: handleSessionChipClick must route through buildSessionJumpTarget)
```

根因是同一个：**工具把「解构声明」误判成「外部作用域」**（见第二节），于是
`useAppAgentLifecycle` 被放到第 1771 行，而它需要的 `handleSendMessage@1826`、
`handleRequestFileContext@1953` 等 35 个绑定都还没声明。

---

## 二、⚠️ 两个此前被低估的硬事实

### 2.1 `tests/source-map.mjs` 的 `"src/App.tsx"` 是 42 项的扁平数组，**50 个测试文件读它**

逻辑路径 `src/App.tsx` 映射成一串物理文件、按顺序 `"\n"` 拼接后喂给测试的正则断言。
**每抽一个新模块就必须追加进这个数组**，否则那 50 个测试里凡是断言搬到新模块的代码的，立刻红。

现在 3 个新模块一个都没登记 —— 这就是那条红测试的成因。
修法照旧：追加数组项，**断言原文一字不改**（这是既定先例，`SessionViewer` / `FileTree` 两刀都这么处理的）。

对比：`tests/message-mechanism-merges.test.mjs` 的 `scanSrc` 是直接 `walk(web/src)`，不走 source-map；
但它是例外，别默认。

### 2.2 我这一轮的可行性分析工具是错的，已修

`place.mjs` / `group.mjs` / `windows.mjs` / `pack.mjs` / `ctx.mjs` 五个探针都把
`const { x } = useHook()` 这种 **`BindingElement` 当作「函数形参」记成第 0 行**，
也就是「外部作用域、永远可见」。结果：**所有 TDZ 全部漏报，窗口普遍乐观**。

修正方式（`bindLine()`）：往上走到 `ObjectBindingPattern` / `ArrayBindingPattern` 的父节点，
是 `Parameter` 才算外部；是 `VariableDeclaration` 就记真实行号。

修正前后对比（同一份 App.tsx）：

| 指标 | 修正前（错） | 修正后（对） |
|---|---:|---:|
| 可搬 `const X = call()` 块 | 79/81 可行 | **67/70 可行** |
| 可搬原始行数 | 1188 | **914** |
| 贪心打包净收益 | 845 | **~647** |

**教训（进 §八 教训区）**：解构声明不是外部作用域。工具漏判 `BindingElement` 的 TDZ，
会让整组参数被放到错误位置——症状是 typecheck 报 `Block-scoped variable used before its declaration`，
而它看起来像「代码写错了」，实际是「工具说谎了」。

---

## 三、修正后的可搬池（真实预算）

| 池子 | 原始行数 | 净收益估计 | 说明 |
|---|---:|---:|---|
| `const X = useCallback/useMemo(...)` 块 | **914**（67 块） | ~647 | 打包成 ~19 个 hook；单块 12–74 行 |
| 内联 `useEffect` / `useMemo` 语句（≥14 行） | **~880** | ~400 | 取前 10 大已够：72/69/63/47/42/38/38/36/35/34 = 474 |
| 尾部 JSX 渲染器 | **258**（7 块） | ~200 | 见 5.3 |
| 合计 | | **~1250** | 越过 -939，预计落到 ~2690 |

`useRealtimeEvents({...})` 那个 105 行的调用是**纯 glue**（68 个已抽 hook 的 props 对象之一），
搬它一行不减，**跳过**。

---

## 四、第 0 步（阻塞项）：先回到绿，再把已完成的 3 刀提交

1. `web/tests/source-map.mjs` 的 `"src/App.tsx"` 数组追加
   `src/app/useAppProjectRemoval.ts`、`src/app/useAppTaskDrawer.ts` → 那条红测试自愈。
2. **`useAppAgentLifecycle` 这一刀回退重做**，不要凑合：
   - `place.mjs` 实测 **PARAM-TDZ 35 项**：`externalSessions@1788` … `handleSendMessage@1826` …
     `currentSelectionSource@1935` … `handleRequestFileContext@1953`。它不能在第 1771 行调用。
   - 拆法：`handleForkAgentMessage` 单独一刀（调用点 ≥1955）；`handleRunAgentLifecycleCommand` +
     `refreshSessionsAfterExternalImport` 另起一刀。顺手补 `SessionMode` 的 import。
3. 门禁：`npm run typecheck` → `npm test`（目标 210/209/1/0）→ `wc -l` → `make build-web` →
   `e2e` → 提交（`Scope: G-BA`）→ `go run ./scripts/check-upstream`。

---

## 五、后续方向

### 5.1 第 1 步：抽干 `const X = ...` 池（约 -647，19 刀）

按「块大 + 窗口干净」排序，**每刀先用 `place.mjs` 验 `PARAM-TDZ == 0 && USE-TDZ == 0` 才动手**。

| 刀 | hook | 块 | 原始 | 备注 |
|---|---|---|---:|---|
| P1 | `useAppUpdateE2EE` | handleStartUpdate, handleOnboardingStepChange, describeE2EEPromptError, submitE2EESecret | 102 | 窗口宽松 |
| P2 | `useAppSessionReload` | scheduleSessionListReload, scheduleMultiProjectSessionReload, handleLoadOlderSessions, clearSlashCommandResultForSession | 69 | 调用点 ≥1588 |
| P3 | `useAppPendingPromotion` | setMultiProjectSessionPending, applyPendingToMultiProjectGroups | 33 | |
| P4 | `useAppPins` | applyPinSnapshot, sessionPinsByRoot | 32 | |
| P5 | `useAppPlugins` | handleTrustPendingPlugins, handleDisablePendingPlugins, invalidatePluginsForRoot, pluginThemeVars, pluginRender, switchToPluginView | 90 | 窗口不齐 → 拆 2–3 个 |
| P6 | `useAppFileNavigation` | showBoundSessionOrRootDir, handleFileViewer*Click, handleGitDiffPathClick, handleDirectoryPathClick, gitFileStatsByPath | ~100 | `showBoundSessionOrRootDir` 窗口 **2362..2600**，必须后移插入 |
| P7 | `useAppMainView` | replaceURLState（早，单独一刀）, handleMainViewSwitcherChange, switchToRawFileView | 66 | |
| P8 | `useAppSessionSelection` | handleNewSession, handleCancelCurrentTurn, currentSelectionSource, rememberCurrentFileScroll | 61 | |
| P9 | `useAppDrawerBindings` | setBoundSessionForRoot, setDrawerSessionForRoot, setDrawerOpenForRoot, resetSessionLockForRoot, resetLocksForRootTransition | 59 | |
| P10 | `useAppKanbanSnapshots` | resolveRootForSessionKey, handleAskUserAnswer, selectedSessionSnapshot, sessionByKey, selectedKanbanTask, selectedKanbanTaskSessionKey, selectedKanbanTaskSessionSnapshot, boardSessionByKey | ~105 | |
| P11 | `useAppWorkspace` | openWorkspaceProject, workspaceBoard, toggleWorkspaceProject, openWorkspaceTaskDetail, liveTasksById, handleSelectArchivedSession, handleEditUserMessage, actionBarQueuedMessages, actionBarInputHistory | ~90 | 拆 2 个 |
| P12 | `useAppKeyHelpers` | getNodeIdForRoot, getDisplayNodeColor, getRootDisplayName, scopedRootKey, rootSessionKey, treeCacheKey, openManagedDir, taskCreateProjectOptions, switchMainView, bumpCacheVersionDebounced, handleRestartAgent, handleSelectSessionRef, formatDirectoryLoadError | ~80 | 窗口只有 468..469，**必须拆**（`getNodeIdForRoot` 单独、最早） |
| P13 | `useAppAgentLifecycle`（重做） | handleForkAgentMessage, handleRunAgentLifecycleCommand, refreshSessionsAfterExternalImport | 105 | 见第 0 步 |

#### ⚠️ 两个已知「结构性卡住」的块，不要硬来

- **`selectRootNode`（74 行）**：需要 `sessionListReloadTimerRef@1561`，却在 `@1390` 就被用掉
  （作为另一个 hook 的入参）。单独一刀无解。两个选项都需要用户裁定：
  - (a) 把它和 `@1390` 处那个 hook 的入参一起搬（改动的面变大，但仍然只搬代码）；
  - (b) 只搬 `selectRootNode`，把 `const sessionListReloadTimerRef = useRef(...)` 的声明上移到它之前。
    **这会改变 hook 调用顺序**（`useRef` 无语义、行为等价，但踩了 §六「不改调用顺序」的红线文字），
    须显式批准并写进日志。
- **`notifyNodeLoadFailed`（16 行）**：窗口 `3432..2205` 为空，同类问题。

### 5.2 第 2 步：内联 effect 池（约 -400）——下一阶段的主力

这些是 `ExpressionStatement` 形态的 `useEffect(...)`。**依赖数组是急切求值的**，
所以它们用到的值天然都在本行之前声明 —— 调用点就在原地，比第 1 步安全得多。

- 边界表要按 **`getStart()`** 重跑，**不要用 `appblocks.mjs` 的 `getFullStart()`**：
  后者会把紧邻的块注释算进来，实测起始行偏 2–3 行（结束行是对的）。
- 按领域命名（读一遍块内容再定），预计落到 `useAppRealtimeWiring` /
  `useAppDirectoryEffects` / `useAppKeyboardShortcuts` / `useAppScrollRestore` 等 8–10 个模块。
- 仍要过 `place.mjs`（effect **体**里可以惰性引用后面的值，但 deps 数组和 hook 入参不行）。

### 5.3 第 3 步：尾部 JSX 渲染器（约 -200）——最干净的一批

都在文件最尾、只读上方已声明的值，**无 TDZ 风险**：

| 块 | 行数 | 去处 |
|---|---:|---|
| `sessionView` | 59 | `components/app/MainSessionView.tsx` 或 `app/renderSessionView.tsx` |
| `kanbanTaskPanel` | 52 | `components/app/KanbanTaskPanel.tsx` |
| `kanbanStageColumns` | 47 | `app/kanbanStageColumns.ts` |
| `renderRootGitContent` | 35 | `app/renderRootGitContent.tsx` |
| `slashCommandResultForSession` | 23 | 并入 `app/renderRootSlashCommandResult.tsx` |
| `renderRootWorktreeContent` | 21 | 并入 `app/useRootContentView.ts` 邻居 |
| `renderRootRelatedContent` | 21 | 同上 |

---

## 六、工具要补的两件事

1. **`/tmp/mfsprobe/cut.py` 支持「窗口内后移插入」**：现在它只会把 hook 调用放在组内**最早**那块的位置。
   而 `showBoundSessionOrRootDir`（块在 2109，窗口 2362..2600）和 `handleForkAgentMessage`
   （块在 1771，窗口 ≥1955）必须能把调用点落到别处。加一个 `call_at` 参数即可
   （从下往上删完所有块之后，再按新行号插入）。
2. **每刀强制门禁**（加进 §3.3 固定流程）：`place.mjs` 的 `PARAM-TDZ` / `USE-TDZ` **必须都为 0**；
   命中就拆组或后移，**绝不用「迟绑定 ref」或转发 stub 绕过**（那会把 TDZ 从编译期挪到运行期）。

---

## 七、文档必须同步的地方（现在就是错的）

`docs/plans/deep-split-phase3.md`：

- §三标题仍写 `4182 → <3000（-1183）`。实测基线 **4179**、当前 **3938**、实际还需 **-939**。
- §3.1 的构成表与「从 2823 行可动池里搬走 1183 行（42%）」是按**错误工具**算的，
  要用修正后的 **914 行 const 池 + ~880 行 effect 池 + 258 行 JSX 池**重写。
- §3.2 的 A1–A9「10–12 次整域抽取」表与实际形态不符：真实形态是
  **~19 个细粒度 `useAppXxx` hook + ~10 个 effect hook + 7 个 JSX 渲染器**。
- §八 日志补 `d839f3b`（FileTree）、`17e3f4b`（AgentSelector）与本轮 3 刀；
  新增教训「解构声明不是外部作用域」+「`appblocks.mjs` 用 `getFullStart()`，起始行偏 2–3 行」。

---

## 八、执行顺序总览

```
第 0 步  救绿 + 提交 3 刀（回退重做 agent-lifecycle + 登记 source-map）
第 1 步  19 刀 const 池        -647      → ~3290
第 2 步  8–10 刀 effect 池     -400      → ~2890
第 3 步  7 刀 JSX 渲染器       -200      → ~2690

每刀固定流程：
  place.mjs 验窗（PARAM-TDZ=0 且 USE-TDZ=0）
  → cut.py / 手切
  → npm run typecheck
  → tests/source-map.mjs 追加新模块
  → npm test（210/209/1/0）
  → wc -l 复核（新模块必须 <1000）
  → make build-web
  → e2e（10 passed）
  → 提交，信息以 Scope: G-BA 结尾
  → go run ./scripts/check-upstream
```
