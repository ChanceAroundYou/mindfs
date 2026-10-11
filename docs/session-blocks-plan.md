# 会话正文块流方案（Blocks）

> 2026-10-10 起。本文档是**唯一生效计划**。
>
> **起因**：回复结束后助手正文消失。`os` 模型约 15% 的轮次只产出 reasoning、没有 `text` 块，
> 而 MindFS 的 exchange 正文**只拼 `text` 块**，于是落库为 `""`；流式时 `thought_chunk` 实时推
> 所以看得见，收尾落库后正文消失。
>
> **本文档取代**：
> - `docs/message-mechanisms-plan.md`（G-T 线，2026-10-08 冻结，代码未采用进 main）→ 删
> - `.omc/plans/canonical-turn-event-phase1.md`、`.omc/plans/gt-wip-reconcile.md` → 删
> - `docs/session-streaming-rework.md` 的 §2 / §3.6（计划部分）→ 其 §0-§1 证据与 §3 已上线契约保留
> - `docs/message-mechanisms.md` 的 ADR 部分 → 其 §2 机制清点 / §3.4 冲突清单保留
>
> **本文档接手**：`docs/session-streaming-rework.md` §3.6「将来还能再扒掉什么」与 §2.4
> 「同一个回合有两个写入者」这两项**明确登记但未做**的后续工作。

---

## 1. 根因（全部实测）

| 层 | 事实 | 证据 |
|---|---|---|
| 落库 | 一轮正文**只拼 `text` 块**，reasoning 划去 aux 当 `thought` | `usecase/session.go:2667` → `:2765` |
| 模型 | `os` 约 15% 轮次**只产 reasoning、无 text** | dsh 转录：os 15.1% / of 2.1% |
| 结果 | `content:""` | 落库统计：dsh::os 16% 空行、claude::os 40% 空行 |
| 观感 | 流式有、收尾消失 | `session-streaming-rework.md` §0.1 已记录「正文本来就是回合末尾才落盘」 |
| 同步 | 救不回：两个 importer **都不收 thought**；ACP 还忽略 `TimestampFloor`、带 `AfterTimestamp` 就早退（`acp/importer.go:137`）、伪造 `time.Now()`（`:241/:247`） | 实测 `1790705517`：转录 18 个 thinking 块 / 本地 aux 0 条 |

**结构根因**（`message-mechanisms-plan.md` §0 已指出）：`exchanges` 数组混装两种本体
（服务端持久行 / 客户端派生行），消费者只能自己推断，推断不足就**退化成内容比对猜**。

---

## 2. 决策清单（已确认）

| # | 决策 |
|---|---|
| D1 | **块流是唯一正文载体**；`Exchange.Content` **删除** |
| D2 | `native_id` 存**步级**（不存到块级，省每块 ~36 字节） |
| D3 | 递给 ACP 的 prompt `messageId` 使用本轮 `mindfs_id`；agent 回显值进入 `agent_id` |
| D4 | `mindfs_id` 由 MindFS 用 UUIDv5 确定性铸造；seed 优先取 `agent_id`，否则用 `sessionKey|role|seq` |
| D5 | live 与 import 共用 mint 规则；重导同一 agent 消息不造新 `mindfs_id` |
| D6 | **删除 `TurnGen`**；pending 身份改为 UUID 字符串，语义仍按 pending 生命周期管理 |
| D7 | 三级 `(mindfs_id, step, pos)`；步内用 **agent 声明序**，无声明时才退回到达序 |
| D8 | 工具结果晚到：复用 `(seq,pos)` + **revision 覆盖** |
| D9 | **块级窗口参数**（不只 exchange 级） |
| D10 | native id 对不上：**跳过 + 日志，永不猜** |
| D11 | 分步 **S2a–S2e**（见 §10） |
| D12 | 建**转录回放钩子**，把「纯 reasoning 轮次」固化成回归用例 |
| D13 | 崩溃语义：接受「丢缓冲区、靠 import 补齐」，写进契约 |
| D14 | 锁粒度：**先不动** `m.mu`（每 root 一把），锁内只追加一行 |
| D15 | 前端 `buildAssistantTimeline` 重写为按 `pos` 遍历块 |
| D16 | 块 schema 带子代理归属（`parentToolUseId` / `taskId` / `subagentType`） |
| D17 | 搜索扫块流：`text` + `reasoning` 都命中，**`text` 优先排序**；scanner cap **1MB → 64MB** |
| D18 | 块**内联**进窗口回包（顺带删掉 `hiddenToKept` 的 aux 重映射） |
| D19 | 旧数据**不迁移**（dsh 可脚本迁、claude 本地无 thought），列为待办 |
| D20 | 解码 G-T §6 两条非目标（见 §11） |

---

## 3. 数据模型

### 3.1 主日志 `<key>.jsonl` —— 退化为轮次元数据

```
seq / role / source / agent / model / model_display_name / mode / effort /
fast_service / token_usage / timestamp
mindfs_id      string   // MindFS 权威 UUIDv5，落盘即定，永不改
turn_seq       int      // = 打开该轮的 user 行 seq（排序 / 人读用）
agent_id       string   // agent 侧桥接 id（可空、可后补）
partial        bool     // 本轮未收尾
```

**删除 `Content`。** 消费者账（实测，逐条已核）：

| 原消费者 | 变更后 |
|---|---|
| 读时重复行投影与审计端点 | S3 已退役；GET 原样返回持久化行 |
| `RemapExchangeAux`（原投影的 aux 重映射） | S3 已随投影删除；aux 按各自 seq 返回 |
| 搜索 `manager.go:3326` | 改扫块流 |
| 同 seq 载入去重 `manager.go:2208/2211` | 改按 revision / 字段完备度 |
| importer 累积正文 `claude:1277` / `codex:754` | 改落块 |
| 前端渲染 | 改按 `type` |
| ~~列表预览~~ | ✅ 不成立：`ListByKeys` 不读正文，Session meta 无预览字段 |
| ~~`ExchangeHasLiveSignature`~~ | ✅ 不成立：靠 ModelDisplayName/TokenUsage/Mode/Effort 判定 |

即**「因为删 Content 而新增」的工作 = 0**。

### 3.2 块流 `<key>.blocks.jsonl` —— 唯一正文载体

