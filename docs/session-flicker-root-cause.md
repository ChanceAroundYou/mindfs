# 切换会话闪烁 —— 根因定位报告

日期：2026-10-05 · 复现环境：本机 `127.0.0.1:7331`，headless Chromium 经 CDP 真实点击复现

## 一、实测证据

被测会话（`root=mindfs`）：

| 会话 | key | 服务端总条数 | `?latest=20` 窗口返回 |
|---|---|---|---|
| A「审核llmux调用日志与优化」 | `1791197567-…` | 20 | seq 1..20，`hasMore:false` |
| B「前后端彻底分离 / #28」 | `1791109592-…` | 45 | seq 26..45，`hasMore:true` |

### 1.1 用户可见的症状（DOM 逐帧采样）

```
切到短会话：
  t=24276ms  n=20   seq 26..45     ← A 的旧内容还挂在屏幕上
  t=24351ms  n= 0   seq null..null ← 整块塌掉（间隔仅 75ms）
  t=25111ms  n=20   seq 1..20      ← 800ms 后才重新出现

切回长会话：
  t=28759ms  n=48   seq 1..48      ← 完整体
  t=30229ms  n=28   seq 21..48     ← 塌掉 20 条
  t=33179ms  n=20   seq 26..45     ← 再塌 8 条
  t=38317ms  n=70   seq 1..70      ← 5 秒后才补回来
```

### 1.2 埋点逐帧（SessionViewer 内部 `composedExchanges`，只看主区 viewer）

「长 → 短」，`init:seed` 的 `winN=0` 是关键：

```
+   0ms  WIN   init:seed              winN=0    seed 取到 **空数组**
+   0ms  VIEW  comp= 20 seq=26..45    vis=20    ← 上一个会话的窗口还在渲染
+ 249ms  VIEW  comp=  0 seq=null..null vis=0    ← 【塌陷窗口】
+ 279ms  WIN   applyWindow:keepOlder  winN=20 seq=1..20
+1088ms  VIEW  comp= 20 seq=1..20     vis=20    ← 重新出现
+1395ms  APP   restoreActiveSession:cacheWrite  cachedBefore=-1 winN=20 written=20
```

同一方向重复切换时，缓存命中就不再塌陷（这解释了为什么「有时闪有时不闪」）：

```
切回已缓存的会话 A：
+   0ms  VIEW  comp= 20 seq=26..45    ← 仍是旧内容
+   1ms  WIN   init:seed              winN=20 seq=1..20   ← seed 非空
（此后全程无 comp 变化）
```

## 二、根因

**两个缺陷叠加，缺一不可。**

### 缺陷 1：切换时 `visibleExchanges` 保留的是**上一个会话**的数据

`SessionViewer` 在 App.tsx:8279 与 9740 两处都**没有 `key` prop**，所以切换会话时组件不重挂载，
`visibleExchanges`（React state）跨会话复用。

init effect（SessionViewer.tsx:1453）依赖 `[sessionKey, rootId, applyWindow]`，切到新会话时
它会重跑，但**重跑前的那一帧仍然拿旧 `visibleExchanges` 渲染**——这就是 `t=0ms comp=20 seq=26..45`
那一行：那是**会话 B 的 seq 26..45**，屏幕却已经切到了会话 A。

### 缺陷 2：`init:seed` 的种子取自**未就绪**的 `session` prop

init effect 里 `seedExs` 来自 `session?.exchanges`，但依赖数组**不含 `session`**。因此它取到的是
「上一次渲染时那个 `session` 对象」的值——而此时 `getSessionSnapshot`（App.tsx:2034）读的是
**新 key 的缓存**，新会话还没缓存 → `fallbackExchanges` 也是空（`toSessionItem`，appSession.ts:168，
压根不复制 `exchanges` 字段）→ **seed 为空**。

于是：

```
旧 visibleExchanges（B 的 20 条）
  ↓ seedExs = []          → setVisibleExchanges([])   ← 塌陷
  ↓ getSessionWindow 回包  → applyWindow(keepOlder)   ← 800ms 后才回来
```

`applyWindow` 的 `keepOlder` 合并的是 **`prev`**，而 `prev` 刚被 `setVisibleExchanges([])` 清空，
所以「保留旧数据」的意图完全落空——它保的是一个空数组。

### 为什么我之前的三次修复都没生效

它们都在修「**合并时丢数据**」，而这里丢的是「**合并之前就被清空**」：

| 我之前的修复 | 修的是 | 与本 bug 的关系 |
|---|---|---|
| 删掉首帧 `slice(-SESSION_WINDOW_SIZE)` | 首帧截断 | seed 本来就是空数组，截断与否无差别 |
| `applyWindow` 加 `keepOlder` | 窗口覆盖更老的数据 | `prev` 已被清空，合并对象是空的 |
| `restoreActiveSession` 窗口并入缓存 | 窗口覆盖整份缓存 | 只影响缓存，**不经过** viewer 的 `visibleExchanges` |
| `toPersistentSession` 从头淘汰 | 淘汰方向反了 | 与本路径无关 |

**共同的错误**：我一直在假设「缓存里有完整数据 → 加载时把它正确地并进去」，
而实际是「切换那一帧缓存里**没有**新会话的数据 → viewer 把仅有的旧数据清空 → 等网络回来」。

