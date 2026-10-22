# 大会话加载卡顿：三层收口（窗口预算 / 载荷去重 / 时间线虚拟化）

**状态**：Implemented（2026-10-22），三批全部落地并真机 A/B 验证；待提交与 ship。

## 结果（同会话、同操作序列、两个隔离实例 A/B）

| 指标 | 基线 HEAD | 现在 |
|---|---|---|
| 首帧最坏长任务 | 544 ms | **285 ms** |
| 首帧 DOM | 3,134 | **856** |
| 上翻 4 页后 DOM | 78,351 | **856** |
| 上翻最坏长任务 | 2,608 ms | **112 ms** |
| `before_seq=93&limit=8` 载荷 | 9.36 MB | **57 KB** |
| 首屏窗口请求次数 | 2 | **1** |

落地细节：
- 批 1（`Scope: G-JS`）：窗口块数预算（默认 400）+ 窗口 aux 的 reasoning 去重。
  单轮自己超预算时仍完整下发并标 `truncated`（空窗口比慢更糟）。
  工具卡 aux **刻意不去重** —— 它承载窗口轻压缩契约、commandShell 推断与懒加载身份。
- 批 2（`Scope: G-JT`）：时间线虚拟化。条目 < 120 时不启用；未答 ask 卡常驻；
  用户消息序号与 targetSeq 走同一份索引而不是 DOM 查询。
- 批 3（`Scope: G-JU`）：首屏窗口请求合流。复用为**显式 opt-in**（`reuseWithinMs`），
  默认仍是「每次重发」—— 保住 `session-window-dedup` 钉住的「去重不能变成缓存」。

## 目标

「pc 节点 / AIS核心算法 / 重新训练」打开即冻结（实测主线程单次 long task 1.4–1.9 s，
DOM 941 → 16,824，上翻一页更差）。要让**任何**大会话的首次打开与上翻都落在固定预算内，
而不是靠「这个会话刚好不大」。

## 定位结论（证据）

会话 `1792407054-f473f636cd37`：109 轮 / **15,340 块** / `blocks.jsonl` 28.0 MB / `aux.jsonl` 27.7 MB。

| 请求 | 轮 | 块 | 未压缩 | 服务端 |
|---|---|---|---|---|
| `?latest=8`（首屏） | 8 | 1,282 | 2.49 MB | 0.13–0.26 s |
| `?before_seq=93&limit=8`（上翻一页） | 8 | **4,526** | **9.36 MB** | 0.52 s |

真机（headless Chromium 连 pc 节点）：642 张 Thinking 卡 + ~640 张工具卡同帧挂载，
DOM 16,824，**1.4–1.9 s long task**；同一窗口请求发了两遍（815,536 × 2 字节）。

三条根因：

1. **窗口按「轮」切，不按「块/字节」切** —— `session/manager_window_reads.go:64`
   `sliceExchangeWindow` 只数 exchanges。本会话单轮最多 3,224 块，于是「8 轮」无上界。
2. **前端全量渲染** —— `hooks/sessionTimeline.ts` 每块一个 timeline item，
   `SessionViewerTimeline.tsx:96` 直接 `timeline.map(...)`，无虚拟化。
3. **载荷翻倍** —— `blocks` 与 `exchange_aux` 各带一份 reasoning（seq 89 实测 1,625 条
   字符串逐字节相同）；且首屏窗口请求被 App restore 与 viewer init 各发一次。

服务端窗口读本身是按设计的 O(窗口) 反向分块读，不是瓶颈；但 `GetWindow` 全程持
`m.mu`（每 root 一把），9 MB 那次会堵住该 root 的其它会话读。

## 落地

### 批 1 · 后端：窗口加块数预算 + 块键去重（G-JS）