```json
{"seq":58,"pos":0,"revision":0,"type":"reasoning","text":"...","step":3,"agent_id":"...","mindfs_id":"..."}
{"seq":58,"pos":1,"revision":0,"type":"tool_call","toolcall":{...},"step":3,"agent_id":"...","mindfs_id":"..."}
{"seq":58,"pos":2,"revision":1,"type":"tool_call","toolcall":{...},"step":3,"agent_id":"...","mindfs_id":"..."}
{"seq":58,"pos":3,"revision":0,"type":"text","text":"...","step":4,"agent_id":"...","mindfs_id":"..."}
```

- `type ∈ {reasoning, text, tool_call, todo, plan, compact}`
- **`pos` 取代 `line`**（`line` 是「在 content 里的第几行」，content 一删就失去意义）
- `step` + `agent_id` 为步级身份；`mindfs_id` 标识所属 MindFS 轮次（D2/D7）
- 同 `(seq,pos)` 多版本按 `revision` 取最高（D8）
- 子代理块带 `parentToolUseId` / `taskId` / `subagentType`（D16）
- 旧格式兼容：无 `pos` 时退回 `line`；`Content` 非空且无块流时合成一个 `text` 块

### 3.3 三级身份

```
mindfs_id MindFS 权威（UUIDv5 确定性 mint，全 agent 通用）
step      MindFS 分配，按 agent 的「消息投递单元」递增：
            claude = 一条 AssistantMessage（= 一次 API 调用）
            dsh/ACP = 一次 messageId 变更
pos       步内段序 = agent 声明序；无声明时才退回到达序
agent_id  agent 侧回查键，可空、可后补、fork 后可变更
```

**`step` 的判定时机（必须先写死，否则增量落库无解）**：`step` 是**已收口**的概念 ——
claude 的步边界（定稿 `AssistantMessage`）在最后才到，而块在流式期间就要落盘。
故规则为：**块先落盘时 `step` 写哨兵 `-1`（本轮步序未定），步收口时用
`(seq,pos)` 加一条 `revision+1` 补正**。这与 D8 的 revision 覆盖是同一机制，不新增花样。
读取侧：忽略 `step < 0` 的版本，取该 `(seq,pos)` 的最高 revision。

**为什么必须以 MindFS id 为主**：`repoint.go:35` 明载——**fork 后 claude 产出全新 uuid**，
而 MindFS 不持久化 uuid。native id 在 fork/repoint/resume 后不稳定，只能当映射值。

**实测支撑**：

| | 步边界 | 步内块 |
|---|---|---|
| claude | 一条 `AssistantMessage` | **99.96% 只有 1 块**（tool_use 56.1% / text 23.3% / thinking 20.5%），多类型合计仅 **46/110037** |
| dsh | `(turn, step)`，986 组各**恰 1 条** record；另有 `step/start`·`step/end` | 一条 record **内含多块且有序**：`reasoning→tool-call` 355、`reasoning→text→tool-call` 328… |

→ 两边都能给出**声明序**；差异小到消费方无需分支。

---

## 4. 写入路径

```
agent 事件 → block → 缓冲 →(300ms / 64KB / boundary)→ ① append 块流 ② 广播同一 block
```

- **单一汇聚点**（codebase 图谱已证）：`appendExchange`（← `addExchangeForAgentAt:695`）
  + `appendExchangeAux`（← `AddExchangeAux:755`）。
- **先落盘、后广播**：保证「前端看到的 ⊆ 已落盘的」，重连/切会话必然自洽。
- **轮次开始即分配 `seq`**，MindFS 按 `agent_id` 或 `sessionKey|role|seq` 铸 `mindfs_id`（D4）。
- **即时档 vs 批量档**：用户自己那条消息、工具卡开始走即时档，不等 flush 周期。
- **落盘顺序**：先写元数据行（含 `mindfs_id`/`seq`），再写块 —— 否则块成孤儿。
- **fsync 策略**：中间 flush 只 `write`（实测 5.3µs），**轮次结束 `fsync` 一次**（实测 1.48ms）。
- **崩溃契约**（D13）：崩溃丢缓冲区，靠 import 补齐；`partial:true` 的轮 UI 显示「进行中/已中断」。
- **三处必修**：`manager.go:2264`（tail 跳过 `seq<=baseSeq`）、`:2267`（tail 内不按 seq 去重）、
  `:2211`（内容不同即告警 → 改为只对**真冲突**告警，增量更新走 revision 不算冲突）。
- **`:2208` 的「空不覆盖非空」必须重写**：它现在按 `Content` 判。删 `Content` 后若不改，
  **native_id 回填会被静默丢弃**。

---

## 5. 读取路径

- **窗口读走块流反向分块读**：复用 `readAuxWindowTail` 的成熟做法（512KB chunk、从尾部、
  见 `seq<minSeq` 再多读两块）。
- **块带自己的窗口参数**（D9）：实测一轮可达 **83 步 / 974 条消息**，20 条 exchange 整包给
  可能几 MB → 块窗口默认给尾部 N 块 + 向前翻。
  **压缩规则必须复用既有机制，不要另造**：`CompactExchangeAuxLight`（G-AT）已经在做
  「窗口只下发能渲染折叠卡片的最小字段 + 展开时 `GET /api/sessions/{key}/toolcalls/{callID}`
  懒加载」（`types.go:137`），窗口路径也已经走 `GetSessionExchangeAuxWindow`（修掉了
  「11.8MB/~300ms 且持 `m.mu`」）。块窗口沿用同一策略即可。
- **块内联进窗口回包**（D18）→ 顺带删掉 `hiddenToKept` 的 aux seq 重映射。
- **必须修 `getSessionWindowUnsafe`**：它现在调 `readExchangesFull` **全量解析再切片**。
- **锁约束**：`m.mu` 每 root 一把（`manager.go:312` / `:703`），窗口读在锁内 → 锁内工作必须
  O(窗口)。**这是前提，不是优化**：否则合并后最坏会话 98ms 全量解析会让整个 root 一起卡。

---

## 6. 身份与对齐

| agent | 客户端能否指定 | live 能否读回 | importer 能否读 | 落地规则 |
|---|---|---|---|---|
| **ACP (dsh)** | ✅ `PromptRequest.messageId`（**UNSTABLE**，agent SHOULD 回显） | ✅ 每 chunk 自带 `messageId` | ✅ 同源 | turn_id 直接带下去，零猜测 |
| **claude** | ⚠️ 类型 `omitempty` 允许但 `Stream.Send(ctx,string)` 无入口 | ❌ **拿不到**：`UserMessage` 事件只带工具结果（`handleUserMessage` 非工具结果直接 return） | ✅ 转录有 `uuid` | 按 **native id 逐条匹配**，对不上**跳过 + 日志，永不猜**（D10） |
| **codex** | 待实证（本机无转录） | 待实证 | ✅ 转录有 `id` | 同 claude |

