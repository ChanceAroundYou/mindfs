# 会话滚动三处回归：按钮显隐跟位置 / 到底逐帧收敛 / 前插按元素锚定
> **Shipped 2026-11-07（v0.5.5-1115）** —— 实现提交已落在该 tag 内，产物已装本机 / WSL / fn 并版本逐字一致
> （本机待用户 `sudo systemctl restart mindfs`、pc-win 已装未运行）。本计划已移入 `docs/plans/README.md` 的历史表，
> 下方「待提交与 ship / 待 ship」是当时的记录，不是当前待办。

状态：Implemented 2026-10-27（含第三轮「一次上滑连翻到底」、第四轮「上翻转圈 + 画面稳定 + 半屏提前量」、第五轮「一屏提前量 + 加载后全屏闪烁」修复），隔离实例 + 无头 Chromium 已验，待 ship。

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

### 第三次验证（2026-10-27 真机反馈：一次上滑连翻到底 → 1 页）

真机复测发现新症状：**上滑碰到加载位置后不再停下，而是不停往上连翻，一口气把整段
历史全加载出来并最终停在最顶部**。`runPagingChecks` 当时抓不到它 —— 它每一轮自己写
`scrollTop = 0` 替用户反复触发，等于把「一次手势翻了多页」这件事盖住了。

取证（`IntersectionObserver` 打桩 + 计数 `getSessionWindow` 的 `beforeSeq` 请求）：

| 量 | 修前 | 修后 |
|---|---|---|
| 一次上滑触发的 `beforeSeq` 请求数 | **29**（233→225→…→9，冲完 240 条） | **1** |
| 回补期间的 `scrollTop` | 恒 **0**（一个字节没写） | 2782（回到原位） |
| 终态 | 停在最顶部 | 与操作前同一行同一位置 |

根因是两条缺陷叠在一起，**都只在「条目数 > 视口 + overscan」时成立**：

1. **锚行有 key 但已卸载时，两条补偿路径都不成立。** 旧 `settle()` 是
   `if (anchorKey) {…只走矩形…} else if (frames < 2) {…重试两帧放弃…}`，而
   `scrollHeight` 兜底被 `if (!anchorKey)` 挡在外面。真实会话一页 8 条 exchange
   展开成 70+ 个时间线条目，前插后原锚行被推到索引 40 开外、远超
   `overscan = 8`，**永远不会再挂载** → `delta` 算不出来 → 一个字节都不写。
2. **`scrollTop` 停在 0 → 顶部哨兵持续 intersecting**，而 IO effect 的依赖里含
   `loadMore`，每次 observer 重建都会立刻回调一次 → 上一页还没回补完就点着下一页。

轻量夹具（每页 16 条）前后都还挂着锚行，所以第 1 条路径从来没被测到 —— 这次把
fixture 改成真实形状（每条 exchange 带 20 张工具卡，`kind` 交替 read/execute 避开
`groupConsecutiveToolCalls` 折叠），首屏 21 个挂载行即进入虚拟化路径。

修法两条：

- `settle()` 里锚行在不在都算同一个 `delta`（缺失时退回 `scrollHeight - anchor - scrollTop`）；
- `loadMore` 入口加 `isPrependingRef` 门禁：上一页还在回补位置时拒收新请求。

`runPagingChecks` 同时补了三条断言：夹具必须真的走虚拟化（挂载行数 0..80）、
回补后 `scrollTop > 0`、`prepends() === pages`，外加检查 ④「滚到顶后完全不再操作，
只应加载 1 页」。变异验证：把两处修复分别撤回，断言立刻红
（`viewport stuck at the very top` / `28 prepend requests for 6 gestures`）。

修后 `runPagingChecks` 在桌面 1280×900 与移动 390×844 均通过：
`{"passed":true,"pages":6,"drifts":[0,0,0,0,0,0],"maxDrift":0,"idlePages":1,"clickGap":0,"upGap":50,"hiddenWhileUp":false}`。

**既有失败与本次无关**（HEAD 上逐字复现）：上一轮记录的
`runVirtualizationChecks` 与 `runStreamingTextChecks` 两条已在收尾轮查明为 fixture
缺陷并修掉（见上表），不是产品回归；顺带修了 fixture 的陈旧 import
（`components/SessionViewer` → `components/session/SessionViewer`），否则页面根本挂不上。

### 第四次验证（2026-10-27 用户第二轮反馈：加载闪、晃、触发太晚）

用户原话：「滚着滚着加载然后闪一下，进度也还是存在晃动。我们能不能明确加入转圈动画，
然后确保加载更旧的内容时画面完全稳定不动？并且把加载触发点提前一点，还差半个屏幕的时候就开始加载。」

