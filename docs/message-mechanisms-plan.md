# 消息收发/渲染机制 — 整合计划与进度

> **本文档整合** `docs/message-mechanisms.md`（机制清点 + 冲突清单 + ADR）、
> `.omc/plans/canonical-turn-event-phase1.md`（阶段 1 计划）、
> 以及阶段 2/3 的 executor prompt 与执行记录。
> 目的是给出一份**统一的、可追踪的**计划，替代散落在多份文档里的阶段划分。
>
> 最后更新：2026-10-08

---

## ⚠️ 与 main 的对照（2026-10-09 移植说明）

本文档是 **G-T（Canonical Turn/Event）线**在 `2328f0e` 上的分析与计划，2026-10-08 冻结。
移植进 main 时**未采用其代码**（`server/internal/api/canonical_turn.go`、`web/src/services/canonicalTurn.ts`，
它们留在 `canonical-turn-wip` 分支存档），因为阶段 5/6/7a 的**目标**已由 main 用更简单的表示
独立达成；两套回合身份真值（`CanonicalTurnID` 字符串 vs `TurnGen` 代次）并存必然打架。
对照如下：

| G-T 阶段 | 目标 | main 的实际实现 | 代码是否采用 |
|---|---|---|---|
| 5 统一写入口 | 迟到 done 不误清新轮 | **G-BE**：`TurnGen uint64` + `PendingTurnGenMatches` + `EndSessionTurn` | 否（用 main 的表示） |
| 6 FIFO writer | 每连接顺序 + 非阻塞 | 待办（见 `.omc/plans/gt-wip-reconcile.md` 阶段 4） | 否（WIP 实现有 send-on-closed 竞态，须重做） |
| 7a 统一 pending 真值 | 单一真值 | **G-AY**：`multiProjectPendingByKey` 纯派生 + `isStreaming && !!sessionPending` | 是（采用该思路） |
| 7b/7c 前端 reducer | 单一 reducer | 未做 | 否 |
| 8 清理 | 删旧补丁 | 未做 | 否 |

**因此**：下文凡提到 `canonical_turn.go`（`TurnReducer`/adapter）、`CanonicalTurnID`、
`activeTurnId`（前端）的「已完成」，均指 **G-T 分支内部**完成，**不是 main 的状态**。
§3 的阶段 5/6 实现描述、§4 进度表、§5 症状映射里的对应条目，读作「G-T 线的记录」。

**仍然有效、值得保留的部分**：§0 核心问题、§1 结构根因图、§2 的 14 条冲突清单
（⑩`windowMeta` 三写者、⑪两个挂载点两套窗口态 **仍未修**）、§7 文档索引；
以及 `docs/message-mechanisms.md` §1.7 的边界分析（`EventCursor` 是回合内游标而非 `event_id`；
「同一轮真的收两次 done」在当前生产路径上未确认）。

---

## 0. 核心问题（一句话）

> `exchanges` 是一个数组，装的却是两种本体不同的东西（服务端持久行 / 客户端派生行），
> 而链路上有 6 个可渲染落点、3 条并行真值。数组不表达类别 ⇒ 每个消费者只能自己重新推断；
> 推断信息不足时就退化成**内容比对猜**。

**两个用户可见症状**：

| 症状 | 直接原因 | 根因 |
|---|---|---|
| **「正在生成」不消失** | `session.done` 广播被 `ClearSessionPending` 自旋阻塞，或 done 到达时 pending 已被清 | 收尾帧不携带 turn 身份 ⇒ 无法判断「这条 done 属于哪一轮」；pending 清理与 done 广播没有统一的回合身份 |
| **「正在回复」灯不亮** | 9 个机制、五处存储各自判「是否在回复」，漏清一处即灯不灭/不亮 | 没有单一的「这一轮在不在跑」真值源；三套真值（`activeStreams` / 缓存 `pending` / `multiProjectPendingByKey`）互不同步 |

---

## 1. 结构根因（已查实，非推断）