**实测要点**：
- dsh：`user/message` 99 条 / **99 个唯一 id**；`assistant/message` 974 / **974 唯一**；另有 `turn` 分组。
- claude：`uuid` 重复是**良性的**（同一记录重发，内容+时间戳一致）→ `uuid` 可当 native 键。
- ACP `PromptRequest.MessageId` 三条限制：**UNSTABLE**、**SHOULD echo**、**MUST 是 UUID 格式**
  → 所以递下去的必须是 UUID（这正是 D3/D4 的由来）。

**`TurnGen` 的处置（D6）**：全仓 ~15 处生产 + 9 处测试，纯类型替换
`TurnGen uint64` → `TurnID string`，语义不变（`0` → `""` 仍表示「无身份 → 保守清」）。
那 9 条测试钉住的正是必须保留的行为（迟到 done 不清新轮、重复 done 幂等、`0` 保守清、
子会话由父轮终结）——**测试就是规格**。

---

## 7. 前端

- **按 `type` 渲染**：`reasoning` 灰显/可折叠、`text` 正常、`tool_call` 工具卡。
- **live 与 load 同一渲染器、同一数据结构** → 「流式好看、收尾消失」结构上不可能。
- **`buildAssistantTimeline` 重写**（`web/src/hooks/useSessionStream.ts:196-300`，~106 行）
  + `buildBaseTimeline:368`：
  - 现在：`content.split("\n")` → `totalLines` → aux 按 `line` 排序插空（`:213` 还有 clamp）
  - 改为：按 `pos` 遍历块
  - `lines` / `totalLines` / `emittedLines` / clamp **全部消失** —— 净简化
- **超大块**（`os` 的失控 reasoning 实测 **119714 / 103467 字符**）默认折叠 + 标注。
- **seq=0 机制退役**（D4 的直接收益）：乐观行从第一刻就有权威 `turn_id`，**「认领」步骤消失**。
  涉及 `isTransientExchange`（6 个调用者：`mergeSessionExchanges` / `isPersistedSeq` /
  `composeLoadedExchanges` / `App.tsx:4578` / `dropTransientExchanges`）、`inferredSeq`、
  `tailOverlay` 5 条启发式（`session-streaming-rework.md` §2.2）。

---

## 8. 连带改动

| 项 | 现状 | 改法 |
|---|---|---|
| **搜索** | `searchSessionContent`（`manager.go:3291`）扫主日志、匹配 `Content` | 改扫块流；`text`+`reasoning` 都命中、`text` 优先；cap **1MB → 64MB**（`:3306` vs `:37`） |
| **投影** | rule A/A2/B + `RemapExchangeAux` | 身份判重替代 → 整块退役；审计端点失去意义 → 一并退役 |
| **同 seq 载入去重** | `manager.go:2208/2211` 按 `Content` | 改按 revision / 字段完备度 |
| **ACP importer** | 四条硬伤 | 收 `AgentThoughtChunk`、尊重 `TimestampFloor`、删 `:137` 早退、别 `time.Now()`（`:241/:247`） |
| **claude importer** | 不收 thinking | 补上；改用 native id 判重 |
| **`AgentCtxSeq`** | 「条数游标」，语义混乱（G-BF 那个 bug 的根源） | 语义收敛为「最后一个已对齐的 native 序数」，不再当索引 |
| **旧格式读取** | — | `Content` 非空且无块流 → 合成 `text` 块；无 `pos` 退回 `line` |

---

## 9. 性能预算（全部实测）

| 项 | 数字 |
|---|---|
| 最坏会话主日志解析 | 391KB / 206 行 → **6.2ms** |
| 同会话把 thought 并进 content | 9.84MB → **97.9ms**（字节 25x、耗时 15.8x） |
| 该 98ms 若在锁内 | **整个 root 卡 98ms** |
| ext4 fsync / 只写 | 1.48ms / **5.3µs** |
| 合并后 <1MB 的会话 | **97.6%**（858 个中仅 8 个 >3MB） |
| 现状最大单行 | 主日志 308KB / aux 515KB |
| 搜索 scanner cap | **1MB**（主路径 64MB） |

**结论**：块流进 sidecar + 窗口按需读（含块级窗口），读成本锁在窗口大小；
主日志退成 metadata 后很小，可全量读。

---

## 10. 交付顺序

| 步 | 内容 | 独立价值 |
|---|---|---|
| **S1** | `getSessionWindowUnsafe` 冷缓存按需读 + 三处 tail 缺陷（`:2264/:2267/:2211`）+ 搜索 cap 对齐 + **O1 两个全量 aux 热路径**（`http.go:937` / `session.go:252`） | 消除 root 级停顿（与主方案正交） |
| **S2a** | 加 `turn_id` / `turn_seq` / `native_*` / `partial`（`step` 在块流上）；块流**写入**（双写：块流 + 旧 `Content`/aux 并存）；**C2 三处服务端 mint UUID**；**C1 repoint 清 native id** | 可回滚，旧路径不动 |
| **S2b** | **读**改走块流；前端按 `type` 渲染；块级窗口（**复用 `CompactExchangeAuxLight` 策略**）；块内联（**省掉 O3 的 round trip**） | 新老并存可对账 |
| **S2c** | ✅ **身份判重**启用；投影**身份化**（只折叠双边无身份的历史残留）+ 审计端点报身份化进度 | 新数据不再猜；旧残留可度量 |
| **S2d** | ✅ **已落地**（S2d-1 旧数据回填 + `CompactExchangeAux` 丢 reasoning 的修复；S2d-2 块流成为正文唯一载体、主日志 `content` 恒空） | 单一真相源 |
| **S2e** | ✅ 删除 `TurnGen`；后由 G-BI 收敛为 `mindfs_id` + `agent_id` 两类 id | 身份模型已统一 |
| **S3** | ✅ 退役读时投影与审计端点、删除 `CountExchanges`、清理可确认的历史副本；待做：seq=0 / `tailOverlay` 机制审计与退役 | 减少读时启发式 |
| 待办 | 旧数据迁移脚本（dsh 可迁、claude 本地无 thought）；codex native id 实证 | — |