三条一起改（都在 `useSessionWindowing.ts` + `SessionViewerLoadingState.tsx`）：

| 症状 | 根因 | 改法 |
|---|---|---|
| 加载时闪一下、位置晃一下 | 转圈是文档流里 `padding: 8px 0` + 14px 的块，出现时把正文整体下推约 30px、消失时推回来，叠在前插回补上 | `position: sticky; top: 6px; height: 0` 浮层 + `pointer-events: none`；盒模型高度为零，出现/消失都不移动任何行 |
| 新一屏「啪」地推上来 | React 把这批 setState 排到下一帧，rAF 回调可能排在提交**之前** —— 那帧读到旧 DOM，`delta` 为 0、回补落空 | `flushSync` 同步提交，紧接着**同步**跑第一步 `settleStep()`（绘制之前），之后才交回 rAF 收敛 |
| 要滚到顶才开始加载 | `rootMargin` 固定 200px | 改成 `clientHeight × LOAD_MORE_AHEAD_RATIO(0.5)`；配 `armedRef` 重新武装闸门（收工上闸，再上滚 `PREPEND_REARM_PX(24)` 才放行） |

**闸门必须重建 observer**（本轮踩的坑）：触发点提前半屏后，回补完成时哨兵通常仍在
提前量之内，intersection 状态没有变化 —— 只把 `armedRef` 置真，IntersectionObserver
不会再投递一次，**上一页结束后就再也点不动**。实测：置于 `scrollTop = 259`（半屏带内）
再滚也不加载。修法是把 `armEpoch` 加进哨兵 effect 的依赖，重新武装时 `setArmEpoch(n+1)`
让 observer 重建、立刻投递一次 intersecting。

`runPagingChecks` 补两条断言（⑤⑥），两条都先被夹具自身的问题挡了一轮：

- **⑤ 提前量**：一度按「跳到 `0.3 × clientHeight` 等 900ms」写，恒为 0 次触发。查明是
  **夹具**写了跨过未渲染区间的 `scrollTop`，虚拟器的测量补偿当场把它推回 501（半屏带之外）；
  代码没问题。改成每步上移 100px（每步都落在已渲染区间），并直接断言「请求发出那一刻
  `scrollTop > 0.3 × clientHeight`」。
- **⑥ 转圈不占布局**：一度从桩函数里取样，`spinner` 恒为 `null` —— 桩里只有 `await`
  之后才有机会读数，而那时 `loadingMore` 已经回到 false。改成在回包挂起期间**直接量 DOM**
  （`[data-session-loading-more]` 的包裹层 `offsetHeight === 0`，且 `scrollHeight` 不缩）。

修后结果（桌面 1280×900）：
`{"passed":true,"pages":6,"drifts":[0,0,0,0,0,0],"maxDrift":0,"idlePages":1,"clickGap":0,"upGap":50,"hiddenWhileUp":false,"aheadFired":1}`
（`prependAt` 显示请求发出时 `scrollTop ≈ 380 / clientHeight 864`，即还剩约 0.44 屏就开跑）。
`runVirtualizationChecks` / `runStreamingTextChecks` / `runScrollIntentChecks` 亦全绿。

> 注意 `runVirtualizationChecks` 之后紧接着跑 `runScrollIntentChecks` 会红
> （`wheel: slow upward scrolling made no progress`）：前者结束时把夹具停在短会话 +
> 折叠 diff 的形状上（`scrollHeight - clientHeight = 35`），上滚 12×5px 走不完 45px。
> 不是产品回归 —— 单跑或紧接 `runPagingChecks` 都通过；README 也是「新开页面再跑」。

### 第五次验证（2026-10-27 用户第三轮反馈：提前量扩到一屏 + 加载后仍全屏闪烁）

用户原话：「提前加载已触发，但加载范围过小，扩大到提前一屏，请根据实际屏幕高度计算，
且加载后仍存在全屏闪烁。」

**① 提前量扩到一整屏**：`LOAD_MORE_AHEAD_RATIO` 0.5 → 1，仍是 `clientHeight × 比例`，
手机与桌面同等提前量。夹具 ⑤ 的判据跟着收紧：起点改到 `1.8 × clientHeight`，断言
「请求发出时 `scrollTop > 0.6 × clientHeight`」——半屏提前量每步 100px 只会落在
0.33–0.5 屏，必然压不过这条。

**② 全屏闪烁：追到根因是「虚拟器按旧 offset 渲染了新数组」**

取证链路（每一步都用无头 Chromium 对着 `session-virtualization.html` 跑）：

