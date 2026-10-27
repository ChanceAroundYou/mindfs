# 会话滚动三处回归：按钮显隐跟位置 / 到底逐帧收敛 / 前插按元素锚定

状态：Implemented 2026-10-27，隔离实例 + 无头 Chromium 已验，待 ship。

## 症状（用户 2026-10-27 报）

1. **上翻跳变**：滚着滚着突然往上跳一大截，似乎发生在上滚触发加载数据的时候 ——
   从当前位置跳到刚加载那一页的顶部。
2. **「回到底部」按钮失效**：经常停在中间，怀疑和分段加载或虚拟挂载有关。
3. **按钮显隐与页面实际状态脱钩**：手动下滚到底按钮不消失（必须点一下）；
   拖动滚动条上滑按钮不出现。
   用户的措辞很关键：「不是逻辑反了/失效，而是它没有跟页面的实际状态挂钩，太死板了，
   必须点或者滑动」。对 ②③ 的要求：「无论什么方式滑动，按钮都应该出现，
   只和页面实际位置到不到底有关，而且点击后应该精准到底实际的最底部」。

## 根因（三条独立，都在「窗口分页 + 虚拟化」同时成立的路径上）

### ① 按钮显隐没有任何位置输入

`useSessionViewerScroll.ts` 里 `b24dbee3` 加手势监听时把
`el.addEventListener("scroll", updateStickiness, ...)` 删掉了，**却留着对应的
`removeEventListener`**。剩下的三个手势（wheel / touchmove / keydown）只会
`setShowJumpToLatest(true)`，永远不置假。于是按钮只能靠「点击自己回写」。

同期 `setShowJumpToLatest(!shouldStickToBottomRef.current)` 把「追加内容时要不要
自动跟随」当成「现在离底多远」用 —— 两件事混成一份真相，而写 scrollTop 的程序化滚动
（拖滚动条、跳转）不会经过任何一个手势，状态根本不变。

### ② 点击「到底」用的平滑滚动会被虚拟库打断

原实现是 `container.scrollTo({ top: maxTop, behavior: "smooth" })`。虚拟化会话里
每次挂载新行都可能撑大 `scrollHeight`，而 `@tanstack/react-virtual` 在**向下**方向的
测量修正会走 `applyScrollAdjustment` → 即时 `_scrollToOffset`（`behavior: undefined`）。
**一次即时写入就取消浏览器进行中的 smooth 动画**，于是滚到半路停住，且没有后续补写。

### ③ 前插锚点用的是 estimate 撑出来的高度

`loadMore` 原来记 `scrollHeight - scrollTop`，回包后单个 rAF 写一次
`scrollTop = scrollHeight - anchor`。虚拟化下前插的这些行先按 `estimateSize` 占位、
overscan 挂载后再实测，每行 estimate→actual 的差值都会整体推移下面的所有行；
而 G-KD 的护栏在**后退方向刻意不补偿**（手势优先），位移原样落在视口上。
实测一页 8 条 → 视口跳 2435–12230px（页数越多越大），正是用户说的「跳一大截」。

## 修法（改的是判据，不是加护栏）

| 处 | 改法 |
|---|---|
| 显隐 | 恢复 `scroll` 监听（与 removeEventListener 对称）；判据改成 `distanceFromBottom > STICK_REARM_PX`；三个手势只负责 `shouldStickToBottomRef = false`（让位），不再碰显隐 |
| 到底 | `stickSessionToBottom` 改为逐帧收敛：每帧写 `maxTop`，跑够 `MIN_FRAMES=8` 且连续 3 帧稳定才收工，上限 120 帧；用户上滚立即让位。`behavior` 参数保留只为签名兼容 |
| 前插 | 锚点改成「视口内第一条可见行的 `data-timeline-key` + 真实矩形」，回包后逐帧比对 `getBoundingClientRect().top` 收敛（`MIN_FRAMES=4`、连续 3 帧稳定、上限 40 帧）；找不到锚行时退回原 `scrollHeight - anchor` |

元素矩形锚定的意义：**库补没补、补了多少都会被自动吸收**，不会双补偿 ——
这也是为什么不去调 `shouldAdjustScrollPositionOnItemSizeChange`。

## 验证（隔离实例，绝不动线上）

`web/tests/session-virtualization.html` 新增 `runPagingChecks()`：fixture 内替换
`sessionService.getSessionWindow`（240 条、每页 8 条），走的是**同一份**
`useSessionWindowing` 代码，没有 HTTP、没有 agent。无头 Chromium（playwright-core +
已装的 chrome-for-testing）驱动。

同一脚本对 HEAD 与改后各跑一遍（`minSeq` 相对基线逐项对比）：

| 检查 | HEAD | 改后 |
|---|---|---|
| 上翻 6 页锚行漂移 | 2435 / 5700 / 8965 / 12230 / 16 px | 0 / 0 / 16 / 16 / 16 / 16 px |
| 点击到底后的 `gap` | **800 px**（上滚 800 后点，原地不动） | **0** |
| 上滚后按钮出现 | 否 | 是（`upGap=50`） |
| 滚回底部按钮消失 | 否 | 是 |

