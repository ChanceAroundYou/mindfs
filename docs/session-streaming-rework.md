# 会话消息链路重构

> 2026-10-06 起。起因：「切走再切回运行中的会话，ask 卡下面多出一整份助手正文」。
> §0 是复现到的证据，§1 是整条链路的实勘结果，§2 判定哪些补丁是**协议**造成的，§3 是方案。

---

## 0. 症状与决定性证据

会话 `性能优化 / #30`（key `1791236887-d02bc42e1eea`）。首次打开正常，应用内切走再切回后：

| | 首次打开 | 切回之后 |
|---|---|---|
| DOM 时间线项数 | 137 | 138 |
| 客户端缓存（React fiber `session.exchanges`） | 136 | 137 |
| 末尾新增项 | — | `role=agent`，len 1652，无 `seq` 字段 |
| 渲染出的属性 | — | `data-session-seq="19"`，len 1649 |

新增的 1652 字 = **该轮全部 16 条 agent 片段长度之和**。它出现在 `ask` 卡**之后**，
形成「文本（含工具）→ ask → 文本（无工具）」。

### 0.1 决定性证据：这一轮的正文**从来没有落过库**

```
$ python3 - <<…  # 读 .mindfs/sessions/1791236887-d02bc42e1eea.jsonl
0  seq=1 user 808 最近出现了多次闪退/卡死问题，在对话界面和看板界面都有出现…
1  seq=2 user 808 最近出现了多次闪退/卡死问题，在对话界面和看板界面都有出现…   ← 同一句话发了两遍
```

`GET /api/sessions/1791236887-d02bc42e1eea?root=mindfs&latest=100` 回
`{total:2, minSeq:1, maxSeq:2}`，PA 节点回 0 条。

**整轮对话（正文 + 思考 + 工具卡）只存在于两个地方：服务端 `ReplyingList` 内存缓冲、客户端缓存/IDB。**
回合尚未结束（`pending=true`，停在 ask 上），而助手正文本就是**回合末尾**才落盘
（`usecase/session.go:2760` 的 `AddExchangeForAgent`）。

这一条同时证明了两件事：

1. **回合追赶不是可选项**。删掉重放 = 这条会话只剩两条一模一样的用户消息。
   这不是「边缘场景」，它就是用户每天在看的那个界面。
2. **重放的语义只能是「重建」，不能是「续传」**。客户端缓存里的那份与服务端的 buffer
   是同一份数据的两个副本，没有任何一方是权威 —— 除非把语义定成「服务端 buffer 是唯一权威，
   每次挂上就照它重建」。

### 0.2 `data-session-seq="19"` 是怎么来的

缓存里那条新增行的 `seq` 是 **undefined**。`19` 是渲染层**按位置现推的**：

```ts
// useSessionStream.ts:349-374  buildBaseTimeline
let inferredSeq = 0;
…
if (role === "user")  { inferredSeq += 1; const seq = Number(ex.seq||0) > 0 ? Number(ex.seq) : inferredSeq; … }
if (role === "agent") { inferredSeq += 1; const seq = …            : inferredSeq;
                        const auxList = seq ? exchangeAux[String(seq)] || [] : []; … }
```

`inferredSeq` 只数 user/agent 两种角色，所以尾部这条多出来的 agent 行拿到 19。
**后果比多一行更糟**：`buildAssistantTimeline` 于是拿 `exchange_aux["19"]`（真·持久化行 19 的 aux）
去切这条假行的文本 —— 它因此「带上了工具卡」，也就是用户看到的「文本 a（含工具）」。

### 0.3 为什么只有「切走再切回」才复现

`appendAgentChunkForSession`（`useSessionStreamCache.ts:164-204`）是**按位置**合并的：

```ts
const last = list[list.length - 1];
if (last && (last.role === "agent" || last.role === "assistant")) { …合并进最后一条… }
list.push({ role: "agent", …, content });        // ← 末条不是 agent 就新开一行
```

- **首次打开**（冷启动）：重放批次作用在空尾巴上，事件顺序 = 产生顺序，
  正文片段自然首尾相接，1 行。
- **切回**：缓存里已经有这一轮的行，而**末条是 ask 工具卡**（ask 是这一轮最后一个事件）。
  重放批次被无条件再投递一次，第一片正文就落到 `list.push` 分支 → 新开一行 →
  后续 15 片全拼进这一行。

`mergeStreamedText`（`session.ts:2120-2128`）是为此写的适配层，但它的判据是
**子串包含**，而这里重复的是「末行是不是 agent」的位置判断，子串判据根本够不着。

---

## 1. 整条链路的实勘

### 1.1 发送（浏览器 → 服务端）

```
ActionBar → service.sendMessage(ws) ──┐
                                      ↓
                          ws.go 单读 goroutine  →  go h.handleSendMessage
                                      ↓
                    usecase.Service.SendMessage   (usecase/session.go:2425-2794)
```

`SendMessage` 里与流式相关的时序（全部实测确认）：