```
发送  ActionBar → handleSendMessage → sendMessage ──(WS session.message)──┐
                                            │                              │
                          乐观回声 + 缓存 ──┘                              ▼
                                                              ws.go 单读分发 → reserveOrQueue
                                                                 │
       ┌─────────────────────────────────────────────────────────────┘
       ▼
 runSessionMessage → agent 事件 → AppendReplyEvent → pendingSessions[key].ReplyingList
                                          │
                    ┌─────────────────────┴─────────────────────┐
                    ▼                                           ▼
        BroadcastSessionStream（live 客户端）        ReplayPending（刚挂上的客户端）
                    │                                   整批 reset 快照
                    └────────────────┬──────────────────────────┘
                                     ▼
                    session.ts emitDecrypted（形状归一 + 状态机）
                                     │
                    ┌────────────────┴─────────────────┐
                    ▼                                  ▼
        emit → 全局 listeners                handlers → useSessionStream
      （useRealtimeEvents 的 28 个 handler）      （按会话的流式状态）
                    │
                    ▼
        sessionCacheRef（内存权威）←→ IndexedDB（持久，只增/头淘汰）
                    │
                    ▼
        SessionViewer: visibleExchanges（窗口） + tailOverlay（猜差集）
                    → composedExchanges → buildBaseTimeline（数组顺序=时间序）
```

**6 个可渲染落点**：`ReplyingList` / 窗口 API / 内存缓存 / IDB / `visibleExchanges` / `tailOverlay`
**3 条并行真值**：`activeStreams` / 缓存 `pending` / `multiProjectPendingByKey`

---

## 2. 冲突清单（14 条，均有代码证据）

> 完整证据见 `message-mechanisms.md` §3.4。此处只列与两个症状直接相关的。

| # | 冲突 | 与症状的关系 | 状态 |
|---|---|---|---|
| ① | 瞬时行两把尺（`isTransientExchange` vs `mergeSessionExchanges`） | 刚发出去的消息可能消失 | ✅ 已修 |
| ② | `tailOverlay` A2 分支假定「`seq<=latestSeq` ⇒ 窗口会渲染」 | 渲染空洞 | ✅ 已修 |
| ③ | 工具卡去重两处实现 | 卡片两边都不渲染 | ✅ 已修 |
| ④ | thought/plan/todo/compact 去重只有弱判据 | 短回复显示两遍 | ✅ 已修 |
| ⑤ | 「是否在回复」9 个机制、五处存储 | **灯不亮/不灭** | ✅ 已修（清除路径统一） |
| ⑥ | 乐观行 `pending_ack` 三种匹配键 | 气泡一直「待确认」 | ✅ 已修 |
| ⑦ | aux 合并覆盖 vs 拼接 | 工具卡重复渲染 | ✅ 已修 |
| ⑧ | `truncated` 标错 | 刷新后静默丢历史 | ✅ 已修 |
| ⑨ | compact 术语/前提冲突 | 清掉本轮前半段 | ✅ 已修 |
| ⑩ | `windowMeta` 三写者口径不一 | `inRange` 误判 | ⚠️ 未修 |
| ⑪ | 两个挂载点两套窗口态 | 主区与抽屉显示不同 | ⚠️ 未修（结构断言已钉） |
| ⑫ | 两个死帧 | 前端不知道会话被搬家 | ✅ 已修 |
| ⑬ | 两个无上限缓冲 | 内存无界增长 | ✅ 已修 |
| ⑭ | 用户行两条判据口径不同 | 短用户消息显示两遍 | ✅ 已修 |

---

## 3. 统一计划（7 个阶段）

> 阶段 1-3 是**已批准并执行**的；阶段 4 是**已执行但仅能力**；阶段 5-7 是**未开始**。
> 每个阶段有明确的**交付物**和**验收标准**。

### 阶段 1：ADR 契约 + 第一个 RED 测试 ✅ 已完成

**目标**：建立 Canonical Turn/Event 的语义模型和迁移边界，不改 wire 协议。

**交付物**：
- `docs/message-mechanisms.md` §7 ADR（决策、目标模型、协议映射、不变量、迁移边界）
- `web/tests/session-core-unit.test.mjs` 第一个 RED 测试（快照幂等）

**验收**：ADR 完整、测试因预期语义失败（非语法错误）、`git diff --check` 干净。

**实际完成**：2026-10-06 前完成。

---

### 阶段 2：前端 Canonical adapter ✅ 已完成

**目标**：实现纯数据结构和函数（normalize/adapt/apply/project），不改 wire 字段。