| 探针 | 结论 |
|---|---|
| `probe-batches.py` | 容器与滚动层**没变**，但前插瞬间容器里一度是 `p-177-*`（新页最老一段 33 行），下一批才回到 `p-185-*` |
| `probe-uid.py` / `probe-uid-control.py` | 对照组（纯等待、下滚再滚回）全部 `survived == sameKey == 29`，前插这一批 `survived == 0` —— 只有前插会整体换节点 |
| `probe-reactkey.py` | React 手里的 key 就是 `data-timeline-key`，没有错位，排除 key 不稳 |
| `probe-commit-coverage.py` | 逐 commit 量「视口被已挂载行覆盖的百分比」：前插那一次 `top=5304 pct=0 first=p-177-8`，紧接着的第二次 commit `pct=100 first=p-185-9` |
| `probe-order.py`（12× CPU 降速） | 两次 commit **同在 frame 170**、相隔 608 ms —— 中间那个 0% 覆盖有一整帧的时间被画出去 |

根因：`flushSync` 提交时时间线已经是「新页 + 旧页」，但虚拟器的 `scrollOffset` **只由
scroll 事件驱动**，而 scroll 事件要等浏览器渲染步骤才派发。于是第一次提交仍按前插前的
`scrollTop`（824）去 map 新数组：原来的视口段被推到很后面，落进视口区间的是刚插进来的
最老一段（全在视口上方，覆盖率 0%）。同步回补把 `scrollTop` 写成 5304 之后，还得等下一个
scroll 事件才换回 `p-185-*`。夹具太快时这两次 commit 挤在同一帧，所以之前抓不到。

修法：同步回补写完 `scrollTop` 之后立刻 `container.dispatchEvent(new Event("scroll"))`，
让虚拟库走它自己设计好的入口（读 offset → 重算 range → `flushSync` 重渲染），第二次提交
仍在绘制前完成，中间态一次都不呈现。

复测（同一 12× 降速探针）：`scroll st=5086` 在前、`rows-commit pct=100 first=p-185-3` 在后，
`p-177-*` 那一帧不再出现。旁路副作用另行核对：前插后「回到底部」按钮状态与 `stick` 均未被
这次合成 scroll 改写（`bottomGap 37238`，没有被拉回底部）。

`runPagingChecks` 与 `session-viewer-scroll-split.test.mjs` 各补一条钉住这两件事的断言
（一屏提前量常量 + `Math.round(clientHeight × 比例)` 的 rootMargin；合成 scroll 必须排在
第一次同步回补之后）。

## 影响文件

- `web/src/components/session/useSessionViewerScroll.ts` — 显隐判据 + 逐帧收敛
- `web/src/components/session/useSessionWindowing.ts` — 元素矩形锚定 + 多帧收敛 + 窗口态按会话重建
  + `flushSync` 提交 + 同步首步回补 + 合成 scroll 驱动重算区间 + 一屏提前量 + `armedRef`/`armEpoch` 重新武装闸门
- `web/src/components/session/SessionViewerLoadingState.tsx` — 上翻转圈改零高度 sticky 浮层
- `web/src/components/session/SessionViewerTimeline.tsx` — 虚拟行加 `data-timeline-key` + 透传 `toolViewCache`
- `web/src/components/session/SessionViewer.tsx` — 会话级 `toolViewCache`
- `web/src/components/session/sessionViewer{Tool,Auxiliary}TimelineHelpers.tsx` — 接上缓存
- `web/src/components/stream/ToolCallGroupCard.tsx` — 组卡展开态进缓存
- `web/tests/session-viewer-scroll-split.test.mjs` — 13 条源码级回归断言（含窗口态重置顺序、
  「一次上滑只翻一页」的 `isPrependingRef` 门禁与锚行缺失兜底、`flushSync` 提交时机、
  零高度转圈、`armEpoch` 重建 observer）
- `web/tests/tool-view-cache.test.mjs` — 新增：钉住 `toolViewCache` 整条接线
- `web/tests/session-virtualization.tsx` — 分页 fixture + `runPagingChecks` + 两处 fixture 修正
- `web/tests/deploy-web-assets.test.mjs` — 断言跟着 `62f88294` 的脚本拆分搬家
- `web/tests/README.md` — 运行方式（含 `/mindfs` 前缀这个坑）
- `docs/upstream-customizations.yaml` — G-KB 补窗口态重置、G-AT 补 `toolViewCache` 接线

## 门禁

`npx tsc --noEmit` 干净；`npm test` 416 通过、6 跳过、**0 失败**（195 个测试文件通过、5 跳过）；
`make build-web` 成功；`make check-upstream` 全绿。

## 2026-10-28 第四轮：加载完成后仍然闪一下