| 行 | 动作 | 落盘? |
|---|---|---|
| `:2475` `persistUserTurnExchange` | 用户行**在回合开始前**落盘（`ExchangeSourceLive`） | ✅ 文件 |
| `:2480` `in.OnStart(MessageStart{BaseExchangeSeq, UserExchangeSeq})` | 广播 `session.user_message`（带真 seq） | — |
| `:2511` `ensureAgentSession` | 拿到 `agentCtxSeq`（已看到的行数） | — |
| `:2534` `plannedAssistantSeq = MaxExchangeSeq+1` | 助手行 seq 是**预测**的，但要到回合末才写 | — |
| `:2621-2628` `shouldPersistToolCallAux` → `UpsertPendingExchangeAux` | 工具卡**在回合进行中就写**（pending aux 区） | ✅ 另一处 |
| `:2760` `AddExchangeForAgent(…, "agent", responseText, …)` | 助手正文本落盘（一整行） | ✅ 回合末 |
| `:2767-2772` `dedupeExchangeAuxBuffer` → `AddExchangeAux` | 缓冲里的 aux 全部正式落盘 | ✅ 回合末 |

**正文在内存里是一个字符串，不是事件列表**：`:2665`
`responseText = appendResponseChunk(responseText, lastResponseUpdateType, chunk.Content)`，
而

```go
// usecase/session.go:3991
func appendResponseChunk(responseText, lastResponseUpdateType, chunk string) string {
    if responseText != "" && <上一条是 thought/tool/todo/plan/compact> &&
       !strings.HasSuffix(responseText, "\n\n") && !strings.HasSuffix(responseText, "\n") {
        responseText += "\n\n"          // ← 落盘版本比现场流版本**多出这些 \n\n**
    }
    return responseText + chunk
}
```

**这直接意味着「落盘正文 ≠ 现场流事件的拼接」**（差在注入的 `\n\n`），
而落盘后 aux 又是**按行号**挂进这段文本的（`currentAssistantLine`，`:3859`）。
行号锚定是上游的既有设计，`\n\n` 是为了让工具卡落在段落之间。

### 1.2 广播（服务端 → 浏览器）

```
runtime.OnUpdate ─┬→ usecase 内部状态机（responseText / auxBuffer / 子会话路由）
                  └→ in.OnUpdate(clientUpdate)
                        ↓
              AppContext.BroadcastSessionUpdate (appcontext.go:975)
                        ↓
              StreamHub.BroadcastSessionStream → AppendReplyEvent (:682)
                        ↓
              GetSessionClientIDs(key, liveOnly=true) → 逐客户端发 session.stream{event}
```

`AppendReplyEvent`（`:682-702`）做四件事：自增 `NextEventSeq` →
`event.EventCursor = "<BaseExchangeSeq>:<NextEventSeq>"` → 尝试
`coalesceUserShellStreamEvent`（同 callId 的 userShell 流式输出就地合并，>256KB 截尾）
→ append 进 `ReplyingList`。

**`ReplyingList` 全仓只有 `nextReplayStepLocked` 一个读者**（已 grep 全部用例）。
它不为持久化、不为统计、不为任何别的功能存在 —— 只为重放。

### 1.3 挂载/切换（`session.ready`）

```
useSessionStream / App.restoreActiveSession
   → WS session.ready{root_id, session_key}            ← 实测帧里**没有 event_cursor**
        ↓
ws.go:1058  eventCursor := getString(req.Payload, "event_cursor")   → 恒为 ""
ws.go:1063  BindSessionClient(key, clientID)                        → 加进 sessionClients
ws.go:1064  ReplayPending(rootID, clientID, key, "")
        ↓
stream_hub.go:811  parseEventCursor("") 失败 → lastEventSeq = 0
                   replayStates[...] = {Status: Replay, LastEventSeq: 0}
        ↓ 循环
   collectReplayStep → nextReplayStepLocked (:1032)
       events = ReplyingList 里 eventSeq > 0 的**全部**（= 整轮）
       replay.LastEventSeq = max
   replayStepToClient → **一条消息**发整批（payload.events）
   collectReplayStep → 已空 → step.live = true
   replayCompletionToClient → completed 表非空时补一帧 done{replay:true}
```

`GetSessionClientIDs(…, liveOnly=true)`（`:362-383`）会**跳过 replay 状态的客户端**，
所以「快照 → 排空 → 切 live」之间不会丢事件也不会插序 —— 这个机制是好的，保留。

### 1.4 客户端接收（两条并行分发路径）

`session.ts:777 emitDecrypted` 对同一条 WS 消息做**两件事**：

```ts
this.emit({ type, sessionKey, payload: nextPayload });      // :789 → 全局 listeners
…
switch (type) {
  case "session.stream":
    for (const handler of handlers) handler.onStream?.(nextPayload.event as StreamEvent);  // :822
}
```

- 路径 A（`:789` 全局 listeners）→ `useRealtimeEvents` 的 `session.stream` 处理器
  （`:1125-1139`）—— **认识批处理形状**（`payload.events` 循环）。
- 路径 B（`:822` per-session `handlers`）→ `useSessionStream` 的 `onStream` ——
  **不认识**，`payload.event` 在批处理帧上是 `undefined`。

**这是一个正在发生的真 bug**（本次探针控制台抓到 3 次，正好等于收到的 3 个批处理帧）：

```
[Session] Failed to parse message: TypeError: Cannot read properties of undefined (reading 'type')
    at Object.onStream (assets/i…)
```

后果：重放期间 `isStreaming` / `streamVersion` / `streamStatusText` 全都不更新
（`updateActiveStreamState` 也在 `:887-888` 对 `undefined` 提前返回）。
内容能显示（走路径 A），但「正在生成」的状态机在重放时是瞎的。

批处理形状是服务端为了「一次渲染」引入的优化（`stream_hub.go:1072-1074` 注释），
客户端**只教了其中一条路**。这是典型的补丁层不同步。