### 为什么用户描述是「先塌成最后一条用户消息，再慢慢补回来」

`init:seed` 为空 + 窗口 `latest=20` 只回尾部：第 1 帧如果恰好有一帧用旧 `visibleAux`/`windowMeta`
渲染，会话 A 的窗口（A 恰好 20 条、`hasMore:false`）就先出来了；随后 B 的窗口以
「A 的 seq 编号」短暂可见——因为 `tailOverlay`（SessionViewer.tsx:1227）在 `latestSeq===0`
时会保留 `seq>0` 的缓存条目，于是 A 的最后几条先以 overlay 身份出现，
B 的窗口（seq 26..45）再整体替换。每一步都是「整体替换」而非「合并」，所以每一步都在闪。

## 三、修复（已实施并验证）

**`App.tsx` 两处 `<SessionViewer>` 加 `key={会话键}`**（`:8280` 主区、`:9748` 浮层）。共 9 行，其中 7 行是注释。

不新增逻辑、不改状态流：让窗口化视图的可见集与当前会话同生共死。

### 3.1 验证：同一套 CDP 探针，同两个会话

| 场景 | 修复前 | 修复后 |
|---|---|---|
| 切回**已缓存**的长会话 | `48 → 28 → 20 → 70`，4 次塌陷，耗时 5s | **1 次采样，零变化** |
| 主区埋点（切到短会话） | `comp 20 → 0 → 20`，中途塌陷 800ms | `comp 20 → 20`，无中间态 |
| 冷缓存首访 | 塌陷（本来就没数据） | 仍塌（见 3.2，属预期） |

线上 bundle（`index-CEov3iup.js`）实测切回：2.4 秒内 `n=22` 全程稳定，无任何中间帧。

### 3.2 剩下的那一次塌陷是**预期的**

首次访问一个**从未加载过**的会话时，缓存里没有它的数据，界面只能先空着等网络。
这不是闪烁而是加载态——有 `loading` 遮罩。用户报告的「切换后再切回来」属于缓存命中路径，已被消除。

### 3.3 回归护栏

`web/tests/session-switch-flicker.test.mjs`（5 条）钉住：
- 两处调用点都存在、都有 `key`、key 不是常量字符串、key 与该处 `session` prop 指向同一对象；
- **前提仍在**：SessionViewer 的可见集确实还是 `useState`（若将来提到全局 store，
  这条会失败，提示重新评估 key 的必要性）。

变异测试验证过护栏是活的：删掉两处 key → 5 条里 3 条立刻红。

## 四、为什么我之前三次修复都没生效

它们都在修「**合并时丢数据**」，而这里丢的是「**合并之前就被清空**」：

| 我之前的修复 | 修的是 | 与本 bug 的关系 |
|---|---|---|
| 删掉首帧 `slice(-SESSION_WINDOW_SIZE)` | 首帧截断 | seed 本来就是空数组，截断与否无差别 |
| `applyWindow` 加 `keepOlder` | 窗口覆盖更老的数据 | `prev` 已被清空，合并对象是空的 |
| `restoreActiveSession` 窗口并入缓存 | 窗口覆盖整份缓存 | 只影响缓存，**不经过** viewer 的 `visibleExchanges` |
| `toPersistentSession` 从头淘汰 | 淘汰方向反了 | 与本路径无关 |

**共同的错误**：我一直在假设「缓存里有完整数据 → 加载时把它正确地并进去」，
而实际是「切换那一帧缓存里**没有**新会话的数据 → viewer 把仅有的旧数据清空 → 等网络回来」。

## 五、附：本次调查新增的事实

- 服务端窗口端点 `GET /api/sessions/{key}?root=&latest=20` 返回 `window_meta{total,hasMore,minSeq,maxSeq}`，
  会话 A 是 `hasMore:false`（20 条全在窗口内），会话 B 是 `hasMore:true`（45 条里只有尾部 20 条）。
  **闪烁在 A（有完整窗口）上同样发生**，所以它与窗口是否覆盖完整无关。
- `App.tsx` 有**两个** `restoreActiveSession` 调用点（`:3924` selectSession 内、`:7883`
  独立的 stale-补载 effect）。前者受 `loadingSessionRef` 去重，第二次调用会 `await inflight`
  并复用同一个 promise，返回**同一个冻结对象**；这解释了切换时 `applySession(restored)`
  与独立 effect 的 `setSelectedSession` 会写入**同一个对象引用**。
- `tailOverlay` 在 `latestSeq===0` 时保留所有 `seq>0` 缓存条目（SessionViewer.tsx
  的 `if (latestSeq === 0 || seq <= latestSeq) continue;` 实为 `latestSeq===0` 时**不 continue**），
  这会让「刚切换、最新窗口还没回来」时把旧数据画出来。加了 key 之后这条路径在切换时不再被走到。
- 本次调查用的探针留在 `/tmp/probe9.mjs`（CDP 真实点击 + `[data-session-seq]` 逐帧采样）
  与 `/tmp/probe12.mjs`（配合临时埋点的组件级 trace）。**未入库**——它们依赖本机密码与
  `/tmp` 下的 chromium profile，不适合当仓库测试。要长期护栏请用
  `web/tests/session-switch-flicker.test.mjs`（源码形状断言，不需浏览器）。