| 文件 | 作用 |
|---|---|
| `server/internal/session/manager_window_reads.go` | `sliceExchangeWindow` 增加块数预算：从窗口尾部往前累加该 seq 的块数，超过上限就截断，`hasMore` 随之变 true；`SessionWindowMeta` 增 `BlockCount`/`Truncated` |
| `server/internal/session/manager.go` | `GetWindow` 接受预算参数并把它传下去；窗口读不再需要额外读盘就能拿到每轮块数（读盘一次，与 blocks window 共用结果） |
| `server/internal/session/blocks_window.go` | 暴露「按 seq 取块数」的廉价探针，避免为预算再解析一遍块流 |
| `server/internal/session/types_aux_compact.go` | `CompactExchangeAuxLight` 在块已承载同一 reasoning 时丢掉 `thought`（按 seq 判定，见下） |

**thought 去重的安全边界**：只有当该 seq 在 `blocks` 里**确实有 reasoning 块**时才丢
aux 的 `thought`；否则保留（旧数据、只有 aux 的轮次靠它渲染）。判据必须逐 seq 判定，
不能全局按「有块流文件」判定 —— 实测存在「有块流但某轮只有工具卡」的轮次。

### 批 2 · 前端：时间线虚拟化（G-JT）

| 文件 | 作用 |
|---|---|
| `web/src/components/session/useSessionTimelineVirtualizer.ts`（新） | 封装 `@tanstack/react-virtual`（已在依赖里，未使用）：`estimateSize` 按 item 类型、`rangeExtractor` 常驻未答 ask 卡与用户消息 |
| `web/src/components/session/SessionViewerTimeline.tsx` | 由 `timeline.map` 改为渲染虚拟区间 + 上下留白；`key` 继续用 item.id |
| `web/src/components/session/useUserMessageSummaries.ts` | 改为从「时间线索引 + 虚拟项测量」算，不再 `querySelectorAll("[data-user-message-index]")` |
| `web/src/components/session/useTargetSeqScroll.ts` | 目标 seq 走 `virtualizer.scrollToIndex`；跨窗口取数后仍先查索引再滚 |
| `web/src/components/session/SessionViewer.tsx` | 接虚拟器；粘底/前插锚定沿用现有 ref 语义（`isPrependingRef` 跳过置底） |

**硬约束（必须保持）**：未答 ask 卡永远挂载（草稿不能被滚走）；流式行不重挂载
（`session-virtualization.tsx` 里已有 remount 断言）；`data-session-seq` /
`data-user-message-index` 语义不变，只是屏外项不在 DOM 里。

### 批 3 · 前端：窗口请求合流 + 载荷收窄（G-JU）

| 文件 | 作用 |
|---|---|
| `web/src/services/session/sessionWindowFetch.ts` | in-flight 去重键去掉易变参数、并允许「刚刚结算」的短窗复用，合并 App restore 与 viewer init 的同参数请求 |
| `web/src/app/useSessionLifecycle.ts` | 与 viewer 共用同一次窗口结果，不再各自拉一遍 |
| `web/src/services/session/sessionService.ts` | `SESSION_WINDOW_SIZE` 的语义注释更新（现在是「请求轮数」，实际载荷由服务端预算收口） |

## 验收（必须重跑，不接受推断）

1. 服务端：`?latest=8` 与 `?before_seq=93&limit=8` 的未压缩载荷分别 ≤ 预算（默认
   1 MB / 400 块），`hasMore` 正确，且窗口内轮次不被截断成半轮（块级截断只允许发生在
   轮边界；单轮超预算则单独下发并标 `truncated`）。
2. 前端：真机 headless Chromium 打开该会话，**long task < 300 ms**，DOM < 6,000。
3. 上翻连续 3 页，每页 long task < 300 ms。
4. 已登记的定制组测试全绿：`make test` + `npm test` + `make check-upstream`。

## 未改动 / 明确不做

- 不改「块流是正文唯一载体」这条模型（D1）。
- 不动 `aux` 的历史落盘格式（只在窗口读路径做去重，盘上仍是两份）。
- 不做「服务端按块分页」的新协议 —— 窗口仍以轮为语义单位，只是加了预算上限。