### 1.5 客户端缓存与渲染

```
路径 A 分发 → handleSessionStream (useRealtimeEvents.ts:443)
   → appendAgentChunkForSession / appendThoughtChunkForSession / appendToolCallForSession
        → upsertSessionCache（useSessionStreamCache.ts:55）→ sessionCacheRef[cacheKey]
```

缓存是**一个按位置排列的可变数组**，没有身份键。六种 `append*` 的合并规则各不相同：

| role | 合并判据 | 依据 |
|---|---|---|
| agent | 末条是 agent/assistant 就并 | **位置** |
| thought | `thought_id` | 身份 |
| tool | `callId` | 身份 |
| todo / plan / compact | id（或末条） | 身份 |

只有正文按位置合并 —— 因为 `message_chunk` 事件**不带任何身份**。
这就是 bug 的形状：**其余角色都有身份，唯一没有身份的恰好是量最大、最需要身份的那个。**

加载期（`App.restoreActiveSession`，`App.tsx:2282-2403`）与渲染期：

```
窗口 API(latest=20/50) ─┐
IDB 缓存 ──────────────┼→ composeLoadedExchanges(server, cached, pending)  ← 唯一组装规则
WS 重放批次 ───────────┘        → sessionCacheRef → SessionViewer
                                                  ├ visibleExchanges（窗口态）
                                                  ├ tailOverlay（seq=0 叠加层，:1227-1296）
                                                  └ composedExchanges → useSessionStream
                                                        → buildBaseTimeline（含 inferredSeq）
```

`tailOverlay` 是**五个启发式的叠罗汉**，每个都对应一次线上事故：

1. `windowToolCallIds`（:1206）—— transient 工具卡的 callId 已在窗口 aux 里就让位（2026-09-12 症状 1）；
2. `windowUserCounts` + `consumed`（:1193, :1268）—— 同内容用户消息按出现次序消抵（含 `ponytail:` 天花板注释）；
3. `renderedPersistedTexts` 子串包含（:1235-1243, :1287-1292）—— transient 正文已被某条持久化行包含就让位（2026-09-17 20:16，ask 上下各一整轮）；
4. `seq > latestSeq` 分支（:1248-1256）—— 重锚定还没把刚落盘的行拉进窗口时，由 overlay 顶替；
5. `composedExchanges` 按对象同一性取差集（:1300-1304）。

再往下 `useSessionStream` 还有 `dedupeToolCards`（:124，重复 callId → React 重复 key）与
`settleRunningTools`（:140）。到 `buildBaseTimeline` 已经是**第四层**去重了。

---

## 2. 判定：哪些补丁是协议造成的

把链路里的「补丁」按**成因**分堆，只有第一堆是本次要扒的：

| # | 补丁 | 成因为何 | 处置 |
|---|---|---|---|
| A | `ReplyingList` 之上的 `event_cursor` / `ClientReplayState.LastEventSeq` / `parseEventCursor` 的客户端入参 | **续传协议**（客户端自报进度） | **删** |
| B | 客户端 `eventCursors` map、3 处 delete、`markSessionReady(event_cursor)`、`getEventCursor`/`clearEventCursor`（**无调用者**） | 同上 | **删** |
| C | `mergeStreamedText` 子串判据 + `appendAgentChunkForSession` 的「重放安全」注释 | 重投递 | **删**（改回普通拼接） |
| D | `done{replay:true}` + `completed` 表 + 客户端两处 `payload?.replay !== true` 守卫 | 「重放中的客户端得知回合已结束」 | **删** |
| E | 批处理帧只教了一条分发路径（`onStream(undefined)` 崩溃） | 批处理优化 | **收敛到一处** |
| F | `composeLoadedExchanges` 的 compact 边界过滤 | 累积的 seq=0 残留 | 保留但可简化（残留不再累积） |
| G | `tailOverlay` 五个启发式 | 窗口态与 seq=0 叠加层的重叠 | **保留**（成因不是协议，见 §2.2） |
| H | `inferredSeq` 按位置编 seq | 瞬时行没有身份，又要查 `exchange_aux[seq]` | **删**（见 §2.3） |
| I | 双写（live 路径 + 转录导入器）与 `exchangeAlreadyRecorded` 的 ±5s/空白归一化/前缀容忍 | **另一个问题**：两个写入者 | 不在本次（见 §2.4） |
| J | `appendResponseChunk` 注入 `\n\n` + aux 按行号锚定 | 上游设计（工具卡落在正文段落间） | 不动 |

### 2.1 A–E 为什么必须一起删

C、D 单独留着不会「更安全」，它们**就是**协议的一部分：
`mergeStreamedText` 的存在前提是「同一段内容会被投递两次」；
`done(replay)` 的存在前提是「客户端可能停在重放中」。
协议一改，这些前提消失，留着它们等于留一段**永远不会被触发、但一旦被触发就是错**的死逻辑。

### 2.2 G 为什么不删

`tailOverlay` 处理的重叠**不是重放造成的**，而是这条固有链路：

```
服务端回合末：AddExchangeForAgent(seq=N)   ─┐
客户端同一时刻：缓存里还有 seq=0 的直播副本 ─┴→ 两者内容相同，都会渲染
```

`session.done` 到达时 `dropTransientExchanges`（`useRealtimeEvents.ts:388-389`）会清掉 seq=0，
但这个窗口期真实存在，而且**窗口态与缓存态本来就是两条独立来源**（翻页、重锚定、
`latest=N` 与缓存里几百条并存）。`composedExchanges` 的对象同一性差集也一样。