登记：S1=`G-BG`、S2a–S2e=`G-BH`、身份模型=`G-BI`、S3 清理=`G-AB`；每步针对性测试 + 提交带 `Scope:` trailer。

**身份契约更新（G-BI，2026-10-11）**：本节早期的 `turn_id` / `native_*` 是历史阶段名，当前落盘只使用两类身份：
`mindfs_id` 由 MindFS 确定性铸造并作为权威行 id；`agent_id` 是 agent 提供的可选桥接 id。非空 `agent_id` 用作 mint seed，缺失时以 `sessionKey|role|seq` 兜底。live 与 import 共享该规则。pending 灯沿用同一 UUID 字符串形状，但按轮次开始时的身份时点单独生成。

**S3 落地记录（2026-10-12）**：删除会话读时投影、aux 重映射和审计端点，并移除 `CountExchanges` 及其专用测试。审核本机历史数据后，原计划中的 10 条候选只有 8 条能按 import/live 来源与角色确认是重复副本，已删除副本、保留 live 行；另 2 条回复不同，未能证明是重复，保留。活动会话里发现一条投影误隐藏不同回合的记录，GET 改为原样返回持久行，aux 继续按原 seq 对应。`exchangeAlreadyRecorded` 保留为写入侧幂等护栏。`appendResponseChunk` 也保留：live 写路径仍用它汇总 text 块，供块持久化与行号计算；要删需先改写这两条真实调用路径。`seq=0` 与 `tailOverlay` 尚未在本轮退役，需单独审计其 UI 对账职责。

---

## 11. 与既有计划的关系

| 处置 | 对象 | 理由 |
|---|---|---|
| **取代** | G-T 文档 §6 两条非目标：①「不把所有流式 chunk 直接写成 Exchange」②「不删除 `Exchange/ExchangeAux`」 | ①**前提已失效**：该条写在快照重建**之前**，那时流乱序/重复，落库等于把 bug 焊进磁盘；现在快照重建 + FIFO writer + TurnGen 均已落地。②本方案 `Exchange` 仍**一轮一行**，写的是 block 到块流；但确实重构 `ExchangeAux`，故该条明确作废 |
| **接手** | `session-streaming-rework.md` §3.6 + §2.4 | 文档自己写明「将来再扒掉」并给出前置条件 |
| **保留** | 快照重建（task-28，已上线）；G-T 7b/7c（单一前端 reducer） | 与本方案正交；本方案让 7b/7c **更容易**（输入形状变少） |
| **作废** | `canonical_turn.go` / `canonicalTurn.ts` | main 从未采用，G-T 文档 §12 自认 |

**不要重复 G-T 的错**：`message-mechanisms-plan.md` §12 警告「两套回合身份真值并存必然打架」。
本方案的 `turn_id` 是**唯一**身份，`TurnGen` 在 S2e 删除 —— 不留第三套。

---

## 12. 验证与回归

**核心难点**：这个 bug **无法用真模型稳定回归**（`os` 只有 ~15% 轮次触发）。

→ **转录回放钩子**（D12）：沿用 `server/internal/agent/claude/zz_replay_live_test.go` 的
`MIND_FS_REPLAY_TRANSCRIPT` 先例，正式化为可重复用例，固化：

1. **只有 reasoning、无 text 的轮次** → 正文不为空（本 bug 本身）
2. 步内多块（`reasoning→text→tool-call`）→ 顺序正确
3. 工具结果晚到 → revision 覆盖，不产生重复卡
4. 同轮重放 N 次 → 逐字节相同（幂等）
5. 旧格式（`Content` + `line` aux）→ 仍能渲染

**S1 的验收标准（可量化，独立于主方案）**：

1. **冷开窗不再全量解析主日志**：取最重会话（`1791339665`，主日志 391KB/206 行），
   冷缓存下开窗耗时从实测 **6.2ms** 量级降到「只读尾部 N 条」的量级；用测试直接断言
   **未调用 `readExchangesFull`**（比计时稳）。
2. **`:2264` 的 `seq<=baseSeq` 跳过被移除或改为按 revision 判定** —— 用一个
   「同 seq 追加更高 revision」的用例钉住：增量快路径（`loadExchanges`）必须看得见更新。
3. **tail 内同 seq 不再重复吐**（`:2267`）。
4. **`:2211` 的冲突告警只在真冲突时打** —— 增量更新（同 seq、高 revision）不打日志。
5. **O1 两个全量 aux 热路径**：`http.go:937`（非窗口 GET）与 `session.go:252`
   （`fillCommandShells` 列表子会话）改为窗口/按需读；用测试断言不再调 `loadExchangeAux` 全量。
6. **搜索 cap** 从 1MB 提到 64MB 与主路径一致，并补一条「单行 >1MB 仍可搜到」的用例。

**每步门禁**：`go vet ./...` / `go test ./...` / `tsc --noEmit` / `npm test` / `make check-upstream`。

**S1 落地记录（2026-10-10，提交 `G-BG`）**：6 条全部达成。

- 验收 1 的实测：合成最坏输入 1.19MB / 206 行（每行 ~5.7KB 正文，共 206 条），
  冷缓存开窗 **15.3ms → 1.4ms**（约 11 倍）；拆解为读盘 0.9ms、字节级行计数 0.14ms、
  解析 9 条 0.8ms。真实最重会话 391KB/206 行 → 按比例约 0.6ms（原 6.2ms）。
  测试不断言计时（计时会随机器抖动），改断言 `Manager.fullExchangeReads` 保持为 0。
- `Total` 在冷缓存下取「非空行数」而不是 `len(全部记录)`。判定：实测 310 份真实主日志、
  2172 行，**0 空行、0 同 seq 多行**，只有 1 份存在 maxSeq 与行数差 86。
  字节级行计数（`bytes.Count` 思路）在 1.2MB 上 0.14ms，比解析便宜两个数量级。
  代价：同 seq 重复行的退化日志会显示「重复条数」。S2d 之后主日志只剩元数据，此代价消失。
- `readAuxWindowTail` 逐块反向读会丢掉跨块长行，**不适用于主日志窗口读**
  （实测单行最大 308KB）。主日志侧改为「只读一个后缀、起点对齐换行」的单次读，
  凑够 `need+1` 条即停，span 按 4 倍增长。