同一轮回归：`runScrollIntentChecks` 桌面 1280×900 与移动 390×844 都通过。

### 第二次验证（2026-10-27 收尾轮）

上一轮留下的 4 个红点（`runVirtualizationChecks`、`runStreamingTextChecks` 各一条，
加上两处 fixture 自身缺陷）在收尾轮全部查清并修掉，**没有一个是产品缺陷被掩盖**：

| 红点 | 真因 | 处置 |
|---|---|---|
| `Streaming content did not update` | fixture 用 `seq: 999` 建模流式行，而持久行按 seq 去重，同 seq 的新对象被丢弃 | 改成真实形状 `seq: 0`（瞬时行），改按正文定位 |
| `Search target should be visible` | fixture 的 `getSessionWindow` 对 `virtual-test` 回 `null`，`windowMeta` 恒 `{0,0}`，`useTargetSeqScroll` 的虚拟化快路径不成立 | 补一份回全量历史的桩（`minSeq:1, maxSeq:len`） |
| `Cannot read properties of undefined (reading 'click')` | `Tool 10-0` 在 G-AT 的折叠组卡里，收起时不渲染子卡 | 先展开组卡，再点目标卡 |
| `runPagingChecks` 退回 `pages: 0` | **产品缺陷**：`windowMeta` 跨会话残留（见下） | 修在 `useSessionWindowing` 根因上 |

`runPagingChecks` 那条是这轮唯一新发现的产品缺陷，已在隔离实例之外的纯前端 fixture
上复现并修复：从 `minSeq` 起于 1 的会话切到起于 233 的会话，`min(1, 233) = 1` 让
`loadMore` 恒发 `beforeSeq=1`，服务端回空 → `res` 为 null 直接返回 → **上翻一页也加载
不出来**。修法是初始化 effect 里在发尾部窗口请求**之前**重置 `windowMeta`。

同时恢复了一处被上游合并冲掉的定制：`toolViewCache`（CUSTOM(G-AT)）。`976a5d0f` 合
v0.5.5 时把 `SessionViewer.tsx` 整体换成上游版本，`useToolCallCard` 的 `viewCache`
参数从此没有任何调用方 —— 编译、类型、全量测试全绿，只有真机上把工具卡滚出视口再
滚回来才会看到展开态丢失、详情重新请求。已恢复整条接线并补 `web/tests/tool-view-cache.test.mjs`
钉住它（删掉任意一行接线，该测试立刻红）。

修后四个检查在桌面 1280×900 与移动 390×844 两个视口全部通过；`runPagingChecks` 的
锚行漂移仍是 `[0,0,16,16,16,16]`（`maxDrift 16` ≤ 一行），`clickGap 0`、`upGap 50`。

**既有失败与本次无关**（HEAD 上逐字复现）：上一轮记录的
`runVirtualizationChecks` 与 `runStreamingTextChecks` 两条已在收尾轮查明为 fixture
缺陷并修掉（见上表），不是产品回归；顺带修了 fixture 的陈旧 import
（`components/SessionViewer` → `components/session/SessionViewer`），否则页面根本挂不上。

## 影响文件

- `web/src/components/session/useSessionViewerScroll.ts` — 显隐判据 + 逐帧收敛
- `web/src/components/session/useSessionWindowing.ts` — 元素矩形锚定 + 多帧收敛 + 窗口态按会话重建
- `web/src/components/session/SessionViewerTimeline.tsx` — 虚拟行加 `data-timeline-key` + 透传 `toolViewCache`
- `web/src/components/session/SessionViewer.tsx` — 会话级 `toolViewCache`
- `web/src/components/session/sessionViewer{Tool,Auxiliary}TimelineHelpers.tsx` — 接上缓存
- `web/src/components/stream/ToolCallGroupCard.tsx` — 组卡展开态进缓存
- `web/tests/session-viewer-scroll-split.test.mjs` — 8 条源码级回归断言（含窗口态重置顺序）
- `web/tests/tool-view-cache.test.mjs` — 新增：钉住 `toolViewCache` 整条接线
- `web/tests/session-virtualization.tsx` — 分页 fixture + `runPagingChecks` + 两处 fixture 修正
- `web/tests/deploy-web-assets.test.mjs` — 断言跟着 `62f88294` 的脚本拆分搬家
- `web/tests/README.md` — 运行方式（含 `/mindfs` 前缀这个坑）
- `docs/upstream-customizations.yaml` — G-KB 补窗口态重置、G-AT 补 `toolViewCache` 接线

## 门禁

`npx tsc --noEmit` 干净；`npm test` 415 通过、6 跳过、**0 失败**（194 个测试文件通过、5 跳过）；
`make build-web` 成功；`make check-upstream` 全绿（1442 个 delta 文件 / 260 组）。

## 明确不做

- 不动后端窗口协议与 `SESSION_WINDOW_SIZE`
- 不动 G-KD 的 `shouldAdjustScrollPositionOnItemSizeChange` 护栏
- 不淘汰已加载历史