删它们需要先让「窗口态」与「瞬时态」合成一个带身份的数据源 —— 那是更大的改动，
收益（少 5 个启发式）远小于风险（这 5 个各自压着一次线上事故）。
**本次不动，但 §3.6 记下它变简单的路径。**

> **2026-10-06 补记（见 §6）**：这里把「两条来源之间的重叠窗口期」当成了**短暂且良性**。
> 实测证明不是 —— 它们的交接在 live 路径上**恒失败**（重锚定被一道恒为假的门跳过），
> 于是「重叠」变成「内容消失」。§6.2 给出机制，§6.5 给出统一方案。

### 2.3 H 为什么现在可以删

`inferredSeq` 的用途只有一个：让没有 seq 的行也能去 `exchange_aux[seq]` 取工具卡。
但瞬时行的工具卡本来就由**另一条路**提供：`role:"tool"` 的瞬时条目（`appendToolCallForSession`
按 callId 维护），`tailOverlay` 的第 1 条启发式正是在处理「两条路给了同一个 callId」。

删掉 `inferredSeq` 后：瞬时行 `seq = undefined` → 不查 aux → 工具卡只从瞬时条目来；
持久化行照旧从 `exchange_aux[seq]` 来。**两条来源各归各的，不再串门。**
多出来的那行也就不会再「带上别人的工具卡」。

风险：某条路径可能依赖 inferred seq 的连续性（例如 `data-session-seq` 被测试或滚动逻辑使用）。
落地前先跑探针确认 137 → 137 且尾部结构一致；单独一个提交，可独立回滚。

### 2.4 I 不在本次的原因

`session.go:2337-2423` 那一整套判重（`exchangeAlreadyRecorded` / `sameRecordedExchangeContent`
/ `normalizeExchangeContent`）存在的唯一理由是：**同一个回合有两个写入者** ——
实时路径在回合末写全文（`:2760`），转录导入器从 Claude 的 JSONL 增量写
（`agent/claude/importer.go`）。两者的快照长度不等（实测 1765 vs 1761 字、
实时 21 字 vs 导入 556 字），所以需要 ±5s 容忍窗 + 空白归一化 + agent 侧前缀关系。

另一处双写同样在 §0.1 的证据里：**两条一模一样的 user 行（seq=1, seq=2）** ——
判重没兜住（`recordedUserExchangeSeq` 要求 ±5s 内，两次发送间隔更久）。

收敛这两条写入路径是一个独立且更大的课题（涉及 `SessionIsLiveOwned` 的判据、
JSONL 增量同步、`session_projection.go` 的读取投影折叠）。
**本次只记录，不动**：它与重放协议正交，混进来会让本次的验证失去焦点。

---

## 3. 方案：把「续传」换成「快照重建」

### 3.1 新契约（一句话）

> `session.ready` 之后，**服务端 buffer 是这一轮的唯一权威**。
> 服务端每次都把整轮事件重发一遍；客户端**先清空瞬时尾巴，再照单重建**。

**不变式（可测）**：
> 客户端里某会话的瞬时尾巴 = **该轮事件列表的纯函数**。
> 任何偏差都是 bug，与客户端此前经历过什么无关。

这是「幂等」的加强版：重放 N 次结果逐字节相同，且**不依赖客户端缓存与游标的任何同步**。

### 3.2 服务端改动

| 位置 | 改动 |
|---|---|
| `stream_hub.go:811 ReplayPending` | 删掉 `eventCursor` 入参；`ClientReplayState.LastEventSeq` **恒从 0 起**（不再读客户端游标）。其余（排空循环、live 切换、liveOnly 跳过）原样保留 |
| `stream_hub.go:1032 nextReplayStepLocked` | 逻辑不变（它本来就是「给 0 就是全量」） |
| `stream_hub.go:811-835` | **无条件**先发一帧 `session.stream{reset:true, events:[…]}`（`events` 可空）。空 buffer 也要发 —— 这样「ready 之后尾巴 = 服务端 buffer」而不是「ready 且 buffer 非空时…」 |
| `stream_hub.go:1091 replayCompletionToClient` | 删；连带删 `completed` 表与 `BroadcastSessionDone` 里的写入 |
| `stream_hub.go:708 parseEventCursor` | 保留（`nextReplayStepLocked` 内部排序/drain 仍用它），但不再有「客户端传入」这一路 |
| `ws.go:1058` | 删掉 `event_cursor` 读取；`ws.go:55` 的 `EventCursor` 字段保留为**服务端内部**字段（不再来自客户端） |
| `stream_hub.go:155 buildSessionStreamBatchResponse` | 加 `reset bool` 参数（**不是**恒 true，见下） |

**`reset` 必须是参数（实现期发现的坑，2026-10-06）**：第一版把 `reset:true` 写死在
`buildSessionStreamBatchResponse` 里，但 `replayStepToClient`（排空循环的**续投**帧）也调它。
后果是：排空期间一到新事件，客户端就把刚由首帧快照重建好的整条尾巴清掉，只剩那几条 ——
**越活跃的会话越容易命中**。首帧是快照（reset），续投是增量（不 reset），两者**形状相同、
语义相反**，所以只能由调用方显式传。测试 `TestReplayDrainStepIsNotAReset` 钉住这条。

`ClearSessionPending` 的等待循环、`coalesceUserShellStreamEvent`、`SetPendingUserAt`
全部保留 —— 与快照语义不冲突。