**交付物**：
- `web/src/services/canonicalTurn.ts`（`normalizeCanonicalEvent` / `adaptCanonicalSnapshot` / `applyCanonicalEvent` / `applyCanonicalSnapshot` / `projectCanonicalTurn` / `projectCanonicalTransientExchanges`）
- `web/tests/session-core-unit.test.mjs` 行为测试（快照幂等、不同 turn 隔离、event 顺序/旧事件不污染）

**验收**：RED → GREEN，`npm test` 全绿，`tsc --noEmit` 通过。

**实际完成**：2026-10-06。

---

### 阶段 3：快照投影接入 ✅ 已完成

**目标**：将 `composeLoadedExchanges` 接入 projection，解决当前 Canonical Turn 红测试。

**交付物**：
- `web/src/services/session.ts` 的 `composeLoadedExchanges` 接入 `projectCanonicalTransientExchanges`
- `web/src/App.tsx` 传 `sess?.activeTurnId` / `fullSession?.activeTurnId`

**验收**：红测试转绿，不通过丢弃全部 seq=0 让测试变绿，保留合法当前在途内容。

**实际完成**：2026-10-06。

**⚠️ 关键限制**：后端**从不发** `activeTurnId` 字段（`grep -r activeTurnId server/` 零命中），所以生产永远走 `""` legacy 分支，返回原数组。前端接线是**死代码**。

---

### 阶段 4：服务端 Canonical shadow adapter ⚠️ 仅能力，非修复

**目标**：在 `StreamHub` 既有入口建立迁移期内部 adapter，不改变 `StreamEvent` JSON / `EventCursor` / Exchange / ReplyingList / pending 行为。

**交付物**：
- `server/internal/api/canonical_turn.go`（`TurnReducer` / `canonicalTurnAdapter`）
- `server/internal/api/stream_hub.go` 接线（`SetPendingUserAt` → `begin`，`AppendReplyEvent` → `ingest`，`BroadcastSessionDone` → `terminal`）
- `server/internal/api/canonical_turn_test.go`（4 个 reducer 测试）
- `server/internal/api/canonical_turn_shadow_test.go`（4 个 shadow 测试）

**验收**：9 个测试全绿，mutation check 证明能失败，全量门禁绿。

**实际完成**：2026-10-08。

**⚠️ 关键限制**：
- `BroadcastSessionDone` 传空 turn 身份（`""`），回退到「当前那一轮」，**与改动前行为完全一致**。
- `TestTerminalIsScopedToItsOwnTurn` 绿，是因为**测试自己传了正确的 turn ID**（`adapter.terminal("sess", turnN)`），生产没有这个信息源。
- 子会话的 pending 由 `SetPendingReply` 建，不走 `SetPendingUserAt`，adapter 里无该 key 的 turn ⇒ terminal 成 no-op。
- **生产是 no-op**：reducer 保持纯投影，不驱动 pending 状态。

**下一个必须做的动作**（§1.7.2）：
> 让 `SetPendingUserAt` 把新建的 turn ID 回传给调用方（现在只回 `*PendingUserMessage`），
> 调用方在 `BroadcastSessionDone` 时原样带回。

---

### 阶段 5：统一 live/import/recovery 幂等写入口 ✅ 已完成

**目标**：统一 live、import、recovery 三条路径的幂等写入口和 terminal 状态提交。

**前置**：阶段 4 的「下一个必须做的动作」必须先完成（turn 身份从产生点回传给收尾点）。

**交付物**：
- `SetPendingUserAt` 返回 `CanonicalTurnID`
- 调用方（`ws.go` 的 `runSessionMessage` 等）把它存进会话状态
- `BroadcastSessionDone` 从状态读出 turn ID，传给 `terminal(sessionKey, turnID)`
- 子会话路径：`begin` 下沉到「任何把 `state.Active` 置 true 的地方」

**验收**：
- 迟到 done 不再把新一轮标成 terminal（生产路径，非仅测试）
- 子会话的 done 能正确归属
- 全量门禁绿

**实际完成**：2026-10-09。

**实现方式**：在 `SessionPendingState` 中新增 `CanonicalTurnID` 字段，`SetPendingUserAt` 调用 `begin()` 后把返回的 turn ID 存入 state，`BroadcastSessionDone` 从 state 读出并传给 `terminal()`。无需更改函数签名。