- `readAuxFile` 加了 `Manager.fullAuxReads` 计数器（与 `fullExchangeReads` 同思路），
  `GetCommandShell` / 窗口 aux 两条路都断言不触发全量读。

**S2a 落地记录（2026-10-10，提交 `G-BH`）**：块存储层 + 轮次身份 + 双写 + C1/C2 全部落地。

- 块流路径 `sessions/<key>.blocks.jsonl`，与主日志 / aux 同目录。API：
  `AppendBlocks` / `ReadBlocks` / `GetBlocksWindow` / `blocksPath`。
- `BuildTurnBlocks` 与前端 `buildAssistantTimeline` **逐条同序**（按 `(line,到达序)` 稳定排序、
  空正文段跳过）。这条等价性是 S2b 的前置条件：不同序就等于切读路径时正文顺序会变。
- **与 §4 的一处偏离（有意）**：S2a 的块在**轮末一次性**落盘，不做 §4 写的
  300ms / 64KB 中间 flush。理由：读路径在 S2a 仍走 `Content`，中间 flush 只增加磁盘写、
  没有任何消费者；流式期落块是 S2b 的工作（那时才有读者）。
- **`step` 的占位**：S2a 写 `step = 1`（`turnBlockStep`）而不是 §3.3 的哨兵 `-1`。
  理由：读取侧规则是「忽略 `step<0` 的版本」，写 `-1` 会把 S2a 期间落下的块**全部作废**，
  S2b 切读路径的那一刻正文会凭空消失。S2a 的块在轮末落盘时投递边界已经过去、
  没有被记录下来，整轮只能算一个投递单元 —— `1` 是当前唯一可写的真值。
  按投递单元真切分（claude 的 `AssistantMessage` / dsh 的 `messageId`）是 S2c 的工作，
  届时用 `(seq,pos)` + `revision+1` 覆盖成真值，**不需要迁移历史**。
- **C2 的 mint 点收敛到一处**：三条服务端发起的轮次路径（`scheduled/tasks.go` 定时任务、
  `appcontext.go` 看板任务阶段、子会话）全部经 `SendMessage` → `persistUserTurnExchange`，
  所以 `newTurnID()` 只在那一个点调用。另外两条独立写入者（命令会话
  `persistCommandTurn`、子会话 `attachBackgroundSessionUpdates`）各自 mint / 复用。
- **写入点四处**：`persistUserTurnExchange`（user 块）、`SendMessage` 轮末（助手块）、
  `persistCommandTurn`（命令块）、`attachBackgroundSessionUpdates`（子会话块）。
  块写失败一律只记日志、不失败本轮 —— 正文已由 `AddExchangeForAgent` 落过盘。
- **§14 C1 的两处调用点**：`RepointSession` 第 6 步（放在最后，前几步失败则 native id 原样
  保留，仍与旧转录一致）与 `copyForkHistory`（fork 开全新 agent 会话，源 native id 一律无效）。
- **§14 未列出的第三个漏洞（本次发现并修）**：`copyForkHistory` 只复制 exchange + aux，
  **不复制块流** —— S2b 之后 fork 会话会渲染成空。已补 `copyForkBlocks`（seq 重映射、
  `native_id` 清空、`turn_id` 保留）。
- **§14 未列出的第四个漏洞（本次发现并修）**：`deleteSessionUnsafe` 只删主日志与 aux，
  不删块流 —— 正文留在盘上，会话 key 复用时会被当成「本来就有正文」读出来。已补。
- **§14 C3 顺带修**：`persistCommandTurn` 原先用调用方预测的 `plannedAssistantSeq` 写 aux，
  而助手行的真实 seq 由 `AddExchangeForAgent` 分配。两者一旦漂移，aux 会挂到别人的 seq 上。
  改为写完后从缓存取真实 seq（块流同理）。

**S2b 落地记录（2026-10-10，提交 `G-BH` 续）**：读路径改走块流 + 前端按 `type` 渲染。

- 后端：`GetBlocksWindow(ctx, key, seqSet)`（`blocks.go`）反向分块读尾部，只读窗口内 seq。
  **没有复用 `readAuxWindowTail`** —— 它丢掉每块的首个不完整行，跨越读块边界的记录会静默丢失；
  块流一行实测可达 700KB（os 单块 119714 字符），必须两阶段：先反向探测停止偏移（只解析
  块内部的完整行），再 `alignLineStart` 对齐行首后**一次正向读到 EOF**。代价 2× IO。
  测试断言 `Manager.fullBlockReads == 0`（比断言计时稳）：300 个 seq × 4 块、~620B/行下
  一次窗口读**一次全量读都没有**。
- `resolveBlocks`：按 `(seq,pos)` 取最高 `revision`、跳过 `step<0` 的未收口块、按 `(seq,pos)` 排序。
  `CompactBlockLight` 只压工具卡细节（复用 S1 的 `CompactToolCall` + `lightCompactToolCall`），
  正文与 reasoning 一个字不动 —— 压缩规则与 S1 同源，不是新写一套。
- `sessionResponse` 内联 `blocks`（只带 `windowSeqsForExchanges(exchanges)` 的 seq），
  `handleSessionGet` 与 `handleSessionSync` 两条路都带上。内联而不是让前端再发一次请求：
  窗口回包本来就是这一屏的唯一权威快照（§14 O3）。
- 前端：新增 `web/src/hooks/sessionBlocks.ts`（纯函数、可被 `node --test` 直接 import）。
  判定逻辑**必须**单独成 `.ts` —— `.tsx` 在 `node --test` 下导入不了（`ts-module-hook.mjs`
  的边界说明），住在 hook 里就只能靠源码文本断言。`TimelineItem` 的定义随之搬过去、
  从 `useSessionStream.ts` 转发，保持既有导入路径不变。
- **块序即渲染序**：`buildAssistantTimelineFromBlocks` 只按 `pos` 遍历，
  `lines`/`totalLines`/`emittedLines`/clamp 全部消失（那套是「把 aux 按 `line` 插进 content
  字符串」的产物）。项 id 用 `block:<seq>:<pos>`，跨 revision 稳定。