### 3.3 客户端改动

| 位置 | 改动 |
|---|---|
| `session.ts:777 emitDecrypted` | **收敛分发**：`session.stream` 若带 `events` 数组，先 `emit({type:"session.stream.reset"})` 一次，再把每个事件**各自**走一遍 `emit` + `handlers.onStream`。`onStream(undefined)` 崩溃由此消失 |
| `session.ts:343, 794-804, 902-914, 1167-1190` | 删 `eventCursors`、`eventCursorKey`、`getEventCursor`、`clearEventCursor`、`markSessionReady` 的 `event_cursor` 参数 |
| `useRealtimeEvents.ts:1125-1139` | 删批处理分支（形状已在 `emitDecrypted` 归一），只留 `handleSessionStream(payload)` |
| `useRealtimeEvents.ts` 新处理器 `session.stream.reset` | 对 `rootSessionKey(root, key)` 调 `dropTransientExchanges`，**谓词收紧为**：丢 `seq` 为假 **且** `role !== "user"` |
| `session.ts:2139 dropTransientExchanges` | 加 `role !== "user"` 排除（现版本丢所有 seq=0，会把刚发的乐观用户行一起丢掉）。同时 `composeLoadedExchanges` 里对 transient 的筛选复用同一谓词 |
| `session.ts:2120 mergeStreamedText` | **删**；`useSessionStreamCache.ts:186/220/230` 三处改普通拼接。注释改写成「清空后重建 ⇒ 拼接」 |
| `useSessionStreamCache.ts:164-204` | 「重放安全」注释删除，改成一句新不变式 |
| `useRealtimeEvents.ts:1344, 1361` | 删 `payload?.replay !== true` 守卫（不再有 replay 回执） |
| `useSessionStream.ts:349-374` | 删 `inferredSeq`（§2.3，单独一个提交） |

**`role !== "user"` 这个例外必须写死在谓词里**：用户行的 seq=0 是客户端自己的乐观回声
（`App.tsx` 建 optimisticExchange），服务端事件流里从来没有它，丢掉会让「刚发的消息」瞬间消失。

### 3.4 幂等性论证

- 批次永远是**本轮事件的全部**（`LastEventSeq` 恒从 0 起）。
- 应用前的动作永远是**清空瞬时尾巴**。
- 因此 `apply(clear(tail), batch)` 与 `tail` 此前的内容无关 ⇒ 重放 N 次结果相同。

三条原先用来兜住「重投递」的机制随之失效并被删除（`mergeStreamedText`、
位置合并的兼容分支、`done(replay)` 守卫）。

### 3.5 提交拆分

| # | 提交 | 内容 | 验证 |
|---|---|---|---|
| **A** | `fix(stream): 挂载会话改为快照重建，删除按序号续传` | 服务端 §3.2 + 客户端 reset 分发/清尾/删游标 + 删 `mergeStreamedText` | 探针 137 → 137；冷启动到等待 ask 的运行中会话正文完整 |
| **B** | `fix(stream): 重放批次只走一条分发路径` | `emitDecrypted` 归一形状 + 删 `handleSessionStream` 批处理分支 | 控制台不再有 `onStream` TypeError（探针抓 0 次） |
| **C** | `refactor(stream): 删除 done(replay) 回执与 completed 表` | §3.2 最后两行 + 客户端守卫 | `session-done-replay-no-reanchor.test.mjs` 可放宽 |
| **D** | `fix(web): 瞬时行不再按位置编造 seq` | 删 `inferredSeq` | 探针尾部结构一致；可独立回滚 |
| **E** | `docs(stream): 重写 G-T 组与守卫测试` | `docs/upstream-customizations.md` §G-T + `web/tests/upstream-restore.test.mjs`（现有 :58/59/66/67 断言的是**旧契约定**，必须反向重写） | 全量门禁 |

### 3.6 记录：将来还能再扒掉什么

不动，但写在这里备查：

- `tailOverlay` 的 5 条启发式（§2.2）—— 需要先把窗口态与瞬时态合成**一个带身份的数据源**。
- 双写与 `exchangeAlreadyRecorded` 的一整套容忍判据（§2.4）—— 需要先收敛 live / importer 两条写入路径。
- `composeLoadedExchanges` 的 compact 边界过滤 —— 残留不再累积后，判据可简化成
  「pending 就接回缓存里的 seq=0」。

### 3.7 风险

| 风险 | 处置 |
|---|---|
| `reset` 空帧被当成内容 | 空 `events` 不调 `handleSessionStream`，只清尾；单测钉住 |
| 用户乐观行被误清 | 谓词显式排除 `role === "user"`；加用例 |
| 与上游永久分叉 | 记进 `docs/upstream-customizations.md` §G-T，反向重写守卫测试，提交带 `Scope: G-T` |
| 删 `inferredSeq` 引发视觉回归 | 独立提交 D，探针比对尾部结构，可单独回滚 |
| 快照重建放大重放开销 | 与今天**完全一样**（今天游标恒空 ⇒ 本来就是全量重发）—— 零回归 |

---

## 4. 验证

1. **复现探针** `/tmp/probe-dup.mjs`：A → B → A，尾部节点数必须 **137 → 137**，
   且不再出现 `data-session-seq="19"` 那条。
