# 下一阶段方向决策（2026-10-07）

> 本文件只回答一个问题：**深拆推进到现在，下一步该往哪走。**
> 事实均为本次实地核验（`wc -l` / AST / `git log`），不是凭记忆复述。
> 落盘待批：按 CLAUDE.md，方案经用户显式批准后才动手。

## 1. 现状核验

### 1.1 行数（`web/src`，i18n 已按要求排除）

```
App.tsx                              4464   ← >1200
services/session.ts                  1430   ← >1200
components/agent/AgentSelector.tsx   1191
components/file/FileTree.tsx         1153
components/session/SessionViewer.tsx 1127
components/task/ScheduledAgentTaskDialog.tsx 1098
app/realtimeWsHandlers.ts            1084
components/file/MarkdownViewer.tsx   1082
app/useRealtimeEvents.ts             1075
components/file/DefaultListView.tsx  1031
```

**除 App.tsx 与 session.ts 外，已无文件 >1200。**

### 1.2 工作区状态

- 分支 `task-35`，工作区干净，HEAD = `206311d`。
- 最近 6 个提交全部通过 `check-upstream`（667 delta 文件 / 50 组完整覆盖）。
- 门禁（上次跑，均为绿）：`npm run typecheck` 0 错；`npm test` 210 / 209 pass / 1 skip / 0 fail；
  `make build-web` 成功；e2e 10 passed。

### 1.3 session.ts 结构（1430 行）

```
  1..  56   导入 + 对外 re-export
 57.. 355   类型块（约 300 行，24 个 type）
356.. 365   PENDING_STREAM_LIMIT + 说明
366..1428   class SessionService（1063 行）
1429..1430  export const sessionService
```

类内部再分四段：

| 段 | 范围 | 行数 | 内容 |
|---|---|---|---|
| 传输 | 366–954 | ~590 | ws / 重连 / 探活 / 心跳 / `handleMessage` / `emitDecrypted` / subscribe |
| 出站 | 956–1175 | ~220 | `sendMessage` / `setPlanMode` / `runSlashCommand` / 队列三件套 / `answerQuestion` |
| 窗口 | 1180–1266 | ~87 | `markSessionReady` / `getSessionWindow` / `getSessionIncrement` |
| 转发 | 1273–1430 | ~158 | 上一轮搬走后的 16 个 thin delegate |

### 1.4 一个真实缺陷（不只是行数问题）

```bash
$ grep -c 'if (!this\.ws || this\.ws\.readyState !== WebSocket\.OPEN)' \
      web/src/services/session.ts
8
```

同一句前置条件抄了 8 遍（另有 3 处把它与参数校验挤在同一行），而 `sendWSMessage`
自己已经有一份同样的守卫 —— 看起来是纯冗余。

**但实地逐条核对后，只有 5 处是纯冗余**（这一步是执行时才做的，初版计划判断过强）：

| 方法 | 守卫是否承重 |
|---|---|
| `sendMessage` | **承重**：守卫后才 `compactContext`（改 `contextCache`）+ `pendingMessages.set` |
| `runSlashCommand` | **承重**：守卫后才 `pendingMessages.set` |
| `markSessionReady` | **承重**：守卫后才 `await e2eeService.ensureSession()` |
| `cancelMessage` / `answerQuestion` | 效果冗余，但各自带 `console.error` |
| `setPlanMode` / `resendPendingMessages` / `removeQueuedMessage` / `updateQueuedMessage` / `sendQueuedMessageNow` | 纯冗余（5 处，已删） |

删掉承重的那 3 处会产生**新副作用**：socket 已关时仍把帧塞进 `pendingMessages`
（下次重连会被 `resendPendingMessages` 误发），或白调一次 e2ee 握手。
所以只删 5 处，其余 6 处（含 `sendWSMessage` 自己那份）保留。

这是 session.ts 里**唯一一个「既减行数、又除重复」的切入点**——但收益比初版估计的小。