- `buildBaseTimeline` 的**每 seq 回退**：有块走块流、没块走旧格式。这条回退是刻意的 ——
  同一会话里 S2a 之前的轮次没有块流，必须仍能渲染。判据是「块数组**非空**」而不是「键存在」，
  因为后端对没有块流的会话下发空 map（`TestHandleSessionGetWithoutBlocksReturnsEmpty` 钉住）。
- 块跟 aux 走完全部缓存路径：`toPersistentSession`（只留还在 exchanges 里的 seq）、
  `withSessionMeta`、`cloneSession`、`appendSessionDelta`、窗口锚定（`App.tsx:2336`）。
  **按 seq 覆盖**而不是拼接 —— 一个 seq 的块集合是完整快照，拼接会让旧 revision 与新 revision
  并存；aux 恰好相反（同 seq 陆续追加），所以 aux 必须拼接。两者形状相同、语义相反，是刻意的。
- **跨语言契约补了一条测试**：`TestBlocksPayloadWireFieldNames` 断言块回包的**精确键集合**。
  TS 的 `SessionBlock` 是手写的，与 Go 的 json tag 没有编译期联系 —— 改一个 tag 就能让前端
  静默读到 `undefined`、正文又变空，而两端各自的单测全绿。这是本次发现的第五个漏洞。
- 两处既有测试随之更新（不是放宽，是钉住的东西变了）：`tool-card-grouping` 的手搓 CJS
  加载器多认一个 `./sessionBlocks` 依赖（**递归加载真模块**而不是桩掉），
  `session-window` 的 `toPersistentSession` 返回字面量多了 `blocks`。

**S2c 落地记录（2026-10-11，提交 `G-BH` 续）**：身份判重启用 + 投影身份化。

- **共同键接上了。** S2a 落了 `native_turn` / `native_prompt` 两个字段，但一直没人往里写 ——
  于是它们是「有字段、无身份」，判重只能退回内容。S2c 把两端接上：
  - 实时侧：`SendMessage` 收 `MessageChunk.MessageID`（ACP `AgentMessageChunk.MessageId`），
    取**最后一个**写进助手行的 `native_turn`；user 行的 `native_prompt` = 本轮 `turn_id`，
    且**只对 `UsesPromptMessageID()` 为真的 agent 写**（问 importer，不问协议常量 ——
    协议能在 `agents.json` 里覆盖，拿 `DefaultProtocol` 猜就是把配置丢掉）。
  - 实时侧发出：`WithPromptTurnID` 把 `turn_id` 带进 ACP 上下文，`process.go` 用它填
    `PromptRequest.MessageId`；回显在 `PromptResponse.userMessageId`，不一致**只记日志**
    （ACP 标该字段 UNSTABLE，agent 可以不认；拿回显值改写 `native_prompt` 等于承认
    「库里的行」是 agent 那条，对不上才是真的错）。
  - 导入侧：`ImportedExchange.NativeID` 带出转录里的 `messageId`（ACP）/ `uuid`（claude，
    合并后取最后一个）。`appendImportedExchange` 写进同一个字段。
- **判重顺序是「先身份、后内容」**（D10：永不猜）。有身份且匹配 → 跳过；有身份且**没匹配上**
  → **一律落库**（那是一轮真新增的内容，丢掉就是用户报的「丢内容」）；没身份 → 退回
  「内容归一化 + ±5s」，并按会话记一条 `[session/identity] 判重退回内容猜测` 日志，
  那是「还在猜」的计数量。内容兜底跳过两侧空内容的行（空正文行不是同一句话）。
- **端到端回归把真实故障钉住了**：`TestFullSyncReplayAddsNoRowsToLiveOwnedSession`。
  把 `exchangeAlreadyRecorded` 的身份短路掉，这条测试红出来的形态与实测一模一样 ——
  4 行而不是 2 行、时间戳差两小时、`前半段` vs `前半段\n\n后半段`。真实会话
  `1791339665-fb5134041392` 的 9 对重复正是这个形状。
- **投影没有删掉，而是身份化了。** 有 native 身份的行整段不参与折叠（读时按内容再猜一次
  只会把真实缺陷藏成看不见，真重复露出来才是对的）。它现在只剩一个作用：折叠 S2c 之前
  遗留的、双边都没有身份的历史行 —— 直接删会让实测那 9 对重复立刻变成用户可见的重复
  正文，那是一次实打实的回退。审计端点新增 `identity_bearing_rows` 报出身份化进度
  （= 历史残留规模），那是删投影与处理历史行的时机判据。`RemapExchangeAux` 随之保留：
  规则 A2/B 还要用它。
- **删掉一道假防线**：`TimestampFloor` 只给 `HasTranscriptTimestamps()` 的导入器。ACP 的
  `SessionUpdate` 不带时间戳（SDK 里 `SessionNotification` 只有 `sessionId` + `update`），
  它给每行盖的是「读到它的那一刻」，恒晚于地板 —— 地板形同虚设，而代码看着像
  「有地板就安全」。`ImportedExchange.SynthesizedAt` 把这件事标出来：非零 ⇒ `Timestamp`
  是观测时刻而非真实时间。
- **顺带补上 ACP 导入丢 reasoning**：`AgentThoughtChunk` 原来整个被丢，只思考不出话的
  轮次导入后正文为空。同一 `messageId` 的 thought 与文本合成**一行**（分成两行会让一轮
  在时间线上变成两轮），thought 落成 `kind=think` 的工具卡（现在前端就在用的表示，
  不必再转一次形状）。
- **一处自我纠错**：中途给 ACP importer 加过一个「`AfterTimestamp` 与 `TimestampFloor`
  都为零就跳过」的守卫。它会挡掉 import-owned 会话的**首次**导入（两个地板都为零），
  也就是「同步什么都导不进来」。已撤掉 —— 幂等性现在由身份判重负责，不需要第二道
  时间启发式。

**S2d-1 落地记录（2026-10-11，提交 `G-BH` 续）**：旧数据回填 + 一条独立的 reasoning 丢失修复。

- **动手前先量了存量，结果改变了做法。** 原计划 S2d 直接删 `Exchange.Content`，但实测：
  **320 个会话 / 2166 行 / 0 个块流文件** —— S2a 写块流的功能从未部署过。直接删字段等于
  把全部历史正文清空。所以 S2d 必须先有回填，顺序不能反。
