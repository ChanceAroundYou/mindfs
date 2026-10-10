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
| D3 | 递给 ACP 的 `messageId` **复用** MindFS 的 `turn_id`（一个逻辑 id 一种表示） |
| D4 | `turn_id` **由客户端生成**，服务端采纳（幂等键）；**无客户端时由服务端 mint UUIDv4**（见 §14 C2） |
| D5 | 纯导入会话用 **UUIDv5** 确定性生成（重导不造新 id） |
| D6 | **删除 `TurnGen`**，统一为 `turn_id`；独立成一步 S2e |
| D7 | 三级 `(turn_id, step, pos)`；步内用 **agent 声明序**，无声明时才退回到达序 |
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
turn_id        string   // MindFS 权威 UUID，落盘即定，永不改
turn_seq       int      // = 打开该轮的 user 行 seq（排序 / 人读用）
native_turn    string   // agent 轮 id：dsh `turn` / ACP `userMessageId`（可空）
native_prompt  string   // agent 的 prompt message id（可空）
partial        bool     // 本轮未收尾
```

**删除 `Content`。** 消费者账（实测，逐条已核）：

| 原消费者 | 变更后 |
|---|---|
| 投影 rule A / A2 / B（`session_projection.go:107/122-131/161`） | 身份判重替代 → 消失 |
| `auditPreview:308`（审计端点，**前端从未消费**） | 规则退役 → 消失 |
| `RemapExchangeAux`（`http.go:1257`，投影唯一生产调用者） | 块内联后 → 消失 |
| 搜索 `manager.go:3326` | 改扫块流 |
| 同 seq 载入去重 `manager.go:2208/2211` | 改按 revision / 字段完备度 |
| importer 累积正文 `claude:1277` / `codex:754` | 改落块 |
| 前端渲染 | 改按 `type` |
| ~~列表预览~~ | ✅ 不成立：`ListByKeys` 不读正文，Session meta 无预览字段 |
| ~~`ExchangeHasLiveSignature`~~ | ✅ 不成立：靠 ModelDisplayName/TokenUsage/Mode/Effort 判定 |

即**「因为删 Content 而新增」的工作 = 0**。

### 3.2 块流 `<key>.blocks.jsonl` —— 唯一正文载体

```json
{"seq":58,"pos":0,"revision":0,"type":"reasoning","text":"...","step":3,"native_id":"..."}
{"seq":58,"pos":1,"revision":0,"type":"tool_call","toolcall":{...},"step":3,"native_id":"..."}
{"seq":58,"pos":2,"revision":1,"type":"tool_call","toolcall":{...},"step":3,"native_id":"..."}
{"seq":58,"pos":3,"revision":0,"type":"text","text":"...","step":4,"native_id":"..."}
```

- `type ∈ {reasoning, text, tool_call, todo, plan, compact}`
- **`pos` 取代 `line`**（`line` 是「在 content 里的第几行」，content 一删就失去意义）
- `step` + `native_id` 为步级身份（D2/D7）
- 同 `(seq,pos)` 多版本按 `revision` 取最高（D8）
- 子代理块带 `parentToolUseId` / `taskId` / `subagentType`（D16）
- 旧格式兼容：无 `pos` 时退回 `line`；`Content` 非空且无块流时合成一个 `text` 块

### 3.3 三级身份

```
turn_id   MindFS 权威（客户端生成，全 agent 通用）
step      MindFS 分配，按 agent 的「消息投递单元」递增：
            claude = 一条 AssistantMessage（= 一次 API 调用）
            dsh/ACP = 一次 messageId 变更
pos       步内段序 = agent 声明序；无声明时才退回到达序
native_id 回查键，可空、可后补、fork 后可变更
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
- **轮次开始即分配 `seq`**（客户端带来 `turn_id`，D4）。
- **即时档 vs 批量档**：用户自己那条消息、工具卡开始走即时档，不等 flush 周期。
- **落盘顺序**：先写元数据行（含 `turn_id`/`seq`），再写块 —— 否则块成孤儿。
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
| **S2c** | **身份判重**启用；退役投影 rule A/A2/B + `RemapExchangeAux` + 审计端点 | 删掉猜测层 |
| **S2d** | **删 `Content`** + 旧 aux 路径（必须晚于 S2c，否则中间判重失效） | 单一真相源 |
| **S2e** | **删 `TurnGen`**，统一为 `turn_id`（独立提交、可单独回滚） | 一套身份 |
| **S3** | seq=0 机制退役、`tailOverlay` 5 条启发式、**§14.1 的约 470 行**（投影整块 / 判重套件 / `CountExchanges` / `appendResponseChunk`） | 净减猜测层 |
| 待办 | 旧数据迁移脚本（dsh 可迁、claude 本地无 thought）；codex native id 实证 | — |

登记：S1=`G-BG`、S2a–S2e=`G-BH`、S3=`G-BI`；每步针对性测试 + 提交带 `Scope:` trailer。

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

### 14.1 可一并移除的机制（本次审核新发现，原方案未计）

| 目标 | 位置 | 规模 |
|---|---|---|
| **投影整块** | `session_projection.go`：`ProjectExchanges` / `projectRowKey` / `isLiveMarkedExchange` / `RemapExchangeAux` / `AuditSession` / `auditPreview` | **328 行** |
| **判重套件** | `session.go:2309-2423`：`exchangeAlreadyRecorded` / `recordedUserExchangeSeq` / `sameRecordedExchangeContent` / `normalizeExchangeContent` | **~115 行** |
| **`CountExchanges`** | `manager.go:330` | **~25 行**（生产零调用，只有 `manager_window_test.go:136` 调） |
| **`appendResponseChunk` + `lastResponseUpdateType`** | `session.go:3996` | 把 text 块拼成 content 并按类型插空行，块化后消失 |
| **`CompactExchangeAux` 的 `aux.Thought=""`** | `types.go:118` | thought 不再在 aux 里 |

合计约 **470 行**。`SessionProjectionEnabled` 开关只剩 `http.go:1254` 一处消费者，随之删除。

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

### 14.5 结论

**会不会引入新冲突？** 会 3 条（C1/C2/C3），均可控，且 C1 是本方案的必要配套。
**未发现会推翻方案的冲突。** 反而净减约 470 行猜测量 + 2 个热路径优化。
