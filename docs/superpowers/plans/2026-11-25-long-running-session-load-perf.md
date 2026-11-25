# 长回合会话「加载卡死、点哪都没反应」根因与修复

> Scope IDs: G-RH（claimed 2026-11-25T02:39Z）

日期：2026-11-25
状态：已完成

## 现象

一个正在执行、单轮跑数小时、含大量工具调用的会话，在打开（看板任务抽屉 / 会话详情）时
显著卡顿，页面其他区域点击无响应。会话越长越明显。

## 根因

正在执行的回合内容只存在于 `StreamHub.ReplyingList`（未落盘），挂载时整轮重放，
因此这条路径绕开了已完成会话的窗口预算。

1. **客户端时间线分片缓存永不命中**（`web/src/hooks/sessionTimelineCache.ts`）：
   命中判据比较每次现包的 `{ [seq]: entry }` 对象，恒不相等；命中分支还漏了
   `touched.add`。每个流式事件都全量重建时间线（块排序 + 全文拼接 + FNV 哈希 id）。
2. **客户端逐事件 O(E) / O(L)**（`realtimeWsSessionStream.ts`、
   `useSessionStreamRuntimeMeta.ts`）：每条 chunk 各自复制 exchanges 数组、线性查找、
   字符串拼接；`tool_call_update` 每条立即 bump；每个事件都给 `setSessions` 造新数组。
   长回合累计 O(E²) / O(L²)，主线程被占满。
3. **服务端 pending 重放无上限**（`server/internal/api/stream_hub_replay.go`）：
   `maxReplayEvents` 只有声明没有实现；客户端挂载时在 StreamHub **全局锁内**克隆/排序
   整份列表（数万条事件、几十 MB 一帧），期间所有会话的流事件排队。
4. **userShell 续传合并全表扫描**：每条工具输出都 O(N) 扫 ReplyingList 并在全局锁内
   重拼最多 256 KB。

## 修复

- 分片缓存按「该 seq 的原值引用」命中，命中计入 touched；包装对象只在 miss 时创建。
- 同一帧内的文本/思考 chunk 在模块级缓冲合流（16ms），结构性事件前强制 flush；
  `tool_call_update` 走 debounce bump；`setSessions` 无变化时返回 prev 让 React bail out。
- `resolveRuntimeMetaForSession` 从尾向前遍历，不再复制+反转整列。
- 服务端：连续 `message_chunk` / `thought_chunk` 在写入时合并成一条（位置不变、
  cursor 取最新）；条数 4000 + 近似字节 16 MB 双上限，超出按 1/8 批量丢最旧；
  userShell 合并只扫尾部 64 条；重放的排序移出全局锁。
  合并只在「本会话无在途重放」时进行，避免把已投递事件改成更大快照造成客户端重复拼接。

## 验证

- 前端合成基准（300 交换 × 8 块，200 次流式追加）：旧实现 8.07 ms/事件，
  新实现 0.22 ms/事件（36×）。
- 服务端重放构造（合成事件）：20000 条时全局锁内 9.2 ms、JSON 49 ms、5.6 MB；
  限额后约 3500 条时 1.4 ms、6 ms、1.0 MB。
- 新增测试：`web/tests/session-timeline-chunk-cache-hit.test.mjs`、
  `web/tests/stream-event-coalescing.test.mjs`、
  `server/internal/api/ws_replay_test.go`（合并/上限/在途重放不合并）。
- 全量：`go test ./server/internal/api/...`、`vitest run`（279 文件 / 992 用例）、
  `tsc --noEmit`、`npm run build` 全绿。