2. **冷启动到等待 ask 的运行中会话**（就是 §0 那条）：助手正文完整 —— (a) 必要性的活证据。
3. **控制台零 `onStream` TypeError**（提交 B 前后对比，今天是 3 次）。
4. 门禁三项：`go test ./...` / `tsc --noEmit` / `node --test web/tests/*.test.mjs`。
5. 新增单测：`reset` 幂等（同一批次连续应用两次，结果与一次相同）、
   seq=0 用户行不被清、`dropTransientExchanges` 的新谓词、**续投帧不带 reset**。

### 4.1 实测记录

**只装前端、不重启服务端**（静态资源按请求读盘，所以前端改动即时生效；服务端仍是旧二进制）
—— 这是一次便宜的**对照实验**，用来确认哪些症状归前端、哪些归协议：

| 观察 | 修复前 | 只装前端 | 结论 |
|---|---|---|---|
| 控制台 `onStream` TypeError | 3 次（= 收到的批帧数，1:1） | **0 次** | 归**前端的形状归一**，与服务端无关（旧服务端的批帧不带 `reset`，也会被正确展开 —— 这正是「形状与 reset 分开判」的价值） |
| 尾部节点数 A→B→A | 137 → 138 | **137 → 138（未变）** | 归**协议**：客户端没有 `reset` 就不知道该清尾巴，只能按增量往末尾叠一行 |
| 重复文本对数 | 16 → 32 | 16 → 32（未变） | 同上 |

所以「协议」这一半**必须重启服务端才能验证** —— 部分证据只能证明前端那一半。
探针随后必须在跑新二进制的实例上重跑（`sudo systemctl restart mindfs` 由用户执行，见 CLAUDE.md）。

---

## 5. 明确不碰

`pendingStreams`（订阅时序）、`coalesceUserShellStreamEvent` + `replaySnapshot`、
`ClearSessionPending` 的等待循环、`composeLoadedExchanges` 的 compact 边界、
aux 按行号锚定与 `appendResponseChunk` 的 `\n\n`、双写与导入器、
`session_projection.go` 的读取投影、`settleRunningTools` / `dedupeToolCards`。

---

## 6. 第二轮：完成时刻的「交接失败」（2026-10-06 追加）

### 6.1 症状（用户原话）

> 进行中的 assistant message 在正在完成的时候会**瞬间全部消失，只剩最后一条 user msg**，
> 但**切换其他会话再回来又能刷出来**。

「切换回来才恢复」= 内容没丢，是**渲染源被清掉了而替代源没装上**。这正是 §2.2 里
「窗口态与瞬时态是两条独立来源」那句话的破坏性形态：两条来源之间的交接不是原子的。

### 6.2 根因：一处**顺序**缺陷让「重锚定」恒被跳过

`session.done` 到达时，同一帧里做了两件事，**顺序错了**：

```ts
// web/src/services/session.ts —— emitDecrypted 的主分发路径
this.emit({ type, sessionKey, payload: nextPayload });   // :816  ← 先派发给监听者
if (!sessionKey) return;
const rootId = ...;
this.updateActiveStreamState(type, sessionKey, nextPayload);  // :821  ← 后更新状态机
```

`useRealtimeEvents` 的监听者是 `sessionService.subscribeEvents(...)`（`useRealtimeEvents.ts:1990`），
**同步**调用 `wsHandlersRef.current["session.done"]`。所以 `session.done` 处理器跑在 :816，
而 `activeStreams.delete(sessionKey)` 发生在 :821 —— **处理器看到的是删除之前的状态**。

于是 `useRealtimeEvents.ts:1374-1379` 那道门：

```ts
if (
  getReplayTargetsForRoot(rootID).includes(sessionKey) &&
  !sessionService.isSessionStreaming(sessionKey)   // ← 此刻恒为 true ⇒ 取反恒为 false
) {
  void reloadSessionForReplay(rootID, sessionKey);   // ← 永远不执行
}
```

**整条 live 回合路径上的重锚定从来没有执行过。** 只有 `ws.reconnected` 走的
`replayTargetsForAllRoots`（`:319`）会重锚定 —— 所以「切走再切回来」能恢复：那条走的是
viewer 的 init effect，与服务端无关。

而**在同一个处理器里，更早的一步已经把这轮内容销毁了**（`useRealtimeEvents.ts:389`）：

```ts
sessionCacheRef.current[cacheKey] = dropTransientExchanges({...cached, pending:false});
```

销毁在前、替代在后；替代那一步又恒被跳过 ⇒ 中间没有任何东西能渲染这一轮。
时间线里剩下的只有已带 seq 的用户行 —— **「只剩最后一条 user msg」由此而来**。

**为什么偏偏现在暴露**：`dropTransientExchanges` 的谓词在这次改动里从「seq 是否为 0」
改成了 `isTransientExchange`（seq=0 **且不是用户行**）。改动前用户行也一起被清，
屏幕会塌成「上一轮」；改动后用户行留下，残留的形态才精确长成「只剩最后一条用户消息」。
**但这个 bug 本身早于本次改动**（`isSessionStreaming` 那道门建于 2026-09-24 `458495b`），
本次只是把它从「看不出所以然」变成了「一眼可描述」。

**已真机复现（2026-10-06，headless CDP，会话 `1791236887-d02bc42e1eea`）**。两次连跑
给出同一机制的两种形态，恰好互为对照：