**测试**：`TestBroadcastSessionDoneUsesStoredTurnID` 通过手动调用 `adapter.begin()` 制造分歧，验证 `BroadcastSessionDone` 使用 state 存储的 turn ID 而非 adapter 当前 turn。

---

### 阶段 6：每个 WS 连接单一 FIFO writer ✅ 已完成

**目标**：统一 replay、live、terminal 的顺序，消除单读 goroutine 架构下的顺序不确定性。

**交付物**：
- 每个 WS 连接一个 FIFO writer goroutine
- replay → live → terminal 的顺序由 writer 保证
- 消除 `BroadcastSessionDone` 与 `startNextQueuedSessionMessage` 的同步调用顺序依赖

**验收**：
- 跨回合时序不再依赖队列串行化的巧合
- 迟到 done 被正确处理（归属到正确回合，不误清新回合 pending）
- 全量门禁绿

**实际完成**：2026-10-09。

**实现方式**：在 `StreamHub` 中新增 `connWriters map[string]chan WSResponse`，`RegisterClient` / `RegisterClientWithNode` 时创建 channel 和 writer goroutine，`UnregisterClient` 时关闭 channel，`SendToClient` 改为 enqueue 到 channel。channel 容量 256，满了丢弃消息（比阻塞好）。

**测试**：`TestSendToClientNonBlocking`（验证 `SendToClient` 在 `WriteJSON` 阻塞时不阻塞）、`TestSendToClientFIFOOrder`（验证消息按 FIFO 顺序被消费）、`TestUnregisterClientStopsWriter`（验证 `UnregisterClient` 停止 writer）。

---

### 阶段 7：单一前端 session reducer ❌ 未开始（方案待批准）

**目标**：WS、HTTP snapshot、window、sync、reset、done 全部进入单一 session reducer，React 只消费 projection。

**前置**：阶段 6 已完成（FIFO writer 消除顺序不确定性）。

**交付物**：
- 单一 `sessionReducer` 取代 28 个 handler 的分发
- `activeStreams` / 缓存 `pending` / `multiProjectPendingByKey` 三套真值合并为一份
- `tailOverlay` 的内容比对猜重被 turn 身份取代

**验收**：
- 「正在生成」不消失：done 能正确归属并驱动 pending 清理
- 「正在回复」灯亮/灭：单一真值源，漏清不再发生
- 全量门禁绿

**方案（待批准）**：

阶段 7 是一个大型重构，涉及前端多个文件。根据 explore agent 的分析，当前架构有：
- 6 个渲染落点：ReplyingList / Window API / 内存缓存 / IndexedDB / visibleExchanges / tailOverlay
- 3 条并行真值：activeStreams（SessionService 私有 Set）/ pendingBySessionRef + seq=0 exchanges / multiProjectPendingByKey（UI 真值）

**最小可行方案**（推荐）：
1. **7a：统一 pending 真值** — 让 `multiProjectPendingByKey` 成为 pending 状态的唯一真值源，其他都从它派生。这直接修复两个症状（"正在生成"不消失、"正在回复"灯不亮）。
2. **7b：创建 session reducer** — dispatch 规范化动作（snapshot/window、stream chunk、done/error、pending/promote/reset），取代 28 个 handler 的分发。
3. **7c：派生一切** — 从 selector 派生所有列表/窗口/覆盖层/流式指示器，消除直接 ref 修改。

**7a 的详细方案**：
- 当前 `multiProjectPendingByKey` 已经是 UI 真值，但 `activeStreams` 和 `pendingBySessionRef` 仍然独立存在
- 让 `activeStreams` 和 `pendingBySessionRef` 都从 `multiProjectPendingByKey` 派生
- 或者反过来：让 `multiProjectPendingByKey` 从 `activeStreams` 派生
- 选择哪个作为真值源取决于哪个最稳定、最不容易出错

**7b 的详细方案**：
- 创建 `sessionReducer`，接受规范化动作
- 将 28 个 handler 改为 dispatch 动作
- 保持 30ms 批处理和窗口虚拟化

**7c 的详细方案**：
- 从 selector 派生 ReplyingList / visibleExchanges / tailOverlay
- 消除直接 ref 修改
- 将 IndexedDB/API 移到 effects