- **回填是读时惰性 + 幂等，不是迁移脚本。** 会话库按项目/账户分区散在各处（本机就有 4 个
  `.mindfs/sessions`，WSL 端另有一份），没有任何一处能保证「迁移脚本被跑过」；而输入
  （exchanges + aux）**在读的时候就在调用方手里**，不额外读盘。`GetSessionBlocksWindowInput`
  因此多了 `Exchanges` / `Aux` 两个字段，它们只作回填输入 —— 关键约束是**零额外 IO**：
  `GET /api/sessions/{key}` 是全站最重的端点（实测单次 1.14MB，客户端 1.7s 一次轮询，
  占 70% 流量），在那里多读一次盘就是把回填变成新的性能问题。
- **回填必须出现在同一个回包里。** `TestHandleSessionGetWithoutBlocksReturnsEmpty` 的断言
  整个反转成 `TestHandleSessionGetBackfillsLegacyBlocks`：回填完再让前端自己拉一次，用户看到的
  就是先空一屏、再突然冒出正文。
- **实测结果**：133 个会话、39517 条块，**救回 29 轮「正文已丢、思考还在」的轮次**，
  第二次跑 0 回填。块流 37MB 对主日志 5.4MB（约 7×，主要是工具卡全量入块）——
  块流只做**窗口尾部读**，体积不进读路径。
- **投影与回填的交互已实测排除风险**：本来担心「被投影隐藏的重复行，其工具卡会因保留行
  有了块而不再走 aux 合并」。查了实测那 9 对 —— **隐藏行的 aux 全为空**，工具卡与 reasoning
  都在保留的实时行上，所以按各自的 content+aux 编译不丢东西。
- **顺带修掉一条独立的、更早的 reasoning 丢失**（`CompactExchangeAux`）：只有 `thought`
  没有 `toolcall` 的 aux **整条返回 false**；`toolcall` 与 `thought` 并存时
  `aux.Thought = ""`。流式期 `thought_chunk` 是实时推的，所以用户「看着有」，关掉重开就没了 ——
  这正是本项目最初报的「回复的时候好好的，回复完瞬间消失」。**实测存量 10826 条 / 2657 万
  字符 / 17 个会话**的 reasoning 一直在盘上但读不出来。压缩只该去掉「展开时才需要」的字段，
  reasoning 不是那一类。
- **一处自我纠错**：S2c 里我给 ACP 导入选的表示是 `kind=think` 的工具卡。查了前端才发现
  `reasoning` 块渲染成灰显的思考文本、工具卡是另一种外观（带💭 图标的折叠卡）—— 同一轮
  reasoning 会「实时一个样、同步后一个样」。已改用 `ImportedExchangeAux.Thought`，与实时路径
  和存量数据（10826 条 `thought` 字段）同一个形状。

**S2e 落地记录（2026-10-11）**：pending 灯的回合身份改用 `turn_id`，`TurnGen` 删除。

- **这是类型替换，不是「统一两套身份」。** 全仓约 15 处生产 + 9 处测试，
  `SessionPendingState.TurnGen uint64` → `TurnID string`，`0` → `""`，
  **语义逐条不变**。D6 那句「统一为 `turn_id`」容易读成「把两套并成一套」—— 并不了：
  pending 灯在 `OnStart` 就要身份（那时轮次还没落盘），而 `turn_id` 是在
  `persistUserTurnExchange` 里才铸出来的。**两个时刻不同，值就必须不同**；
  要做的是换表示（都用 UUID 字符串），不是换真值。
- **只保留一条形状判据。** `session.IsTurnIDShape` 放在 `TurnID` 字段旁边，
  ACP 的 `acp.isUUIDShape` 退化成薄转发。两份判据 = 两份「谁算合法身份」的真相。
  mint 同理收进 `session.NewTurnID`，`usecase.newTurnID` 只剩包内转发 ——
  api 的 pending 灯也要铸 id，两个铸口迟早分叉。
- **抓到第三处同形状缺陷（`AppContext.RunAgentStage`，看板阶段执行）。** 它置 pending
  的两个调用点都丢了返回的身份，收尾按字面 `0` 走 —— 与 G-BE 当年修的
  `scheduled/tasks.go` **完全同一个形状**。而 `pending_identity_test.go` 只扫 `tasks.go`，
  管不到这里：于是这条路径长期停在「灯不灭被兜住（保守清），但新轮的灯会被误抹」。
  新守卫 `kanban_turn_identity_guard_test.go` 把它补上，与 ws.go / tasks.go 守同一条纪律。
- **一处「守卫变成筛子」的自我纠错。** `pending_identity_test.go` 拦哨兵的正则只认字面 `0`；
  换成 `""` 之后这条不变量就**再也拦不住任何东西**，而测试全绿。用探针程序证伪
  （`old_matches=false` 对 `..., "")`），改成新旧哨兵都拦。
  教训与 S2d-2 相同：**换表示时，守卫的正则也是要换的零件。**

**S2d-2 落地记录（2026-10-11，提交 `37a0a1f`）**：块流成为正文唯一载体，主日志 `content` 恒空。

- **删的是「写」，不是字段。** `Exchange.Content` 改名为 `LegacyContent`，json tag **仍叫
  `content`** —— 老会话文件必须还能解析，而它现在的唯一读者是 S2d-1 的回填。三个写入点
  （live user 行 / `SendMessage` 轮末助手行 / `appendImportedExchange` 导入行）全部改成只写块流。
- **最险的一刀是 user 行，而它不是本方案原计划里的那条。** `addExchangeForAgentAt` 有个
  「空正文 + 非 agent 角色就跳过」的守卫；user 行一旦不再带正文，这个守卫会把**每一行 user**
  静默丢掉 —— 前端看不到自己说过的话、搜索的 user 命中失效，且没有任何报错。它是在
  `TestSearchPrefersUserTurnHitFromBlocks` 红掉之后才被挖出来的，**不是**预先设计的。
  改成「什么都没带」才算空行：空正文 + 空 agent + 无任何轮次身份。
- **助手块写入失败从「记日志」升级成返回 err。** 双写期块失败无所谓（正文已在主日志），
  现在主日志只剩元数据，块丢了就是正文丢了。导入路径仍只记日志 —— 那一行已经落盘，
  块失败不该让整行消失。两条路径的不对称是刻意的。
- **判重与搜索各换了一次数据源，两处都是静默失效的形状。** 判重的 user 行没有 native uuid
  （`Stream.Send(ctx, string)` 不给这个入口），只能比正文，于是 `recordedExchangeBody` 改读
  块流；搜索改成「先块流、再主日志」两遍，后一遍为了还没回填的老会话。这两处不换不会报错，
  只会让同一个 prompt 被反复写成新行、老会话搜不到词。