## 2. 卡住的地方：目标自相矛盾

当前 goal 原文：

> App.tsx 9585 → **≤4500** 行，**无文件 >1200 行**（i18n 排除）

实地核验结果：

| 子目标 | 现状 | 判定 |
|---|---|---|
| App.tsx ≤4500 | 4464 | ✅ 已达成 |
| 无文件 >1200（App.tsx 除外） | 只剩 session.ts | 差 230 行 |
| 无文件 >1200（**含** App.tsx） | 4464 | ✗ 差 3264 行 |

后两条不可能同时成立：**App.tsx ≤4500 与 App.tsx ≤1200 互斥。**

从 App.tsx 的实际构成看，字面达成第二条意味着什么：

```
   1.. 254   导入 / 类型
 255..4141   App() 的 hook 体（3887 行）：61 个 useEffect + 72 个 useCallback
4142..4464   return 的 JSX（323 行）
```

渲染体**已经**被抽空了（`SessionPane` / `FileTreePanel` / `AppActionBar` / 各 Dialog
都是独立组件），JSX 只剩 323 行。所以再砍 3264 行 = 把 3887 行 hook 体几乎全部
搬进领域 hook，App.tsx 退化成 200–400 行的组合根。

- 你此前的决策是 **「App.tsx 分阶段 ~4000–5000」**，与「≤1200」不是一回事；
- App.tsx 是全应用脊柱，现有测试对它的覆盖**主要靠 e2e（10 条）**，单测基本读不到它；
- 上一轮审核已记为「Phase B 的 AppView prop-bag，低 ROI」。

**所以「无文件 >1200」只应按「除 App.tsx 外的其他文件」解释**——这也是我一直在用的口径。
需要你确认或推翻这一点；它决定 goal 能否收口。

## 3. 推荐方向

> **先封口 session.ts（这是可达成的最后一个「>1200」），再统一收尾ship。
> App.tsx 是否继续下探，单独立项，不在本轮混做。**

理由是收益/风险比：session.ts 有 4 个专门的 VM 测试（`session-window`、
`session-window-dedup`、`pins-authority`、`message-mechanism-merges`）压着，
搬移有真实护栏；App.tsx 剩下的部分几乎没有单测护栏，只能靠 e2e 事后验。

### 3.1 刀 1：类型块外移（约 300 行，风险≈0）

- 新增 `web/src/services/sessionTypes.ts`：接收 57–355 的类型块。
- `session.ts` 用 `export type { ... } from "./sessionTypes"` 保住对外面；
  现在有 8 个模块在 `import type { X } from "./session"`，**一行都不用改**。
- 类型在运行期被擦除，VM 测试、source-map 守卫、e2e 全部不受影响。
- 预期：`session.ts` 1430 → **~1140**，本轮即达成「除 App.tsx 外无文件 >1200」。

### 3.2 刀 2：出站域 + 守卫去重（约 220 行 + 8 处重复，风险中）

- 新增 `web/src/services/sessionOutbound.ts`：把「构造 WS 帧」做成纯函数
  （`buildSessionMessage` / `buildPlanModeMessage` / …），不碰连接状态。
- 删掉 8 份重复的 socket-open 守卫（`sendWSMessage` 已保证同一件事），
  真需要调试信息的那一处保留为单点。
- `SessionService` 保留同名方法：构造帧 → `sendWSMessage`。
- 预期：`session.ts` → **~900**，顺带消掉一个漂移点。

> 刀 1 是「够到 1200」的手段，刀 2 是「值回票价」的手段。只做刀 1 也能收口，
> 但 session.ts 会停在「行数达标、重复没修」的半成品状态——建议两刀都做。

### 3.3 收尾（必做）

1. 刷新两份已过期的计划文档（下面列了具体过期项）。
2. 跑全量门禁：`npm run typecheck` / `npm test` / `make build-web` /
   `go run ./scripts/check-upstream` / `make test` / e2e 10 条。