| | 第 1 跑 | 第 2 跑 |
|---|---|---|
| 真实回合 | 未发出（输入框写入失败，只有 3 条 stream 帧） | 发出（`session.accepted` + `session.user_message` 各 1） |
| `session.done` 帧数 | 1（旧服务端 `completed` 表的补发） | **80** |
| done 之后本会话的请求数 | **0** | **78** |
| 时间线（时间线项 / 正文总长） | 137/11970 → **2/1614**（只剩两条 seq=1,2 的用户行） | 84/9448（保持），整页重开 84/9433 |

两跑是同一个会话、同一个 replay 目标集合，唯一差别是**时序**：

- **第 1 跑 = 用户报的原症状。** done 到达时 `isSessionStreaming` 仍为 true（状态机还没更新）⇒
  重锚定被跳过；而同一处理器更早一步已把瞬时尾巴清了 ⇒ 时间线塌成「只剩用户行」。
  `before/after` 两帧的差值（137→2 项）与用户原话「瞬间全部消失，只剩最后一条 user msg」逐字吻合。
- **第 2 跑 = 同一个缺陷的另一半。** 第 1 条 done 被吞掉，但吞掉它的那步**正好删掉了
  `activeStreams` 里的键**；于是**第 2 条 done**（旧服务端补发的那条）通过判定 ⇒ 重锚定执行 ⇒
  `session.ready` ⇒ 服务端补发 done ⇒ 再重锚定 …… 实测 **≈8 次/秒**自持闭环
  （`session.done : session.ready : ?latest=20` 严格 1:1:1），与 2026-09-13 记录的 18 次/秒是同一个环。

第 2 跑还解释了「为什么这个 bug 看起来时隐时现」：环一旦起来就一直在重装窗口，
**症状被掩盖成「正常」**；环不起来时（只有一条 done）就必塌。也就是说用户看到的
「忽然消失」与「有时又正常」是同一处竞态的两种结果，不是两个 bug。

**结论**：§6.2 的源码推导成立，判据已由真机数据落定 —— 追踪脚本记录的「done 之后针对本会话的
请求数」在第 1 跑为 0、第 2 跑为 78，正是「这道门在 lagging 状态下恒为假」的直接观测。

### 6.3 同一个 handler 里还有第二重错误：门本身是多余的

`handleSessionStreamDone` 在 `:379-382` 已经对「队列续跑」提前 return 了
（那时才真的不该重锚定）。走到 :1374 时，`hasQueuedContinuation` 必然为假。
`isSessionStreaming` 这道门是旧结构的残留 —— 它想拦的情况已经被上游拦掉了，
留下的效果只是**把正常情况也拦掉**。

改完之后 `isSessionStreaming` 本身是**诚实**的（§6.5 第 1 条把状态机挪到了派发之前，
它剩下的那个真实消费者 `useSessionStream.ts:535` 因此也变准了）。但诚实不等于问对了问题：
「我们收到过 stream 帧吗」和「这一轮还会继续吗」是两件事，后者由 `hasQueuedContinuation`
回答。所以这道门是**被删掉**，不是被修好 —— 收尾路径上不再有第二个「还在不在流」的判断。

### 6.4 「多套显示机制」的确切清单

这才是用户说的「还是存在多个复杂的消息显示和加载机制」。同一轮助手内容，
**服务端有 2 份表示、客户端有 3 份、渲染层再合成 2 层**：

| 侧 | # | 机制 | 装什么 | 生命周期 |
|---|---|---|---|---|
| 服务端 | S1 | 持久化行（JSONL + SQLite meta） | 回合结束后的那一轮 | 回合末由 live 路径写入（用户行在回合初） |
| | S2 | `StreamHub.pendingSessions[key].ReplyingList` | **当前轮**的事件缓冲 | 实时事件追加；`BroadcastSessionDone → ClearSessionPending` 清空 |
| | S3 | 窗口 API（`?latest=` / `?beforeSeq=`） | S1 的切片 | 按需 |
| 客户端 | C1 | `App.sessionCacheRef` | 已认领的持久行 + seq=0 瞬时行 | 事件流写入 / done・compact・reset 时清瞬时 |
| | C2 | IndexedDB | 只有持久行，按预算从头部淘汰 | 写入时 |
| | C3 | 重锚定载荷 `_windowMeta`/`_anchoredAt` | 强制 C4 ← 缓存 | `restoreActiveSession` |
| 渲染 | C4 | `SessionViewer.visibleExchanges` | 窗口态（只有持久行） | init / loadMore / targetSeq / 重锚定 |
| | C5 | `SessionViewer.tailOverlay` | C1 中窗口未覆盖的行，**5 条启发式** | 每次渲染派生 |
| | C6 | `composedExchanges` = C4 ∪ C5（对象同一性） | 交给 `useSessionStream` → `buildBaseTimeline` | 每次渲染派生 |

另有 **2 个 `SessionViewer` 挂载点**（`App.tsx:8254` 主视图、`:9722` 抽屉），
各自持有一份独立的 C4，共享同一份 C1 —— 同一个会话可以有两个不同的「窗口态」。

C5 的 5 条启发式（§2.2 已列）存在的唯一理由：**同一轮内容同时存在持久表示与瞬时表示，
系统不知道「该看哪一份」**，于是逐条用内容比对去猜。§6.2 证明了这套猜测的兜底
（done 时清瞬时 + 重锚定装持久）**在 live 路径上根本没跑起来**。

### 6.5 统一方案（已落地）

共同前提：**「这一轮该看哪份表示」不该由内容比对猜，而应该是一个显式的、可判定的谓词。**
服务端在回合末就知道这一轮的结束 seq（它刚写下那一行），客户端在回合初就知道用户行的 seq
（`session.user_message.seq`）。所以谓词是现成的：