用户报「加载完成后还是有闪烁」。定位到**两个**独立缺陷，都在前插的回补循环里。

### 缺陷一 · 锚点的时间基准错了

锚点在**发请求之前**采样（`containerTop` / `anchorTop` / `scrollHeight - scrollTop`）。
提前量改成一整屏之后，请求总在用户**还在上滚**的时候发出去；飞行几百毫秒里他滚掉的 S
被 `settleStep` 的 `row.top - anchorTop` 一起算进 delta，回补写完视口整体跳 S。

证据：有序事件日志里 `scrollTop` 涨 10839、`scrollHeight` 涨 10839 本应相等，实测多出
正好 120px（一个滚轮格）；可见行探针在那一帧记录到 15 行同样的 -480px，`dContent`
每行都等于 9984 —— 只有 `scrollTop` 被多写才会这样。

修法：锚点采样收进 `captureAnchor()`，在**回包之后、`flushSync` 提交之前**才采。

### 缺陷二 · 回补循环与手势对抗

循环按屏幕 y 收敛，而屏幕 y 同时被「布局变了」和「用户滚了」改动，读数分不开 ——
用户滚 120px 就被当成「还没补够」，一路补回去，屏幕上整块上行一格（真机 3/5 次）。

**试过的错解（必须记下来，别再走一遍）**：换成内容坐标
`rect.top - 容器顶 + scrollTop`。它对滚动不变，看着更干净，但会把**虚拟器自己写的
`scrollTop` 补偿**也抵消掉 —— 库在前进方向（`scrollWithAdjustments`）已经补过一笔，
内容坐标读到的还是全额，我们再补一次。夹具实测：库写 4480→4698，我们又 +218，
六页每页漂 218px，`maxDrift` 从 0 变成 218。

最终修法：**留在屏幕 y**（它天然吸收库的补偿），单独认出「这一帧是用户滚的」并让位。
方向判据可靠：库只会往**大**了写（后退方向被 G-KD 整个关掉），所以 `scrollTop` 变小
只可能来自手势。让位分支一个字节都不写，只把参照系挪到锚行当前位置。

### 附带修复 · 回补提前收工

收工判据是「连续 3 帧不动」，但新挂载行的测量是**分批**到达的，两批之间可能隔着好几个
安静帧，循环就此收工；下一批实测把视口上方的高度又改了，而没人再补（真机 3 次里 1 次：
收工后 `scrollHeight` 又涨 699px，`scrollTop` 没跟上，视口内整体下移 120px）。
所以「`scrollHeight` 还在变」也算不稳定。

### 验收（线上 A/B，同一会话、同一段手势）

`e2e/live/prepend_visible_jump.py` 的判据不需要知道「谁写的 `scrollTop`」：取视口内
最靠上那一行当锚，它上面只有屏外内容，于是正确行为恰好是 `屏幕位移 == -探针滚动量`。
任何补多、补少、补晚一帧都直接偏离。探针自己写 `scrollTop` 并记账，所以滚动量可精确核算。

| bundle | 内容 | 失败帧 |
|---|---|---|
| `index-CXHZpJ53`（10-27 20:03） | 全部修复之前 | 29 |
| `index-uUQy0xRi`（10-28 01:10） | 全部修复之前 | 32 |
| `index-CJ-H-65i`（01:40） | 缺陷一 | 38 |
| `index-D8AbHAng`（01:52） | 缺陷一 + 收工修复 | 37 |
| `index-p7UZxgw-`（02:13） | + 让位重采样 | **0（6/6 跑法全干净）** |

失败帧的形态是 `scrollTop` 卡死不动、`屏幕位移=0`、应为 60 —— 回补每一帧把手势抵消掉。

### 踩过的探针坑（写在这里省下一次重走）

- **`page.route` 顶替线上 bundle 会被 service worker 绕过**：SW 在自己的 target 里
  fetch `index-*.js`，`page.route` 拦不到，A/B 两边其实是同一份产物。必须
  `browser.new_context(service_workers="block")`。
- **不能按 `dS` 正负或 wheel 计数判「谁滚的」**：同一帧里回补写 +9864、用户滚 -120，
  合成 +9744，怎么分类都会错。
- **不能在 `scrollTop` 触顶时提前结束手势循环**：提前量是一整屏，请求发出后余量不足一屏，
  必然先滚到顶；此时退出恰好漏掉前插落地那一帧，于是所有产物（包括修坏的那版）都「通过」。
  这个假阴性骗过一次 A/B。

## 明确不做

- 不动后端窗口协议与 `SESSION_WINDOW_SIZE`
- 不动 G-KD 的 `shouldAdjustScrollPositionOnItemSizeChange` 护栏
- 不淘汰已加载历史