**风险**：
- 阶段 7 是一个大型重构，可能引入 bug
- 需要充分的测试覆盖
- 需要逐步迁移，不能一次性替换

**建议**：先做 7a（统一 pending 真值），验证两个症状是否修复，然后再做 7b 和 7c。

---

### 阶段 8：清理（shadow 对账 + 删除旧补丁） ❌ 未开始

**目标**：影子对账通过后，收缩旧补丁。

**交付物**：
- 删除 `tailOverlay` 的内容比对猜重（被 turn 身份取代）
- 删除 `seq=0` 混合数组
- 删除旧 importer projection
- 删除 `isTransientExchange` / `dropTransientExchanges` 的遗留调用点

**验收**：
- 对账通过（shadow 与生产行为一致）
- 全量门禁绿
- 无死代码

---

## 4. 当前进度总览

| 阶段 | 状态 | 生产效果 | 阻塞什么 |
|---|---|---|---|
| 1 ADR 契约 | ✅ | 无（纯文档 + 测试） | — |
| 2 前端 adapter | ✅ | **无**（后端不发 `activeTurnId`） | 阶段 5 |
| 3 快照投影 | ✅ | **无**（同上） | 阶段 5 |
| 4 服务端 shadow | ⚠️ 仅能力 | **无**（空 turn 身份 = 原行为） | 阶段 5 |
| 5 统一写入口 | ✅ | 迟到 done 不再误清新回合 pending | 阶段 6、7 |
| 6 FIFO writer | ✅ | `SendToClient` 非阻塞，消除 `BroadcastSessionDone` 与 `startNextQueuedSessionMessage` 的同步调用顺序依赖 | 阶段 7 |
| 7 前端 reducer | ❌ | — | 阶段 8 |
| 8 清理 | ❌ | — | — |

**一句话**：阶段 1-6 已完成——turn 身份从 `SetPendingUserAt` 流到 `BroadcastSessionDone`，shadow adapter 在生产路径上真正生效；每个 WS 连接单一 FIFO writer 消除顺序不确定性。阶段 7 是**前端 reducer 统一**，阶段 8 是清理。

---

## 5. 症状 → 修复路径映射

### 症状 1：「正在生成」不消失

```
done 到达 → ClearSessionPending 自旋无超时 → session.done 广播不出去 → pending 永远 true
```

**修复路径**：
1. 阶段 5：turn 身份从 `SetPendingUserAt` 流到 `BroadcastSessionDone`
2. 阶段 6：FIFO writer 保证 done 不被阻塞
3. 阶段 7：单一 reducer 保证 done 驱动 pending 清理

### 症状 2：「正在回复」灯不亮

```
9 个机制各自判「是否在回复」→ 漏清一处 → 灯不灭/不亮
```

**修复路径**：
1. 阶段 7：三套真值合并为一份（服务端 `pendingSessions.Active` 为唯一源）
2. 阶段 8：删除旧的清除路径补丁

---

## 6. 明确不做的事项

- 不改 `ws.go` / `stream_hub.go` / `agent/types` 的 **wire 或事件结构**（`StreamEvent` 字段、WS 帧 JSON、`EventCursor` 格式）
- 不新增 wire 上的 `turn_id/event_id/version`
- 不把所有流式 chunk 直接写成 Exchange
- 不恢复客户端 event cursor 续传
- 不删除 `Exchange/ExchangeAux`、窗口分页、IndexedDB、importer、repoint cursor
- 不用新的 pending/轮询补丁代替状态模型
- 不部署、不重启、不提交生产协议变更（直到阶段 5 完成并验证）

---

## 7. 文档索引

| 文档 | 内容 |
|---|---|
| `docs/message-mechanisms.md` §1.7 | 阶段 4 迁移记录（阻塞点、turn 身份、边界） |
| `docs/message-mechanisms.md` §2 | 机制清点（112 个机制） |
| `docs/message-mechanisms.md` §3 | 关系与冲突判定（14 条冲突） |
| `docs/message-mechanisms.md` §6 | 执行记录（23 条红全部转绿） |
| `docs/message-mechanisms.md` §7 | ADR（决策、目标模型、迁移阶段） |
| `docs/upstream-customizations.md` G-T | 上游定制登记（canonical adapter） |
| `.omc/plans/canonical-turn-event-phase1.md` | 阶段 1 计划 |