```
看瞬时尾巴  ⟺  窗口.maxSeq < 本轮.结束seq
看持久行    ⟺  窗口.maxSeq ≥ 本轮.结束seq
```

但落地的第一步**不是**引入这个谓词 —— 先看已经存在的那条规则就够了：
`restoreActiveSession` 的组装规则（`composeLoadedExchanges(winExs, cachedBefore, pending)`）
在 `pending === false` 时**本来就不把 seq=0 接回去**。回合结束时服务端已落盘、窗口回包里
`pending` 就是 false ⇒ **窗口一装上，尾巴自然退役**。也就是说退役机制早就有了、而且只有一条，
只是完成路径**没走它**，走的是一条并行的、先跑的、破坏性的 `dropTransientExchanges`。

于是统一 = 把那条并行路径删掉，而不是再加一个谓词。三处改动（全部是「删」或「挪」）：

| # | 文件 | 改动 |
|---|---|---|
| 1 | `services/session.ts` `emitDecrypted` | `updateActiveStreamState` **挪到 `emit` 之前**。状态机反映「正在派发的这一帧」，监听者问「还在不在流」拿到的才是这一帧之后的事实。 |
| 2 | `app/useRealtimeEvents.ts` `handleSessionStreamDone` | 收尾的**唯一出口**：返回值 = 本轮是否真的结束；`willReanchor ? base : dropTransientExchanges(base)` —— 会重锚定的会话**不在这里清尾巴**，交给窗口组装退役；重锚定调用移进本函数末尾（`return true` 前）。 |
| 3 | `app/useRealtimeEvents.ts` done 处理器 | 删掉 `!sessionService.isSessionStreaming(sessionKey)` 那道门（连同整段解释性注释）。处理器只做簿记。 |

为什么第 2 条是**必须**的而不只是「更整洁」：清尾巴是同步的、装窗口是异步的。
先清后装 = 一个非原子的交接 —— 只要装窗口被拦下、抛错或还没回来，这一轮就**永远不见**
（第 1 跑就是它）。而失败时**留着尾巴**是安全的：下一次成功加载会退役它，
「短暂重复」远好于「凭空消失」。

代价与残留：C5 的 5 条启发式（`tailOverlay`）仍在。它们存在的理由是
「同一轮内容同时存在持久表示与瞬时表示，系统不知道该看哪一份」。现在**交接不再失败**，
但两套表示仍在，所以那 5 条启发式仍然在猜。它们要到**方案 B** 才消失。

**方案 B —— 两条泳道（目标形态，未做）**

把「瞬时」从**行数组里的一种行**，改成**另一种东西**：

- `persistedRows`（按 seq 键、只来自窗口）与 `liveTurn`（按事件派生、本轮专属）**分别持有**，
  渲染时拼接，**从不合并**。
- 唯一规则就是上面的谓词：`liveTurn` 在 `窗口.maxSeq ≥ 本轮.结束seq` 时退役，
  或在新一轮开始时替换。两者是不同对象，**不可能重复**，所以：
  删 `tailOverlay` 的 5 条启发式、删 `windowUserCounts`/`windowToolCallIds`/`windowTailTexts`/
  `renderedPersistedTexts`、删重锚定 effect 的 `filter(seq>0)`、
  删 `composeLoadedExchanges` 的 compact 边界规则、`dropTransientExchanges` 也就不再需要区分
  用户行（瞬时尾巴是独立对象，不在行数组里）。
- 代价：`useSessionStream` 的输入结构改了；`ask_user` 卡、队列续轮、乐观回声、
  子会话、双 SessionViewer 这五处边界都得逐个过一遍。

B 的立足点正是 A：A 做完之后，「瞬时尾巴什么时候该死」只剩一个答案（窗口快照到齐），
B 才有条件把它从「行」提升成「对象」。

### 6.6 本节尚未验证的部分

- **改动后的真机验证做不了，因为没有新后端。** 本机跑着的进程是 **05:01:50 启动的旧二进制**
  （`ActiveEnterTimestamp`），而 `~/.local/share/mindfs/web/` 是 12:39 的新前端 —— 混合态。
  §6.5 的第 3 条（删门）与第 1、2 条在**新前端**下都已生效，但**旧后端仍在补发 done**，
  于是那个自持环在新前端下**照样会起来**（新前端不再按 `replay:true` 跳过重锚定）。
  要真机验证「环消失 + 完成不再塌」，必须先跑一次 `sudo systemctl restart mindfs`
  —— 那一步**只能由用户执行**（会杀掉所有托管的 claude 子进程）。
- 已验证的部分：`tsc --noEmit` 通过；契约测试
  `web/tests/session-done-replay-no-reanchor.test.mjs`（含「先更新状态机再派发」「收尾单出口」
  「会重锚定就不预清尾巴」三条）与 Go 的 `TestDoneCarriesNoReplayReceipt` /
  `TestReplayAfterClearYieldsEmptySnapshot` / `TestReplayBatchIsOneMessage` /
  `TestReplayDrainStepIsNotAReset` 全绿。
- **未验证**：新后端下「真实回合结束时不再塌一次」的端到端观感 —— 即本次改动的核心收益，
  仍待在重启后跑一次 `/tmp/probe-done.mjs` 复测（期望：done 帧数 1、done 后本会话请求数 ≥1、
  时间线项数不下降）。