- **fork 的两个函数是分工，此前一度想删掉其一。** `copyForkBlocks` 复制源**已有的块**
  （revision/step 是盘上真值、不重编号），`fillForkBlocksFromLegacy` 给老会话补编译。
  只补编译会丢源块的 revision；只复制会让老会话 fork 出没有正文的会话。测试 fixture 恰好
  只覆盖了其中一条（源有块 + aux 有 thought），所以删掉 `copyForkBlocks` 之后测试仍是绿的 ——
   **测试绿不等于删对了**，这是本条最该记住的一条。
- **门禁的两个新发现**：三个新测试文件在提交前不算 delta，`check-upstream` 会把它们报成
  「已登记但不在 delta」；`manager_duplicate_seq_test.go` 是 fork 定制却从未登记，这次
  改了字段名才进 delta。

---

## 13. 待办 / 未实证

- **codex native id**：本机无 `~/.codex/sessions`，`payload.id` 是否 per-message 未实证。
  设计已按「可缺失」处理，不阻塞。
- **旧数据迁移**：不迁移。dsh 的 `thought` 在本地 aux 里（可脚本迁）；claude 的 thinking
  大部分从未落盘（实测 `os` 420 个会话只有 27 个有 thought），只能回转录捞。
- **分文件**：暂不做。实测单会话 aux 最大 19MB，`readAuxWindowTail` 已能在其上只读尾部
  512KB×N —— 瓶颈是「有没有做真正的窗口读」，不是文件大小。

---

## 14. 全局审核结论（2026-10-10，codebase 图谱辅助）

### 14.1 机制清理审核（2026-10-10 初审，2026-10-12 更新）

| 目标 | 位置 | 规模 |
|---|---|---|
| 目标 | 当前结论 |
|---|---|
| **读时投影 / aux 重映射 / 审计端点** | ✅ 2026-10-12 删除（G-AB）。历史审核中 8 条 import 副本已清理；2 条不能确认的候选保留。 |
| **`exchangeAlreadyRecorded` 与内容辅助判据** | ✅ 保留。它是写入侧幂等护栏，避免重复同步；不属于可删除的读时投影。 |
| **`CountExchanges`** | ✅ 删除。生产零调用，连同仅依赖它的 manager 测试删除。 |
| **`appendResponseChunk` / `lastResponseUpdateType`** | 保留。两条 live 写路径仍需拼接 text 块，供 `persistTurnBlocks` 与块行位置计算；必须先重构调用路径才能移除。 |
| **`CompactExchangeAux` reasoning 保留** | ✅ S2d-1 已修复，不能退回清空 reasoning。 |
| **seq=0 / `tailOverlay`** | 待独立审计。它们目前参与前端瞬时行与持久窗口对账，本次没有删除。 |

### 14.2 新冲突（3 条，原方案未覆盖）

**C1 · `repoint` 必须清 native id** ⚠️
`repoint.go:35` 现写着：「mindfs 并不持久化 claude 的 uuid，所以 uuid 重映射对 mindfs 不可见、
无副作用」。本方案**恰恰要持久化 native id**——repoint/fork 之后 claude 的 uuid 全部改变，
故 `native_turn` / `native_prompt` / `native_id` **必须在 repoint 时清空**，否则旧 native id
与 fork 后的新转录对不上，判重失效。**这是本方案直接引入的新步骤。**

**C2 · 无客户端时 `turn_id` 由谁生成** ⚠️
D4 定的是「客户端生成」，但实测有**三条服务端发起的轮次**：
`scheduled/tasks.go:527`（定时任务）、`appcontext.go:371`（看板任务阶段）、
`appcontext.go:964` `SetPendingReply`（子会话）→ 这三处必须**服务端 mint UUIDv4**。

**C3 · `CountExchanges` 删除要连测试**（`manager_window_test.go:136`）——小。

### 14.3 优化空间（4 条）

**O1 · 全量 aux 读还有两个热点**（本次新发现）
`GetExchangeAux(key, 0)` = **全量读 aux**（单会话最大 19MB），仍有 5 个调用点，其中两个在热路径：

- `http.go:937` 非窗口 GET（sync / 增量路径）
- `session.go:252` **`fillCommandShells`（列表子会话）** —— 逐个 child session 全量读 aux

（另两处 `session.go:678` fork 一次性、`session.go:739` 通用入口）
→ 块流落地时应一并改成窗口 / 按需读。

**O2 · 冷开窗全量解析会变廉价**（修正原 §5 的表述）
实测 `getSessionWindowUnsafe:1635` **有缓存快路径**，只有**冷缓存**才 `readExchangesFull`。
不是「每次开窗都全量」。本方案让主日志退成 metadata → 冷开窗只解析小元数据 + 块流尾部有界读。
**这比原方案「S1 修窗口读」更准确地描述了收益。**

**O3 · 省一个 round trip**（D18 附带）
`GetSessionExchangeAuxWindow`（`usecase:745`）是独立请求，块内联后省掉。

**O4 · `:2264` 的 `seq<=baseSeq` 跳过**（原来只当作理论问题）
`loadExchanges` 的**增量快路径正是走 `readExchangeTail`**，而它跳过 `seq <= baseSeq`
→ 本方案的「同 seq 不同 revision」更新对这条**已存在的最热读路径不可见**。
§4 已列必修，此处确认它**不是理论问题**。

### 14.4 三处自查纠正

1. 「窗口读每次全量解析」**不准确** —— 只有冷缓存才全量（`manager.go:1635`）。
2. 「`CountExchanges` 也是热点」**不成立** —— 它确实全量，但**生产零调用**（仅测试）。
3. `mergeStreamedText` / `inferredSeq` **已删干净**（只剩注释与测试引用），**不需要**列进 S3。

### 14.5 初审结论与后续修正

2026-10-10 初审列出的 C1/C2/C3 已在 S2/G-BH 中处理。后续实地审计修正了「整块猜测层都可删除」的判断：读时投影与 `CountExchanges` 已移除，但写入幂等和 `appendResponseChunk` 仍被真实路径使用，必须保留。剩余 seq=0 / `tailOverlay` 工作以实际 UI 数据流为准逐项审计，不能按旧的行数估算机械删除。