3. `bash scripts/deploy-all.sh` 走 ship skill 收尾。

### 3.4 文档过期项（实地核对）

`docs/plans/app-tsx-split-lessons-and-next-plan.md`：

- §10 写 `FileTree.tsx 2681`、`SessionViewer.tsx 2378`、`session.ts 2659 ⛔ 被 VM 测试锁住`
  —— 三个数全错（实际 1153 / 1127 / 1430，且 VM 已解锁）。

`docs/plans/frontend-split-refactor.md`：

- §4.2 写 FileTree `1448`（实际 1153）；
- §4.3 写 SessionViewer `1144`（实际 1127）；
- §4.5 写 session.ts `2469`（实际 1430），待拆清单需重写。

## 4. 备选方向（不推荐，列出以便决策）

| 方向 | 做什么 | 代价 | 我不推荐的理由 |
|---|---|---|---|
| **B. 继续砍 App.tsx** | 3887 行 hook 体继续抽领域 hook | 多轮；只有 e2e 护栏；已判低 ROI | 收益是「4464 → 更小」，但 4500 目标已达成，不解决任何已知缺陷 |
| **C. 直接收尾不做刀 2** | 只做刀 1 + 收尾 | session.ts 停在 ~1140 | 行数达标但 8 处重复仍在，等于把已知问题留成技术债 |
| **D. WS 传输整体外移** | 抽 `SessionSocket` 类（~590 行） | 需组合式重构；`ws` 被出站域 15 处直接读 | 收益与刀 2 重叠，但风险高一个量级；应等刀 2 落地、护栏验证过再评估 |
| **E. 字面达成 ≤1200** | App.tsx 砍到 1200 | 多轮，App.tsx 变组合根 | 与你「App.tsx 4000–5000」的既定决策冲突，需你先改决策 |

## 5. 决策记录

**用户裁决（2026-10-07）：「无文件 >1200」只按「除 App.tsx 外的其他文件」解释。**

- App.tsx 的口径维持既定决策 **≤4500**（当前 4464，已达标）；
- 本轮要达成的是：**除 App.tsx 外无文件 >1200** → 唯一待办是 `services/session.ts` 1430 → ≤1200；
- App.tsx 是否继续下探**不在本目标内**，需要时另起目标。
- 据此按 §3 执行：刀 1 + 刀 2 + 收尾。

## 7. 执行结果（2026-10-07 当日）

| 项 | 计划 | 实际 |
|---|---|---|
| 刀 1 类型块外移 | 1430 → ~1140 | ✅ `sessionTypes.ts` 286 行，session.ts **1195** |
| 刀 2 出站域 + 守卫去重 | → ~900 | ✅ `sessionOutbound.ts` 238 行，5 处守卫删除，session.ts **1136** |

留下的三个新模块（均在 `web/src/services/`，G-BA 目录前缀覆盖）：

- `sessionTypes.ts` — 24 个公开类型 + 3 个模块内类型；
- `sessionOutbound.ts` — 9 个 WS 帧构造纯函数；
- 上一阶段已有的 `sessionApi.ts` / `sessionContext.ts` / `sessionWindow.ts` /
  `sessionListCache.ts` / `sessionCacheStore.ts` / `sessionExchangeMerge.ts` /
  `sessionIndexedDb.ts` / `sessionCacheTypes.ts` / `sessionTerminalLayout.ts`。

**「除 App.tsx 外无文件 >1200」已达成。** 后续若继续，下一刀是 §4 的表 D
（`SessionSocket` 整体外移），但那是独立目标。

## 8. 停损条件（任一条命中即停当前刀并重划边界）

- 需要改业务逻辑 / 依赖数组 / 调用顺序 / 状态清理顺序；
- 定向测试失败且不是纯路径映射；
- 出现无法解释的 `any`、重复 import、类型泄漏；
- 需要为通过测试而改变测试语义（**判据加强另算，削弱不算**）；
- 参数超过可读边界且没有明确领域对象。
